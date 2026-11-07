import { useCallback, useEffect, useState } from 'react';
import { api, type QueryResult, type TableInfo } from '../api/client';
import { ResultsTable } from './ResultsTable';
import { Icon } from './Icon';

interface Props {
  dbName: string;
  tableName: string;
  tableInfo?: TableInfo;
  onDataChange: () => Promise<void>;
}

type CellMode = 'default' | 'value' | 'null';

interface CellDraft {
  mode: CellMode;
  value: string;
}

function canOmitColumn(column: TableInfo['Columns'][number]): boolean {
  const isGeneratedIntegerKey = column.PrimaryKey && column.Type.trim().toUpperCase() === 'INTEGER';
  return isGeneratedIntegerKey || column.Nullable || column.DefaultValue !== '';
}

function createRowDraft(tableInfo?: TableInfo): Record<string, CellDraft> {
  return Object.fromEntries((tableInfo?.Columns ?? []).map((column) => [
    column.Name,
    {
      mode: canOmitColumn(column) ? 'default' : 'value',
      value: '',
    },
  ]));
}

export function TableBrowser({ dbName, tableName, tableInfo, onDataChange }: Props) {
  const [data, setData] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const [offset, setOffset] = useState(0);
  const [showRowComposer, setShowRowComposer] = useState(false);
  const [savingRow, setSavingRow] = useState(false);
  const [rowDraft, setRowDraft] = useState<Record<string, CellDraft>>(() => createRowDraft(tableInfo));
  const limit = 50;

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const result = await api.getTableData(dbName, tableName, limit, offset);
      setData(result);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load table data');
    } finally {
      setLoading(false);
    }
  }, [dbName, tableName, offset]);

  useEffect(() => {
    setOffset(0);
    setShowRowComposer(false);
    setSuccess(null);
  }, [dbName, tableName]);

  useEffect(() => {
    setRowDraft(createRowDraft(tableInfo));
  }, [tableInfo]);

  useEffect(() => {
    void load();
  }, [load]);

  const updateCell = (columnName: string, changes: Partial<CellDraft>) => {
    setRowDraft((current) => ({
      ...current,
      [columnName]: { ...current[columnName], ...changes },
    }));
  };

  const handleInsertRow = async () => {
    if (!tableInfo) return;

    const columns: string[] = [];
    const values: Array<string | null> = [];
    for (const column of tableInfo.Columns) {
      const cell = rowDraft[column.Name];
      if (!cell || cell.mode === 'default') continue;
      columns.push(column.Name);
      values.push(cell.mode === 'null' ? null : cell.value);
    }

    setSavingRow(true);
    setError(null);
    setSuccess(null);
    try {
      const result = await api.insertRow(dbName, tableName, columns, values);
      await Promise.all([load(), onDataChange()]);
      setRowDraft(createRowDraft(tableInfo));
      setShowRowComposer(false);
      setSuccess(result.LastInsertID > 0 ? `Row added with ID ${result.LastInsertID}.` : 'Row added.');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add row');
    } finally {
      setSavingRow(false);
    }
  };

  if (loading && !data) return <div className="loading" role="status">Loading table data...</div>;
  if (!data) return error ? <div className="error-msg" role="alert">{error}<button className="btn-sm" onClick={load}>Retry</button></div> : null;

  return (
    <div className="table-browser">
      <div className="table-header">
        <div><h3><Icon name="table" />{tableName}</h3><p>{tableInfo?.Columns.length ?? data.Columns.length} fields / {tableInfo?.RowCount.toLocaleString() ?? data.Rows.length} rows</p></div>
        <div className="table-header-actions">
          <button
            type="button"
            className="btn-primary add-row-button"
            onClick={() => {
              setShowRowComposer((visible) => !visible);
              setError(null);
              setSuccess(null);
            }}
            disabled={!tableInfo || savingRow}
            aria-label="Add row"
            aria-expanded={showRowComposer}
            title="Add row"
          >
            <Icon name="plus" />
            <span>Add row</span>
          </button>
          <div className="pagination">
            <button
              disabled={offset === 0}
              onClick={() => setOffset(Math.max(0, offset - limit))}
              className="btn-sm"
            >
              <Icon name="chevron-left" /> Prev
            </button>
            <span className="page-info">
              Rows {data.Rows?.length ? offset + 1 : 0}-{offset + (data.Rows?.length || 0)}
            </span>
            <button
              disabled={!data.Rows || data.Rows.length < limit}
              onClick={() => setOffset(offset + limit)}
              className="btn-sm"
            >
              Next <Icon name="chevron-right" />
            </button>
          </div>
        </div>
      </div>

      {showRowComposer && tableInfo && (
        <div className="row-composer">
          <div className="row-composer-heading">
            <div>
              <h4>Add row</h4>
              <p>Choose a value, NULL, or let SQLite use the field default.</p>
            </div>
          </div>
          <div className="row-composer-fields">
            {tableInfo.Columns.map((column) => {
              const cell = rowDraft[column.Name] ?? { mode: 'default', value: '' };
              const canUseDefault = canOmitColumn(column);
              return (
                <label className="row-composer-field" key={column.Name}>
                  <span className="row-composer-field-name">
                    {column.Name}
                    <small>{column.Type}</small>
                  </span>
                  <select
                    value={cell.mode}
                    onChange={(event) => updateCell(column.Name, { mode: event.target.value as CellMode })}
                    disabled={savingRow}
                    aria-label={`${column.Name} value mode`}
                  >
                    {canUseDefault && <option value="default">Default</option>}
                    <option value="value">Value</option>
                    {column.Nullable && <option value="null">NULL</option>}
                  </select>
                  {cell.mode === 'value' && (
                    <input
                      value={cell.value}
                      onChange={(event) => updateCell(column.Name, { value: event.target.value })}
                      disabled={savingRow}
                      aria-label={`${column.Name} value`}
                      placeholder="Enter value"
                    />
                  )}
                </label>
              );
            })}
          </div>
          <div className="row-composer-actions">
            <button
              type="button"
              className="btn-sm"
              onClick={() => {
                setShowRowComposer(false);
                setRowDraft(createRowDraft(tableInfo));
                setError(null);
              }}
              disabled={savingRow}
            >
              Cancel
            </button>
            <button type="button" className="btn-primary" onClick={handleInsertRow} disabled={savingRow}>
              {savingRow ? 'Adding…' : 'Add row'}
            </button>
          </div>
        </div>
      )}

      {error && <div className="error-msg" role="alert">{error}</div>}
      {success && <div className="scan-result" role="status"><Icon name="check" />{success}</div>}
      <ResultsTable result={data} rowOffset={offset} />
    </div>
  );
}
