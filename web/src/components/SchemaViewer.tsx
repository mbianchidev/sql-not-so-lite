import { useState } from 'react';
import { api, type ColumnDefinition, type ColumnInfo, type TableInfo } from '../api/client';

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

interface DraftColumn {
  id: number;
  definition: ColumnDefinition;
}

let nextDraftColumnId = 0;
const createDraftColumn = (): DraftColumn => ({
  id: nextDraftColumnId++,
  definition: emptyColumn(),
});

function editableDefault(value: string): string | undefined {
  if (!value) return undefined;
  if (value.startsWith("'") && value.endsWith("'")) {
    return value.slice(1, -1).replaceAll("''", "'");
  }
  return value;
}

export function SchemaViewer({ dbName, tables, selectedTable, onSelectTable, onSchemaChange }: Props) {
  const selected = tables.find((t) => t.Name === selectedTable);
  const [newTableName, setNewTableName] = useState('');
  const [tableColumns, setTableColumns] = useState<DraftColumn[]>([createDraftColumn()]);
  const [newColumn, setNewColumn] = useState<ColumnDefinition>(emptyColumn);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [editingColumn, setEditingColumn] = useState<string | null>(null);
  const [columnEdit, setColumnEdit] = useState<ColumnDefinition>(emptyColumn);

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
    const columns = tableColumns.map(({ definition }) => ({
      ...definition,
      Name: definition.Name.trim(),
    }));
    if (!tableName || columns.some((column) => !column.Name)) {
      setError('Enter a table name and a name for every field.');
      return;
    }
    const names = columns.map((column) => column.Name.toLocaleLowerCase());
    if (new Set(names).size !== names.length) {
      setError('Field names must be unique.');
      return;
    }
    void save(
      () => api.createTable(dbName, tableName, columns),
      () => {
        setNewTableName('');
        setTableColumns([createDraftColumn()]);
        onSelectTable(tableName);
      },
    );
  };

  const updateTableColumn = (index: number, column: ColumnDefinition) => {
    setTableColumns((current) => current.map((item, itemIndex) => (
      itemIndex === index ? { ...item, definition: column } : item
    )));
  };

  const moveTableColumn = (index: number, direction: -1 | 1) => {
    setTableColumns((current) => {
      const destination = index + direction;
      if (destination < 0 || destination >= current.length) return current;
      const reordered = [...current];
      [reordered[index], reordered[destination]] = [reordered[destination], reordered[index]];
      return reordered;
    });
  };

  const removeTableColumn = (index: number) => {
    setTableColumns((current) => current.filter((_, itemIndex) => itemIndex !== index));
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

  const startColumnEdit = (column: ColumnInfo) => {
    setError(null);
    setEditingColumn(column.Name);
    setColumnEdit({
      Name: column.Name,
      Type: column.Type || 'BLOB',
      NotNull: !column.Nullable,
      PrimaryKey: column.PrimaryKey,
      DefaultValue: editableDefault(column.DefaultValue),
    });
  };

  const handleEditColumn = () => {
    if (!selected || !editingColumn) return;
    const name = columnEdit.Name.trim();
    if (!name) {
      setError('Enter a field name.');
      return;
    }
    void save(
      () => api.editColumn(dbName, selected.Name, {
        OriginalName: editingColumn,
        Name: name,
        Type: columnEdit.Type,
        Nullable: !columnEdit.NotNull,
        DefaultValue: columnEdit.DefaultValue ?? null,
      }),
      () => setEditingColumn(null),
    );
  };

  return (
    <div className="schema-viewer">
      <div className="schema-tables">
        <h3>Tables</h3>
        <div className="schema-form create-table-form">
          <div className="schema-form-heading">
            <div>
              <h4>Create table</h4>
              <p>Define the fields before creating it.</p>
            </div>
          </div>
          <input
            value={newTableName}
            onChange={(event) => setNewTableName(event.target.value)}
            placeholder="Table name"
            aria-label="Table name"
            disabled={saving}
          />
          <div className="create-table-columns">
            {tableColumns.map((column, index) => (
              <div className="create-table-column" key={column.id}>
                <div className="create-table-column-order">
                  <button
                    type="button"
                    className="btn-icon"
                    onClick={() => moveTableColumn(index, -1)}
                    disabled={saving || index === 0}
                    aria-label={`Move field ${index + 1} up`}
                    title="Move field up"
                  >
                    ↑
                  </button>
                  <button
                    type="button"
                    className="btn-icon"
                    onClick={() => moveTableColumn(index, 1)}
                    disabled={saving || index === tableColumns.length - 1}
                    aria-label={`Move field ${index + 1} down`}
                    title="Move field down"
                  >
                    ↓
                  </button>
                </div>
                <span className="create-table-column-number" aria-hidden="true">{index + 1}</span>
                <ColumnForm
                  value={column.definition}
                  onChange={(value) => updateTableColumn(index, value)}
                  allowPrimaryKey
                  disabled={saving}
                />
                <button
                  type="button"
                  className="btn-danger-sm create-table-column-remove"
                  onClick={() => removeTableColumn(index)}
                  disabled={saving || tableColumns.length === 1}
                  aria-label={`Remove field ${index + 1}`}
                  title="Remove field"
                >
                  ✕
                </button>
              </div>
            ))}
          </div>
          <div className="create-table-actions">
            <button
              type="button"
              className="btn-sm"
              onClick={() => setTableColumns((current) => [...current, createDraftColumn()])}
              disabled={saving}
            >
              + Add field
            </button>
            <button
              className="btn-primary"
              onClick={handleCreateTable}
              disabled={
                saving
                || !newTableName.trim()
                || tableColumns.some(({ definition }) => !definition.Name.trim())
              }
            >
              {saving ? 'Creating…' : 'Create table'}
            </button>
          </div>
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
                  <th aria-label="Actions" />
                </tr>
              </thead>
              <tbody>
                {selected.Columns?.map((col) => editingColumn === col.Name ? (
                  <tr key={col.Name} className="schema-column-edit-row">
                    <td colSpan={6}>
                      <div className="schema-column-editor">
                        <ColumnForm
                          value={columnEdit}
                          onChange={setColumnEdit}
                          disabled={saving}
                          lockType={col.PrimaryKey}
                        />
                        <div className="schema-column-editor-actions">
                          <button type="button" className="btn-primary" onClick={handleEditColumn} disabled={saving}>
                            {saving ? 'Saving…' : 'Save changes'}
                          </button>
                          <button
                            type="button"
                            className="btn-sm"
                            onClick={() => setEditingColumn(null)}
                            disabled={saving}
                          >
                            Cancel
                          </button>
                        </div>
                      </div>
                    </td>
                  </tr>
                ) : (
                  <tr key={col.Name}>
                    <td className="col-name">{col.Name}</td>
                    <td className="col-type-badge">{col.Type}</td>
                    <td>{col.Nullable ? '✓' : '✕'}</td>
                    <td className="col-default">{col.DefaultValue || '—'}</td>
                    <td>{col.PrimaryKey ? '🔑' : ''}</td>
                    <td>
                      <button
                        type="button"
                        className="btn-sm"
                        onClick={() => startColumnEdit(col)}
                        disabled={saving}
                        aria-label={`Edit field ${col.Name}`}
                      >
                        Edit
                      </button>
                    </td>
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
  lockType?: boolean;
  disabled: boolean;
}

function ColumnForm({
  value,
  onChange,
  allowPrimaryKey = false,
  lockType = false,
  disabled,
}: ColumnFormProps) {
  const set = (changes: Partial<ColumnDefinition>) => onChange({ ...value, ...changes });

  return (
    <div className="column-form">
      <input
        value={value.Name}
        onChange={(event) => set({ Name: event.target.value })}
        placeholder="Field name"
        disabled={disabled}
      />
      <select
        value={value.Type}
        onChange={(event) => set({ Type: event.target.value })}
        disabled={disabled || lockType}
      >
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
