import { useCallback, useState } from 'react';
import { DatabaseList } from './components/DatabaseList';
import { SchemaViewer } from './components/SchemaViewer';
import { TableBrowser } from './components/TableBrowser';
import { SqlEditor } from './components/SqlEditor';
import { StatsPanel } from './components/StatsPanel';
import { DiscoveredPanel } from './components/DiscoveredPanel';
import { Icon } from './components/Icon';
import { TabBar } from './components/TabBar';
import { APIExposurePanel } from './components/APIExposurePanel';
import { hasAdminToken, signOut } from './api/client';
import { useDatabases, useSchema, useStats } from './hooks/useDatabase';
import { useTheme } from './hooks/useTheme';
import { formatBytes } from './utils/format';

type DatabaseTab = 'browse' | 'schema' | 'query';
type View = 'database' | 'discovered';

function App() {
  const [selectedDb, setSelectedDb] = useState<string | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<DatabaseTab>('browse');
  const [startCreatingTable, setStartCreatingTable] = useState(false);
  const [showAPI, setShowAPI] = useState(false);
  const [view, setView] = useState<View>('discovered');
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [selectedDiscoveredId, setSelectedDiscoveredId] = useState<number | null>(null);
  const { darkMode, toggleTheme, error: themeError } = useTheme();
  const { databases, loading, error, refresh } = useDatabases();
  const { tables, loading: schemaLoading, error: schemaError, refresh: refreshSchema } = useSchema(selectedDb);
  const { stats, error: statsError } = useStats();
  const database = databases.find((item) => item.Name === selectedDb);
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);

  const refreshWorkspace = async () => { await Promise.all([refresh(), refreshSchema()]); };
  const handleSelectDb = (name: string) => {
    setSelectedDb(name);
    setSelectedTable(null);
    setActiveTab('browse');
    setStartCreatingTable(false);
    setShowAPI(false);
    setView('database');
    closeSidebar();
  };
  const handleSelectTable = (name: string) => {
    setSelectedTable(name);
    setActiveTab('browse');
    closeSidebar();
  };
  const openDiscovery = () => { setView('discovered'); closeSidebar(); };

  return (
    <div className="app">
      <a className="skip-link" href="#main-content">Skip to main content</a>
      <header className="app-header">
        <button
          className="btn-icon mobile-menu"
          onClick={() => setSidebarOpen(true)}
          aria-label="Open database navigation"
          aria-haspopup="dialog"
          aria-expanded={sidebarOpen}
          aria-controls="database-navigation"
        >
          <Icon name="menu" />
        </button>
        <div className="logo">
          <span className="logo-icon"><Icon name="database" /></span>
          <h1>sql-not-so-lite</h1>
        </div>
        <div className="header-context">
          <span>Workspace</span>
          <Icon name="chevron-right" />
          <strong>{view === 'discovered' ? 'Discovery' : selectedDb}</strong>
        </div>
        <div className="header-actions">
          {hasAdminToken() && <button className="btn-sm" onClick={signOut}>Sign out</button>}
          <a className="btn-icon" href="https://github.com/mbianchidev/sql-not-so-lite/tree/main/docs" target="_blank" rel="noopener noreferrer" aria-label="Documentation" title="Documentation">
            <Icon name="help" />
          </a>
          <button className="btn-icon theme-toggle" onClick={toggleTheme} aria-label={darkMode ? 'Switch to light mode' : 'Switch to dark mode'} title={darkMode ? 'Switch to light mode' : 'Switch to dark mode'}>
            <Icon name={darkMode ? 'sun' : 'moon'} />
          </button>
        </div>
      </header>

      <div className="app-body">
        <DatabaseList
          databases={databases}
          selectedDb={view === 'database' ? selectedDb : null}
          selectedTable={selectedTable}
          tables={tables}
          onSelect={handleSelectDb}
          onSelectTable={handleSelectTable}
          onRefresh={refreshWorkspace}
          onDrop={(name) => {
            if (name === selectedDb) {
              setSelectedDb(null);
              setSelectedTable(null);
              setView('discovered');
            }
          }}
          discoveryActive={view === 'discovered'}
          onOpenDiscovery={openDiscovery}
          mobileOpen={sidebarOpen}
          onClose={closeSidebar}
          loading={loading}
        />

        <main id="main-content" className="main-panel" tabIndex={-1}>
          {error && <div className="error-msg workspace-error" role="alert">{error}</div>}
          {themeError && <div className="error-msg workspace-error" role="alert">{themeError}</div>}
          {view === 'database' && selectedDb && (
            <>
              <div className="workspace-heading">
                <div>
                  <h2>{selectedDb}</h2>
                  <p>{tables.length} tables <span aria-hidden="true">/</span> {formatBytes(database?.SizeBytes ?? 0)} <span aria-hidden="true">/</span> Managed database</p>
                </div>
                <div className="workspace-heading-actions"><span className="access-badge"><Icon name="database" /> Read &amp; write</span><button className="btn-sm" onClick={() => setShowAPI((current) => !current)} aria-expanded={showAPI}><Icon name="terminal" /> Expose API</button></div>
              </div>
              <TabBar<DatabaseTab>
                id="database"
                label="Database workspace"
                active={activeTab}
                onChange={(tab) => { setStartCreatingTable(false); setActiveTab(tab); }}
                tabs={[
                  { value: 'browse', label: 'Browse', icon: 'table' },
                  { value: 'schema', label: 'Schema', icon: 'schema' },
                  { value: 'query', label: 'SQL Editor', icon: 'terminal' },
                ]}
              />
            </>
          )}
          <div
            className="tab-content"
            id={view === 'database' ? 'database-panel' : undefined}
            role={view === 'database' ? 'tabpanel' : undefined}
            aria-labelledby={view === 'database' ? `database-tab-${activeTab}` : undefined}
            tabIndex={view === 'database' ? 0 : undefined}
          >
            {view === 'discovered' ? (
              <DiscoveredPanel selectedId={selectedDiscoveredId} onSelect={setSelectedDiscoveredId} darkMode={darkMode} />
            ) : selectedDb ? (
              <>
                {showAPI && <APIExposurePanel source={{ managed_database: selectedDb }} tables={tables} onClose={() => setShowAPI(false)} />}
                {schemaError && <div className="error-msg" role="alert">{schemaError}<button className="btn-sm" onClick={refreshSchema}>Retry schema</button></div>}
                {schemaLoading && tables.length === 0 && activeTab !== 'query' ? <div className="loading" role="status">Reading database schema...</div> : (
                  <>
                    {activeTab === 'browse' && selectedTable && (
                      <TableBrowser key={`${selectedDb}/${selectedTable}`} dbName={selectedDb} tableName={selectedTable} tableInfo={tables.find((table) => table.Name === selectedTable)} onDataChange={refreshWorkspace} />
                    )}
                    {activeTab === 'browse' && !selectedTable && !schemaError && (
                      <section className="database-overview" aria-label="Database tables">
                        <div className="section-heading">
                          <div><h3>Your tables</h3><p>Select a table to explore its data.</p></div>
                          <button className="btn-primary" onClick={() => { setStartCreatingTable(true); setActiveTab('schema'); }}><Icon name="plus" /> New table</button>
                        </div>
                        {tables.length > 0 ? (
                          <div className="overview-table-list">
                            <div className="overview-table-labels"><span>Table</span><span>Fields</span><span>Rows</span><span /></div>
                            {tables.map((table) => (
                              <button className="overview-table-row" key={table.Name} onClick={() => handleSelectTable(table.Name)}>
                                <span><Icon name="table" />{table.Name}</span>
                                <span>{table.Columns.length}</span>
                                <span>{table.RowCount.toLocaleString()}</span>
                                <Icon name="chevron-right" />
                              </button>
                            ))}
                          </div>
                        ) : <div className="empty-state"><Icon name="table" /><h3>A fresh database</h3><p>Create your first table in Schema, or write a CREATE TABLE statement in the SQL editor.</p><button className="btn-sm" onClick={() => setActiveTab('query')}><Icon name="terminal" /> Open SQL editor</button></div>}
                      </section>
                    )}
                    {activeTab === 'schema' && !schemaError && (
                      <SchemaViewer key={selectedDb} dbName={selectedDb} tables={tables} selectedTable={selectedTable} onSelectTable={setSelectedTable} onSchemaChange={refreshWorkspace} startCreating={startCreatingTable} />
                    )}
                    {activeTab === 'query' && <SqlEditor key={selectedDb} dbName={selectedDb} darkMode={darkMode} />}
                  </>
                )}
              </>
            ) : <div className="empty-state"><Icon name="database" /><h2>Your SQLite workspace</h2><p>Select a database from the sidebar or create a new one to get started.</p></div>}
          </div>
        </main>
      </div>

      <footer className="app-footer">
        <span className={`connection-status ${statsError ? 'offline' : stats ? 'online' : ''}`} role="status" title={statsError ?? 'Local SQLite daemon'}>
          <span className="status-dot" />
          {statsError ? 'Daemon unavailable' : stats ? 'Daemon connected' : 'Connecting...'}
        </span>
        <StatsPanel stats={statsError ? null : stats} />
      </footer>
    </div>
  );
}

export default App;
