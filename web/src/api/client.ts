export interface DBInfo {
  Name: string;
  Path: string;
  SizeBytes: number;
  Active: boolean;
  TableCount: number;
}

export interface Column {
  Name: string;
  Type: string;
}

export interface QueryResult {
  Columns: Column[];
  Rows: string[][];
  TotalCount: number;
}

export interface ExecResult {
  RowsAffected: number;
  LastInsertID: number;
}

export interface ColumnInfo {
  Name: string;
  Type: string;
  Nullable: boolean;
  DefaultValue: string;
  PrimaryKey: boolean;
}

export interface IndexInfo {
  Name: string;
  Columns: string[];
  Unique: boolean;
}

export interface TableInfo {
  Name: string;
  Columns: ColumnInfo[];
  Indexes: IndexInfo[];
  RowCount: number;
}

export interface ColumnDefinition {
  Name: string;
  Type: string;
  NotNull: boolean;
  PrimaryKey: boolean;
  DefaultValue?: string;
}

export interface EditColumnRequest {
  OriginalName: string;
  Name: string;
  Type: string;
  Nullable: boolean;
  DefaultValue: string | null;
}

export interface StatsInfo {
  version: string;
  uptime_seconds: number;
  active_databases: number;
  memory_alloc: number;
  memory_sys: number;
  goroutines: number;
}

export type APISource =
  | { managed_database: string; discovered_id?: never }
  | { discovered_id: number; managed_database?: never };

export interface APIExposure {
  alias: string;
  database_id: number;
  managed_database?: string;
  tables: string[];
  writable: boolean;
}

let adminToken: string | null = null;
const authenticationListeners = new Set<() => void>();

export function setAdminToken(token: string | null) { adminToken = token; }
export function hasAdminToken() { return adminToken !== null; }
export function onAuthenticationRequired(listener: () => void) {
  authenticationListeners.add(listener);
  return () => { authenticationListeners.delete(listener); };
}
export function signOut() {
  adminToken = null;
  authenticationListeners.forEach((listener) => listener());
}

export class APIError extends Error {
  readonly status: number;
  constructor(message: string, status: number) { super(message); this.status = status; }
}

export const CRUD_METHODS = ['GET', 'POST', 'PATCH', 'DELETE'] as const;
export type CRUDMethod = typeof CRUD_METHODS[number];

export interface APIRequestOptions {
  method: CRUDMethod;
  url: string;
  headers: string;
  body: string;
  token: string;
  useAdminSession: boolean;
  signal: AbortSignal;
}

export interface APIRequestResponse {
  status: number;
  statusText: string;
  headers: Record<string, string>;
  body: string;
  durationMs: number;
  truncated: boolean;
}

