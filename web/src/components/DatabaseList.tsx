import { useEffect, useRef, useState } from 'react';
import { api, type DBInfo, type TableInfo } from '../api/client';
import { formatBytes } from '../utils/format';
import { Icon } from './Icon';

interface Props {
  databases: DBInfo[];
  selectedDb: string | null;
  selectedTable: string | null;
  tables: TableInfo[];
  onSelect: (name: string) => void;
  onSelectTable: (name: string) => void;
  onRefresh: () => Promise<void>;
  onDrop: (name: string) => void;
  discoveryActive: boolean;
  onOpenDiscovery: () => void;
  mobileOpen: boolean;
  onClose: () => void;
  loading: boolean;
}

export function DatabaseList({ databases, selectedDb, selectedTable, tables, onSelect, onSelectTable, onRefresh, onDrop, discoveryActive, onOpenDiscovery, mobileOpen, onClose, loading }: Props) {
  const [newDbName, setNewDbName] = useState('');
  const [showCreate, setShowCreate] = useState(false);
  const [search, setSearch] = useState('');
  const [creating, setCreating] = useState(false);
  const [dropping, setDropping] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [mobile, setMobile] = useState(() => window.matchMedia('(max-width: 800px)').matches);
  const dialog = useRef<HTMLDialogElement>(null);

  useEffect(() => {
    const media = window.matchMedia('(max-width: 800px)');
    const change = () => { setMobile(media.matches); if (!media.matches) onClose(); };
    media.addEventListener('change', change);
    return () => media.removeEventListener('change', change);
  }, [onClose]);
  useEffect(() => {
    if (mobile && mobileOpen) dialog.current?.showModal();
    else dialog.current?.close();
  }, [mobile, mobileOpen]);

  const handleCreate = async () => {
    if (!newDbName.trim() || creating) return;
    const name = newDbName.trim();
    setCreating(true);
    setError(null);
    try {
      await api.createDatabase(name);
      await onRefresh();
      setNewDbName('');
      setShowCreate(false);
      setSearch('');
      onSelect(name);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create database');
    } finally {
      setCreating(false);
    }
  };
  const handleDrop = async (name: string) => {
    if (!confirm(`Drop database "${name}"? This cannot be undone.`)) return;
    setDropping(name);
    setError(null);
    try {
      await api.dropDatabase(name);
      onDrop(name);
      await onRefresh();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to drop database');
    } finally {
      setDropping(null);
    }
  };
  const visible = databases.filter((database) => `${database.Name} ${database.Path}`.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase()));

  const content = (
    <>
      <div className="sidebar-workspace"><span className="workspace-mark"><Icon name="folder" /></span><div><strong>Local workspace</strong><span>SQLite databases</span></div>{mobile && <button className="btn-icon" onClick={onClose} aria-label="Close database navigation"><Icon name="close" /></button>}</div>
      <nav className="sidebar-menu" aria-label="Workspace navigation">
        <button className={`sidebar-menu-item ${discoveryActive ? 'active' : ''}`} onClick={onOpenDiscovery} aria-current={discoveryActive ? 'page' : undefined}><Icon name="search" /><span>Discovery</span><Icon name="chevron-right" /></button>
      </nav>
      <div className="sidebar-header"><h2>Managed databases <span>{databases.length}</span></h2><button onClick={onRefresh} className="btn-icon" aria-label="Refresh databases" title="Refresh databases"><Icon name="refresh" /></button><button onClick={() => setShowCreate((current) => !current)} className="btn-icon" aria-label="New database" title="New database" aria-expanded={showCreate}><Icon name="plus" /></button></div>
      <div className="sidebar-search"><Icon name="search" /><input type="search" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Find a database..." aria-label="Search managed databases" /></div>
      {showCreate && (
        <form className="create-db" onSubmit={(event) => { event.preventDefault(); void handleCreate(); }}>
          <label htmlFor="new-database-name">Database name</label>
          <input id="new-database-name" value={newDbName} onChange={(event) => setNewDbName(event.target.value)} placeholder="my_database" disabled={creating} required autoFocus />
          <div><button type="button" className="btn-sm" onClick={() => setShowCreate(false)} disabled={creating}>Cancel</button><button type="submit" className="btn-primary" disabled={creating || !newDbName.trim()}>{creating ? 'Creating...' : 'Create database'}</button></div>
        </form>
      )}
      {error && <div className="error-msg" role="alert">{error}</div>}
      <ul className="db-list">
        {visible.map((database) => (
          <li key={database.Name} className={`db-item ${selectedDb === database.Name ? 'active' : ''}`}>
            <div className="db-item-row">
              <button className="db-select" onClick={() => onSelect(database.Name)} aria-label={`Open ${database.Name}`} aria-current={selectedDb === database.Name ? 'page' : undefined}>
                <Icon name={selectedDb === database.Name ? 'chevron-down' : 'chevron-right'} />
                <Icon name="database" />
                <span><strong>{database.Name}</strong><small><span className={`status-dot ${database.Active ? 'active' : ''}`} />{database.Active ? 'Active' : 'Idle'}<span>{formatBytes(database.SizeBytes)}</span></small></span>
              </button>
              <button className="btn-danger-sm db-drop" onClick={() => void handleDrop(database.Name)} disabled={dropping !== null} aria-label={`Drop ${database.Name}`} title={`Drop ${database.Name}`}><Icon name="trash" /></button>
            </div>
            {selectedDb === database.Name && (
              <ul className="sidebar-table-list">
                {tables.map((table) => <li key={table.Name}><button className={selectedTable === table.Name ? 'active' : ''} onClick={() => onSelectTable(table.Name)} aria-label={`Browse ${table.Name} table`} aria-current={selectedTable === table.Name ? 'true' : undefined}><Icon name="table" /><span>{table.Name}</span><small>{table.RowCount.toLocaleString()}</small></button></li>)}
                {tables.length === 0 && <li className="sidebar-empty">No tables loaded</li>}
              </ul>
            )}
          </li>
        ))}
        {!loading && visible.length === 0 && <li className="sidebar-empty">{search ? 'No matching databases.' : 'No managed databases yet.'}{!search && <button className="btn-sm" onClick={() => setShowCreate(true)}><Icon name="plus" /> Create a database</button>}</li>}
        {loading && databases.length === 0 && <li className="loading" role="status">Loading databases...</li>}
      </ul>
      <div className="sidebar-note"><Icon name="lock" /><p>Discovered files stay in place.<br />Managed databases are writable.</p></div>
    </>
  );

  return mobile ? (
    <dialog
      ref={dialog}
      id="database-navigation"
      className="sidebar-dialog"
      aria-label="Database navigation"
      onCancel={(event) => { event.preventDefault(); onClose(); }}
      onClick={(event) => { if (event.target === event.currentTarget) onClose(); }}
      onKeyDown={(event) => {
        if (event.key !== 'Tab' || event.ctrlKey || event.metaKey || event.altKey) return;
        const controls = Array.from(event.currentTarget.querySelectorAll<HTMLElement>(
          'button:not(:disabled), input:not(:disabled), select:not(:disabled), a[href], [tabindex="0"]',
        )).filter((element) => element.getClientRects().length > 0);
        const first = controls[0];
        const last = controls.at(-1);
        if (!first || !last) return;
        if (event.shiftKey && document.activeElement === first) {
          event.preventDefault();
          last.focus();
        } else if (!event.shiftKey && document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }}
    >
      <div className="sidebar">{content}</div>
    </dialog>
  ) : <aside id="database-navigation" className="sidebar" aria-label="Database navigation">{content}</aside>;
}
