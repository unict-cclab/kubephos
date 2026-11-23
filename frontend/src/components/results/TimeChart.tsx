import {createContext, useContext, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type PointerEvent} from 'react'
import {ResponsiveTable} from '../ResponsiveTable'
import {displayValue, formatClock} from './display'

// On-screen time-series chart (not a publication figure): stacked panels that share the
// x axis and one hover crosshair. No frame, hairline baseline and grid, lines labelled
// directly at their right end (a legend only when the plot is too narrow), annotations
// written on the plot, CI band as a faint wash, keyboard stepping and a table view as the
// accessible twin.

export interface ChartPoint {t: number; v: number}

// Series label → note shown as the legend tooltip (e.g. why a user colour was adjusted).
export const SeriesNotesContext = createContext<Record<string, string>>({})
export interface ChartSeries {
  id: string
  label: string
  // Any CSS colour, typically a token: var(--chart-1).
  color: string
  points: ChartPoint[]
  // Uncertainty band [low, high] per point (same index as points).
  band?: Array<[number, number]>
  dashed?: boolean
  // Direct label written at the line's end (defaults to label).
  short?: string
  // Expected spacing (ms): lines break across gaps larger than 1.5 steps.
  step?: number
}
export interface ChartPanel {
  id: string
  title: string
  unit: string
  series: ChartSeries[]
  reference?: {value: number; label: string}
  // Shade where the `lower` series falls below the `upper` one, with a note on the plot.
  shortfall?: {upper: string; lower: string; label: string}
}

const baseMargin = {left: 48, right: 12, top: 12, bottom: 8}
const axisBand = 26

export function niceTicks(maximum: number, count = 4): number[] {
  if (!Number.isFinite(maximum) || maximum <= 0) return [0, 1]
  const raw = maximum / count
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  const step = ([1, 2, 2.5, 5, 10].find(candidate => candidate * magnitude >= raw) ?? 10) * magnitude
  const ticks: number[] = []
  for (let value = 0; value < maximum + step * .999; value += step) ticks.push(Number(value.toPrecision(12)))
  return ticks
}

