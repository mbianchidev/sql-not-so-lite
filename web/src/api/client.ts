const BASE_URL = import.meta.env.DEV ? 'http://localhost:9147' : '';

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

export interface StatsInfo {
  version: string;
  uptime_seconds: number;
  active_databases: number;
  memory_alloc: number;
  memory_sys: number;
  goroutines: number;
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
  const res = await fetch(`${BASE_URL}${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...options?.headers },
  });

  const data = await res.json();
  if (!res.ok) {
    throw new Error(data.error || `Request failed: ${res.status}`);
  }
  return data as T;
}

export const api = {
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

  getTableData: (dbName: string, table: string, limit = 100, offset = 0) =>
    request<QueryResult>(
      `/api/databases/${dbName}/tables/${table}?limit=${limit}&offset=${offset}`
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
  listDiscovered: async () =>
    (await request<RawDiscoveredDB[]>('/api/discovered')).map(toDiscoveredDB),
  getDiscovered: async (id: number) =>
    toDiscoveredDB(await request<RawDiscoveredDB>(`/api/discovered/${id}`)),
  deleteDiscovered: (id: number) =>
    request<{ success: boolean }>(`/api/discovered/${id}`, { method: 'DELETE' }),
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
