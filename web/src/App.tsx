import { useState } from 'react';
import { DatabaseList } from './components/DatabaseList';
import { SchemaViewer } from './components/SchemaViewer';
import { TableBrowser } from './components/TableBrowser';
import { SqlEditor } from './components/SqlEditor';
import { StatsPanel } from './components/StatsPanel';
import { DiscoveredPanel } from './components/DiscoveredPanel';
import { useDatabases, useSchema, useStats } from './hooks/useDatabase';

type DatabaseTab = 'browse' | 'schema' | 'query';
type View = 'database' | 'discovered';

function App() {
  const [selectedDb, setSelectedDb] = useState<string | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<DatabaseTab>('browse');
  const [view, setView] = useState<View>('database');
  const [darkMode, setDarkMode] = useState(true);
  const [selectedDiscoveredId, setSelectedDiscoveredId] = useState<number | null>(null);

  const { databases, loading, error, refresh } = useDatabases();
  const { tables, refresh: refreshSchema } = useSchema(selectedDb);
  const stats = useStats();

  const handleSelectDb = (name: string) => {
    setSelectedDb(name);
    setSelectedTable(null);
    setView('database');
  };

  const handleSelectTable = (name: string) => {
    setSelectedTable(name);
    setActiveTab('browse');
  };

  return (
    <div className={`app ${darkMode ? 'dark' : 'light'}`}>
      <header className="app-header">
        <div className="logo">
          <span className="logo-icon">⛁</span>
          <h1>sql-not-so-lite</h1>
        </div>
        <StatsPanel stats={stats} />
        <button
          className="btn-icon theme-toggle"
          onClick={() => setDarkMode(!darkMode)}
          title={darkMode ? 'Light mode' : 'Dark mode'}
        >
          {darkMode ? '☀' : '☾'}
        </button>
      </header>

      <div className="app-body">
        <DatabaseList
          databases={databases}
          selectedDb={view === 'database' ? selectedDb : null}
          onSelect={handleSelectDb}
          onRefresh={() => { refresh(); refreshSchema(); }}
          discoveryActive={view === 'discovered'}
          onOpenDiscovery={() => setView('discovered')}
        />

        <main className="main-panel">
          {loading && <div className="loading">Loading...</div>}
          {error && <div className="error-msg">{error}</div>}

          {view === 'database' && selectedDb && (
            <div className="tabs">
              <>
                <button
                  className={`tab ${activeTab === 'browse' ? 'active' : ''}`}
                  onClick={() => setActiveTab('browse')}
                >
                  Browse
                </button>
                <button
                  className={`tab ${activeTab === 'schema' ? 'active' : ''}`}
                  onClick={() => setActiveTab('schema')}
                >
                  Schema
                </button>
                <button
                  className={`tab ${activeTab === 'query' ? 'active' : ''}`}
                  onClick={() => setActiveTab('query')}
                >
                  SQL Editor
                </button>
              </>
            </div>
          )}

          <div className="tab-content">
            {view === 'discovered' ? (
              <DiscoveredPanel
                selectedId={selectedDiscoveredId}
                onSelect={setSelectedDiscoveredId}
              />
            ) : selectedDb ? (
              <>
                {activeTab === 'browse' && selectedTable && (
                  <TableBrowser dbName={selectedDb} tableName={selectedTable} />
                )}
                {activeTab === 'browse' && !selectedTable && (
                  <SchemaViewer
                    dbName={selectedDb}
                    tables={tables}
                    selectedTable={null}
                    onSelectTable={handleSelectTable}
                    onSchemaChange={async () => { await Promise.all([refresh(), refreshSchema()]); }}
                  />
                )}
                {activeTab === 'schema' && (
                  <SchemaViewer
                    dbName={selectedDb}
                    tables={tables}
                    selectedTable={selectedTable}
                    onSelectTable={setSelectedTable}
                    onSchemaChange={async () => { await Promise.all([refresh(), refreshSchema()]); }}
                  />
                )}
                {activeTab === 'query' && (
                  <SqlEditor dbName={selectedDb} />
                )}
              </>
            ) : (
              <div className="welcome">
                <h2>Welcome to sql-not-so-lite</h2>
                <p>Select a database from the sidebar or create a new one to get started.</p>
              </div>
            )}
          </div>
        </main>
      </div>
    </div>
  );
}

export default App;