export function useWidth(fallback = 720) {
  const reference = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(fallback)
  useEffect(() => {
    const element = reference.current
    if (!element) return
    const measure = () => {const next = Math.round(element.getBoundingClientRect().width); if (next > 0) setWidth(next)}
    measure()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [])
  return [reference, width] as const
}

function linePath(points: ChartPoint[], x: (t: number) => number, y: (v: number) => number, step?: number): string {
  let path = ''
  points.forEach((point, index) => {
    const gap = index > 0 && step !== undefined && point.t - points[index - 1].t > step * 1.5
    path += `${!index || gap ? 'M' : 'L'}${x(point.t).toFixed(1)},${y(point.v).toFixed(1)}`
  })
  return path
}

function bandPath(series: ChartSeries, x: (t: number) => number, y: (v: number) => number): string {
  if (!series.band || series.band.length !== series.points.length || !series.points.length) return ''
  const upper = series.points.map((point, index) => `${x(point.t).toFixed(1)},${y(series.band![index][1]).toFixed(1)}`)
  const lower = series.points.map((point, index) => `${x(point.t).toFixed(1)},${y(Math.max(0, series.band![index][0])).toFixed(1)}`).reverse()
  return `M${upper.join('L')}L${lower.join('L')}Z`
}

function shortfallShape(upper: ChartSeries, lower: ChartSeries, x: (t: number) => number, y: (v: number) => number, tolerance: number): {path: string; at?: {t: number; v: number}} {
  const pairs = lower.points.map(point => ({t: point.t, low: point.v, high: valueAt(upper, point.t, tolerance)?.v})).filter((pair): pair is {t: number; low: number; high: number} => pair.high !== undefined)
  if (pairs.length < 2) return {path: ''}
  const top = pairs.map(pair => `${x(pair.t).toFixed(1)},${y(Math.max(pair.high, pair.low)).toFixed(1)}`)
  const bottom = pairs.map(pair => `${x(pair.t).toFixed(1)},${y(pair.low).toFixed(1)}`).reverse()
  const widest = pairs.reduce((best, pair) => pair.high - pair.low > best.high - best.low ? pair : best, pairs[0])
  // Anchor the note around 60 % of the way, where the gap is usually open and clear of the end labels.
  const middle = pairs[Math.floor(pairs.length * .6)]
  const at = middle.high - middle.low > (widest.high - widest.low) * .5 ? middle : widest
  return {path: `M${top.join('L')}L${bottom.join('L')}Z`, at: {t: at.t, v: (at.high + at.low) / 2}}
}

// End-of-line labels pushed apart so they never overlap (at least `gap` px between baselines).
function placeLabels(items: Array<{id: string; y: number}>, top: number, bottom: number, gap = 13): Map<string, number> {
  const sorted = [...items].sort((a, b) => a.y - b.y)
  const placed = sorted.map(item => ({...item}))
  for (let index = 1; index < placed.length; index++) placed[index].y = Math.max(placed[index].y, placed[index - 1].y + gap)
  const overflow = placed.length ? placed[placed.length - 1].y - bottom : 0
  if (overflow > 0) for (const item of placed) item.y -= overflow
  for (let index = placed.length - 2; index >= 0; index--) placed[index].y = Math.min(placed[index].y, placed[index + 1].y - gap)
  return new Map(placed.map(item => [item.id, Math.max(top, item.y)]))
}

function nearestIndex(times: number[], t: number): number {
  let low = 0
  let high = times.length - 1
  while (high - low > 1) {
    const middle = (low + high) >> 1
    if (times[middle] < t) low = middle
    else high = middle
  }
  return Math.abs(times[low] - t) <= Math.abs(times[high] - t) ? low : high
}

function valueAt(series: ChartSeries, t: number, tolerance: number): {v: number; band?: [number, number]} | undefined {
  if (!series.points.length) return undefined
  const index = nearestIndex(series.points.map(point => point.t), t)
  const point = series.points[index]
  return Math.abs(point.t - t) <= tolerance ? {v: point.v, band: series.band?.[index]} : undefined
}

export function LegendKey({color, dashed}: {color: string; dashed?: boolean}) {
  return <svg className="legend-key" width="16" height="8" aria-hidden="true"><line x1="1" x2="15" y1="4" y2="4" style={{stroke: color}} strokeWidth="2" strokeLinecap="round" strokeDasharray={dashed ? '3 3' : undefined} /></svg>
}

export function TimeChart({panels, ariaLabel, panelHeight = 168, compact = false, durationLabel, panelTitles = true}: {panels: ChartPanel[]; ariaLabel: string; panelHeight?: number; compact?: boolean; durationLabel?: string; panelTitles?: boolean}) {
  const [container, width] = useWidth()
  const [hover, setHover] = useState<number | undefined>()
  const [table, setTable] = useState(false)
  const notes = useContext(SeriesNotesContext)
  const id = useId()
  const visible = panels.filter(panel => panel.series.some(series => series.points.length))
  const times = useMemo(() => [...new Set(visible.flatMap(panel => panel.series.flatMap(series => series.points.map(point => point.t))))].sort((a, b) => a - b), [visible])
  const step = useMemo(() => {
    let minimum = Infinity
    for (let index = 1; index < times.length; index++) minimum = Math.min(minimum, times[index] - times[index - 1])
    return Number.isFinite(minimum) ? minimum : 15000
  }, [times])
  if (!visible.length || !times.length) return <p className="empty-inline">No samples to plot.</p>
  const tMin = Math.min(0, times[0])
  const tMax = Math.max(times[times.length - 1], tMin + 1)
  const labelText = (series: ChartSeries) => series.short ?? series.label
  const longest = Math.max(0, ...visible.flatMap(panel => panel.series.filter(series => series.points.length).map(series => labelText(series).length)))
  // Direct labels need room on the right; on narrow plots (phones) a compact legend replaces them.
  const direct = !compact && width >= 520 && longest <= 22
  const margin = {...baseMargin, right: direct ? Math.round(Math.min(150, 14 + longest * 6.6)) : baseMargin.right}
  const plotWidth = Math.max(80, width - margin.left - margin.right)
  const x = (t: number) => margin.left + (t - tMin) / (tMax - tMin) * plotWidth
  const minutes = (tMax - tMin) / 60000
  const xTicks = niceTicks(minutes, Math.max(2, Math.min(8, Math.floor(plotWidth / 90)))).filter(tick => tick <= minutes + 1e-9)
  const height = panelHeight
  const hoverIndex = hover === undefined ? undefined : nearestIndex(times, hover)
  const hoverTime = hoverIndex === undefined ? undefined : times[hoverIndex]
  const onPointer = (event: PointerEvent<SVGSVGElement>) => {
    const box = event.currentTarget.getBoundingClientRect()
    const scale = box.width ? width / box.width : 1
    const px = (event.clientX - box.left) * scale
    if (px < margin.left - 8 || px > width - margin.right + 8) return setHover(undefined)
    setHover(tMin + (px - margin.left) / plotWidth * (tMax - tMin))
  }
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'Escape'].includes(event.key)) return
    event.preventDefault()
    if (event.key === 'Escape') return setHover(undefined)
    const current = hoverIndex ?? (event.key === 'ArrowLeft' ? times.length : -1)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? times.length - 1 : Math.max(0, Math.min(times.length - 1, current + (event.key === 'ArrowLeft' ? -1 : 1)))
    setHover(times[next])
  }
  const readout = hoverTime === undefined ? [] : visible.map(panel => ({panel, rows: panel.series.map(series => ({series, value: valueAt(series, hoverTime, step * .75)})).filter(row => row.value)}))
  const tooltipLeft = hoverTime === undefined ? 0 : x(hoverTime)
  const flip = tooltipLeft > width * .6
  return <div className={`time-chart ${compact ? 'compact' : ''}`} ref={container}>
    <div className="time-chart-plot" tabIndex={0} role="img" aria-label={ariaLabel} aria-describedby={`${id}-readout`} onKeyDown={onKey} onBlur={() => setHover(undefined)}>
      {visible.map((panel, panelIndex) => {
        const last = panelIndex === visible.length - 1
        const values = panel.series.flatMap(series => [...series.points.map(point => point.v), ...(series.band ?? []).map(band => band[1])]).filter(Number.isFinite)
        const yTicks = niceTicks(Math.max(...values, panel.reference?.value ?? 0, 0) * 1.05 || 1, compact ? 2 : 3)
        const yMax = yTicks[yTicks.length - 1]
        const bottom = height - margin.bottom
        const y = (v: number) => bottom - Math.min(1.02, Math.max(0, v / yMax)) * (bottom - margin.top)
        const totalHeight = height + (last ? axisBand : 0)
        const upper = panel.shortfall && panel.series.find(series => series.id === panel.shortfall!.upper)
        const lower = panel.shortfall && panel.series.find(series => series.id === panel.shortfall!.lower)
        const gap = upper && lower ? shortfallShape(upper, lower, x, y, step * .75) : undefined
        const ends = direct ? panel.series.filter(series => series.points.length).map(series => {const end = series.points[series.points.length - 1]; return {id: series.id, y: y(end.v) + 4, series}}) : []
        const labelY = placeLabels(ends, margin.top + 4, bottom + 4)
        return <div className="time-chart-panel" key={panel.id}>
          {!compact && (panelTitles || (panel.series.length > 1 && !direct)) && <div className="time-chart-panel-head" aria-hidden="true">
            {panelTitles ? <span className="time-chart-panel-title">{panel.title}</span> : <span />}
            {panel.series.length > 1 && !direct && <span className="time-chart-legend">{panel.series.map(series => <span key={series.id} title={notes[series.label]} className={notes[series.label] ? 'legend-adjusted' : undefined}><LegendKey color={series.color} dashed={series.dashed} />{series.label}{notes[series.label] && <sup aria-hidden="true">*</sup>}</span>)}</span>}
          </div>}
          <svg width="100%" height={totalHeight} viewBox={`0 0 ${width} ${totalHeight}`} preserveAspectRatio="none" onPointerMove={onPointer} onPointerLeave={() => setHover(undefined)} aria-hidden="true" focusable="false">
            {yTicks.map(tick => <g key={tick}>
              <line className="chart-grid-line" x1={margin.left} x2={width - margin.right} y1={y(tick)} y2={y(tick)} />
              <text className="chart-tick" x={margin.left - 8} y={y(tick) + 4} textAnchor="end">{displayValue(tick, panel.unit, true).text}</text>
            </g>)}
            <line className="chart-baseline" x1={margin.left} x2={width - margin.right} y1={bottom} y2={bottom} />
            {gap?.path && <path className="chart-shortfall" d={gap.path} />}
            {panel.reference && <g className="chart-reference">
              <line x1={margin.left} x2={width - margin.right} y1={y(panel.reference.value)} y2={y(panel.reference.value)} />
              <text x={margin.left + 6} y={y(panel.reference.value) - 5}>{panel.reference.label}</text>
            </g>}
            {panel.series.map(series => series.band && <path key={`${series.id}-band`} className="chart-band" d={bandPath(series, x, y)} style={{fill: series.color}} />)}
            {panel.series.map(series => <path key={series.id} className={`chart-line ${series.dashed ? 'dashed' : ''}`} d={linePath(series.points, x, y, series.step ?? step)} style={{stroke: series.color}} strokeDasharray={series.dashed ? '5 4' : undefined} />)}
            {gap?.at && panel.shortfall && <text className="chart-annotation" x={x(gap.at.t)} y={y(gap.at.v) + 4} textAnchor="middle">{panel.shortfall.label}</text>}
            {ends.map(({id, series}) => <text key={`${id}-label`} className="chart-end-label" x={width - margin.right + 8} y={labelY.get(id)} style={{fill: series.color}}>{labelText(series)}{notes[series.label] ? '*' : ''}</text>)}
            {hoverTime !== undefined && <g className="chart-crosshair">
              <line x1={x(hoverTime)} x2={x(hoverTime)} y1={margin.top - 6} y2={bottom} />
              {panel.series.map(series => {const value = valueAt(series, hoverTime, step * .75); return value && <circle key={series.id} cx={x(hoverTime)} cy={y(value.v)} r="4" style={{fill: series.color}} />})}
            </g>}
            {last && xTicks.map(tick => <text key={tick} className="chart-tick" x={x(tMin + tick * 60000)} y={height + 18} textAnchor={tick === 0 ? 'start' : 'middle'}>{formatClock(tick * 60)}</text>)}
          </svg>
        </div>
      })}
      {direct && visible.some(panel => panel.series.some(series => notes[series.label])) && <p className="time-chart-note">{[...new Set(visible.flatMap(panel => panel.series.filter(series => notes[series.label]).map(series => series.label)))].map(label => <span key={label}>*{label}: {notes[label]}. </span>)}</p>}
      {hoverTime !== undefined && readout.some(item => item.rows.length) && <div className={`chart-tooltip ${flip ? 'flip' : ''}`} style={{left: `${tooltipLeft / width * 100}%`}}>
        <div className="chart-tooltip-time">{formatClock((hoverTime - tMin) / 1000)}{durationLabel ? ` ${durationLabel}` : ''}</div>
        {readout.filter(item => item.rows.length).map(({panel, rows}) => <div key={panel.id} className="chart-tooltip-group">
          {visible.length > 1 && <div className="chart-tooltip-panel">{panel.title}</div>}
          {rows.map(({series, value}) => <div key={series.id} className="chart-tooltip-row"><LegendKey color={series.color} dashed={series.dashed} /><strong>{displayValue(value!.v, panel.unit).text}</strong><span>{series.label}</span></div>)}
        </div>)}
      </div>}
    </div>
    <p id={`${id}-readout`} className="visually-hidden" aria-live="polite">{hoverTime === undefined ? 'Use the left and right arrow keys to read values.' : `${formatClock((hoverTime - tMin) / 1000)}: ${readout.flatMap(({panel, rows}) => rows.map(({series, value}) => `${panel.title} ${series.label} ${displayValue(value!.v, panel.unit).text}`)).join('; ')}`}</p>
    {!compact && <div className="time-chart-foot">
      <span className="time-chart-axis-label">Minutes since the measured window began</span>
      <button type="button" className="link-button" aria-expanded={table} onClick={() => setTable(value => !value)}>{table ? 'Hide data table' : 'View as table'}</button>
    </div>}
    {table && <ChartTable panels={visible} times={times} step={step} label={ariaLabel} />}
  </div>
}

