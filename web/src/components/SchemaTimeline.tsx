import { useState, useEffect, useCallback } from 'react';
import { api, type SchemaVersionInfo, type SchemaTransitionInfo, type SnapshotInfo } from '../api/client';
import { Icon } from './Icon';
import { formatBytes } from '../utils/format';

interface Props {
  dbId: number;
  dbName: string;
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleString();
  } catch {
    return iso;
  }
}

export function SchemaTimeline({ dbId, dbName }: Props) {
  const [versions, setVersions] = useState<SchemaVersionInfo[]>([]);
  const [transitions, setTransitions] = useState<SchemaTransitionInfo[]>([]);
  const [snapshots, setSnapshots] = useState<SnapshotInfo[]>([]);
  const [expandedVersion, setExpandedVersion] = useState<number | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [restoring, setRestoring] = useState<number | null>(null);

  const refresh = useCallback(async () => {
    try {
      setLoading(true);
      setError(null);
      const [v, t, s] = await Promise.all([
        api.listVersions(dbId),
        api.listTransitions(dbId),
        api.listSnapshots(dbId),
      ]);
      setVersions(v || []);
      setTransitions(t || []);
      setSnapshots(s || []);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load schema history');
    } finally {
      setLoading(false);
    }
  }, [dbId]);

  useEffect(() => { refresh(); }, [refresh]);

  const handleRestore = async (snapshotVersion: number) => {
    if (!confirm(`Restore snapshot ${snapshotVersion} to the local replica of "${dbName}"? This replaces replica contents, not the original source file.`)) return;
    try {
      setRestoring(snapshotVersion);
      await api.restoreSnapshot(dbId, snapshotVersion);
      await refresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to restore snapshot');
    } finally {
      setRestoring(null);
    }
  };

  const getTransitionTo = (version: number): SchemaTransitionInfo | undefined =>
    transitions.find((t) => t.ToVersion === version);

  const getSnapshotsForVersion = (schemaVersion: number): SnapshotInfo[] =>
    snapshots.filter((s) => s.SchemaVersion === schemaVersion);

  if (loading) return <div className="loading" role="status">Loading schema history...</div>;
  if (error) return <div className="error-msg" role="alert">{error}</div>;

  return (
    <div className="schema-timeline">
      <div className="timeline-header">
        <h4>Schema history</h4>
        <button className="btn-icon" onClick={refresh} aria-label="Refresh schema history" title="Refresh"><Icon name="refresh" /></button>
      </div>

      {versions.length === 0 ? (
        <div className="timeline-empty">No schema versions recorded yet.</div>
      ) : (
        <div className="timeline-list">
          {versions.map((ver) => {
            const transition = getTransitionTo(ver.Version);
            const versionSnapshots = getSnapshotsForVersion(ver.Version);
            const isExpanded = expandedVersion === ver.Version;

            return (
              <div key={ver.Version} className="timeline-entry">
                {transition && (
                  <div className="transition-arrow">
                    <span className="transition-line" />
                    <span className="transition-summary">{transition.Summary}</span>
                    {transition.DetectedDDL && (
                      <code className="transition-ddl">{transition.DetectedDDL}</code>
                    )}
                  </div>
                )}

                <div
                  className={`version-card ${isExpanded ? 'expanded' : ''}`}
                >
                  <button className="version-card-header" onClick={() => setExpandedVersion(isExpanded ? null : ver.Version)} aria-expanded={isExpanded} aria-label={`Schema version ${ver.Version}, ${formatDate(ver.DetectedAt)}`}>
                    <span className="version-badge">v{ver.Version}</span>
                    <span className="version-hash" title={ver.SchemaHash}>
                      {ver.SchemaHash.slice(0, 12)}
                    </span>
                    <span className="version-date">{formatDate(ver.DetectedAt)}</span>
                    <Icon name={isExpanded ? 'chevron-down' : 'chevron-right'} />
                  </button>

                  {isExpanded && (
                    <div className="version-card-body">
                      <pre className="schema-sql">{ver.SchemaSQL}</pre>
                    </div>
                  )}

                  {versionSnapshots.length > 0 && (
                    <div className="snapshot-list">
                      <span className="snapshot-label">Snapshots:</span>
                      {versionSnapshots.map((snap) => (
                        <div key={snap.ID} className="snapshot-item">
                          <span className="snapshot-version">#{snap.Version}</span>
                          <span className="snapshot-size">{formatBytes(snap.SizeBytes)}</span>
                          <span className="snapshot-trigger">{snap.Trigger}</span>
                          <span className="snapshot-date">{formatDate(snap.CreatedAt)}</span>
                          <button
                            className="btn-sm"
                            onClick={(e) => { e.stopPropagation(); handleRestore(snap.Version); }}
                            disabled={restoring === snap.Version}
                          >
                            <Icon name="history" />{restoring === snap.Version ? 'Restoring...' : 'Restore'}
                          </button>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
