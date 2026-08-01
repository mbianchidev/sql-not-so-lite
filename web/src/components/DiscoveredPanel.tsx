import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { api, type DiscoveredDB } from '../api/client';
import { DiscoveredInspector } from './DiscoveredInspector';

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

const DEFAULT_SCAN_PATH = '~/';

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function statusClass(status: string, isReplica: boolean, available: boolean): string {
  if (isReplica) return 'replica';
  if (!available) return 'missing';
  switch (status) {
    case 'replicating': return 'replicating';
    case 'paused': return 'paused';
    case 'error': return 'error';
    default: return 'discovered';
  }
}

function statusLabel(status: string, isReplica: boolean, available: boolean): string {
  if (isReplica) return 'Replica';
  if (!available) return 'Missing';
  switch (status) {
    case 'replicating': return 'Replicating';
    case 'paused': return 'Paused';
    case 'error': return 'Error';
    case 'archived': return 'Archived';
    default: return 'Discovered';
  }
}

export function DiscoveredPanel({ selectedId, onSelect }: Props) {
  const [databases, setDatabases] = useState<DiscoveredDB[]>([]);
  const [loading, setLoading] = useState(false);
  const [manualScanning, setManualScanning] = useState(false);
  const [serverScanning, setServerScanning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [scanMessage, setScanMessage] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState<number | null>(null);
  const [scanPath, setScanPath] = useState(DEFAULT_SCAN_PATH);
  const [searchQuery, setSearchQuery] = useState('');
  const scanWasInProgress = useRef(false);
  const scanning = manualScanning || serverScanning;

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

  const scan = useCallback(async (paths: string[]): Promise<boolean> => {
    try {
      setManualScanning(true);
      setError(null);
      setScanMessage(null);
      const result = await api.scanDatabases(paths);
      await refresh();
      setScanMessage(
        `Scan complete: ${result.scanned} database${result.scanned === 1 ? '' : 's'} found.`
      );
      return true;
    } catch (err) {
      if (err instanceof Error && err.message === 'scan already in progress') {
        scanWasInProgress.current = true;
        setServerScanning(true);
        return false;
      }
      setError(err instanceof Error ? err.message : 'Scan failed');
      return false;
    } finally {
      setManualScanning(false);
    }
  }, [refresh]);

  const handleScan = async () => {
    const trimmed = scanPath.trim();
    if (!trimmed) return;
    await scan([trimmed]);
  };

  useEffect(() => {
    let active = true;

    const pollScanStatus = async () => {
      try {
        const status = await api.getScanStatus();
        if (!active) return;
        const completed = scanWasInProgress.current && !status.in_progress;
        scanWasInProgress.current = status.in_progress;
        setServerScanning(status.in_progress);
        if (completed) {
          await refresh();
        }
      } catch (err) {
        if (active) {
          setError(err instanceof Error ? err.message : 'Failed to load scan status');
        }
      }
    };

    void refresh();
    void pollScanStatus();
    const interval = window.setInterval(() => void pollScanStatus(), 1000);
    return () => {
      active = false;
      window.clearInterval(interval);
    };
  }, [refresh]);

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

  const handleFavorite = async (id: number, favorite: boolean) => {
    try {
      setActionLoading(id);
      await api.updateFavorite(id, favorite);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to update favorite');
    } finally {
      setActionLoading(null);
    }
  };

  const handleRestore = async (id: number, name: string) => {
    if (!confirm(`Restore database "${name}"? This will overwrite the file at its original source path.`)) return;
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

  const handleRemove = async (id: number, name: string) => {
    if (!confirm(`Remove "${name}" from discovered databases? This does not delete the original file.`)) return;
    try {
      setActionLoading(id);
      await api.deleteDiscovered(id);
      if (selectedId === id) onSelect(null);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to remove');
    } finally {
      setActionLoading(null);
    }
  };

  const filteredDatabases = useMemo(() => {
    const query = searchQuery.trim().toLocaleLowerCase();
    if (!query) return databases;

    return databases.filter((db) => [
      db.Name,
      db.SourcePath,
      db.GitHubRepo,
      db.Status,
      db.Priority,
      PRIORITY_LABELS[db.Priority],
      db.SQLiteVersion,
      db.JournalMode,
      db.IsReplica ? 'replica' : '',
      db.Favorite ? 'favorite' : '',
      db.Available ? '' : 'missing',
    ].some((value) => value?.toLocaleLowerCase().includes(query)));
  }, [databases, searchQuery]);

  const selected = databases.find((db) => db.ID === selectedId) ?? null;
  const isFiltering = searchQuery.trim().length > 0;

  return (
    <div className="discovered-panel">
      <div className="discovered-header">
        <h3>Discovered Databases</h3>
        <div className="discovered-actions">
          <button className="btn-icon" onClick={refresh} title="Refresh list">⟳</button>
        </div>
      </div>

      <div className="scan-path-input">
        <input
          type="text"
          value={scanPath}
          onChange={(e) => setScanPath(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleScan()}
          placeholder="Enter path to scan…"
          disabled={scanning}
        />
        <button className="btn-primary" onClick={handleScan} disabled={scanning || !scanPath.trim()}>
          {scanning ? 'Scanning…' : 'Scan'}
        </button>
      </div>

      {error && <div className="error-msg">{error}</div>}
      {scanMessage && <div className="scan-result">{scanMessage}</div>}
      {loading && <div className="loading">Loading…</div>}

      {databases.length > 0 && (
        <div className="discovered-search">
          <span className="discovered-search-icon" aria-hidden="true">⌕</span>
          <input
            type="search"
            value={searchQuery}
            onChange={(event) => setSearchQuery(event.target.value)}
            placeholder="Search name, path, repository, status…"
            aria-label="Search discovered databases"
          />
          <span className="discovered-search-count" aria-live="polite">
            {filteredDatabases.length} / {databases.length}
          </span>
          {isFiltering && (
            <button
              type="button"
              className="discovered-search-clear"
              onClick={() => setSearchQuery('')}
              aria-label="Clear search"
            >
              Clear
            </button>
          )}
        </div>
      )}

      {selected && (
        <div className="discovered-detail">
          <DiscoveredInspector key={selected.ID} database={selected} />
        </div>
      )}

      <div className="discovered-list">
        {filteredDatabases.map((db) => (
          <div
            key={db.ID}
            className={`discovered-item ${selectedId === db.ID ? 'active' : ''} ${db.Available ? '' : 'missing'}`}
            onClick={() => onSelect(selectedId === db.ID ? null : db.ID)}
          >
            <div className="discovered-item-header">
              <span className={`status-indicator ${statusClass(db.Status, db.IsReplica, db.Available)}`} />
              <span className="discovered-name">{db.Name}</span>
              <button
                type="button"
                className={`favorite-toggle ${db.Favorite ? 'active' : ''}`}
                onClick={(event) => {
                  event.stopPropagation();
                  void handleFavorite(db.ID, !db.Favorite);
                }}
                disabled={actionLoading === db.ID}
                aria-label={db.Favorite ? `Remove ${db.Name} from favorites` : `Add ${db.Name} to favorites`}
                title={db.Favorite ? 'Remove from favorites' : 'Add to favorites'}
              >
                {db.Favorite ? '★' : '☆'}
              </button>
              <span className={`discovered-status-label ${db.IsReplica ? 'replica' : ''}`}>
                {statusLabel(db.Status, db.IsReplica, db.Available)}
              </span>
              <span className={`priority-badge priority-${db.Priority}`}>
                {PRIORITY_LABELS[db.Priority] ?? db.Priority}
              </span>
            </div>

            <div className="discovered-item-meta">
              <span className="discovered-path" title={db.SourcePath}>{db.SourcePath}</span>
            </div>

            {db.Status === 'error' && db.ErrorMessage && (
              <div className="discovered-error-detail">⚠ {db.ErrorMessage}</div>
            )}

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
              {db.Available && !db.IsReplica && (db.Status === 'discovered' || db.Status === 'paused' || db.Status === 'error') && (
                <button
                  className="btn-sm"
                  onClick={(e) => { e.stopPropagation(); handleReplicate(db.ID); }}
                  disabled={actionLoading === db.ID}
                >
                  {db.Status === 'paused' ? '▶ Resume' : db.Status === 'error' ? '↻ Retry' : '▶ Replicate'}
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
              {db.Available && (db.Status === 'replicating' || db.Status === 'paused') && (
                <button
                  className="btn-sm"
                  onClick={(e) => { e.stopPropagation(); handleRestore(db.ID, db.Name); }}
                  disabled={actionLoading === db.ID}
                >
                  ↻ Restore
                </button>
              )}
              <button
                className="btn-danger-sm"
                onClick={(e) => { e.stopPropagation(); handleRemove(db.ID, db.Name); }}
                disabled={actionLoading === db.ID}
                title="Remove from catalog"
              >
                ✕
              </button>
            </div>
          </div>
        ))}
        {!loading && databases.length === 0 && !scanMessage && (
          <div className="discovered-empty">
            No databases discovered yet. Click <strong>Scan</strong> to search.
          </div>
        )}
        {!loading && databases.length > 0 && filteredDatabases.length === 0 && (
          <div className="discovered-empty">
            No databases match <strong>“{searchQuery.trim()}”</strong>.
            <button type="button" className="discovered-empty-clear" onClick={() => setSearchQuery('')}>
              Clear search
            </button>
          </div>
        )}
      </div>

    </div>
  );
}
