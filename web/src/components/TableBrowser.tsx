import { useState, useEffect } from 'react';
import { api, type QueryResult } from '../api/client';

interface Props {
  dbName: string;
  tableName: string;
}

export function TableBrowser({ dbName, tableName }: Props) {
  const [data, setData] = useState<QueryResult | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [offset, setOffset] = useState(0);
  const limit = 50;

  useEffect(() => {
    setOffset(0);
  }, [dbName, tableName]);

  useEffect(() => {
    const load = async () => {
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
    };
    load();
  }, [dbName, tableName, offset]);

  if (loading) return <div className="loading">Loading table data...</div>;
  if (error) return <div className="error-msg">{error}</div>;
  if (!data) return null;

  return (
    <div className="table-browser">
      <div className="table-header">
        <h3>{tableName}</h3>
        <div className="pagination">
          <button
            disabled={offset === 0}
            onClick={() => setOffset(Math.max(0, offset - limit))}
            className="btn-sm"
          >
            ← Prev
          </button>
          <span className="page-info">
            Rows {offset + 1}–{offset + (data.Rows?.length || 0)}
          </span>
          <button
            disabled={!data.Rows || data.Rows.length < limit}
            onClick={() => setOffset(offset + limit)}
            className="btn-sm"
          >
            Next →
          </button>
        </div>
      </div>

      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              {data.Columns?.map((col) => (
                <th key={col.Name}>
                  {col.Name}
                  <span className="col-type">{col.Type}</span>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {data.Rows?.map((row, i) => (
              <tr key={i}>
                {row.map((val, j) => (
                  <td key={j} className={val === 'NULL' ? 'null-val' : ''}>
                    {val}
                  </td>
                ))}
              </tr>
            ))}
            {(!data.Rows || data.Rows.length === 0) && (
              <tr>
                <td colSpan={data.Columns?.length || 1} className="empty-row">
                  No rows
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