function ChartTable({panels, times, step, label}: {panels: ChartPanel[]; times: number[]; step: number; label: string}) {
  const columns = panels.flatMap(panel => panel.series.map(series => ({panel, series})))
  return <div className="chart-table-wrap"><ResponsiveTable aria-label={`${label}: data table`}>
    <thead><tr><th>Time</th>{columns.map(({panel, series}) => <th key={`${panel.id}-${series.id}`}>{panel.title} · {series.label}</th>)}</tr></thead>
    <tbody>{times.map(t => <tr key={t}><td>{formatClock((t - Math.min(0, times[0])) / 1000)}</td>{columns.map(({panel, series}) => {const value = valueAt(series, t, step * .75); return <td key={`${panel.id}-${series.id}`}>{value ? displayValue(value.v, panel.unit).text : '—'}</td>})}</tr>)}</tbody>
  </ResponsiveTable></div>
}

// Word-sized trend line: no axes, end dot. Decorative (the value sits next to it).
export function Sparkline({points, color = 'var(--chart-1)', width = 96, height = 28, label}: {points: ChartPoint[]; color?: string; width?: number; height?: number; label?: string}) {
  const finite = points.filter(point => Number.isFinite(point.v))
  if (finite.length < 2) return null
  const tMin = finite[0].t
  const tMax = finite[finite.length - 1].t || tMin + 1
  const values = finite.map(point => point.v)
  const lo = Math.min(...values)
  const hi = Math.max(...values)
  const span = hi - lo || Math.abs(hi) || 1
  const x = (t: number) => 2 + (t - tMin) / (tMax - tMin || 1) * (width - 6)
  const y = (v: number) => height - 3 - (v - (hi - lo ? lo : lo - span / 2)) / span * (height - 6)
  const end = finite[finite.length - 1]
  return <svg className="sparkline" width={width} height={height} viewBox={`0 0 ${width} ${height}`} role={label ? 'img' : undefined} aria-label={label} aria-hidden={label ? undefined : true}>
    <path d={linePath(finite, x, y)} style={{stroke: color}} fill="none" strokeWidth="1.25" strokeLinejoin="round" strokeLinecap="round" />
    <circle cx={x(end.t)} cy={y(end.v)} r="2" style={{fill: color}} />
  </svg>
}
