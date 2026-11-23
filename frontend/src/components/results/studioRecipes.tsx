import {useEffect, useId, useRef, useState, type ReactNode} from 'react'
import {cumulativeCounterMetrics, isControlMetric, type AggregateSeries} from '../../resultsData'
import {defaultSpec, type FigureSpec} from './figureSpec'
import {metricTitle} from './format'
import type {OverviewGroup} from './overview'
import {alignByTimestamp, defaultXYOptions, defaultXYSelection, type XYState} from './xy'
import type {FigureKind, StudioData} from './FigureStudio'

// KubeStudio recipes: ready-made figures drawn from THIS experiment's data (one click gives a
// finished figure with sensible defaults), offered by the top-bar figure switcher.

export const kindOrder: FigureKind[] = ['timeseries', 'xy', 'runlevel', 'ladder', 'services', 'fidelity', 'placement']
export const kindLabels: Record<FigureKind, string> = {timeseries: 'Time series', xy: 'X–Y relationship', runlevel: 'Run-level dots', ladder: 'Percentile ladder', services: 'Services', fidelity: 'Load fidelity', placement: 'Placement'}
export const kindHints: Record<FigureKind, string> = {timeseries: 'Metric over the measured window', xy: 'One metric against another', runlevel: 'Every repetition, mean and CI', ladder: 'P50–P99, client vs server', services: 'One value per service', fidelity: 'Offered vs achieved load', placement: 'Replicas per zone over time'}

export const timeSeriesExcluded = (metric: string) => isControlMetric(metric)
export const runLevelExcluded = (metric: string) => isControlMetric(metric) || ['load.configured_rps', 'load.offered_rps', 'load.users'].includes(metric) || cumulativeCounterMetrics.includes(metric)

// Small schematic of each figure type (currentColor strokes, 48×32).
export function KindIcon({kind}: {kind: FigureKind}) {
  const shapes: Record<FigureKind, ReactNode> = {
    timeseries: <><path d="M5 22c6-2 8-9 14-8s8 6 14 3 6-8 10-9" strokeWidth="1.6" /><path d="M5 26c6-1 9-5 14-5s9 3 14 2 6-3 10-4" strokeWidth="1.6" opacity=".55" /></>,
    xy: <><circle cx="10" cy="24" r="1.6" /><circle cx="15" cy="21" r="1.6" /><circle cx="19" cy="22" r="1.6" /><circle cx="24" cy="16" r="1.6" /><circle cx="29" cy="15" r="1.6" /><circle cx="34" cy="10" r="1.6" /><circle cx="39" cy="9" r="1.6" /><path d="M7 26 42 7" strokeWidth="1.2" opacity=".6" /></>,
    runlevel: <><circle cx="14" cy="12" r="1.7" /><circle cx="12" cy="16" r="1.7" /><circle cx="16" cy="18" r="1.7" /><path d="M9 15h10" strokeWidth="2" /><circle cx="34" cy="20" r="1.7" /><circle cx="32" cy="24" r="1.7" /><circle cx="36" cy="22" r="1.7" /><path d="M29 22h10" strokeWidth="2" /></>,
    ladder: <><path d="M8 25 18 20 28 14 40 6" strokeWidth="1.6" /><path d="M8 27 18 25 28 22 40 17" strokeWidth="1.6" strokeDasharray="3 2" opacity=".6" /><circle cx="8" cy="25" r="1.7" /><circle cx="18" cy="20" r="1.7" /><circle cx="28" cy="14" r="1.7" /><circle cx="40" cy="6" r="1.7" /></>,
    services: <><path d="M10 10v10M8 10h4M8 20h4M22 18v8M20 18h4M20 26h4M34 6v10M32 6h4M32 16h4" strokeWidth="1.2" /><circle cx="10" cy="15" r="1.9" /><circle cx="22" cy="22" r="1.9" /><circle cx="34" cy="11" r="1.9" /></>,
    fidelity: <><path d="M5 24 18 12h24" strokeWidth="1.6" strokeDasharray="3 2" opacity=".6" /><path d="M5 25 18 16c5-2 10 0 14-1s7-1 10-1" strokeWidth="1.6" /></>,
    placement: <>{[0, 1, 2].flatMap(row => [0, 1, 2, 3, 4].map(column => <rect key={`${row}-${column}`} x={6 + column * 7.4} y={6 + row * 7} width="6.4" height="6" rx="1" opacity={[.25, .5, .85, .4, .65][(row * 2 + column) % 5]} stroke="none" />))}</>
  }
  return <svg className="studio-kind-icon" width="48" height="32" viewBox="0 0 48 32" fill="currentColor" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    <path d="M3 3v26h42" fill="none" strokeWidth="1" opacity=".35" />
    <g className={kind === 'placement' ? '' : 'studio-kind-marks'}>{shapes[kind]}</g>
  </svg>
}

