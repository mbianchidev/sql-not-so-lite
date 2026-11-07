import type { EtcqliteInspection } from '../api/client';

export function formatInspectionState(state: EtcqliteInspection['state']): string {
  return {
    ready: 'Projection checked',
    stale: 'Stale projection',
    disconnected: 'Disconnected',
    missing: 'Missing projection',
    disabled: 'Inspection disabled',
  }[state];
}

export function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KB`;
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
}

export function formatUptime(seconds: number): string {
  if (seconds < 60) return `${Math.floor(seconds)}s`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m`;
  return `${Math.floor(seconds / 3600)}h ${Math.floor((seconds % 3600) / 60)}m`;
}
