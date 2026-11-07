import { useState, useEffect, useCallback, useMemo, useRef } from 'react';
import { api, type DiscoveredDB, type EtcqliteInspection, type EtcqliteStatus } from '../api/client';
import { DiscoveredInspector } from './DiscoveredInspector';
import { Icon } from './Icon';
import { formatBytes, formatInspectionState } from '../utils/format';

interface Props {
  selectedId: number | null;
  onSelect: (id: number | null) => void;
  darkMode: boolean;
}

const PRIORITY_LABELS: Record<string, string> = {
  docker: 'Docker',
  workspace: 'Workspace',
  copilot: 'Copilot',
  app_data: 'App Data',
  other: 'Other',
};

const DEFAULT_SCAN_PATH = '~/';

function statusClass(status: string, isReplica: boolean, available: boolean, inspection?: EtcqliteInspection): string {
  if (inspection) {
    if (inspection.state === 'disconnected') return 'error';
    if (inspection.state === 'stale') return 'paused';
    if (!available || inspection.state === 'disabled') return 'missing';
    return 'discovered';
  }
  if (isReplica) return 'replica';
  if (!available) return 'missing';
  switch (status) {
    case 'replicating': return 'replicating';
    case 'paused': return 'paused';
    case 'error': return 'error';
    default: return 'discovered';
  }
}