// ---------------------------------------------------------------------------------------
// Recipes.

export interface Recipe {
  id: string
  title: string
  description: string
  kind: FigureKind
  metric?: string
  xy?: XYState
  // Fresh FigureSpec for spec-driven kinds (smart defaults from the data).
  spec?: FigureSpec
  thumb: ReactNode
}

const W = 160
const H = 84
const PAD = 8

const finiteValues = (series: AggregateSeries[]) => series.flatMap(item => item.points.map(point => point.value)).filter(Number.isFinite)

function frame(children: ReactNode) {
  return <svg className="studio-thumb" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" aria-hidden="true" focusable="false">
    <path d={`M${PAD} ${PAD - 2}V${H - PAD}H${W - PAD + 2}`} fill="none" stroke="currentColor" strokeWidth="1" opacity=".3" vectorEffect="non-scaling-stroke" />
    {children}
  </svg>
}

const scaler = (values: number[], from: number, to: number, zero = false) => {
  const finite = values.filter(Number.isFinite)
  let low = Math.min(...finite, ...(zero ? [0] : []))
  let high = Math.max(...finite)
  if (!finite.length) {low = 0; high = 1}
  if (high - low < 1e-9) {high = low + 1}
  return (value: number) => from + (value - low) / (high - low) * (to - from)
}

const colorOf = (groups: OverviewGroup[], label: string, index: number, fallback?: string) => groups.find(group => group.label === label || label.startsWith(`${group.label} · `))?.color ?? fallback ?? `var(--chart-${index % 8 + 1})`

function lineThumb(series: Array<{label: string; color?: string; points: Array<{x: number; y: number}>; dashed?: boolean}>, groups: OverviewGroup[]) {
  const xs = series.flatMap(item => item.points.map(point => point.x))
  const ys = series.flatMap(item => item.points.map(point => point.y))
  const px = scaler(xs, PAD + 2, W - PAD)
  const py = scaler(ys, H - PAD - 2, PAD + 2, true)
  return frame(series.slice(0, 6).map((item, index) => {
    const step = Math.max(1, Math.ceil(item.points.length / 60))
    const points = item.points.filter((_, position) => position % step === 0).filter(point => Number.isFinite(point.x) && Number.isFinite(point.y))
    return <polyline key={`${item.label}-${index}`} points={points.map(point => `${px(point.x).toFixed(1)},${py(point.y).toFixed(1)}`).join(' ')} fill="none" stroke={colorOf(groups, item.label, index, item.color)} strokeWidth="1.6" strokeDasharray={item.dashed ? '4 3' : undefined} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
  }))
}

const curvePoints = (series: AggregateSeries) => {
  const start = Date.parse(series.points[0]?.timestamp ?? '')
  return series.points.map(point => ({x: Date.parse(point.timestamp) - start, y: point.value}))
}

function dotsThumb(columns: Array<{label: string; color?: string; values: number[]}>, groups: OverviewGroup[]) {
  const all = columns.flatMap(item => item.values)
  const py = scaler(all, H - PAD - 4, PAD + 4)
  const width = (W - PAD * 2) / Math.max(1, columns.length)
  return frame(columns.slice(0, 8).map((column, index) => {
    const cx = PAD + width * (index + .5)
    const mean = column.values.reduce((sum, value) => sum + value, 0) / Math.max(1, column.values.length)
    const color = colorOf(groups, column.label, index, column.color)
    return <g key={`${column.label}-${index}`}>
      {column.values.slice(0, 12).map((value, position) => <circle key={position} cx={cx + (position % 3 - 1) * 4} cy={py(value)} r="2.4" fill={color} fillOpacity=".75" />)}
      <path d={`M${cx - 9} ${py(mean)}h18`} stroke="currentColor" strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
    </g>
  }))
}

function scatterThumb(points: Array<{x: number; y: number; color: string}>) {
  const px = scaler(points.map(point => point.x), PAD + 3, W - PAD - 2)
  const py = scaler(points.map(point => point.y), H - PAD - 3, PAD + 3)
  return frame(points.slice(0, 200).map((point, index) => <circle key={index} cx={px(point.x)} cy={py(point.y)} r="2" fill={point.color} fillOpacity=".7" />))
}

function placeholderThumb(kind: FigureKind) {
  return <span className="studio-thumb studio-thumb-icon"><KindIcon kind={kind} /></span>
}

const latencyCandidates = ['load.p95_response_time', 'application.p95_response_time', 'load.p99_response_time', 'load.p50_response_time']

