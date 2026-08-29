// Local-engine API client.
//
// Authentication model (see docs/security.md):
//  - The Go engine generates a per-launch random token. It opens the browser
//    to /bootstrap/<one-time-code>; this page exchanges the code for the
//    token over loopback, then replaces the URL so the code never lingers in
//    history.
//  - The token is kept in sessionStorage (cleared when the tab closes) and
//    sent as an Authorization: Bearer header on every request, plus a custom
//    X-Requested-With header that forces a CORS preflight for state-changing
//    calls (which any cross-origin page cannot satisfy).
//  - WebSocket auth uses the Sec-WebSocket-Protocol subprotocol instead of a
//    query parameter, so the token never appears in a URL.

export interface VersionInfo {
  name: string;
  version: string;
  commit: string;
  buildTime: string;
  goVersion: string;
  os: string;
  arch: string;
}

const TOKEN_KEY = 'remorasftp.token';

class ApiError extends Error {
  status: number;
  code?: string;
  data?: unknown;
  constructor(status: number, message: string, code?: string, data?: unknown) {
    super(message);
    this.status = status;
    this.code = code;
    this.data = data;
  }
}

function token(): string | null {
  return sessionStorage.getItem(TOKEN_KEY);
}

function setToken(t: string) {
  sessionStorage.setItem(TOKEN_KEY, t);
}

function clearToken() {
  sessionStorage.removeItem(TOKEN_KEY);
}

async function request<T = unknown>(
  method: string,
  path: string,
  body?: unknown,
  opts: { raw?: BodyInit; headers?: Record<string, string> } = {},
): Promise<T> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...(opts.headers ?? {}),
  };
  if (method !== 'GET') headers['X-Requested-With'] = 'RemoraSFTP';
  const t = token();
  if (t) headers['Authorization'] = `Bearer ${t}`;

  let payload: BodyInit | undefined;
  if (opts.raw) {
    payload = opts.raw;
  } else if (body !== undefined) {
    headers['Content-Type'] = 'application/json';
    payload = JSON.stringify(body);
  }

  const res = await fetch(path, { method, headers, body: payload });
  const ct = res.headers.get('content-type') ?? '';
  if (!res.ok) {
    let data: unknown = null;
    if (ct.includes('application/json')) {
      try {
        data = await res.json();
      } catch {
        /* ignore */
      }
    }
    const msg =
      (data as Record<string, string> | null)?.error ||
      (data as Record<string, string> | null)?.detail ||
      res.statusText;
    throw new ApiError(
      res.status,
      typeof msg === 'string' ? msg : `request failed (${res.status})`,
      (data as Record<string, string> | null)?.error,
      data,
    );
  }
  if (res.status === 204) return undefined as T;
  if (ct.includes('application/json')) return (await res.json()) as T;
  return undefined as T;
}

