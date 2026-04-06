import { useState } from 'react';
import { DatabaseList } from './components/DatabaseList';
import { SchemaViewer } from './components/SchemaViewer';
import { TableBrowser } from './components/TableBrowser';
import { SqlEditor } from './components/SqlEditor';
import { StatsPanel } from './components/StatsPanel';
import { DiscoveredPanel } from './components/DiscoveredPanel';
import { useDatabases, useSchema, useStats } from './hooks/useDatabase';

type Tab = 'browse' | 'schema' | 'query' | 'discovered';

function App() {
  const [selectedDb, setSelectedDb] = useState<string | null>(null);
  const [selectedTable, setSelectedTable] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<Tab>('browse');
  const [darkMode, setDarkMode] = useState(true);
  const [selectedDiscoveredId, setSelectedDiscoveredId] = useState<number | null>(null);

  const { databases, loading, error, refresh } = useDatabases();
  const { tables, refresh: refreshSchema } = useSchema(selectedDb);
  const stats = useStats();

  const handleSelectDb = (name: string) => {
    setSelectedDb(name);
    setSelectedTable(null);
    if (activeTab === 'discovered') setActiveTab('browse');
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
          selectedDb={selectedDb}
          onSelect={handleSelectDb}
          onRefresh={() => { refresh(); refreshSchema(); }}
        />

        <main className="main-panel">
          {loading && <div className="loading">Loading...</div>}
          {error && <div className="error-msg">{error}</div>}

          <div className="tabs">
            {selectedDb && (
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
            )}
            <button
              className={`tab ${activeTab === 'discovered' ? 'active' : ''}`}
              onClick={() => { setActiveTab('discovered'); setSelectedDb(null); }}
            >
              Discovered
            </button>
          </div>

          <div className="tab-content">
            {activeTab === 'discovered' && (
              <DiscoveredPanel
                selectedId={selectedDiscoveredId}
                onSelect={setSelectedDiscoveredId}
              />
            )}
            {activeTab !== 'discovered' && selectedDb ? (
              <>
                {activeTab === 'browse' && selectedTable && (
                  <TableBrowser dbName={selectedDb} tableName={selectedTable} />
                )}
                {activeTab === 'browse' && !selectedTable && (
                  <SchemaViewer
                    tables={tables}
                    selectedTable={null}
                    onSelectTable={handleSelectTable}
                  />
                )}
                {activeTab === 'schema' && (
                  <SchemaViewer
                    tables={tables}
                    selectedTable={selectedTable}
                    onSelectTable={setSelectedTable}
                  />
                )}
                {activeTab === 'query' && (
                  <SqlEditor dbName={selectedDb} />
                )}
              </>
            ) : activeTab !== 'discovered' ? (
              <div className="welcome">
                <h2>Welcome to sql-not-so-lite</h2>
                <p>Select a database from the sidebar or create a new one to get started.</p>
              </div>
            ) : null}
          </div>
        </main>
      </div>
    </div>
  );
}

export default App;