// Smart axis defaults for a time-series metric: milliseconds shown in seconds once the
// values pass 1000 ms (ticks then read 1.2 instead of 1,200).
export function smartTimeSeriesSpec(series: AggregateSeries[], base: FigureSpec = defaultSpec('timeseries')): FigureSpec {
  const values = finiteValues(series)
  const unit = series[0]?.unit
  const max = values.length ? Math.max(...values) : 0
  const panel = base.panels[0]
  const left = unit === 'milliseconds' && max >= 1000 ? {...panel.left, unit: 's'} : panel.left
  return {...base, panels: [{...panel, left}, ...base.panels.slice(1)]}
}

export function buildRecipes(data: StudioData, groups: OverviewGroup[]): Recipe[] {
  const recipes: Recipe[] = []
  const timeMetrics = data.metrics.filter(metric => !timeSeriesExcluded(metric) && data.plot(metric).length)
  const many = groups.length > 1
  const latency = latencyCandidates.find(metric => timeMetrics.includes(metric)) ?? timeMetrics.find(metric => metric.endsWith('response_time'))
  if (latency) {
    const series = data.plot(latency)
    recipes.push({id: 'latency-time', kind: 'timeseries', metric: latency, title: many ? 'Latency over time by variant' : 'Latency over time', description: `${metricTitle(latency, data.provenance(latency))}, mean and 95 % CI per ${many ? 'variant' : 'series'}`, spec: smartTimeSeriesSpec(series), thumb: lineThumb(series.map(item => ({label: item.label, color: item.color, points: curvePoints(item)})), groups)})
  }
  if (data.xy && data.xy.metrics.length > 1) {
    const selection = defaultXYSelection(data.xy.metrics)
    const xRuns = data.xy.runs(selection.x)
    const yRuns = data.xy.runs(selection.y)
    const points = xRuns.flatMap((run, index) => {
      const partner = yRuns.find(item => item.runID === run.runID && item.seriesID === run.seriesID)
      return partner ? alignByTimestamp(run.series.points, partner.series.points).map(point => ({x: point.x, y: point.y, color: colorOf(groups, run.seriesLabel, index, run.color)})) : []
    })
    const throughput = /rps$/.test(selection.x.metric)
    recipes.push({id: 'xy', kind: 'xy', xy: {...selection, options: {...defaultXYOptions, fit: true}}, title: throughput ? 'Throughput vs latency' : 'Metric vs metric', description: `${metricTitle(selection.y.metric, data.xy.provenance(selection.y.metric))} against ${metricTitle(selection.x.metric, data.xy.provenance(selection.x.metric)).toLowerCase()}, with a fitted trend per series`, thumb: points.length ? scatterThumb(points) : placeholderThumb('xy')})
  }
  if (data.serviceMetrics.length) {
    const withRows = data.serviceMetrics.filter(item => data.services(item)?.rows.length)
    const metric = withRows.includes('cpu.cores') ? 'cpu.cores' : withRows[0] ?? data.serviceMetrics[0]
    const rows = data.services(metric)?.rows ?? []
    const byService = [...new Set(rows.map(row => row.service))].map(service => ({label: service, values: rows.filter(row => row.service === service).map(row => row.summary.mean).filter(Number.isFinite)}))
    recipes.push({id: 'services', kind: 'services', metric, title: metric === 'cpu.cores' ? 'Per-service CPU' : `Per-service ${metricTitle(metric).toLowerCase()}`, description: `${byService.length} services, one value per ${many ? 'variant' : 'run'} with 95 % CI`, thumb: rows.length ? dotsThumb(byService, []) : placeholderThumb('services')})
  }
  if (data.fidelity.length) {
    recipes.push({id: 'fidelity', kind: 'fidelity', title: 'Load fidelity', description: 'Offered (dashed) vs achieved request rate: did the generator keep up?', thumb: lineThumb(data.fidelity.map(item => ({label: item.label, color: item.color, dashed: item.role === 'offered', points: curvePoints(item)})), groups)})
  }
  if (data.ladder.length) {
    const levels = ['p50', 'p90', 'p95', 'p99']
    recipes.push({id: 'ladder', kind: 'ladder', title: 'Percentile ladder', description: 'P50 → P99, client and server side per configuration', thumb: lineThumb(data.ladder.map(item => ({label: item.group, color: item.color, dashed: item.vantage === 'server', points: item.points.map(point => ({x: levels.indexOf(point.level), y: point.mean}))})), groups)})
  }
  const runMetric = data.runLevel.find(entry => entry.metric === latency && entry.groups.some(group => group.values.length)) ?? data.runLevel.find(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length))
  if (runMetric) {
    recipes.push({id: 'runlevel', kind: 'runlevel', metric: runMetric.metric, title: 'Run-level comparison', description: `${metricTitle(runMetric.metric, data.provenance(runMetric.metric))}: every repetition, mean and 95 % CI`, thumb: dotsThumb(runMetric.groups.map(group => ({label: group.label, values: group.values})), groups)})
  }
  return recipes
}

