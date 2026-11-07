import type { StatsInfo } from '../api/client';
import { formatBytes, formatUptime } from '../utils/format';

interface Props {
  stats: StatsInfo | null;
}

export function StatsPanel({ stats }: Props) {
  if (!stats) return null;

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
