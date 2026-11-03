import {ResponsiveTable} from './ResponsiveTable'

export type ReadoutTableCell = string | {type: 'color'; value: string; label: string}
export type ReadoutTableData = {headers: string[]; rows: ReadoutTableCell[][]}

export function ReadoutTable({data, label}: {data: ReadoutTableData; label: string}) {
  const fieldValue = data.headers.length === 2 && data.headers[0] === 'Field' && data.headers[1] === 'Value'
  return <div className="readout-table-wrap"><ResponsiveTable className={`readout-table${fieldValue ? ' readout-field-value' : ''}`} aria-label={label}><thead><tr>{data.headers.map((header, index) => <th scope="col" key={index}>{header}</th>)}</tr></thead><tbody>{data.rows.map((row, index) => <tr key={index}>{row.map((cell, column) => <td key={column}>{typeof cell === 'string' ? cell : cell?.type === 'color' && /^#[0-9a-f]{6}$/i.test(cell.value) ? <span className="configuration-color-swatch" role="img" aria-label={cell.label} style={{backgroundColor: cell.value}} /> : '—'}</td>)}</tr>)}</tbody></ResponsiveTable></div>
}