// ---------------------------------------------------------------------------------------
// Recent figures (per experiment).

export interface RecentFigure {kind: FigureKind; metric?: string; xy?: XYState; name?: string; at: number}

export function sameRecent(left: RecentFigure, right: RecentFigure) {
  return left.kind === right.kind && (left.metric ?? '') === (right.metric ?? '') && JSON.stringify(left.xy ? [left.xy.x, left.xy.y] : null) === JSON.stringify(right.xy ? [right.xy.x, right.xy.y] : null)
}


export interface SwitcherItem {kind: FigureKind; title: string; description: string; thumb: ReactNode; recipe?: Recipe}

// One entry per available figure type: the recipe built from the data when there is one.
export function switcherItems(available: FigureKind[], recipes: Recipe[]): SwitcherItem[] {
  return available.map(kind => {
    const recipe = recipes.find(item => item.kind === kind)
    return recipe ? {kind, title: recipe.title, description: recipe.description, thumb: recipe.thumb, recipe} : {kind, title: kindLabels[kind], description: kindHints[kind], thumb: placeholderThumb(kind)}
  })
}

// Chart forms of a custom chart: X and Y stay free to choose.
export type CustomForm = 'timeseries' | 'xy' | 'services' | 'runlevel'
export const customForms: Array<{kind: CustomForm; label: string; hint: string}> = [
  {kind: 'timeseries', label: 'Line over time', hint: 'Any metric against time'},
  {kind: 'xy', label: 'Scatter X–Y', hint: 'Any metric against any other'},
  {kind: 'services', label: 'Per service', hint: 'One value per service'},
  {kind: 'runlevel', label: 'Per variant', hint: 'Every repetition, mean and CI'}
]

// Top-bar chart switcher: the current chart's name; opens a popover with a custom chart (pick
// the form, then X and Y) and the starting points built from this experiment's data.
export function FigureSwitcher({items, active, prefix = '', label, onPick, forms, onCustom}: {items: SwitcherItem[]; active?: FigureKind; prefix?: string; label?: string; onPick: (item: SwitcherItem) => void; forms: CustomForm[]; onCustom: (form: CustomForm) => void}) {
  const id = useId()
  const [open, setOpen] = useState(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const found = items.find(item => item.kind === active)
  const current = found && label ? {...found, title: label} : found
  useEffect(() => {
    if (!open) return
    panel.current?.querySelector<HTMLElement>('button')?.focus()
    const outside = (event: PointerEvent) => {if (!panel.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) setOpen(false)}
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  const close = () => {setOpen(false); trigger.current?.focus()}
  return <div className="ks-switcher">
    <button ref={trigger} type="button" className="ks-switcher-button" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? `${id}-panel` : undefined} aria-label={`Figure: ${prefix}${current?.title ?? 'choose'}`} title="Change this chart" onClick={() => setOpen(!open)} disabled={!items.length}>
      {current && <KindIcon kind={current.kind} />}<span>{prefix}{current?.title ?? 'Choose a chart'}</span>
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="m3 4.5 3 3 3-3" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
    </button>
    {open && <div ref={panel} id={`${id}-panel`} className="ks-popover ks-switcher-panel" role="dialog" aria-label="Choose a figure" onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); close()}}}>
      {forms.length > 0 && <section className="ks-custom" aria-labelledby={`${id}-custom`}>
        <p className="ks-popover-title" id={`${id}-custom`}>Custom chart <span>— pick the form, then choose X and Y freely</span></p>
        <div className="ks-custom-forms">
          {customForms.filter(form => forms.includes(form.kind)).map(form => <button key={form.kind} type="button" className="ks-custom-form" data-form={form.kind} onClick={() => {setOpen(false); onCustom(form.kind)}}>
            <KindIcon kind={form.kind} /><span><strong>{form.label}</strong><small>{form.hint}</small></span>
          </button>)}
        </div>
      </section>}
      <p className="ks-popover-title">Starting points from this experiment <span>— fully editable</span></p>
      <ul className="ks-figure-list">
        {items.map(item => <li key={item.kind}>
          <button type="button" className="ks-figure-item" data-kind={item.kind} aria-current={(item.kind === active && !label) || undefined} onClick={() => {setOpen(false); onPick(item)}}>
            {item.thumb}
            <span><strong>{item.title}</strong><small>{item.description}</small></span>
          </button>
        </li>)}
      </ul>
    </div>}
  </div>
}
