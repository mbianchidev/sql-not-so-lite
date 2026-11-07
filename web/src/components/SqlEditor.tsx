import { useState, useCallback, useRef } from 'react';
import { api, type QueryResult, type ExecResult } from '../api/client';
import { ResultsTable } from './ResultsTable';
import { CodeEditor } from './CodeEditor';
import { Icon } from './Icon';

interface Props {
  dbName: string;
  darkMode: boolean;
}

type ResultType =
  | { kind: 'query'; data: QueryResult }
  | { kind: 'exec'; data: ExecResult }
  | { kind: 'error'; message: string };

export function SqlEditor({ dbName, darkMode }: Props) {
  const [sql, setSql] = useState('SELECT 1;');
  const [result, setResult] = useState<ResultType | null>(null);
  const [running, setRunning] = useState(false);
  const [history, setHistory] = useState<string[]>([]);
  const sqlRef = useRef(sql);
  const runningRef = useRef(false);

  const updateSql = (value: string) => { sqlRef.current = value; setSql(value); };
  const execute = useCallback(async () => {
    const trimmed = sqlRef.current.trim();
    if (!trimmed || runningRef.current) return;
    runningRef.current = true;
    setRunning(true);
    setResult(null);
    try {
      const response = await api.executeQuery(dbName, trimmed);
      setResult('Columns' in response ? { kind: 'query', data: response } : { kind: 'exec', data: response });
      setHistory((previous) => [trimmed, ...previous.filter((entry) => entry !== trimmed)].slice(0, 50));
    } catch (err) {
      setResult({ kind: 'error', message: err instanceof Error ? err.message : 'Query failed' });
    } finally {
      runningRef.current = false;
      setRunning(false);
    }
  }, [dbName]);

  return (
    <div className="sql-editor">
      <div className="section-heading">
        <div><h3>SQL Editor</h3><p>Write a query. Explore the result.</p></div>
        <span className="editor-language">SQLite</span>
      </div>
      <div className="sql-workbench">
        <div className="editor-toolbar"><span className="query-file"><Icon name="terminal" /> query.sql</span><span className="editor-database"><span className="status-dot active" />{dbName}</span></div>
        <CodeEditor value={sql} onChange={updateSql} onExecute={() => void execute()} darkMode={darkMode} />
        <div className="editor-runbar"><span className="shortcut-hint"><kbd>Ctrl</kbd> / <kbd>Cmd</kbd> + <kbd>Enter</kbd> to run</span><button onClick={() => void execute()} disabled={running || !sql.trim()} className="btn-primary" aria-label="Run query"><Icon name="play" />{running ? 'Running...' : 'Run query'}</button></div>
      </div>
      {running && <div className="loading" role="status">Executing query...</div>}
      {result ? (
        <div className="query-result">
          {result.kind === 'query' && <ResultsTable result={result.data} />}
          {result.kind === 'exec' && <div className="scan-result" role="status"><Icon name="check" />{result.data.RowsAffected} row{result.data.RowsAffected === 1 ? '' : 's'} affected{result.data.LastInsertID > 0 && <span> / Last insert ID: {result.data.LastInsertID}</span>}</div>}
          {result.kind === 'error' && <div className="error-msg" role="alert">{result.message}</div>}
        </div>
      ) : !running && <div className="query-placeholder"><Icon name="table" /><div><strong>Your results will appear here</strong><p>Run a query to view, sort, and export its rows.</p></div></div>}
      {history.length > 0 && (
        <div className="query-history"><h4><Icon name="history" /> Recent queries</h4><ul>{history.slice(0, 10).map((entry) => <li key={entry}><button onClick={() => updateSql(entry)} title={entry}>{entry.length > 100 ? `${entry.slice(0, 100)}...` : entry}<Icon name="arrow-up-right" /></button></li>)}</ul></div>
      )}
    </div>
  );
}
