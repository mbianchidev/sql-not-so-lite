import { useState, useCallback } from 'react';
import Editor from '@monaco-editor/react';
import { api, type QueryResult, type ExecResult } from '../api/client';
import { ResultsTable } from './ResultsTable';

interface Props {
  dbName: string;
}

type ResultType = 
  | { kind: 'query'; data: QueryResult }
  | { kind: 'exec'; data: ExecResult }
  | { kind: 'error'; message: string };

export function SqlEditor({ dbName }: Props) {
  const [sql, setSql] = useState('SELECT 1;');
  const [result, setResult] = useState<ResultType | null>(null);
  const [running, setRunning] = useState(false);
  const [history, setHistory] = useState<string[]>([]);

  const execute = useCallback(async () => {
    const trimmed = sql.trim();
    if (!trimmed) return;

    setRunning(true);
    setResult(null);

    try {
      const res = await api.executeQuery(dbName, trimmed);

      if ('Columns' in res) {
        setResult({ kind: 'query', data: res as QueryResult });
      } else {
        setResult({ kind: 'exec', data: res as ExecResult });
      }

      setHistory((prev) => [trimmed, ...prev.filter((h) => h !== trimmed)].slice(0, 50));
    } catch (err) {
      setResult({
        kind: 'error',
        message: err instanceof Error ? err.message : 'Query failed',
      });
    } finally {
      setRunning(false);
    }
  }, [dbName, sql]);

  return (
    <div className="sql-editor">
      <div className="editor-toolbar">
        <h3>SQL Editor — {dbName}</h3>
        <div className="editor-actions">
          <button onClick={execute} disabled={running} className="btn-primary">
            {running ? 'Running...' : '▶ Execute'}
          </button>
          <span className="shortcut-hint">Ctrl+Enter</span>
        </div>
      </div>

      <div className="editor-container">
        <Editor
          height="200px"
          defaultLanguage="sql"
          value={sql}
          onChange={(v) => setSql(v || '')}
          theme="vs-dark"
          options={{
            minimap: { enabled: false },
            fontSize: 14,
            lineNumbers: 'on',
            scrollBeyondLastLine: false,
            wordWrap: 'on',
            automaticLayout: true,
          }}
          onMount={(editor) => {
            editor.addCommand(
              // Ctrl+Enter / Cmd+Enter
              2048 | 3, // KeyMod.CtrlCmd | KeyCode.Enter
              () => execute()
            );
          }}
        />
      </div>

      {result && (
        <div className="query-result">
          {result.kind === 'query' && (
            <ResultsTable result={result.data} />
          )}
          {result.kind === 'exec' && (
            <div className="exec-result">
              <span className="success-badge">✓</span>
              {result.data.RowsAffected} row(s) affected
              {result.data.LastInsertID > 0 && (
                <span> · Last insert ID: {result.data.LastInsertID}</span>
              )}
            </div>
          )}
          {result.kind === 'error' && (
            <div className="error-msg">{result.message}</div>
          )}
        </div>
      )}

      {history.length > 0 && (
        <div className="query-history">
          <h4>History</h4>
          <ul>
            {history.slice(0, 10).map((h, i) => (
              <li key={i} onClick={() => setSql(h)} className="history-item">
                {h.length > 80 ? h.slice(0, 80) + '...' : h}
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
