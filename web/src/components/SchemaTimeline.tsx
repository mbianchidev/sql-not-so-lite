import { useState, useEffect, useCallback } from 'react';
import { api, type SchemaVersionInfo, type SchemaTransitionInfo, type SnapshotInfo } from '../api/client';

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

function formatSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
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

  if (loading) return <div className="loading">Loading schema history…</div>;
  if (error) return <div className="error-msg">{error}</div>;

  return (
    <div className="schema-timeline">
      <div className="timeline-header">
        <h4>Schema History — {dbName}</h4>
        <button className="btn-icon" onClick={refresh} title="Refresh">⟳</button>
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
                  onClick={() => setExpandedVersion(isExpanded ? null : ver.Version)}
                >
                  <div className="version-card-header">
                    <span className="version-badge">v{ver.Version}</span>
                    <span className="version-hash" title={ver.SchemaHash}>
                      {ver.SchemaHash.slice(0, 12)}
                    </span>
                    <span className="version-date">{formatDate(ver.DetectedAt)}</span>
                  </div>

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
                          <span className="snapshot-size">{formatSize(snap.SizeBytes)}</span>
                          <span className="snapshot-trigger">{snap.Trigger}</span>
                          <span className="snapshot-date">{formatDate(snap.CreatedAt)}</span>
                          <button
                            className="btn-sm"
                            onClick={(e) => { e.stopPropagation(); handleRestore(snap.Version); }}
                            disabled={restoring === snap.Version}
                          >
                            {restoring === snap.Version ? '…' : '↻ Restore'}
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
