import { useState } from 'react';
import type { QueryResult } from '../api/client';
import { Icon } from './Icon';

interface Props {
  result: QueryResult;
  rowOffset?: number;
}

function download(name: string, content: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const link = document.createElement('a');
  try {
    link.href = url;
    link.download = name;
    document.body.append(link);
    link.click();
  } finally {
    link.remove();
    URL.revokeObjectURL(url);
  }
}

export function ResultsTable({ result, rowOffset = 0 }: Props) {
  const [sort, setSort] = useState<{ name: string; direction: 'asc' | 'desc' } | null>(null);
  const sortColumn = sort ? result.Columns.findIndex((column) => column.Name === sort.name) : -1;
  const sortedRows = [...(result.Rows || [])].sort((a, b) => {
    if (!sort || sortColumn < 0) return 0;
    const valueA = a[sortColumn] || '';
    const valueB = b[sortColumn] || '';
    const numericA = Number(valueA);
    const numericB = Number(valueB);
    const comparison = !isNaN(numericA) && !isNaN(numericB) ? numericA - numericB : valueA.localeCompare(valueB);
    return sort.direction === 'asc' ? comparison : -comparison;
  });
  const exportCSV = () => {
    const quote = (value: string) => `"${value.replaceAll('"', '""')}"`;
    const headers = result.Columns.map((column) => quote(column.Name)).join(',');
    const rows = (result.Rows || []).map((row) => row.map(quote).join(','));
    download('query-result.csv', [headers, ...rows].join('\n'), 'text/csv');
  };
  const exportJSON = () => {
    const data = (result.Rows || []).map((row) => Object.fromEntries(result.Columns.map((column, index) => [column.Name, row[index]])));
    download('query-result.json', JSON.stringify(data, null, 2), 'application/json');
  };

  return (
    <div className="results-table">
      <div className="results-header"><span className="result-title"><Icon name="table" /> Results <span className="row-count">{result.Rows?.length || 0} row{result.Rows?.length === 1 ? '' : 's'}</span></span><div className="export-btns"><button onClick={exportCSV} className="btn-sm" aria-label="Export CSV"><Icon name="download" /> CSV</button><button onClick={exportJSON} className="btn-sm" aria-label="Export JSON"><Icon name="download" /> JSON</button></div></div>
      <div className="table-scroll" tabIndex={0} aria-label="Scrollable query results">
        <table>
          <caption className="sr-only">Database query results</caption>
          <thead><tr><th className="row-number" scope="col">#</th>{result.Columns?.map((column) => (
            <th key={column.Name} scope="col" aria-sort={sort?.name === column.Name ? sort.direction === 'asc' ? 'ascending' : 'descending' : 'none'}>
              <button className="sort-button" onClick={() => setSort({ name: column.Name, direction: sort?.name === column.Name && sort.direction === 'asc' ? 'desc' : 'asc' })} aria-label={`Sort by ${column.Name}`}>{column.Name}{sort?.name === column.Name && <Icon name={sort.direction === 'asc' ? 'arrow-up' : 'arrow-down'} />}</button>
              <span className="col-type">{column.Type || 'ANY'}</span>
            </th>
          ))}</tr></thead>
          <tbody>
            {sortedRows.map((row, index) => <tr key={index}><td className="row-number">{rowOffset + index + 1}</td>{row.map((value, column) => <td key={column} className={value === 'NULL' ? 'null-val' : ''} title={value}>{value}</td>)}</tr>)}
            {sortedRows.length === 0 && <tr><td colSpan={(result.Columns?.length || 0) + 1} className="empty-row">No rows returned.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
