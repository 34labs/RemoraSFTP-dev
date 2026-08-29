# RemoraSFTP architecture

RemoraSFTP is a **local engine with a browser-rendered interface**. This document
describes how the pieces relate and where the trust boundaries are.

## 1. Topology

```
┌─────────────────────── your device ───────────────────────┐
│                                                            │
│  Browser tab          ── loopback HTTP/WS ──▶  RemoraSFTP     │
│  (React SPA,                            (127.0.0.1) Go    │
│   served by RemoraSFTP)                        engine         │
│                                                │           │
└────────────────────────────────────────────────┼───────────┘
                                                 │ FTP / FTPS / SFTP
                                                 ▼
                                       Your remote file server
```

- There is **no cloud component** in the core product. The engine talks only
  to the remote servers you choose and to loopback clients on this device.
- The browser cannot reach FTP/SFTP directly; all protocol work happens in
  Go. Browser-to-engine traffic is local loopback traffic only.
- File contents stream **engine ↔ remote server** for the CLI and
  **engine ↔ browser ↔ disk** for GUI downloads/uploads, always between the
  device and the destination server. No third party ever sees a relayed
  stream.

## 2. Processes and ports

- On launch the engine binds `127.0.0.1:<port>` with `port` chosen
  automatically (`:0`) by default. A fixed port may be chosen with
  `--port`.
- The same listener serves the embedded UI (`/`, SPA fallback) and the JSON
  API + WebSocket (`/api/...`).
- Binding beyond loopback is **off by default**. It is only possible when the
  user explicitly enables the documented remote-access option, and even then
  the per-instance token remains mandatory.

## 3. Request lifecycle and local authorization

1. `start` generates a 256-bit random **session token** and a single-use
   **bootstrap code**.
2. The browser is opened to
   `http://127.0.0.1:<port>/bootstrap/<code>`.
3. The SPA POSTs the code to `/api/bootstrap`. It is redeemed exactly once
   (and expires quickly) for the session token, stored in `sessionStorage`;
   `history.replaceState` immediately removes the code from the URL.
4. Subsequent requests carry `Authorization: Bearer <token>`. State-changing
   requests additionally carry `X-Requested-With: RemoraSFTP`, which forces a
   CORS preflight for any cross-origin page; because the engine emits no
   `Access-Control-Allow-Origin`, such a preflight fails.
5. Every `/api/*` request (except health/bootstrap) is rejected unless the
   token matches (constant-time compare) and the `Origin` header, when
   present, exactly matches the loopback listener's scheme, host, and port.
6. WebSocket auth uses the `Sec-WebSocket-Protocol` subprotocol
   (`remorasftp.<token>`) - the token is never placed in a query string.

These layers mean a malicious website cannot: call privileged endpoints
(no token), send state-changing requests even to a browser that has the token
(custom header requires preflight, which is refused), or disguise an origin
(exact host/port check). CORS is not the boundary; it is defense in depth.

## 4. Internal components

```
cmd/remorasftp         minimal entry point
internal/cli        subcommand routing (start, ls, put, ...)
internal/app        engine construction & lifecycle
internal/server     HTTP/WS server, embedded assets, auth middleware
  ├─ handlers_*     REST endpoints
  ├─ auth.go        token, bootstrap, origin & CSRF checks
  └─ webassets/     embedded production build of web/
internal/manager    connection profiles + live sessions
internal/protocol   Client interface
  ├─ sftp_client.go   SFTP (golang.org/x/crypto/ssh + pkg/sftp)
  ├─ ftp_client.go    FTP + explicit/implicit FTPS (net/textproto + crypto/tls)
  ├─ ftp_parse.go     LIST parsing helpers
internal/transfers  streaming queue: workers, progress, cancel, retry
internal/credentials Provider interface; keychain + AES-GCM file fallback
internal/trust      SSH known-hosts + TLS certificate pin store
internal/config     schema-versioned JSON config & profiles
internal/safepath   path normalization / traversal prevention
internal/events     in-process bus + on-disk activity log
```

