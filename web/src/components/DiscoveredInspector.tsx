import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Editor from '@monaco-editor/react';
import {
  api,
  type DiscoveredDB,
  type QueryResult,
  type TableInfo,
} from '../api/client';
import { ResultsTable } from './ResultsTable';
import { SchemaTimeline } from './SchemaTimeline';

interface Props {
  database: DiscoveredDB;
}

type InspectorTab = 'browse' | 'query' | 'history';

const PAGE_SIZE = 100;

export function DiscoveredInspector({ database }: Props) {
  const [activeTab, setActiveTab] = useState<InspectorTab>('browse');
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [tableData, setTableData] = useState<QueryResult | null>(null);
  const [offset, setOffset] = useState(0);
  const [loadingSchema, setLoadingSchema] = useState(false);
  const [loadingTable, setLoadingTable] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sql, setSql] = useState('SELECT * FROM sqlite_master WHERE type = \'table\' ORDER BY name;');
  const [queryResult, setQueryResult] = useState<QueryResult | null>(null);
  const [queryError, setQueryError] = useState<string | null>(null);
  const [runningQuery, setRunningQuery] = useState(false);
  const sqlRef = useRef(sql);

  const selected = useMemo(
    () => tables.find((table) => table.Name === selectedTable) ?? null,
    [selectedTable, tables],
  );

  const loadSchema = useCallback(async () => {
    if (!database.Available) return;
    setLoadingSchema(true);
    setError(null);
    try {
      const schema = await api.getDiscoveredSchema(database.ID);
      setTables(schema);
      setSelectedTable((current) => (
        current && schema.some((table) => table.Name === current)
          ? current
          : schema[0]?.Name ?? null
      ));
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to inspect database schema');
    } finally {
      setLoadingSchema(false);
    }
  }, [database.Available, database.ID]);

  useEffect(() => {
    setActiveTab('browse');
    setTables([]);
    setSelectedTable(null);
    setTableData(null);
    setOffset(0);
    setError(null);
    setQueryResult(null);
    setQueryError(null);
    void loadSchema();
  }, [database.ID, loadSchema]);

  useEffect(() => {
    if (!selectedTable || !database.Available) {
      setTableData(null);
      return;
    }
    let active = true;
    setLoadingTable(true);
    setError(null);
    void api.getDiscoveredTable(database.ID, selectedTable, PAGE_SIZE, offset)
      .then((result) => {
        if (active) setTableData(result);
      })
      .catch((err) => {
        if (active) setError(err instanceof Error ? err.message : 'Failed to load table data');
      })
      .finally(() => {
        if (active) setLoadingTable(false);
      });
    return () => {
      active = false;
    };
  }, [database.Available, database.ID, offset, selectedTable]);

  const selectTable = (name: string) => {
    setSelectedTable(name);
    setOffset(0);
  };

  const runQuery = async () => {
    const statement = sqlRef.current.trim();
    if (!statement) return;
    setRunningQuery(true);
    setQueryError(null);
    setQueryResult(null);
    try {
      setQueryResult(await api.queryDiscovered(database.ID, statement));
    } catch (err) {
      setQueryError(err instanceof Error ? err.message : 'Query failed');
    } finally {
      setRunningQuery(false);
    }
  };

  return (
    <section className="discovered-inspector" aria-label={`Inspect ${database.Name}`}>
      <div className="inspector-header">
        <div>
          <span className="inspector-kicker">Read-only inspector</span>
          <h3>{database.Name}</h3>
        </div>
        <span className="read-only-badge">No writes</span>
      </div>

      <div className="inspector-tabs" role="tablist" aria-label="Database inspector">
        <button
          type="button"
          className={activeTab === 'browse' ? 'active' : ''}
          onClick={() => setActiveTab('browse')}
        >
          Schema &amp; data
        </button>
        <button
          type="button"
          className={activeTab === 'query' ? 'active' : ''}
          onClick={() => setActiveTab('query')}
          disabled={!database.Available}
        >
          SELECT query
        </button>
        <button
          type="button"
          className={activeTab === 'history' ? 'active' : ''}
          onClick={() => setActiveTab('history')}
        >
          History
        </button>
      </div>

      {!database.Available && activeTab !== 'history' && (
        <div className="inspector-unavailable">
          The source file is unavailable. Restore it to inspect its current contents.
        </div>
      )}
      {error && <div className="error-msg">{error}</div>}

      {activeTab === 'browse' && database.Available && (
        <div className="inspector-browser">
          <aside className="inspector-table-rail">
            <div className="inspector-rail-heading">
              <span>Tables</span>
              <span>{tables.length}</span>
            </div>
            {loadingSchema && <div className="loading">Reading schema…</div>}
            {!loadingSchema && tables.map((table) => (
              <button
                type="button"
                key={table.Name}
                className={selectedTable === table.Name ? 'active' : ''}
                onClick={() => selectTable(table.Name)}
              >
                <span>{table.Name}</span>
                <small>{table.RowCount} rows</small>
              </button>
            ))}
            {!loadingSchema && tables.length === 0 && (
              <div className="inspector-empty">No user tables</div>
            )}
          </aside>

          <div className="inspector-table-detail">
            {selected ? (
              <>
                <div className="inspector-table-title">
                  <div>
                    <span className="inspector-kicker">Table</span>
                    <h4>{selected.Name}</h4>
                  </div>
                  <span>{selected.Columns?.length ?? 0} fields</span>
                </div>

                <div className="inspector-schema-grid">
                  {(selected.Columns ?? []).map((column) => (
                    <div className="inspector-column" key={column.Name}>
                      <div>
                        <strong>{column.Name}</strong>
                        <span className="col-type-badge">{column.Type || 'ANY'}</span>
                      </div>
                      <small>
                        {column.PrimaryKey ? 'Primary key' : column.Nullable ? 'Nullable' : 'Required'}
                        {column.DefaultValue ? ` · Default ${column.DefaultValue}` : ''}
                      </small>
                    </div>
                  ))}
                </div>

                {(selected.Indexes?.length ?? 0) > 0 && (
                  <div className="inspector-indexes">
                    <span>Indexes</span>
                    {(selected.Indexes ?? []).map((index) => (
                      <code key={index.Name}>
                        {index.Name}
                        {(index.Columns?.length ?? 0) > 0 ? ` (${index.Columns.join(', ')})` : ' (expression)'}
                        {index.Unique ? ' unique' : ''}
                      </code>
                    ))}
                  </div>
                )}

                <div className="inspector-data-heading">
                  <div>
                    <span className="inspector-kicker">Live data</span>
                    <strong>Rows {offset + 1}–{offset + (tableData?.Rows?.length ?? 0)}</strong>
                  </div>
                  <div className="pagination">
                    <button
                      type="button"
                      className="btn-sm"
                      disabled={offset === 0 || loadingTable}
                      onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                    >
                      ← Prev
                    </button>
                    <button
                      type="button"
                      className="btn-sm"
                      disabled={loadingTable || (tableData?.Rows?.length ?? 0) < PAGE_SIZE}
                      onClick={() => setOffset(offset + PAGE_SIZE)}
                    >
                      Next →
                    </button>
                  </div>
                </div>
                {loadingTable ? <div className="loading">Reading rows…</div> : tableData && (
                  <ResultsTable result={tableData} />
                )}
              </>
            ) : !loadingSchema && (
              <div className="inspector-empty">Select a table to inspect its fields and rows.</div>
            )}
          </div>
        </div>
      )}

      {activeTab === 'query' && database.Available && (
        <div className="inspector-query">
          <div className="inspector-query-note">
            Only one SELECT statement can run at a time. Results are capped at 1,000 rows.
          </div>
          <div className="editor-container">
            <Editor
              height="190px"
              defaultLanguage="sql"
              value={sql}
              onChange={(value) => {
                sqlRef.current = value ?? '';
                setSql(sqlRef.current);
              }}
              theme="vs-dark"
              options={{
                minimap: { enabled: false },
                fontSize: 13,
                lineNumbers: 'on',
                scrollBeyondLastLine: false,
                wordWrap: 'on',
                automaticLayout: true,
              }}
              onMount={(editor) => {
                editor.addCommand(2048 | 3, () => void runQuery());
              }}
            />
          </div>
          <div className="inspector-query-actions">
            <span>Ctrl/Cmd + Enter</span>
            <button type="button" className="btn-primary" onClick={runQuery} disabled={runningQuery || !sql.trim()}>
              {runningQuery ? 'Running…' : 'Run SELECT'}
            </button>
          </div>
          {queryError && <div className="error-msg">{queryError}</div>}
          {queryResult && <ResultsTable result={queryResult} />}
        </div>
      )}

      {activeTab === 'history' && (
        <SchemaTimeline dbId={database.ID} dbName={database.Name} />
      )}
    </section>
  );
}
