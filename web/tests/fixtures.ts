import { test as base, expect, type Page } from '@playwright/test';
import type { ColumnDefinition, DBInfo, EditColumnRequest, QueryResult, TableInfo } from '../src/api/client';

export const inventory: TableInfo = {
  Name: 'inventory',
  Columns: [
    { Name: 'id', Type: 'INTEGER', Nullable: false, DefaultValue: '', PrimaryKey: true },
    { Name: 'sku', Type: 'TEXT', Nullable: false, DefaultValue: '', PrimaryKey: false },
    { Name: 'stock', Type: 'INTEGER', Nullable: true, DefaultValue: '0', PrimaryKey: false },
  ],
  Indexes: [{ Name: 'inventory_sku', Columns: ['sku'], Unique: true }],
  RowCount: 3,
};

export function createMockState() {
  return {
    databases: [
      { Name: 'workbench_demo', Path: '/fixtures/workbench_demo.sqlite', SizeBytes: 24576, Active: true, TableCount: 2 },
      { Name: 'analytics_demo', Path: '/fixtures/analytics_demo.sqlite', SizeBytes: 12288, Active: false, TableCount: 1 },
    ] satisfies DBInfo[],
    tables: [
      structuredClone(inventory),
      { Name: 'events', Columns: inventory.Columns, Indexes: [], RowCount: 12 },
    ] satisfies TableInfo[],
    rows: [['1', 'SKU-001', '24'], ['2', 'SKU-002', '8'], ['3', 'SKU-003', 'NULL']],
    discovered: [
      { id: 1, name: 'inventory.sqlite', source_path: '/fixtures/workspace/inventory.sqlite', size_bytes: 24576, sqlite_version: '3.50.0', page_size: 4096, journal_mode: 'wal', last_modified: '2026-01-01T00:00:00Z', status: 'discovered', error_message: '', github_repo: 'example/fixture-workspace', github_url: 'https://github.com/example/fixture-workspace', priority: 'workspace', is_replica: false, favorite: false, available: true },
      { id: 2, name: 'events.sqlite', source_path: '/fixtures/app/events.sqlite', size_bytes: 12288, sqlite_version: '3.50.0', page_size: 4096, journal_mode: 'wal', last_modified: '2026-01-01T00:00:00Z', status: 'replicating', error_message: '', github_repo: '', github_url: '', priority: 'app_data', is_replica: false, favorite: true, available: true },
      { id: 3, name: 'archive.sqlite', source_path: '/fixtures/archive/archive.sqlite', size_bytes: 8192, sqlite_version: '3.50.0', page_size: 4096, journal_mode: 'delete', last_modified: '2026-01-01T00:00:00Z', status: 'paused', error_message: '', github_repo: '', github_url: '', priority: 'other', is_replica: false, favorite: false, available: false },
    ],
    statements: [] as string[],
    inserts: [] as Array<{ Columns: string[]; Values: Array<string | null> }>,
    fieldEdits: [] as EditColumnRequest[],
    scans: [] as string[][],
    restores: [] as number[],
    failures: new Map<string, string>(),
    unexpected: [] as string[],
  };
}

export type MockState = ReturnType<typeof createMockState>;