function statusLabel(status: string, isReplica: boolean, available: boolean, inspection?: EtcqliteInspection): string {
  if (inspection) return formatInspectionState(inspection.state);
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

export function DiscoveredPanel({ selectedId, onSelect, darkMode }: Props) {
  const [databases, setDatabases] = useState<DiscoveredDB[]>([]);
  const [loading, setLoading] = useState(false);
  const [manualScanning, setManualScanning] = useState(false);
  const [serverScanning, setServerScanning] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [scanMessage, setScanMessage] = useState<string | null>(null);
  const [actionLoading, setActionLoading] = useState<number | null>(null);
  const [scanPath, setScanPath] = useState(DEFAULT_SCAN_PATH);
  const [searchQuery, setSearchQuery] = useState('');
  const [favoritesOnly, setFavoritesOnly] = useState(false);
  const [etcqlite, setEtcqlite] = useState<EtcqliteStatus | null>(null);
  const [metadataConsent, setMetadataConsent] = useState(false);
  const [connecting, setConnecting] = useState(false);
  const [integrationError, setIntegrationError] = useState<string | null>(null);
  const connectionAbort = useRef<AbortController | null>(null);
  const scanWasInProgress = useRef(false);
  const scanning = manualScanning || serverScanning;

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [status, dbs] = await Promise.allSettled([api.getEtcqliteStatus(), api.listDiscovered()]);
      if (dbs.status === 'fulfilled') {
        setDatabases(dbs.value);
      } else {
        setError(dbs.reason instanceof Error ? dbs.reason.message : 'Failed to load discovered databases');
      }
      if (status.status === 'fulfilled') {
        setEtcqlite(status.value);
        setIntegrationError(null);
        if (status.value.database) {
          const updated = status.value.database;
          setDatabases((current) => current.map((database) => database.ID === updated.ID ? updated : database));
        }
      } else {
        setIntegrationError(status.reason instanceof Error ? status.reason.message : 'Etcqlite inspection status unavailable');
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load discovered databases');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => () => connectionAbort.current?.abort(), []);

  useEffect(() => {
    if (!etcqlite?.enabled || !etcqlite.database?.ID) return;
    const interval = window.setInterval(() => void refresh(), 15_000);
    return () => window.clearInterval(interval);
  }, [etcqlite?.enabled, etcqlite?.database?.ID, refresh]);

  const connectEtcqlite = async () => {
    if (!metadataConsent || connectionAbort.current) return;
    const controller = new AbortController();
    connectionAbort.current = controller;
    setConnecting(true);
    setIntegrationError(null);
    try {
      const database = await api.fetchEtcqliteProjection(false, controller.signal);
      await refresh();
      onSelect(database.ID);
      setMetadataConsent(false);
    } catch (err) {
      if (!controller.signal.aborted) {
        setIntegrationError(err instanceof Error ? err.message : 'Etcqlite inspection failed');
      }
    } finally {
      connectionAbort.current = null;
      setConnecting(false);
    }
  };

  const projectionChanged = async (database: DiscoveredDB) => {
    setDatabases((current) => current.map((entry) => entry.ID === database.ID ? database : entry));
    await refresh();
  };

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
    if (!confirm(`Restore the latest snapshot to the local replica of "${name}"? This replaces replica contents, not the original source file.`)) return;
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
    const inspection = databases.find((database) => database.ID === id)?.Inspection;
    const message = inspection
      ? `Remove "${name}" and its private cached projection? This does not change the Etcqlite store.`
      : `Remove "${name}" from discovered databases? This does not delete the original file.`;
    if (!confirm(message)) return;
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
    return databases.filter((db) => (!favoritesOnly || db.Favorite) && (!query || [
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
      db.Inspection ? 'etcqlite read-only projection' : '',
      db.Inspection?.revision,
      db.Inspection?.cluster_id,
      db.Inspection?.member_id,
    ].some((value) => value?.toLocaleLowerCase().includes(query))));
  }, [databases, searchQuery, favoritesOnly]);

  const selected = databases.find((db) => db.ID === selectedId) ?? null;
  const isFiltering = searchQuery.trim().length > 0;

  return (
    <div className="discovered-panel">
      <div className="page-heading">
        <div><h2>Discovery</h2><p>Your SQLite files, right where they belong.</p></div>
        <button className="btn-sm" onClick={refresh} disabled={loading} aria-label="Refresh discovered databases"><Icon name="refresh" /> Refresh</button>
      </div>

      <div className="scan-strip">
        <div className="scan-description"><Icon name="folder" /><div><strong>Find a database</strong><span>Scan a directory without moving your files.</span></div></div>
        <form className="scan-path-input" onSubmit={(event) => { event.preventDefault(); void handleScan(); }}>
          <input type="text" value={scanPath} onChange={(event) => setScanPath(event.target.value)} placeholder="/path/to/databases" aria-label="Directory to scan" disabled={scanning} required />
          <button className="btn-primary" type="submit" disabled={scanning || !scanPath.trim()}><Icon name="search" />{scanning ? 'Scanning...' : 'Scan directory'}</button>
        </form>
      </div>

      {etcqlite?.enabled && !etcqlite.database && (
        <section className="scan-strip etcqlite-connection" aria-labelledby="etcqlite-connect-heading">
          <div className="scan-description"><Icon name="lock" /><div><strong id="etcqlite-connect-heading">Etcqlite read-only inspection</strong><span>{etcqlite.endpoint}</span><span>Fetch a private metadata projection. Never opens the internal store or creates a recovery backup.</span></div></div>
          <form className="inspection-consent" onSubmit={(event) => { event.preventDefault(); void connectEtcqlite(); }}>
            <label><input type="checkbox" checked={metadataConsent} onChange={(event) => setMetadataConsent(event.target.checked)} disabled={connecting} required /> I consent to metadata-only read-only inspection.</label>
            <button className="btn-primary" type="submit" disabled={!metadataConsent || connecting}><Icon name="lock" />{connecting ? 'Fetching projection...' : 'Inspect Etcqlite metadata'}</button>
          </form>
        </section>
      )}
      {integrationError && <div className="error-msg" role="alert">{integrationError}</div>}
      {error && <div className="error-msg" role="alert">{error}</div>}
      {scanMessage && <div className="scan-result" role="status"><Icon name="check" />{scanMessage}</div>}

      <div className="catalog-toolbar">
        <div className="catalog-filters" aria-label="Catalog filters">
          <button className={!favoritesOnly ? 'active' : ''} onClick={() => setFavoritesOnly(false)} aria-pressed={!favoritesOnly}>All databases <span>{databases.length}</span></button>
          <button className={favoritesOnly ? 'active' : ''} onClick={() => setFavoritesOnly(true)} aria-pressed={favoritesOnly}><Icon name="star" /> Favorites <span>{databases.filter((database) => database.Favorite).length}</span></button>
        </div>
        <div className="discovered-search">
          <Icon name="search" />
          <input type="search" value={searchQuery} onChange={(event) => setSearchQuery(event.target.value)} placeholder="Search files, paths, repositories..." aria-label="Search discovered databases" />
          {isFiltering && <button className="btn-icon" onClick={() => setSearchQuery('')} aria-label="Clear search"><Icon name="close" /></button>}
        </div>
      </div>
      <div className="catalog-summary"><span role="status">{filteredDatabases.length} database{filteredDatabases.length === 1 ? '' : 's'}{isFiltering || favoritesOnly ? ` of ${databases.length}` : ''}</span><span><Icon name="lock" /> Read-only inspection</span></div>
      {loading && databases.length === 0 && <div className="loading" role="status">Loading discovered databases...</div>}

      <div className={`discovery-workspace ${selected ? 'has-selection' : ''}`}>
      <ul className="discovered-list" aria-label="Discovered databases">
        {filteredDatabases.map((db) => (
          <li key={db.ID} className={`discovered-item ${selectedId === db.ID ? 'active' : ''} ${db.Available ? '' : 'missing'}`}>
            <div className="discovered-item-main">
              <button className="discovered-select" onClick={() => onSelect(selectedId === db.ID ? null : db.ID)} aria-label={`Inspect ${db.Name}`} aria-expanded={selectedId === db.ID} aria-controls={selectedId === db.ID ? 'discovered-inspector' : undefined}>
                <span className="file-icon"><Icon name="database" /></span>
                <span className="discovered-file"><strong>{db.Name}</strong><span className="discovered-path" title={db.SourcePath}>{db.SourcePath}</span></span>
                <Icon name="chevron-right" />
              </button>
              <button type="button" className={`favorite-toggle btn-icon ${db.Favorite ? 'active' : ''}`} onClick={() => void handleFavorite(db.ID, !db.Favorite)} disabled={actionLoading === db.ID} aria-label={db.Favorite ? `Remove ${db.Name} from favorites` : `Add ${db.Name} to favorites`} title={db.Favorite ? 'Remove from favorites' : 'Add to favorites'}><Icon name="star" filled={db.Favorite} /></button>
            </div>
            <div className="discovered-item-details">
              <span>{formatBytes(db.SizeBytes)}</span>
              <span>SQLite {db.SQLiteVersion}</span>
              <span className="journal-label">{db.JournalMode || 'Unknown journal'}</span>
              <span className="priority-badge">{PRIORITY_LABELS[db.Priority] ?? db.Priority}</span>
              {db.GitHubRepo && db.GitHubURL && <a className="github-link" href={db.GitHubURL} target="_blank" rel="noopener noreferrer">{db.GitHubRepo}<Icon name="arrow-up-right" /></a>}
            </div>
            {db.Status === 'error' && db.ErrorMessage && <div className="discovered-error-detail" role="alert">{db.ErrorMessage}</div>}
            <div className="discovered-item-bottom">
              <span className={`discovered-status-label ${statusClass(db.Status, db.IsReplica, db.Available, db.Inspection)}`}><span className={`status-indicator ${statusClass(db.Status, db.IsReplica, db.Available, db.Inspection)}`} />{statusLabel(db.Status, db.IsReplica, db.Available, db.Inspection)}</span>
            <div className="discovered-item-actions">
              {db.Available && !db.Inspection && !db.IsReplica && (db.Status === 'discovered' || db.Status === 'paused' || db.Status === 'error') && (
                <button className="btn-sm" onClick={() => void handleReplicate(db.ID)} disabled={actionLoading === db.ID} aria-label={db.Status === 'paused' ? `Resume replication of ${db.Name}` : `Replicate ${db.Name}`}><Icon name={db.Status === 'error' ? 'refresh' : 'play'} />{db.Status === 'paused' ? 'Resume' : db.Status === 'error' ? 'Retry' : 'Replicate'}</button>
              )}
              {!db.Inspection && db.Status === 'replicating' && (
                <button className="btn-sm" onClick={() => void handleStop(db.ID)} disabled={actionLoading === db.ID} aria-label={`Stop replication of ${db.Name}`}><Icon name="pause" /> Stop</button>
              )}
              {db.Available && !db.Inspection && (db.Status === 'replicating' || db.Status === 'paused') && (
                <button className="btn-sm" onClick={() => void handleRestore(db.ID, db.Name)} disabled={actionLoading === db.ID} aria-label={`Restore ${db.Name}`}><Icon name="history" /> Restore</button>
              )}
              <button className="btn-danger-sm" onClick={() => void handleRemove(db.ID, db.Name)} disabled={actionLoading === db.ID} aria-label={`Remove ${db.Name} from catalog`} title="Remove from catalog"><Icon name="close" /></button>
            </div>
            </div>
          </li>
        ))}
        {!loading && databases.length === 0 && (
          <li className="empty-state"><Icon name="search" /><h3>Find your first database</h3><p>Enter a directory above and scan for SQLite files. Your originals stay untouched.</p></li>
        )}
        {!loading && databases.length > 0 && filteredDatabases.length === 0 && (
          <li className="empty-state"><Icon name={favoritesOnly ? 'star' : 'search'} /><h3>{favoritesOnly && !isFiltering ? 'No favorites yet' : 'No matching databases'}</h3><p>{favoritesOnly && !isFiltering ? 'Star a database to keep it close at hand.' : `No databases match "${searchQuery.trim()}". Try a different name or path.`}</p><button className="btn-sm" onClick={() => { setSearchQuery(''); setFavoritesOnly(false); }}>Show all databases</button></li>
        )}
      </ul>
      {selected && (
        <div className="discovered-detail" id="discovered-inspector">
          <button className="inspector-back btn-sm" onClick={() => onSelect(null)} aria-label="Close inspector"><Icon name="chevron-left" /> Back to databases</button>
          <DiscoveredInspector key={selected.Inspection ? `${selected.ID}/${selected.Inspection.fetched_at}` : selected.ID} database={selected} darkMode={darkMode} onProjectionChange={projectionChanged} />
        </div>
      )}
      </div>
    </div>
  );
}
