import type { TableInfo } from '../api/client';

interface Props {
  tables: TableInfo[];
  selectedTable: string | null;
  onSelectTable: (name: string) => void;
}

export function SchemaViewer({ tables, selectedTable, onSelectTable }: Props) {
  const selected = tables.find((t) => t.Name === selectedTable);

  return (
    <div className="schema-viewer">
      <div className="schema-tables">
        <h3>Tables</h3>
        <ul className="table-list">
          {tables.map((t) => (
            <li
              key={t.Name}
              className={`table-item ${selectedTable === t.Name ? 'active' : ''}`}
              onClick={() => onSelectTable(t.Name)}
            >
              <span className="table-icon">⊞</span>
              <span className="table-name">{t.Name}</span>
              <span className="row-count">{t.RowCount} rows</span>
            </li>
          ))}
          {tables.length === 0 && (
            <li className="table-item empty">No tables</li>
          )}
        </ul>
      </div>

      {selected && (
        <div className="schema-details">
          <h3>{selected.Name}</h3>

          <div className="schema-section">
            <h4>Columns</h4>
            <table className="schema-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                  <th>Nullable</th>
                  <th>Default</th>
                  <th>PK</th>
                </tr>
              </thead>
              <tbody>
                {selected.Columns?.map((col) => (
                  <tr key={col.Name}>
                    <td className="col-name">{col.Name}</td>
                    <td className="col-type-badge">{col.Type}</td>
                    <td>{col.Nullable ? '✓' : '✕'}</td>
                    <td className="col-default">{col.DefaultValue || '—'}</td>
                    <td>{col.PrimaryKey ? '🔑' : ''}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>

          {selected.Indexes && selected.Indexes.length > 0 && (
            <div className="schema-section">
              <h4>Indexes</h4>
              <table className="schema-table">
                <thead>
                  <tr>
                    <th>Name</th>
                    <th>Columns</th>
                    <th>Unique</th>
                  </tr>
                </thead>
                <tbody>
                  {selected.Indexes.map((idx) => (
                    <tr key={idx.Name}>
                      <td>{idx.Name}</td>
                      <td>{idx.Columns?.join(', ')}</td>
                      <td>{idx.Unique ? '✓' : ''}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
