import type { StatsInfo } from '../api/client';

interface Props {
  stats: StatsInfo | null;
}

export function StatsPanel({ stats }: Props) {
  if (!stats) return null;

  const formatUptime = (seconds: number): string => {
    if (seconds < 60) return `${seconds}s`;
    if (seconds < 3600) return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
    const h = Math.floor(seconds / 3600);
    const m = Math.floor((seconds % 3600) / 60);
    return `${h}h ${m}m`;
  };

  const formatBytes = (bytes: number): string => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  return (
    <div className="stats-panel">
      <div className="stat">
        <span className="stat-label">Version</span>
        <span className="stat-value">{stats.version}</span>
      </div>
      <div className="stat">
        <span className="stat-label">Uptime</span>
        <span className="stat-value">{formatUptime(stats.uptime_seconds)}</span>
      </div>
      <div className="stat">
        <span className="stat-label">Active DBs</span>
        <span className="stat-value">{stats.active_databases}</span>
      </div>
      <div className="stat">
        <span className="stat-label">Memory</span>
        <span className="stat-value">{formatBytes(stats.memory_alloc)}</span>
      </div>
      <div className="stat">
        <span className="stat-label">Goroutines</span>
        <span className="stat-value">{stats.goroutines}</span>
      </div>
    </div>
  );
}