export async function mockApi(page: Page, state = createMockState()) {
  await page.route(/^https?:\/\/[^/]+\/api\//, async (route) => {
    const request = route.request();
    const path = new URL(request.url()).pathname;
    const method = request.method();
    const failure = state.failures.get(path);
    const respond = (json: unknown, status = 200) => route.fulfill({ status, json });
    const rows = (): QueryResult => ({
      Columns: inventory.Columns.map(({ Name, Type }) => ({ Name, Type })),
      Rows: state.rows,
      TotalCount: state.rows.length,
    });

    if (failure) return respond({ error: failure }, 503);
    if (path === '/api/stats') {
      return respond({ version: 'test', uptime_seconds: 7380, active_databases: 1, memory_alloc: 12582912, memory_sys: 25165824, goroutines: 8 });
    }
    if (path === '/api/databases') {
      if (method === 'POST') {
        const { name } = request.postDataJSON() as { name: string };
        const database = { Name: name, Path: `/fixtures/${name}.sqlite`, SizeBytes: 0, Active: true, TableCount: 0 };
        state.databases.push(database);
        return respond(database);
      }
      return respond(state.databases);
    }
    const managed = path.match(/^\/api\/databases\/([^/]+)(.*)$/);
    if (managed) {
      const name = decodeURIComponent(managed[1]);
      const endpoint = managed[2];
      if (!endpoint && method === 'DELETE') {
        state.databases = state.databases.filter((database) => database.Name !== name);
        return respond({ success: true });
      }
      if (endpoint === '/schema') return respond(name === 'empty_demo' ? [] : state.tables);
      if (endpoint === '/query') {
        const sql = (request.postDataJSON() as { sql: string }).sql;
        state.statements.push(sql);
        if (/FROM inventory/i.test(sql)) return respond(rows());
        const answer = sql.match(/SELECT\s+(\d+)/i)?.[1] ?? '42';
        return respond({ Columns: [{ Name: 'answer', Type: 'INTEGER' }], Rows: [[answer]], TotalCount: 1 });
      }
      if (endpoint === '/tables' && method === 'POST') {
        const table = request.postDataJSON() as { Name: string; Columns: Array<{ Name: string; Type: string; NotNull: boolean; PrimaryKey: boolean }> };
        state.tables.push({
          Name: table.Name,
          Columns: table.Columns.map((column) => ({ ...column, Nullable: !column.NotNull, DefaultValue: '' })),
          Indexes: [],
          RowCount: 0,
        });
        return respond({ success: true });
      }
      if (endpoint.endsWith('/columns')) {
        const tableName = decodeURIComponent(endpoint.split('/')[2]);
        const table = state.tables.find((entry) => entry.Name === tableName);
        if (!table) return respond({ error: 'Mock table not found' }, 404);
        if (method === 'PUT') {
          const edit = request.postDataJSON() as EditColumnRequest;
          const column = table.Columns.find((entry) => entry.Name === edit.OriginalName);
          if (!column) return respond({ error: 'Mock field not found' }, 404);
          state.fieldEdits.push(edit);
          Object.assign(column, { Name: edit.Name, Type: edit.Type, Nullable: edit.Nullable, DefaultValue: edit.DefaultValue ?? '' });
        } else if (method === 'POST') {
          const column = request.postDataJSON() as ColumnDefinition;
          table.Columns.push({ Name: column.Name, Type: column.Type, Nullable: !column.NotNull, PrimaryKey: column.PrimaryKey, DefaultValue: column.DefaultValue ?? '' });
        } else {
          return respond({ error: 'Mock method not allowed' }, 405);
        }
        return respond({ success: true });
      }
      if (endpoint.endsWith('/rows') && method === 'POST') {
        const insert = request.postDataJSON() as MockState['inserts'][number];
        state.inserts.push(insert);
        state.rows.push([String(state.rows.length + 1), insert.Values[0] ?? 'NULL', '0']);
        state.tables[0].RowCount = state.rows.length;
        return respond({ RowsAffected: 1, LastInsertID: state.rows.length });
      }
      if (endpoint.startsWith('/tables/')) return respond(rows());
    }
    if (path === '/api/scan') {
      if (method === 'POST') {
        state.scans.push((request.postDataJSON() as { paths: string[] }).paths);
        return respond({ scanned: state.discovered.length, files: [] });
      }
      return respond({ in_progress: false });
    }
    if (path === '/api/discovered') return respond(state.discovered);
    const discovered = path.match(/^\/api\/discovered\/(\d+)(.*)$/);
    if (discovered) {
      const id = Number(discovered[1]);
      const endpoint = discovered[2];
      const database = state.discovered.find((entry) => entry.id === id);
      if (!endpoint && method === 'PATCH') {
        if (database) database.favorite = (request.postDataJSON() as { favorite: boolean }).favorite;
        return respond({ success: true });
      }
      if (!endpoint && method === 'DELETE') {
        state.discovered = state.discovered.filter((entry) => entry.id !== id);
        return respond({ success: true });
      }
      if (endpoint === '/schema') return respond([inventory]);
      if (endpoint === '/table' || endpoint === '/query') return respond(rows());
      if (endpoint === '/replicate') {
        if (database) database.status = method === 'DELETE' ? 'paused' : 'replicating';
        return respond({ success: true });
      }
      if (endpoint === '/restore') {
        state.restores.push(id);
        return respond({ success: true });
      }
      if (endpoint === '/versions') {
        return respond([{ version: 0, schema_hash: 'synthetic-schema-hash', schema_sql: 'CREATE TABLE inventory (id INTEGER PRIMARY KEY);', detected_at: '2026-01-01T00:00:00Z' }]);
      }
      if (endpoint === '/transitions') return respond([]);
      if (endpoint === '/snapshots') {
        return respond([{ id: 1, version: 1, schema_version: 0, created_at: '2026-01-01T00:00:00Z', size_bytes: 24576, trigger: 'baseline' }]);
      }
    }
    state.unexpected.push(`${method} ${path}`);
    return respond({ error: `Unexpected mock request: ${method} ${path}` }, 500);
  });
  await page.route('https://cdn.jsdelivr.net/**', (route) => route.abort());
  return state;
}

export const test = base.extend<{ mock: MockState }>({
  mock: [async ({ page }, use) => {
    const state = await mockApi(page);
    await use(state);
    expect(state.unexpected).toEqual([]);
  }, { auto: true }],
});

export { expect };
