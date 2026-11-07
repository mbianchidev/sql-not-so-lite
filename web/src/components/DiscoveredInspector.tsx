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
import { APIExposurePanel } from './APIExposurePanel';
import { formatInspectionState } from '../utils/format';

interface Props {
  database: DiscoveredDB;
  darkMode: boolean;
  onProjectionChange: (database: DiscoveredDB) => Promise<void>;
}

type InspectorTab = 'browse' | 'query' | 'history';

const PAGE_SIZE = 100;

export function DiscoveredInspector({ database, darkMode, onProjectionChange }: Props) {
  const inspection = database.Inspection;
  const [activeTab, setActiveTab] = useState<InspectorTab>('browse');
  const [showAPI, setShowAPI] = useState(false);
  const [tables, setTables] = useState<TableInfo[]>([]);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [tableData, setTableData] = useState<QueryResult | null>(null);
  const [offset, setOffset] = useState(0);
  const [loadingSchema, setLoadingSchema] = useState(false);
  const [loadingTable, setLoadingTable] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [sql, setSql] = useState(inspection ? 'SELECT * FROM key_metadata ORDER BY ordinal;' : 'SELECT * FROM sqlite_master WHERE type = \'table\' ORDER BY name;');
  const [queryResult, setQueryResult] = useState<QueryResult | null>(null);
  const [queryError, setQueryError] = useState<string | null>(null);
  const [runningQuery, setRunningQuery] = useState(false);
  const sqlRef = useRef(sql);
  const running = useRef(false);
  const requestEpoch = useRef(0);
  const projectionAbort = useRef<AbortController | null>(null);
  const [projectionBusy, setProjectionBusy] = useState(false);
  const [sensitiveConsent, setSensitiveConsent] = useState(false);

  const selected = useMemo(
    () => tables.find((table) => table.Name === selectedTable) ?? null,
    [selectedTable, tables],
  );

  const loadSchema = useCallback(async () => {
    if (!database.Available) return;
    setLoadingSchema(true);
    setError(null);
    const epoch = requestEpoch.current;
    try {
      const schema = await api.getDiscoveredSchema(database.ID);
      if (epoch !== requestEpoch.current) return;
      setTables(schema);
      setSelectedTable((current) => (
        current && schema.some((table) => table.Name === current)
          ? current
          : schema[0]?.Name ?? null
      ));
    } catch (err) {
      if (epoch === requestEpoch.current) setError(err instanceof Error ? err.message : 'Failed to inspect database schema');
    } finally {
      if (epoch === requestEpoch.current) setLoadingSchema(false);
    }
  }, [database.Available, database.ID]);

  useEffect(() => () => {
    requestEpoch.current++;
    projectionAbort.current?.abort();
  }, []);

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
    const epoch = requestEpoch.current;
    try {
      const result = await api.queryDiscovered(database.ID, statement);
      if (epoch === requestEpoch.current) setQueryResult(result);
    } catch (err) {
      if (epoch === requestEpoch.current) setQueryError(err instanceof Error ? err.message : 'Query failed');
    } finally {
      running.current = false;
      setRunningQuery(false);
    }
  };

  const refreshProjection = async (includeData: boolean) => {
    if (!inspection || projectionAbort.current || (includeData && !sensitiveConsent)) return;
    const controller = new AbortController();
    projectionAbort.current = controller;
    requestEpoch.current++;
    setProjectionBusy(true);
    setActiveTab('browse');
    setTables([]);
    setSelectedTable(null);
    setTableData(null);
    setQueryResult(null);
    setQueryError(null);
    setError(null);
    setLoadingSchema(false);
    if (!includeData) setSensitiveConsent(false);
    try {
      const projection = await api.fetchEtcqliteProjection(includeData, controller.signal);
      await onProjectionChange(projection);
    } catch (err) {
      if (!controller.signal.aborted) {
        setError(err instanceof Error ? err.message : 'Etcqlite projection refresh failed');
      }
    } finally {
      projectionAbort.current = null;
      setProjectionBusy(false);
    }
  };

  return (
    <section className="discovered-inspector" aria-label={`Inspect ${database.Name}`}>
      <div className="inspector-header">
        <div><h3>{database.Name}</h3><p>{inspection ? 'Immutable inspection projection, not the internal store' : 'No writes through this inspector'}</p></div>
        <div className="workspace-heading-actions"><span className="read-only-badge"><Icon name="lock" /> Read-only inspector</span>{!inspection && <button className="btn-sm" disabled={!database.Available || loadingSchema} onClick={() => setShowAPI((current) => !current)} aria-expanded={showAPI}><Icon name="terminal" /> Expose API</button>}</div>
      </div>

      {inspection && (
        <div className="projection-summary">
          <div className="projection-status" role="status">
            <strong>{formatInspectionState(inspection.state)}</strong>
            <span>Captured revision {inspection.revision}</span>
            <span>Fetched {new Date(inspection.fetched_at).toLocaleString()}. Rows are from this capture, never live data.</span>
          </div>
          <dl className="projection-facts">
            <div><dt>Cluster ID</dt><dd>{inspection.cluster_id}</dd></div>
            <div><dt>Member ID</dt><dd>{inspection.member_id}</dd></div>
            <div><dt>Compact revision</dt><dd>{inspection.compact_revision}</dd></div>
            <div><dt>Source schema</dt><dd>{inspection.schema_version}</dd></div>
            <div><dt>Engine / API</dt><dd>{inspection.engine_version} / {inspection.api_version}</dd></div>
            <div><dt>Build</dt><dd>{inspection.build}{inspection.dirty ? ' (dirty)' : ''}</dd></div>
            <div><dt>Configuration ID</dt><dd>{inspection.configuration_id ?? 'Local mode'}</dd></div>
            <div><dt>Source revision at last check</dt><dd>{inspection.source_revision}</dd></div>
          </dl>
          <p className="api-warning">Not a backup. Projection rows do not establish consensus, high availability or recovery readiness. Internal authentication, protocol commands and peer credentials are never projected.</p>
          {inspection.includes_data && <p className="api-warning">Sensitive projection active. Raw user keys and values may contain application secrets. Browsing and exports remain sensitive until you refresh metadata, remove the projection or restart the daemon.</p>}
          <form className="inspection-consent" onSubmit={(event) => { event.preventDefault(); void refreshProjection(true); }}>
            <div className="api-copy-actions">
              <button type="button" className="btn-sm" onClick={() => void refreshProjection(false)} disabled={projectionBusy || inspection.state === 'disabled'}><Icon name="refresh" /> Refresh metadata</button>
              <button type="submit" className="btn-sm" disabled={!sensitiveConsent || projectionBusy || inspection.state === 'disabled'}><Icon name="lock" /> Load sensitive keys and values</button>
            </div>
            <label><input type="checkbox" checked={sensitiveConsent} onChange={(event) => setSensitiveConsent(event.target.checked)} disabled={projectionBusy || inspection.state === 'disabled'} /> I consent to loading sensitive raw keys and values.</label>
          </form>
          {projectionBusy && <div className="loading" role="status">Fetching a new immutable projection...</div>}
        </div>
      )}

      <TabBar<InspectorTab> id={`inspector-${database.ID}`} label="Database inspector" active={activeTab} onChange={setActiveTab} tabs={[
        { value: 'browse', label: 'Schema & data', icon: 'table' },
        { value: 'query', label: 'SELECT query', icon: 'terminal', disabled: !database.Available || projectionBusy },
        ...(!inspection ? [{ value: 'history' as const, label: 'History', icon: 'history' as const }] : []),
      ]} />

      <div className="inspector-content" role="tabpanel" id={`inspector-${database.ID}-panel`} aria-labelledby={`inspector-${database.ID}-tab-${activeTab}`} tabIndex={0}>
      {showAPI && !inspection && <APIExposurePanel source={{ discovered_id: database.ID }} tables={tables} onClose={() => setShowAPI(false)} />}
      {!database.Available && activeTab !== 'history' && (
        <div className="inspector-unavailable">
          {inspection ? 'The cached projection is unavailable or inspection is disabled. Fetch a new projection after reconnecting; the internal store is never opened.' : 'The source file is unavailable. Restore it to inspect its current contents.'}
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