export async function sendAPIRequest(options: APIRequestOptions): Promise<APIRequestResponse> {
  const url = new URL(options.url, window.location.origin);
  if (url.origin !== window.location.origin) throw new Error('Requests must use the same origin as this workspace.');
  if (!url.pathname.startsWith('/api/data/')) throw new Error("The request editor only calls this daemon's /api/data/ routes.");
  if (url.username || url.password || url.hash) throw new Error('Use the authentication controls for credentials and omit URL fragments.');
  let values: unknown;
  try { values = JSON.parse(options.headers || '{}'); }
  catch { throw new Error('Request headers must be valid JSON.'); }
  if (!values || typeof values !== 'object' || Array.isArray(values)) throw new Error('Request headers must be a JSON object with string values.');
  const headers = new Headers();
  for (const [name, value] of Object.entries(values)) {
    if (typeof value !== 'string') throw new Error(`Header "${name}" must have a string value.`);
    if (name.toLowerCase() === 'authorization') throw new Error('Set Authorization using the request authentication controls.');
    headers.set(name, value);
  }
  const token = options.useAdminSession ? adminToken : options.token.trim();
  if (options.useAdminSession && !token) throw new Error('Sign in before using the admin session.');
  if (token) headers.set('Authorization', `Bearer ${token}`);
  const hasBody = options.method === 'POST' || options.method === 'PATCH';
  if (hasBody) {
    try { JSON.parse(options.body); }
    catch { throw new Error('Request body must be valid JSON.'); }
    if (new TextEncoder().encode(options.body).length > 1024 * 1024) throw new Error('Request body exceeds the 1 MiB API limit.');
    if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json');
  }
  const started = performance.now();
  const signal = AbortSignal.any([options.signal, AbortSignal.timeout(30_000)]);
  const response = await fetch(url.href, {
    method: options.method,
    headers,
    body: hasBody ? options.body : undefined,
    signal,
    credentials: 'omit',
    redirect: 'error',
    cache: 'no-store',
  });
  const reader = response.body?.getReader();
  const decoder = new TextDecoder();
  const chunks: string[] = [];
  const maximum = 1024 * 1024;
  let bytes = 0;
  let truncated = false;
  if (reader) {
    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;
        const remaining = maximum - bytes;
        const captured = value.subarray(0, remaining);
        chunks.push(decoder.decode(captured, { stream: true }));
        bytes += captured.byteLength;
        if (captured.byteLength < value.byteLength) {
          truncated = true;
          await reader.cancel();
          break;
        }
      }
      chunks.push(decoder.decode());
    } finally {
      reader.releaseLock();
    }
  }
  return {
    status: response.status,
    statusText: response.statusText,
    headers: Object.fromEntries(response.headers.entries()),
    body: chunks.join(''),
    durationMs: performance.now() - started,
    truncated,
  };
}

export interface DiscoveredDB {
  ID: number;
  Name: string;
  SourcePath: string;
  SQLiteVersion: string;
  PageSize: number;
  JournalMode: string;
  SizeBytes: number;
  LastModified: string;
  Status: string;
  ErrorMessage: string;
  GitHubRepo: string;
  GitHubURL: string;
  Priority: string;
  IsReplica: boolean;
  Favorite: boolean;
  Available: boolean;
}

export interface SnapshotInfo {
  ID: number;
  Version: number;
  SchemaVersion: number;
  CreatedAt: string;
  SizeBytes: number;
  Trigger: string;
}

export interface SchemaVersionInfo {
  Version: number;
  SchemaHash: string;
  SchemaSQL: string;
  DetectedAt: string;
}

export interface SchemaTransitionInfo {
  FromVersion: number;
  ToVersion: number;
  Summary: string;
  DetectedDDL: string;
  DetectedAt: string;
}

interface RawDiscoveredDB {
  id: number;
  name: string;
  source_path: string;
  sqlite_version: string;
  page_size: number;
  journal_mode: string;
  size_bytes: number;
  last_modified: string;
  status: string;
  error_message: string;
  github_repo: string;
  github_url: string;
  priority: string;
  is_replica: boolean;
  favorite: boolean;
  available: boolean;
}

interface RawSnapshotInfo {
  id: number;
  version: number;
  schema_version: number;
  created_at: string;
  size_bytes: number;
  trigger: string;
}

interface RawSchemaVersionInfo {
  version: number;
  schema_hash: string;
  schema_sql: string;
  detected_at: string;
}

interface RawSchemaTransitionInfo {
  from_version: number;
  to_version: number;
  summary: string;
  detected_ddl: string;
  detected_at: string;
}

interface ScanFile {
  id: number;
  name: string;
  source_path: string;
  size_bytes: number;
  sqlite_version: string;
  priority: string;
  github_repo: string;
}

export interface ScanResult {
  scanned: number;
  files: ScanFile[];
}

export interface ScanStatus {
  in_progress: boolean;
}

