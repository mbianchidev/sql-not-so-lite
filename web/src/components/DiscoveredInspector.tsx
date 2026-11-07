import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  api,
  type DiscoveredDB,
  type QueryResult,
  type TableInfo,
} from '../api/client';
import { ResultsTable } from './ResultsTable';
import { SchemaTimeline } from './SchemaTimeline';
import { CodeEditor } from './CodeEditor';
import { TabBar } from './TabBar';
import { Icon } from './Icon';

interface Props {
  database: DiscoveredDB;
  darkMode: boolean;
}

type InspectorTab = 'browse' | 'query' | 'history';

const PAGE_SIZE = 100;

export function DiscoveredInspector({ database, darkMode }: Props) {
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
  const running = useRef(false);

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
    if (!statement || running.current) return;
    running.current = true;
    setRunningQuery(true);
    setQueryError(null);
    setQueryResult(null);
    try {
      setQueryResult(await api.queryDiscovered(database.ID, statement));
    } catch (err) {
      setQueryError(err instanceof Error ? err.message : 'Query failed');
    } finally {
      running.current = false;
      setRunningQuery(false);
    }
  };

  return (
    <section className="discovered-inspector" aria-label={`Inspect ${database.Name}`}>
      <div className="inspector-header">
        <div><h3>{database.Name}</h3><p>No writes to the source file</p></div>
        <span className="read-only-badge"><Icon name="lock" /> Read-only inspector</span>
      </div>

      <TabBar<InspectorTab> id={`inspector-${database.ID}`} label="Database inspector" active={activeTab} onChange={setActiveTab} tabs={[
        { value: 'browse', label: 'Schema & data', icon: 'table' },
        { value: 'query', label: 'SELECT query', icon: 'terminal', disabled: !database.Available },
        { value: 'history', label: 'History', icon: 'history' },
      ]} />

      <div className="inspector-content" role="tabpanel" id={`inspector-${database.ID}-panel`} aria-labelledby={`inspector-${database.ID}-tab-${activeTab}`} tabIndex={0}>
      {!database.Available && activeTab !== 'history' && (
        <div className="inspector-unavailable">
          The source file is unavailable. Restore it to inspect its current contents.
        </div>
      )}
      {error && <div className="error-msg" role="alert">{error}</div>}

      {activeTab === 'browse' && database.Available && (
        <div className="inspector-browser">
          <aside className="inspector-table-rail">
            <div className="inspector-rail-heading">
              <span>Tables</span>
              <span>{tables.length}</span>
            </div>
            {loadingSchema && <div className="loading" role="status">Reading schema...</div>}
            {!loadingSchema && tables.map((table) => (
              <button
                type="button"
                key={table.Name}
                className={selectedTable === table.Name ? 'active' : ''}
                onClick={() => selectTable(table.Name)}
              >
                <Icon name="table" /><span>{table.Name}</span>
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
                    <strong>Rows {tableData?.Rows?.length ? offset + 1 : 0}-{offset + (tableData?.Rows?.length ?? 0)}</strong>
                  </div>
                  <div className="pagination">
                    <button
                      type="button"
                      className="btn-sm"
                      disabled={offset === 0 || loadingTable}
                      onClick={() => setOffset(Math.max(0, offset - PAGE_SIZE))}
                    >
                      <Icon name="chevron-left" /> Prev
                    </button>
                    <button
                      type="button"
                      className="btn-sm"
                      disabled={loadingTable || (tableData?.Rows?.length ?? 0) < PAGE_SIZE}
                      onClick={() => setOffset(offset + PAGE_SIZE)}
                    >
                      Next <Icon name="chevron-right" />
                    </button>
                  </div>
                </div>
                {loadingTable ? <div className="loading" role="status">Reading rows...</div> : tableData && (
                  <ResultsTable result={tableData} rowOffset={offset} />
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
          <CodeEditor height="220px" value={sql} onChange={(value) => { sqlRef.current = value; setSql(value); }} onExecute={() => void runQuery()} darkMode={darkMode} />
          <div className="inspector-query-actions">
            <span>Ctrl/Cmd + Enter</span>
            <button type="button" className="btn-primary" onClick={runQuery} disabled={runningQuery || !sql.trim()}>
              <Icon name="play" />{runningQuery ? 'Running...' : 'Run SELECT'}
            </button>
          </div>
          {queryError && <div className="error-msg" role="alert">{queryError}</div>}
          {queryResult && <ResultsTable result={queryResult} />}
        </div>
      )}

      {activeTab === 'history' && (
        <SchemaTimeline dbId={database.ID} dbName={database.Name} />
      )}
      </div>
    </section>
  );
}
