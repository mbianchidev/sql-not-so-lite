import { useState } from 'react';
import { api, type ColumnDefinition, type TableInfo } from '../api/client';

interface Props {
  dbName: string;
  tables: TableInfo[];
  selectedTable: string | null;
  onSelectTable: (name: string) => void;
  onSchemaChange: () => Promise<void>;
}

const columnTypes = ['INTEGER', 'TEXT', 'REAL', 'NUMERIC', 'BOOLEAN', 'DATE', 'DATETIME', 'BLOB'];

const emptyColumn = (): ColumnDefinition => ({
  Name: '',
  Type: 'TEXT',
  NotNull: false,
  PrimaryKey: false,
});

export function SchemaViewer({ dbName, tables, selectedTable, onSelectTable, onSchemaChange }: Props) {
  const selected = tables.find((t) => t.Name === selectedTable);
  const [newTableName, setNewTableName] = useState('');
  const [tableColumn, setTableColumn] = useState<ColumnDefinition>(emptyColumn);
  const [newColumn, setNewColumn] = useState<ColumnDefinition>(emptyColumn);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const save = async (action: () => Promise<unknown>, afterSave: () => void) => {
    setSaving(true);
    setError(null);
    try {
      await action();
      await onSchemaChange();
      afterSave();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Schema update failed');
    } finally {
      setSaving(false);
    }
  };

  const handleCreateTable = () => {
    const tableName = newTableName.trim();
    const column = { ...tableColumn, Name: tableColumn.Name.trim() };
    if (!tableName || !column.Name) return;
    void save(
      () => api.createTable(dbName, tableName, [column]),
      () => {
        setNewTableName('');
        setTableColumn(emptyColumn());
        onSelectTable(tableName);
      },
    );
  };

  const handleAddColumn = () => {
    if (!selected) return;
    const column = { ...newColumn, Name: newColumn.Name.trim() };
    if (!column.Name) return;
    void save(
      () => api.addColumn(dbName, selected.Name, column),
      () => setNewColumn(emptyColumn()),
    );
  };

  return (
    <div className="schema-viewer">
      <div className="schema-tables">
        <h3>Tables</h3>
        <div className="schema-form">
          <input
            value={newTableName}
            onChange={(event) => setNewTableName(event.target.value)}
            placeholder="Table name"
            disabled={saving}
          />
          <ColumnForm value={tableColumn} onChange={setTableColumn} allowPrimaryKey disabled={saving} />
          <button
            className="btn-primary"
            onClick={handleCreateTable}
            disabled={saving || !newTableName.trim() || !tableColumn.Name.trim()}
          >
            Create table
          </button>
        </div>
        {error && <div className="error-msg">{error}</div>}
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
            <div className="schema-form schema-form-inline">
              <ColumnForm value={newColumn} onChange={setNewColumn} disabled={saving} />
              <button
                className="btn-sm"
                onClick={handleAddColumn}
                disabled={saving || !newColumn.Name.trim()}
              >
                Add field
              </button>
            </div>
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

interface ColumnFormProps {
  value: ColumnDefinition;
  onChange: (value: ColumnDefinition) => void;
  allowPrimaryKey?: boolean;
  disabled: boolean;
}

function ColumnForm({ value, onChange, allowPrimaryKey = false, disabled }: ColumnFormProps) {
  const set = (changes: Partial<ColumnDefinition>) => onChange({ ...value, ...changes });

  return (
    <div className="column-form">
      <input
        value={value.Name}
        onChange={(event) => set({ Name: event.target.value })}
        placeholder="Field name"
        disabled={disabled}
      />
      <select value={value.Type} onChange={(event) => set({ Type: event.target.value })} disabled={disabled}>
        {columnTypes.map((type) => <option key={type}>{type}</option>)}
      </select>
      <input
        value={value.DefaultValue ?? ''}
        onChange={(event) => set({ DefaultValue: event.target.value === '' ? undefined : event.target.value })}
        placeholder="Default (optional)"
        disabled={disabled}
      />
      <label>
        <input
          type="checkbox"
          checked={value.NotNull}
          onChange={(event) => set({ NotNull: event.target.checked })}
          disabled={disabled || value.PrimaryKey}
        />
        Not null
      </label>
      {allowPrimaryKey && (
        <label>
          <input
            type="checkbox"
            checked={value.PrimaryKey}
            onChange={(event) => set({ PrimaryKey: event.target.checked, NotNull: event.target.checked || value.NotNull })}
            disabled={disabled}
          />
          Primary key
        </label>
      )}
    </div>
  );
}