function toDiscoveredDB(db: RawDiscoveredDB): DiscoveredDB {
  return {
    ID: db.id,
    Name: db.name,
    SourcePath: db.source_path,
    SQLiteVersion: db.sqlite_version,
    PageSize: db.page_size,
    JournalMode: db.journal_mode,
    SizeBytes: db.size_bytes,
    LastModified: db.last_modified,
    Status: db.status,
    ErrorMessage: db.error_message,
    GitHubRepo: db.github_repo,
    GitHubURL: db.github_url,
    Priority: db.priority,
    IsReplica: db.is_replica,
    Favorite: db.favorite,
    Available: db.available,
  };
}

function toSnapshotInfo(snapshot: RawSnapshotInfo): SnapshotInfo {
  return {
    ID: snapshot.id,
    Version: snapshot.version,
    SchemaVersion: snapshot.schema_version,
    CreatedAt: snapshot.created_at,
    SizeBytes: snapshot.size_bytes,
    Trigger: snapshot.trigger,
  };
}

function toSchemaVersionInfo(version: RawSchemaVersionInfo): SchemaVersionInfo {
  return {
    Version: version.version,
    SchemaHash: version.schema_hash,
    SchemaSQL: version.schema_sql,
    DetectedAt: version.detected_at,
  };
}

function toSchemaTransitionInfo(transition: RawSchemaTransitionInfo): SchemaTransitionInfo {
  return {
    FromVersion: transition.from_version,
    ToVersion: transition.to_version,
    Summary: transition.summary,
    DetectedDDL: transition.detected_ddl,
    DetectedAt: transition.detected_at,
  };
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const requestToken = adminToken;
  const headers = new Headers(options?.headers);
  headers.set('Content-Type', 'application/json');
  if (requestToken) headers.set('Authorization', `Bearer ${requestToken}`);
  const res = await fetch(path, {
    ...options,
    headers,
  });

  const data = res.status === 204 ? undefined : await res.json();
  if (!res.ok) {
    if (res.status === 401 && adminToken === requestToken) signOut();
    throw new APIError(data?.error || `Request failed: ${res.status}`, res.status);
  }
  return data as T;
}

