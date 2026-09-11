import {humanize} from '../lib'

interface Props {
  value: unknown
}

export function JsonPreview({value}: Props) {
  return <div className="json-preview">
    <StructuredValue value={value} depth={0} />
    <details className="json-source"><summary>Raw JSON</summary><pre>{JSON.stringify(value, null, 2)}</pre></details>
  </div>
}

function StructuredValue({value, depth}: {value: unknown; depth: number}) {
  if (value === null || value === undefined || typeof value !== 'object') return <span className="json-value">{displayValue(value)}</span>
  if (depth >= 6) return <span className="json-value">{JSON.stringify(value)}</span>
  if (Array.isArray(value)) return <ArrayValue value={value} depth={depth} />
  const entries = Object.entries(value as Record<string, unknown>)
  const simple = entries.filter(([, current]) => scalar(current))
  const nested = entries.filter(([, current]) => !scalar(current))
  return <div className="json-object">
    {!!simple.length && <dl>{simple.map(([name, current]) => <div key={name}><dt>{humanize(name)}</dt><dd>{displayValue(current)}</dd></div>)}</dl>}
    {nested.map(([name, current]) => <section key={name}><h4>{humanize(name)}</h4><StructuredValue value={current} depth={depth + 1} /></section>)}
  </div>
}

function ArrayValue({value, depth}: {value: unknown[]; depth: number}) {
  if (!value.length) return <p className="json-empty">No items</p>
  if (tabular(value)) {
    const rows = value.slice(0, 200) as Array<Record<string, unknown>>
    const columns = Array.from(new Set(rows.flatMap(row => Object.keys(row)))).slice(0, 12)
    return <div className="json-table-wrap"><table className="json-table"><thead><tr>{columns.map(column => <th key={column}>{humanize(column)}</th>)}</tr></thead><tbody>{rows.map((row, index) => <tr key={index}>{columns.map(column => <td key={column}>{displayValue(row[column])}</td>)}</tr>)}</tbody></table>{value.length > rows.length && <p className="json-empty">Showing {rows.length} of {value.length} items</p>}</div>
  }
  return <div className="json-list">{value.slice(0, 200).map((current, index) => <div key={index}><StructuredValue value={current} depth={depth + 1} /></div>)}</div>
}

function tabular(value: unknown[]): boolean {
  return value.every(current => current !== null && typeof current === 'object' && !Array.isArray(current) && Object.values(current as Record<string, unknown>).every(scalar))
}

function scalar(value: unknown): boolean {
  return value === null || value === undefined || typeof value !== 'object'
}

function displayValue(value: unknown): string {
  if (value === null) return 'null'
  if (value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? 'Yes' : 'No'
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}
