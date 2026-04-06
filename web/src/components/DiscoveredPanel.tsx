import { useState, useEffect, useCallback } from 'react';
import { api, type DiscoveredDB } from '../api/client';
import { SchemaTimeline } from './SchemaTimeline';

interface Props {
  selectedId: number | null;
  onSelect: (id: number | null) => void;
}

const PRIORITY_LABELS: Record<string, string> = {
  docker: 'Docker',
  workspace: 'Workspace',
  copilot: 'Copilot',
  app_data: 'App Data',
  other: 'Other',
};

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function statusClass(status: string): string {
  switch (status) {
    case 'replicating': return 'replicating';
    case 'paused': return 'paused';
    case 'error': return 'error';
    default: return 'discovered';
  }
}

export function DiscoveredPanel({ selectedId, onSelect }: Props) {
  const [databases, setDatabases] = useState<DiscoveredDB[]>([]);
  const [loading, setLoading] = useState(false);
  const [scanning, setScanning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const dbs = await api.listDiscovered();
      setDatabases(dbs || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load discovered databases');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { refresh(); }, [refresh]);

  const handleScan = async () => {
    try {
      setScanning(true);
      setError(null);
      const dbs = await api.scanDatabases();
      setDatabases(dbs || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Scan failed');
    } finally {
      setScanning(false);
    }
  };

  const handleReplicate = async (id: number) => {
    try {
      setActionLoading(id);
      await api.startReplication(id);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to start replication');
    } finally {
      setActionLoading(null);
    }
  };

  const handleStop = async (id: number) => {
    try {
      setActionLoading(id);
      await api.stopReplication(id);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to stop replication');
    } finally {
      setActionLoading(null);
    }
  };

  const handleRestore = async (id: number) => {
    try {
      setActionLoading(id);
      await api.restoreSnapshot(id);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to restore');
    } finally {
      setActionLoading(null);
    }
  };

  const selected = databases.find((db) => db.ID === selectedId) ?? null;

  return (
    <div className="discovered-panel">
      <div className="discovered-header">
        <h3>Discovered Databases</h3>
        <div className="discovered-actions">
          <button className="btn-primary" onClick={handleScan} disabled={scanning}>
            {scanning ? 'Scanning…' : '⟳ Scan'}
          </button>
          <button className="btn-icon" onClick={refresh} title="Refresh list">⟳</button>
        </div>
      </div>

      {error && <div className="error-msg">{error}</div>}
      {loading && <div className="loading">Loading…</div>}

      <div className="discovered-list">
        {databases.map((db) => (
          <div
            key={db.ID}
            className={`discovered-item ${selectedId === db.ID ? 'active' : ''}`}
            onClick={() => onSelect(selectedId === db.ID ? null : db.ID)}
          >
            <div className="discovered-item-header">
              <span className={`status-indicator ${statusClass(db.Status)}`} />
              <span className="discovered-name">{db.Name}</span>
              <span className={`priority-badge priority-${db.Priority}`}>
                {PRIORITY_LABELS[db.Priority] ?? db.Priority}
              </span>
            </div>

            <div className="discovered-item-meta">
              <span className="discovered-path" title={db.SourcePath}>{db.SourcePath}</span>
            </div>

            <div className="discovered-item-details">
              <span>{formatSize(db.SizeBytes)}</span>
              <span>SQLite {db.SQLiteVersion}</span>
              <span>{db.JournalMode}</span>
              {db.GitHubRepo && (
                <a
                  className="github-link"
                  href={db.GitHubURL}
                  target="_blank"
                  rel="noopener noreferrer"
                  onClick={(e) => e.stopPropagation()}
                  title={`Open ${db.GitHubRepo} on GitHub`}
                >
                  <svg className="github-icon" viewBox="0 0 16 16" width="14" height="14" fill="currentColor">
                    <path d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.013 8.013 0 0016 8c0-4.42-3.58-8-8-8z" />
                  </svg>
                  {db.GitHubRepo}
                </a>
              )}
            </div>

            <div className="discovered-item-actions">
              {db.Status === 'discovered' && (
                <button
                  className="btn-sm"
                  onClick={(e) => { e.stopPropagation(); handleReplicate(db.ID); }}
                  disabled={actionLoading === db.ID}
                >
                  ▶ Replicate
                </button>
              )}
              {db.Status === 'replicating' && (
                <button
                  className="btn-sm"
                  onClick={(e) => { e.stopPropagation(); handleStop(db.ID); }}
                  disabled={actionLoading === db.ID}
                >
                  ⏸ Stop
                </button>
              )}
              {(db.Status === 'replicating' || db.Status === 'paused') && (
                <button
                  className="btn-sm"
                  onClick={(e) => { e.stopPropagation(); handleRestore(db.ID); }}
                  disabled={actionLoading === db.ID}
                >
                  ↻ Restore
                </button>
              )}
            </div>
          </div>
        ))}
        {!loading && databases.length === 0 && (
          <div className="discovered-empty">
            No databases discovered yet. Click <strong>Scan</strong> to search.
          </div>
        )}
      </div>

      {selected && (
        <div className="discovered-detail">
          <SchemaTimeline dbId={selected.ID} dbName={selected.Name} />
        </div>
      )}
    </div>
  );
}
