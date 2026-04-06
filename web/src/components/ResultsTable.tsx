import { useState } from 'react';
import type { QueryResult } from '../api/client';

interface Props {
  result: QueryResult;
}

export function ResultsTable({ result }: Props) {
  const [sortCol, setSortCol] = useState<number | null>(null);
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>('asc');

  const handleSort = (colIdx: number) => {
    if (sortCol === colIdx) {
      setSortDir(sortDir === 'asc' ? 'desc' : 'asc');
    } else {
      setSortCol(colIdx);
      setSortDir('asc');
    }
  };

  const sortedRows = [...(result.Rows || [])].sort((a, b) => {
    if (sortCol === null) return 0;
    const valA = a[sortCol] || '';
    const valB = b[sortCol] || '';
    const numA = Number(valA);
    const numB = Number(valB);
    if (!isNaN(numA) && !isNaN(numB)) {
      return sortDir === 'asc' ? numA - numB : numB - numA;
    }
    return sortDir === 'asc' ? valA.localeCompare(valB) : valB.localeCompare(valA);
  });

  const exportCSV = () => {
    const headers = result.Columns?.map((c) => c.Name).join(',') || '';
    const rows = (result.Rows || []).map((r) =>
      r.map((v) => `"${v.replace(/"/g, '""')}"`).join(',')
    );
    const csv = [headers, ...rows].join('\n');
    const blob = new Blob([csv], { type: 'text/csv' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'query-result.csv';
    a.click();
    URL.revokeObjectURL(url);
  };

  const exportJSON = () => {
    const cols = result.Columns?.map((c) => c.Name) || [];
    const data = (result.Rows || []).map((row) => {
      const obj: Record<string, string> = {};
      row.forEach((val, i) => { obj[cols[i]] = val; });
      return obj;
    });
    const blob = new Blob([JSON.stringify(data, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'query-result.json';
    a.click();
    URL.revokeObjectURL(url);
  };

  return (
    <div className="results-table">
      <div className="results-header">
        <span className="row-count">{result.Rows?.length || 0} row(s)</span>
        <div className="export-btns">
          <button onClick={exportCSV} className="btn-sm">CSV</button>
          <button onClick={exportJSON} className="btn-sm">JSON</button>
        </div>
      </div>
      <div className="table-scroll">
        <table>
          <thead>
            <tr>
              {result.Columns?.map((col, i) => (
                <th key={col.Name} onClick={() => handleSort(i)} className="sortable">
                  {col.Name}
                  {sortCol === i && (sortDir === 'asc' ? ' ↑' : ' ↓')}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sortedRows.map((row, i) => (
              <tr key={i}>
                {row.map((val, j) => (
                  <td key={j} className={val === 'NULL' ? 'null-val' : ''}>
                    {val}
                  </td>
                ))}
              </tr>
            ))}
            {sortedRows.length === 0 && (
              <tr>
                <td colSpan={result.Columns?.length || 1} className="empty-row">
                  No results
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}