export const api = {
  getAuthentication: () => request<{ required: boolean }>('/api/auth'),
  listExposures: () => request<APIExposure[]>('/api/exposures'),
  createExposure: (source: APISource, alias: string, tables: string[], writable: boolean) =>
    request<{ exposure: APIExposure; token: string }>('/api/exposures', {
      method: 'POST', body: JSON.stringify({ ...source, alias, tables, writable }),
    }),
  revokeExposure: (alias: string) => request<void>(`/api/exposures/${encodeURIComponent(alias)}`, { method: 'DELETE' }),
  rotateExposure: (alias: string) => request<{ token: string }>(`/api/exposures/${encodeURIComponent(alias)}/token`, { method: 'POST' }),
  listDatabases: () => request<DBInfo[]>('/api/databases'),

  createDatabase: (name: string) =>
    request<DBInfo>('/api/databases', {
      method: 'POST',
      body: JSON.stringify({ name }),
    }),

  dropDatabase: (name: string) =>
    request<{ success: boolean }>(`/api/databases/${name}`, { method: 'DELETE' }),

  getDatabaseInfo: (name: string) => request<DBInfo>(`/api/databases/${name}`),

  getSchema: (dbName: string) => request<TableInfo[]>(`/api/databases/${dbName}/schema`),

  getTables: (dbName: string) =>
    request<{ tables: string[] }>(`/api/databases/${dbName}/tables`),

  createTable: (dbName: string, name: string, columns: ColumnDefinition[]) =>
    request<{ success: boolean }>(`/api/databases/${encodeURIComponent(dbName)}/tables`, {
      method: 'POST',
      body: JSON.stringify({ Name: name, Columns: columns }),
    }),

  addColumn: (dbName: string, table: string, column: ColumnDefinition) =>
    request<{ success: boolean }>(
      `/api/databases/${encodeURIComponent(dbName)}/tables/${encodeURIComponent(table)}/columns`,
      {
      method: 'POST',
      body: JSON.stringify(column),
      },
    ),

  editColumn: (dbName: string, table: string, column: EditColumnRequest) =>
    request<{ success: boolean }>(
      `/api/databases/${encodeURIComponent(dbName)}/tables/${encodeURIComponent(table)}/columns`,
      {
        method: 'PUT',
        body: JSON.stringify(column),
      },
    ),

  getTableData: (dbName: string, table: string, limit = 100, offset = 0) =>
    request<QueryResult>(
      `/api/databases/${encodeURIComponent(dbName)}/tables/${encodeURIComponent(table)}?limit=${limit}&offset=${offset}`
    ),

  insertRow: (dbName: string, table: string, columns: string[], values: Array<string | null>) =>
    request<ExecResult>(
      `/api/databases/${encodeURIComponent(dbName)}/tables/${encodeURIComponent(table)}/rows`,
      {
        method: 'POST',
        body: JSON.stringify({ Columns: columns, Values: values }),
      },
    ),

  executeQuery: (dbName: string, sql: string, params?: string[]) =>
    request<QueryResult | ExecResult>(`/api/databases/${dbName}/query`, {
      method: 'POST',
      body: JSON.stringify({ sql, params }),
    }),

  getHealth: () => request<{ status: string; version: string }>('/api/health'),

  getStats: () => request<StatsInfo>('/api/stats'),

  scanDatabases: (paths?: string[]) =>
    request<ScanResult>('/api/scan', {
      method: 'POST',
      body: paths ? JSON.stringify({ paths }) : undefined,
    }),
  getScanStatus: () => request<ScanStatus>('/api/scan'),
  listDiscovered: async () =>
    (await request<RawDiscoveredDB[]>('/api/discovered')).map(toDiscoveredDB),
  getDiscoveredSchema: (id: number) =>
    request<TableInfo[]>(`/api/discovered/${id}/schema`),
  getDiscoveredTable: (id: number, table: string, limit = 100, offset = 0) =>
    request<QueryResult>(
      `/api/discovered/${id}/table?name=${encodeURIComponent(table)}&limit=${limit}&offset=${offset}`,
    ),
  queryDiscovered: (id: number, sql: string, params?: string[]) =>
    request<QueryResult>(`/api/discovered/${id}/query`, {
      method: 'POST',
      body: JSON.stringify({ sql, params, limit: 1000 }),
    }),
  getDiscovered: async (id: number) =>
    toDiscoveredDB(await request<RawDiscoveredDB>(`/api/discovered/${id}`)),
  deleteDiscovered: (id: number) =>
    request<{ success: boolean }>(`/api/discovered/${id}`, { method: 'DELETE' }),
  updateFavorite: (id: number, favorite: boolean) =>
    request<{ success: boolean; favorite: boolean }>(`/api/discovered/${id}`, {
      method: 'PATCH',
      body: JSON.stringify({ favorite }),
    }),
  startReplication: (id: number) => request<{ success: boolean }>(`/api/discovered/${id}/replicate`, { method: 'POST' }),
  stopReplication: (id: number) => request<{ success: boolean }>(`/api/discovered/${id}/replicate`, { method: 'DELETE' }),
  restoreSnapshot: (id: number, version?: number) =>
    request<{ success: boolean }>(`/api/discovered/${id}/restore`, {
      method: 'POST',
      body: JSON.stringify(version != null ? { version } : {}),
    }),
  listSnapshots: async (id: number) =>
    (await request<RawSnapshotInfo[]>(`/api/discovered/${id}/snapshots`)).map(toSnapshotInfo),
  listVersions: async (id: number) =>
    (await request<RawSchemaVersionInfo[]>(`/api/discovered/${id}/versions`)).map(toSchemaVersionInfo),
  listTransitions: async (id: number) =>
    (await request<RawSchemaTransitionInfo[]>(`/api/discovered/${id}/transitions`)).map(toSchemaTransitionInfo),
};
