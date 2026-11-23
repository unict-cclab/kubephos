import {createContext, useContext, useLayoutEffect, useMemo, useRef, useState, type ReactNode} from 'react'

// Shared pieces of the report tables (Endpoints, Services, Environment): a sortable column
// header with aria-sort and a horizontally scrollable wrapper that keyboard users can scroll.

export type SortDirection = 'ascending' | 'descending'
export interface SortState {key: string; direction: SortDirection}
type Accessor<T> = (row: T) => number | string | undefined

// Rows sorted by the chosen column; missing values always last. No sort = given order.
export function useSort<T>(rows: T[], accessors: Record<string, Accessor<T>>, initial?: SortState) {
  const [sort, setSort] = useState<SortState | undefined>(initial)
  const sorted = useMemo(() => {
    const accessor = sort && accessors[sort.key]
    if (!sort || !accessor) return rows
    const sign = sort.direction === 'ascending' ? 1 : -1
    return rows.map((row, index) => ({row, index, value: accessor(row)})).sort((left, right) => {
      if (left.value === undefined || right.value === undefined) return left.value === right.value ? left.index - right.index : left.value === undefined ? 1 : -1
      const order = typeof left.value === 'number' && typeof right.value === 'number' ? left.value - right.value : String(left.value).localeCompare(String(right.value), undefined, {numeric: true})
      return order ? order * sign : left.index - right.index
    }).map(item => item.row)
  }, [rows, sort, accessors])
  // Numbers start descending (worst first), text ascending; a third press restores the default order.
  const toggle = (key: string, numeric: boolean) => setSort(current => {
    const first: SortDirection = numeric ? 'descending' : 'ascending'
    if (current?.key !== key) return {key, direction: first}
    if (current.direction === first) return {key, direction: first === 'ascending' ? 'descending' : 'ascending'}
    return initial
  })
  return {sorted, sort, toggle}
}

export function SortHeader({id, label, sort, onSort, numeric = false, title, className = ''}: {id: string; label: ReactNode; sort?: SortState; onSort: (key: string, numeric: boolean) => void; numeric?: boolean; title?: string; className?: string}) {
  const active = sort?.key === id
  return <th scope="col" className={`${numeric ? 'numeric' : ''} ${className}`.trim() || undefined} aria-sort={active ? sort.direction : undefined} title={title}>
    <button type="button" className="sort-button" onClick={() => onSort(id, numeric)}>
      <span>{label}</span>
      <svg className={`sort-icon ${active ? sort.direction : ''}`} width="8" height="12" viewBox="0 0 8 12" aria-hidden="true"><path className="up" d="M1 4.5 4 1.5l3 3" /><path className="down" d="m1 7.5 3 3 3-3" /></svg>
    </button>
  </th>
}

// Scroll container for a wide table. Only when the table actually overflows does it become a
// labelled, focusable region (so the keyboard can scroll it) — otherwise it adds no tab stop.
export function TableScroll({label, children, className = ''}: {label: string; children: ReactNode; className?: string}) {
  const ref = useRef<HTMLDivElement>(null)
  const [scrollable, setScrollable] = useState(false)
  useLayoutEffect(() => {
    const element = ref.current
    if (!element) return
    const measure = () => setScrollable(element.scrollWidth > element.clientWidth + 1 || element.scrollHeight > element.clientHeight + 1)
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    if (element.firstElementChild) observer.observe(element.firstElementChild)
    return () => observer.disconnect()
  }, [])
  return <div ref={ref} className={`report-table-wrap ${className}`.trim()} {...scrollable ? {role: 'region', 'aria-label': label, tabIndex: 0} : {}}>{children}</div>
}

// Indexes of the rows holding the highest value of a column, when it singles out at most two rows.
export function worstRows(values: Array<number | undefined>): Set<number> {
  const finite = values.filter((value): value is number => value !== undefined && Number.isFinite(value))
  if (finite.length < 3) return new Set()
  // Compared at the displayed precision (three significant digits): 60,001 and 60,002 tie.
  const shown = (value: number) => Number(value.toPrecision(3))
  const max = Math.max(...finite.map(shown))
  if (max <= 0) return new Set()
  const indexes = values.flatMap((value, index) => value !== undefined && Number.isFinite(value) && shown(value) === max ? [index] : [])
  return indexes.length <= 2 ? new Set(indexes) : new Set()
}

// Printing (or any context that must show everything): no row limits, every tab panel.
export const ShowEverything = createContext(false)

// Long tables show their first `limit` rows; a link reveals the rest.
export function useRowLimit<T>(rows: T[], limit = 10, noun = 'rows') {
  const everything = useContext(ShowEverything)
  const [all, setAll] = useState(false)
  const limited = rows.length > limit && !everything
  const shown = limited && !all ? rows.slice(0, limit) : rows
  const toggle = limited ? <button type="button" className="link-button show-all-rows" aria-expanded={all} onClick={() => setAll(value => !value)}>{all ? `Show first ${limit} only` : `Show all ${rows.length} ${noun}`}</button> : null
  return {shown, toggle}
}