The **connection manager** is the only component that constructs protocol
clients. Both the HTTP handlers and CLI commands go through it, so session
lifecycle, trust checks, and credential handling can never diverge between
GUIs.

## 5. Protocol adapter model

Each protocol implements the same high-level interface (`protocol.Client`):

`Connect, Close, Capabilities, ServerInfo, List, Stat, Mkdir, Remove,
Rename, Chmod, Upload, Download, ReadPartial`.

Differences are exposed via a **Capabilities** value rather than hidden:

| Capability | SFTP | FTP | FTPS |
| --- | --- | --- | --- |
| Encrypted transport | ✓ | - | ✓ |
| Unix permissions / chmod | ✓ | via SITE CHMOD | via SITE CHMOD |
| Symlinks | ✓ | partial | partial |
| Server-side rename | ✓ | ✓ | ✓ |
| Resume (REST / offsets) | ✓ | ✓ | ✓ |

This lets the UI enable/disable actions honestly (for example, hiding
permission editing when a server has no SITE CHMOD) instead of failing late.

Remote paths always pass through `internal/safepath`: paths are normalized,
absolute, and contained checks prevent traversal. Every filename and path
from a server is treated as untrusted input.

### FTP/FTPS details

The FTP adapter is implemented directly on `net/textproto`-style I/O to give
full control over `AUTH TLS`/`PBSZ`/`PROT P` for explicit FTPS, implicit TLS
wrapping for port 990, `EPSV`/`PASV` data connections, TLS session reuse,
and `SITE CHMOD`. Certificate failures surface a fingerprint and go through
the same explicit-trust flow as SSH host keys; plain FTP produces a visible
insecure-transport warning in the UI.

### SFTP details

Host-key verification uses a strict `HostKeyCallback`. An unknown key returns
a structured `UnknownHostKeyError` carrying the fingerprint; the UI prompts,
the user explicitly trusts, and the decision is stored. Bad passwords and
unparsable keys surface as `AuthError`.

## 6. Transfers

- All uploads/downloads stream through `io.Copy` pipelines between network
  and either an HTTP body or a file - large files are never fully buffered.
- A bounded worker pool implements the concurrency setting; jobs carry
  throttled progress events over the event bus to the browser via WebSocket.
- Cancellation uses `context`; resume uses SFTP offsets and FTP
  `REST`/`APPE` where supported.
- Browser uploads stream the request body straight to the remote; browser
  downloads stream the response, and a `.remorasftp-part` file protects CLI
  downloads until completion. The isolated `tmp/` directory is wiped on
  startup and shutdown.

## 7. Preview isolation

Remote content is classified by extension/MIME:

- Media (image/video/audio) and PDFs are streamed with a restrictive CSP and
  `nosniff`.
- Text/code/data formats are fetched as a base64 JSON envelope and rendered
  by the app - never served as the document origin. Syntax highlighting
  escapes all HTML before tokenizing.
- HTML is served with `Content-Security-Policy: default-src 'none';
  script-src 'none'; sandbox` and rendered only inside
  `<iframe sandbox="">` **without** `allow-same-origin` or `allow-scripts`,
  so the document cannot access the RemoraSFTP origin, the token, or make
  requests. Two independent layers enforce isolation.
- Server-side source files (`.php`, `.py`, `.rb`, `.sh`, ...) are always
  shown as highlighted source. There is no remote code execution path.

## 8. Frontend

React 18 + TypeScript + Vite. The production build emits static files into
`internal/server/webassets`, embedded via `//go:embed all:webassets`. All
assets are local - no CDN dependency and no external network calls. All
user-facing strings come from `web/src/i18n` (`en.ts` complete, `id.ts`
Bahasa Indonesia); add languages by adding a locale file with the same
shape.

## 9. Startup & shutdown

`app.New()` builds the data directory (cleaning stale temp artifacts first),
config store, credential backend, trust store, event bus, managers, and
server. `Shutdown()` cancels transfers, closes sessions, shuts down HTTP
gracefully, and removes the per-instance `instance.json`.
