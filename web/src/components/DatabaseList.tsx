import { useState } from 'react';
import { api, type DBInfo } from '../api/client';

interface Props {
  databases: DBInfo[];
  selectedDb: string | null;
  onSelect: (name: string) => void;
  onRefresh: () => void;
}

export function DatabaseList({ databases, selectedDb, onSelect, onRefresh }: Props) {
  const [newDbName, setNewDbName] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleCreate = async () => {
    if (!newDbName.trim()) return;
    setCreating(true);
    setError(null);
    try {
      await api.createDatabase(newDbName.trim());
      setNewDbName('');
      onRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create database');
    } finally {
      setCreating(false);
    }
  };

  const handleDrop = async (name: string, e: React.MouseEvent) => {
    e.stopPropagation();
    if (!confirm(`Drop database "${name}"? This cannot be undone.`)) return;
    try {
      await api.dropDatabase(name);
      onRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to drop database');
    }
  };

  const formatSize = (bytes: number): string => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  return (
    <div className="sidebar">
      <div className="sidebar-header">
        <h2>Databases</h2>
        <button onClick={onRefresh} className="btn-icon" title="Refresh">⟳</button>
      </div>

      <div className="create-db">
        <input
          type="text"
          value={newDbName}
          onChange={(e) => setNewDbName(e.target.value)}
          onKeyDown={(e) => e.key === 'Enter' && handleCreate()}
          placeholder="New database name..."
          disabled={creating}
        />
        <button onClick={handleCreate} disabled={creating || !newDbName.trim()} className="btn-sm">
          {creating ? '...' : '+'}
        </button>
      </div>

      {error && <div className="error-msg">{error}</div>}

      <ul className="db-list">
        {databases.map((db) => (
          <li
            key={db.Name}
            className={`db-item ${selectedDb === db.Name ? 'active' : ''}`}
            onClick={() => onSelect(db.Name)}
          >
            <div className="db-item-info">
              <span className={`status-dot ${db.Active ? 'active' : 'idle'}`} />
              <span className="db-name">{db.Name}</span>
            </div>
            <div className="db-item-meta">
              <span className="db-size">{formatSize(db.SizeBytes)}</span>
              <span className="db-tables">{db.TableCount} tables</span>
              <button
                className="btn-danger-sm"
                onClick={(e) => handleDrop(db.Name, e)}
                title="Drop database"
              >
                ✕
              </button>
            </div>
          </li>
        ))}
        {databases.length === 0 && (
          <li className="db-item empty">No databases yet</li>
        )}
      </ul>
    </div>
  );
}