export const api = {
  // --- bootstrap / health ---
  async health(): Promise<{ status: string; engine: string; version: string }> {
    const res = await fetch('/api/health', { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new ApiError(res.status, 'engine unavailable');
    return res.json();
  },

  async bootstrap(code: string): Promise<{ token: string; version: VersionInfo }> {
    const res = await request<{ token: string; version: VersionInfo }>('POST', '/api/bootstrap', {
      code,
    });
    setToken(res.token);
    return res;
  },

  handshake() {
    return request<{
      version: VersionInfo;
      startTime: string;
      credentialStore: string;
      onboarded: boolean;
      settings: Settings;
    }>('GET', '/api/handshake');
  },

  version(): Promise<VersionInfo> {
    return request<VersionInfo>('GET', '/api/version');
  },

  hasToken() {
    return token() !== null;
  },

  clearToken,

  // --- connections ---
  connections() {
    return request<{ connections: Connection[] }>('GET', '/api/connections');
  },
  connection(id: string) {
    return request<{ connection: Connection }>('GET', `/api/connections/${id}`);
  },
  saveConnection(c: Partial<Connection> & { id?: string }) {
    const id = (c as Connection).id;
    if (id) return request<{ connection: Connection }>('PUT', `/api/connections/${id}`, c);
    return request<{ connection: Connection }>('POST', '/api/connections', c);
  },
  deleteConnection(id: string) {
    return request<{ deleted: string }>('DELETE', `/api/connections/${id}`);
  },
  duplicateConnection(id: string) {
    return request<{ connection: Connection }>('POST', `/api/connections/${id}/duplicate`);
  },
  testConnection(id: string) {
    return request<{ ok: boolean }>('POST', `/api/connections/${id}/test`);
  },

  // --- sessions ---
  sessions() {
    return request<{ sessions: SessionInfo[] }>('GET', '/api/sessions');
  },
  connect(id: string) {
    return request<{ session: SessionInfo }>('POST', `/api/connections/${id}/connect`);
  },
  disconnect(sessionId: string) {
    return request<{ disconnected: string }>('POST', `/api/sessions/${sessionId}/disconnect`);
  },
  reconnect(sessionId: string) {
    return request<{ session: SessionInfo }>('POST', `/api/sessions/${sessionId}/reconnect`);
  },
  list(sessionId: string, path?: string) {
    const q = path ? `?path=${encodeURIComponent(path)}` : '';
    return request<{ path: string; entries: Entry[]; caps: Capabilities }>(
      'GET',
      `/api/sessions/${sessionId}/list${q}`,
    );
  },
  stat(sessionId: string, path: string) {
    return request<{ entry: Entry }>(
      'GET',
      `/api/sessions/${sessionId}/stat?path=${encodeURIComponent(path)}`,
    );
  },
  mkdir(sessionId: string, path: string) {
    return request('POST', `/api/sessions/${sessionId}/mkdir`, { path });
  },
  remove(sessionId: string, path: string, recursive: boolean) {
    return request('POST', `/api/sessions/${sessionId}/remove`, { path, recursive });
  },
  rename(sessionId: string, from: string, to: string) {
    return request('POST', `/api/sessions/${sessionId}/rename`, { from, to });
  },
  chmod(sessionId: string, path: string, mode: string) {
    return request('POST', `/api/sessions/${sessionId}/chmod`, { path, mode });
  },

  // --- upload / download / preview ---
  uploadUrl(sessionId: string, dir: string, name: string) {
    return `/api/sessions/${sessionId}/upload?dir=${encodeURIComponent(dir)}&name=${encodeURIComponent(name)}`;
  },
  async uploadStream(
    sessionId: string,
    dir: string,
    name: string,
    stream: BodyInit,
    size: number,
  ) {
    await request('POST', this.uploadUrl(sessionId, dir, name), undefined, {
      raw: stream,
      headers: { 'Content-Length': String(size) },
    });
  },
  downloadUrl(sessionId: string, path: string) {
    return `/api/sessions/${sessionId}/download?path=${encodeURIComponent(path)}`;
  },
  previewUrl(sessionId: string, path: string) {
    return `/api/sessions/${sessionId}/preview?path=${encodeURIComponent(path)}`;
  },
  async previewText(sessionId: string, path: string) {
    return request<PreviewResponse>('GET', this.previewUrl(sessionId, path));
  },

  // --- trust ---
  trusted() {
    return request<{ trusted: TrustEntry[] }>('GET', '/api/trust');
  },
  trust(hostPort: string, fingerprint: string) {
    return request('POST', '/api/trust', { hostPort, fingerprint });
  },
  untrust(id: string) {
    return request('DELETE', `/api/trust/${id}`);
  },

  // --- transfers / activity / settings ---
  transfers() {
    return request<{ transfers: TransferJob[] }>('GET', '/api/transfers');
  },
  cancelTransfer(id: string) {
    return request('POST', `/api/transfers/${id}/cancel`);
  },
  retryTransfer(id: string) {
    return request('POST', `/api/transfers/${id}/retry`);
  },
  clearTransfers() {
    return request('POST', '/api/transfers/clear');
  },
  activity() {
    return request<{ activity: ActivityEntry[] }>('GET', '/api/activity');
  },
  clearActivity() {
    return request('DELETE', '/api/activity');
  },
  getSettings() {
    return request<{ settings: Settings }>('GET', '/api/settings');
  },
  saveSettings(s: Settings) {
    return request<{ settings: Settings }>('PUT', '/api/settings', s);
  },
  onboard() {
    return request('POST', '/api/onboarding', {});
  },
};

export { ApiError, clearToken };

// ---- shared types ----
export type Protocol = 'ftp' | 'ftps' | 'sftp';
export type AuthMethod = 'password' | 'key' | 'keyagent' | 'none';

export interface ConnectionSettings {
  ftpsImplicit?: boolean;
  tlsVerify?: boolean;
  passiveMode?: boolean;
  keepAliveSeconds?: number;
  encoding?: string;
}

export interface Connection {
  id: string;
  name: string;
  protocol: Protocol;
  host: string;
  port: number;
  username: string;
  auth: AuthMethod;
  startDir?: string;
  hasSecret?: boolean;
  hasPrivateKey?: boolean;
  settings?: ConnectionSettings;
}

export interface Capabilities {
  protocol: Protocol;
  encrypted: boolean;
  unixPermissions: boolean;
  chmod: boolean;
  chown: boolean;
  symlinks: boolean;
  serverSideRename: boolean;
  serverSideCopy: boolean;
  resumeDownload: boolean;
  resumeUpload: boolean;
  maxPreviewBytes: number;
}

export interface SessionInfo {
  id: string;
  connectionId: string;
  connectionName: string;
  protocol: Protocol;
  host: string;
  state: 'disconnected' | 'connecting' | 'connected' | 'error';
  error?: string;
  startDir?: string;
  cwd?: string;
  capabilities?: Capabilities;
  caps?: Capabilities;
}

export type EntryType = 'file' | 'dir' | 'symlink' | 'other';

export interface Entry {
  name: string;
  path: string;
  type: EntryType;
  size: number;
  modTime?: string;
  mode?: number;
  permissions?: string;
  owner?: string;
  group?: string;
  isSymlink?: boolean;
  linkTarget?: string;
  mimeType?: string;
}

export interface TrustEntry {
  id: string;
  kind: string;
  fingerprint: string;
  algorithm?: string;
  note?: string;
  trustedAt: string;
}

export type TransferStatus =
  | 'queued'
  | 'running'
  | 'paused'
  | 'completed'
  | 'failed'
  | 'canceled';

export interface TransferJob {
  id: string;
  direction: 'upload' | 'download';
  status: TransferStatus;
  sessionId: string;
  connectionName?: string;
  remotePath: string;
  name: string;
  total: number;
  done: number;
  error?: string;
  attempts: number;
  queuedAt: string;
  startedAt?: string;
  finishedAt?: string;
  speed: number;
  etaSeconds: number;
  resumable: boolean;
}

export interface ActivityEntry {
  id: number;
  time: string;
  level: 'info' | 'warning' | 'error';
  type: string;
  message: string;
  connectionName?: string;
  sessionId?: string;
}

export interface Settings {
  language: string;
  theme: 'system' | 'light' | 'dark';
  defaultView: 'list' | 'grid';
  showHidden: boolean;
  confirmDeletes: boolean;
  openBrowserOnStart: boolean;
  concurrentTransfers: number;
  remoteAccess: boolean;
  listenAddress: string;
  logLevel: 'off' | 'error' | 'warning' | 'info' | 'debug';
  reducedMotion: boolean;
}

export interface PreviewResponse {
  kind: 'image' | 'media' | 'pdf' | 'text' | 'code' | 'data' | 'html' | 'unsupported';
  mime: string;
  entry?: Entry;
  content?: string; // base64
  truncated?: boolean;
  encoding?: string;
  message?: string;
  sourceNote?: string;
}
