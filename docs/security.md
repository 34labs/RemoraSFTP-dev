# RemoraSFTP security model

This document states the concrete threat boundaries and guarantees. RemoraSFTP
is a local application that connects to remote file servers you choose.

## What the trust boundary looks like

```
 Browser tab                 Local RemoraSFTP engine             Remote server
 (any origin, e.g. a     ──▶ 127.0.0.1 HTTP/WS            ──▶ FTP / FTPS / SFTP
  malicious web page)        (your machine)                  (host you configure)
```

Two boundaries matter:

1. **Web page → local engine.** Any browser tab can send requests to
   `127.0.0.1`. RemoraSFTP must not let a hostile page perform privileged
   actions or steal data from an already-running instance.
2. **Local engine → remote server.** Credentials and file traffic flow over
   FTP/FTPS/SFTP to a server you configured.

We do not treat the remote server's contents as trusted: every path,
filename, and file body from a server is handled as untrusted data.

## Local API authentication (boundary 1)

- **Loopback bind.** The listener binds `127.0.0.1` only. Other machines on
  the LAN cannot reach it. Binding beyond loopback requires an explicit,
  documented opt-in setting and still requires the token.
- **Per-launch token.** Each engine run generates a random 256-bit token. It
  is shown in the terminal (CLI) and written to a `0600` `instance.json` in
  the data directory for the local status command.
- **One-time bootstrap.** The browser is opened with a single-use code
  (`/bootstrap/<code>`) that is exchanged once (short expiry) for the token,
  after which the URL is rewritten so the code does not remain in history.
- **Authorization.** Every `/api/*` request (except health and bootstrap
  exchange) must present the token (`Authorization: Bearer`, an
  `X-RemoraSFTP-Token` header, or the WebSocket subprotocol). Comparison is
  constant-time.
- **Origin validation.** Requests carrying an `Origin` header must match the
  exact loopback scheme/host/port; cross-origin requests are rejected
  before token checks.
- **CSRF defense.** State-changing requests require a custom
  `X-Requested-With: RemoraSFTP` header. A browser cannot set that header
  cross-origin without a successful CORS preflight; the engine never emits
  `Access-Control-Allow-Origin`, so the request never reaches application
  logic.
- **No secrets in URLs.** WebSocket auth uses the
  `Sec-WebSocket-Protocol` header rather than query parameters.
- **Browser hardening headers.** Every response sets
  `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`,
  `Referrer-Policy: no-referrer`,
  `Cross-Origin-Opener-Policy: same-origin`,
  `Cross-Origin-Resource-Policy: same-origin`, and a strict
  `Content-Security-Policy` on the app shell (`default-src 'none'`,
  same-origin scripts/styles/connect, no frame ancestors).

CORS is explicitly **not** relied upon as a boundary - it is one layer among
token, header, and origin checks.

## Remote connections (boundary 2)

### Protocols & transport

- **SFTP** runs over SSH with the host key callback enforced. Unknown keys
  are refused and surfaced for explicit trust.
- **FTPS** uses TLS 1.2+. Explicit mode negotiates `AUTH TLS`, `PBSZ 0`,
  `PROT P` so the data channel is encrypted; implicit mode connects with
  TLS from the start. Certificates are verified; when verification fails,
  the fingerprint is presented and can be explicitly pinned.
- **Plain FTP** is supported for legacy servers but the UI and CLI display a
  visible warning that credentials and data travel unencrypted. Prefer FTPS.

### Host keys and certificate pins

- The trust store (`hostkeys.json`) records explicit decisions per
  `host:port`.
- A different key/certificate on a later connection is **not** trusted
  silently; it triggers a new prompt.
- Revoking a decision is supported in the Security & Privacy area.

## Credentials

- Passwords, private keys, and key passphrases are stored separately from
  normal configuration:
  - Preferred: the OS-native keychain via
    `github.com/zalando/go-keyring` - Windows Credential Manager,
    macOS Keychain, and Secret Service (D-Bus) on Linux.
  - Fallback: an encrypted file (`secrets/`) using AES-256-GCM with a random
    per-install 256-bit key (`master.key`, file mode `0600`, directory
    `0700`). Tampering causes GCM authentication to fail.
- The file fallback is clearly identified in the UI as the active backend;
  it is device-local and never uploaded anywhere.
- Secrets are **write-only** across the API: the browser can submit them and
  test for their presence (`hasSecret`, `hasPrivateKey`), but no endpoint
  ever returns secret material.
- Secrets are never logged (the event/activity log only records high-level
  actions such as "connected" or "upload completed"), never embedded in
  URLs, and never written to config files.
- Secret buffers exist only for the duration of a connection attempt.

## Remote content as untrusted input

- **Paths:** every remote path is normalized by `internal/safepath` before
  use; `..` traversal and absolute escapes are rejected. Standalone file
  names for mkdir/rename/upload are validated (no separators, no NUL/control
  characters).
- **Previews** are isolated as described in
  [architecture.md](architecture.md#7-preview-isolation):
  - HTML renders inside `<iframe sandbox="">` without `allow-scripts` and
    without `allow-same-origin`, served with a locking CSP. The document
    cannot read app state, storage, or make requests.
  - Code/text highlighting escapes HTML before adding colors.
  - Server-side code (PHP, Python, Ruby, shell, ...) is source-only; RemoraSFTP
    never executes it and provides no execution environment.
- Downloads/preview responses are sent with `nosniff`; media is constrained
  by CSP.
- File metadata (owner/group strings, names, targets) is rendered as text,
  not HTML.

## Application data and logging

- All data lives under the OS user config directory (see README) with `0700`
  directories and `0600` files.
- The activity log is a ring of high-level, sanitized events. It never
  contains credentials, key material, request bodies, or file contents. It
  can be disabled (log level "off") and cleared from the UI.
- Temporary transfer artifacts live in an isolated `tmp/` directory and are
  removed after success, failure, cancellation, and on startup/shutdown.

## Out-of-scope / explicit non-goals

- RemoraSFTP does not protect against a fully compromised local user account.
  Anyone able to run code as your user can access the loopback token from
  `instance.json` and the local credential stores. (OS keychain access is,
  where supported, additionally gated by login/session.)
- It does not guarantee the remote server's goodwill - pinning host keys
  prevents MITM after first trust, but only if you verify the fingerprint on
  first contact.
- Plain FTP cannot provide confidentiality; use SFTP or FTPS.
- The application does not phone home, auto-update against an online
  service, or include analytics. Signed updates are a documented future
  extension; core operation remains offline.

## Security invariants that must hold in every change

If you contribute, the following must remain true:

1. Default bind is loopback only; non-loopback requires explicit opt-in.
2. No `/api/*` route bypasses token + origin + custom-header checks.
3. Secrets never appear in logs, URLs, DOM storage, or API responses.
4. Unknown SSH host keys / unverified certificates never connect silently.
5. Remote HTML never executes inside the privileged origin.
6. Remote paths are normalized and containment-checked before use.
7. No analytics, telemetry, debug endpoints, or development credentials in
   release builds.
