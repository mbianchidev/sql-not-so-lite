const BASE_URL = import.meta.env.DEV ? 'http://localhost:8080' : '';

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

export interface StatsInfo {
  version: string;
  uptime_seconds: number;
  active_databases: number;
  memory_alloc: number;
  memory_sys: number;
  goroutines: number;
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
};
