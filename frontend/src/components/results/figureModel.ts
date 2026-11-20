import {niceAxis} from '../../charts'
import type {AggregateSeries} from '../../resultsData'
import {quantileSorted, summarize, tCritical} from '../../statistics'
import type {FigureLabelOverrides} from './chartKit'
import {exportColor} from './chartColor'
import {compactLabel, formatMetric, metricAxisLabel, metricTitle, provenanceSentence, toCSV} from './format'
import {displayUnit, formatTick, formatTime, gridShape, relabelUnit, resolveStyle, seriesKey, timeAxisLabel, type Annotation, type AxisSide, type AxisSpec, type FigureSpec, type ResolvedStyle, type RunMode, type TimeFormat} from './figureSpec'
import type {ResolvedProvenance} from './runs'
import {extent, linearScale, type Scale} from './scales'
import {buildXY, fitSummary, fitXY, type XYOptions, type XYRunInput, type XYSource} from './xy'

// Resolves a FigureSpec against the experiment data into a renderer-independent model:
// series in display units with their effective styles, axes with domains and ticks, and
// annotations. The SVG figure, the CSV files and the matplotlib/pgfplots exports all draw
// from this one model, so they cannot disagree.

export interface LoadProfile {warmupSeconds: number; steps: Array<{label: string; durationSeconds: number}>}

export interface ModelData {
  plot: (metric: string) => AggregateSeries[]
  // Individual repetitions of a metric (absent on saved comparisons: only means are kept).
  runs?: (metric: string) => XYRunInput[] | undefined
  provenance: (metric: string) => ResolvedProvenance
  slo?: number
  profile?: LoadProfile
  xy?: {source: XYSource; x: {metric: string; service?: string}; y: {metric: string; service?: string}; options: XYOptions}
}

export interface ModelSeries {
  key: string
  // Default (automatic) label; `label` is the effective one after overrides.
  defaultLabel: string
  label: string
  metric: string
  group: string
  axis: AxisSide
  style: ResolvedStyle
  kind: 'line' | 'scatter'
  // x in display units of the X axis; NaN separates segments (missing windows, runs).
  x: number[]
  y: number[]
  low?: number[]
  high?: number[]
  // Thin lines of individual repetitions (spaghetti / runs modes), on the same x.
  runs?: Array<{label: string; y: number[]}>
  showMean: boolean
  n: number
  fit?: {x: number[]; y: number[]; summary: string}
}

export interface ModelAxis {
  side: AxisSide | 'x'
  label: string
  // Label before the studio's x/y label overrides (offered for editing).
  defaultLabel: string
  spec: AxisSpec
  scale: Scale
  minor: number[]
  format: (value: number) => string
  // Only series on this axis has this colour (dual axes): tick labels and title wear it.
  color?: string
  unit: string
}

export interface ModelAnnotation {kind: 'vline' | 'hline' | 'xrange' | 'text'; axis: AxisSide; x?: number; x2?: number; y?: number; label?: string; source: 'phase' | 'warmup' | 'slo' | 'user'; color: string}

export interface ModelPanel {index: number; letter?: string; x: ModelAxis; left?: ModelAxis; right?: ModelAxis; series: ModelSeries[]; annotations: ModelAnnotation[]; title: string; metrics: string[]}

export interface FigureModel {
  type: FigureSpec['type']
  rows: number
  columns: number
  panels: ModelPanel[]
  // Legend entries in order, grouped by axis when both are used (header opens a group).
  legend: Array<{axis: AxisSide; series: ModelSeries; header?: string}>
  dual: boolean
  runMode: RunMode
  // Modes needing individual repetitions fall back to the mean when they are not available.
  runModeFallback: boolean
  title: string
  caption: string
  methods: string
  csv: () => string
  empty?: string
}

const letters = 'abcd'
export const uncertaintyName: Record<RunMode, string> = {ci: 'mean with 95 % CI band', sd: 'mean ± 1 SD band', iqr: 'median with interquartile band', spaghetti: 'every repetition (thin) and their mean (bold)', runs: 'individual repetitions', mean: 'mean only'}

// ---------------------------------------------------------------------------------------
// Scales from axis specs.

const candidates = [1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
function niceStep(range: number, count = 5): number {
  const raw = Math.abs(range) / Math.max(1, count) || 1
  const magnitude = 10 ** Math.floor(Math.log10(raw))
  return (candidates.find(candidate => candidate >= raw / magnitude * .98) ?? 10) * magnitude
}
const clean = (value: number) => Number(value.toPrecision(12))

function stepTicks(lower: number, upper: number, step: number): number[] {
  if (!(step > 0) || !Number.isFinite(lower) || !Number.isFinite(upper)) return []
  const ticks: number[] = []
  for (let tick = Math.ceil(lower / step - 1e-9) * step; tick <= upper + step * 1e-9 && ticks.length < 60; tick += step) ticks.push(clean(tick))
  return ticks
}

// timeSteps: preferred tick steps (seconds) for an mm:ss axis.
export function axisScale(values: number[], axis: AxisSpec, {integer = false, timeSteps}: {integer?: boolean; timeSteps?: number[]} = {}): Scale & {minor: number[]} {
  const range = extent(values)
  const custom = axis.range === 'custom'
  if (axis.log) {
    const lower = custom && axis.min !== undefined && axis.min > 0 ? axis.min : range.count ? 10 ** Math.floor(Math.log10(range.minPositive === Infinity ? 1 : range.minPositive)) : 1
    let upper = custom && axis.max !== undefined && axis.max > lower ? axis.max : range.count && range.max > 0 ? 10 ** Math.ceil(Math.log10(range.max)) : lower * 10
    if (upper <= lower) upper = lower * 10
    const low = Math.floor(Math.log10(lower))
    const high = Math.ceil(Math.log10(upper))
    const decades = high - low
    const ticks: number[] = []
    const minor: number[] = []
    for (let exponent = low; exponent <= high; exponent++) for (let factor = 1; factor <= 9; factor++) {
      const value = clean(factor * 10 ** exponent)
      if (value < lower * (1 - 1e-9) || value > upper * (1 + 1e-9)) continue
      if (factor === 1 || decades <= 3 && (factor === 2 || factor === 5)) ticks.push(value)
      else minor.push(value)
    }
    const span = Math.log10(upper) - Math.log10(lower)
    return {lower, upper, ticks, minor, log: true, position: value => value > 0 && Number.isFinite(value) ? (Math.log10(value) - Math.log10(lower)) / span : undefined}
  }
  const minimum = range.count ? range.min : 0
  const maximum = range.count ? range.max : 1
  const base = linearScale(minimum, maximum, {zero: axis.range === 'zero', integer})
  let lower = base.lower
  let upper = base.upper
  if (timeSteps && !custom) {
    const step = timeSteps.find(item => maximum / item <= 6) ?? timeSteps.at(-1)!
    lower = Math.min(0, Math.floor(minimum / step) * step)
    upper = Math.max(step, Math.ceil(maximum / step) * step)
  }
  if (custom) {
    lower = axis.min ?? lower
    upper = axis.max ?? upper
    if (upper <= lower) upper = lower + (Math.abs(lower) || 1)
  }
  let ticks = base.ticks.filter(tick => tick >= lower - 1e-9 && tick <= upper + 1e-9)
  let step = ticks.length > 1 ? ticks[1] - ticks[0] : niceStep(upper - lower)
  if (axis.ticks === 'step' && axis.tickValue && (upper - lower) / axis.tickValue <= 60) step = axis.tickValue
  else if (axis.ticks === 'count' && axis.tickValue && axis.tickValue >= 2) step = (upper - lower) / (Math.round(axis.tickValue) - 1)
  else if (timeSteps) step = timeSteps.find(item => (upper - lower) / item <= 6) ?? timeSteps.at(-1)!
  else if (custom) step = niceStep(upper - lower)
  if (integer && axis.ticks !== 'count') step = Math.max(1, Math.round(step))
  if (axis.ticks !== 'auto' || custom || timeSteps) ticks = axis.ticks === 'count' ? Array.from({length: Math.round(axis.tickValue ?? 2)}, (_, index) => clean(lower + index * step)) : stepTicks(lower, upper, step)
  const minorStep = step / 2
  const minor = stepTicks(lower, upper, minorStep).filter(value => !ticks.some(tick => Math.abs(tick - value) < minorStep * 1e-6))
  return {lower, upper, ticks, minor, log: false, position: value => Number.isFinite(value) ? (value - lower) / (upper - lower) : undefined}
}

// ---------------------------------------------------------------------------------------
// Repetition statistics on the windows every repetition sampled (never interpolated).

export interface RunStatistics {t: number[]; mean: number[]; sd: number[]; ci: number[]; median: number[]; q1: number[]; q3: number[]; runs: number[][]; step: number; n: number}

export function runStatistics(runs: AggregateSeries[]): RunStatistics | undefined {
  if (runs.length < 2) return undefined
  let step = Math.max(0, ...runs.map(run => run.stepMilliseconds ?? 0))
  if (!step) {
    step = Infinity
    for (const run of runs) for (let index = 1; index < run.points.length; index++) {const gap = Date.parse(run.points[index].timestamp) - Date.parse(run.points[index - 1].timestamp); if (gap > 0) step = Math.min(step, gap)}
    if (!Number.isFinite(step)) step = 1000
  }
  const windows = runs.map(run => {
    const bins = new Map<number, number[]>()
    for (const point of run.points) if (Number.isFinite(point.value)) {const bin = Math.round(Date.parse(point.timestamp) / step); bins.set(bin, [...(bins.get(bin) ?? []), point.value])}
    return new Map([...bins].map(([bin, values]) => [bin, values.reduce((sum, value) => sum + value, 0) / values.length]))
  })
  const common = [...windows[0].keys()].filter(bin => windows.every(window => window.has(bin))).sort((a, b) => a - b)
  if (!common.length) return undefined
  const result: RunStatistics = {t: [], mean: [], sd: [], ci: [], median: [], q1: [], q3: [], runs: runs.map(() => []), step, n: runs.length}
  const factor = tCritical(runs.length - 1) / Math.sqrt(runs.length)
  for (const bin of common) {
    const values = windows.map(window => window.get(bin)!)
    const summary = summarize(values)
    const sorted = [...values].sort((a, b) => a - b)
    result.t.push(bin * step / 1000)
    result.mean.push(summary.mean)
    result.sd.push(summary.standardDeviation)
    result.ci.push(summary.standardDeviation * factor)
    result.median.push(quantileSorted(sorted, .5))
    result.q1.push(quantileSorted(sorted, .25))
    result.q3.push(quantileSorted(sorted, .75))
    values.forEach((value, index) => result.runs[index].push(value))
  }
  return result
}

// Inserts NaN where consecutive samples are further apart than 1.5 steps.
function withGaps(t: number[], columns: number[][], step: number): {t: number[]; columns: number[][]} {
  const out = {t: [] as number[], columns: columns.map(() => [] as number[])}
  t.forEach((value, index) => {
    if (index && value - t[index - 1] > step * 1.5) {out.t.push(NaN); out.columns.forEach(column => column.push(NaN))}
    out.t.push(value)
    columns.forEach((column, position) => out.columns[position].push(column[index]))
  })
  return out
}

// ---------------------------------------------------------------------------------------

const xValue = (seconds: number, format: TimeFormat) => format === 'min' ? seconds / 60 : seconds

function phaseAnnotations(profile: LoadProfile | undefined, spec: FigureSpec, format: TimeFormat): ModelAnnotation[] {
  if (!profile) return []
  const out: ModelAnnotation[] = []
  if (spec.auto.warmup && profile.warmupSeconds > 0) out.push({kind: 'vline', axis: 'left', x: 0, label: `after ${profile.warmupSeconds} s warm-up`, source: 'warmup', color: '#6b6b6b'})
  if (spec.auto.phases && profile.steps.length) {
    let at = 0
    profile.steps.forEach((step, index) => {
      if (profile.steps.length > 1 || index > 0) out.push({kind: 'vline', axis: 'left', x: xValue(at, format), label: step.label, source: 'phase', color: '#8a8a8a'})
      at += step.durationSeconds
    })
    out.push({kind: 'vline', axis: 'left', x: xValue(at, format), label: 'end of load', source: 'phase', color: '#8a8a8a'})
  }
  return out
}

function userAnnotations(items: Annotation[], format: TimeFormat | undefined): ModelAnnotation[] {
  const x = (value: number) => format ? xValue(value, format) : value
  return items.map((item): ModelAnnotation => {
    if (item.kind === 'hline') return {kind: 'hline', axis: item.axis, y: item.value, label: item.label, source: 'user', color: '#333333'}
    if (item.kind === 'vline') return {kind: 'vline', axis: 'left', x: x(item.at), label: item.label, source: 'user', color: '#333333'}
    if (item.kind === 'xrange') return {kind: 'xrange', axis: 'left', x: x(item.from), x2: x(item.to), label: item.label, source: 'user', color: '#000000'}
    return {kind: 'text', axis: item.axis, x: x(item.at), y: item.value, label: item.text, source: 'user', color: '#111111'}
  })
}

function axisModel(side: AxisSide | 'x', spec: AxisSpec, values: number[], label: string, unit: string, options: {integer?: boolean; timeFormat?: TimeFormat} = {}): ModelAxis {
  const format = options.timeFormat
  const timeSteps = format === 'mmss' ? [5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200] : undefined
  let scale = axisScale(values, spec, {integer: options.integer, timeSteps})
  // Time axes start at 0 and leave a little room after the last window (like the page charts).
  if (format && spec.range !== 'custom' && !spec.log && format !== 'mmss') {
    const max = Math.max(0, extent(values).max)
    const padded = niceAxis(max || 1)
    const step = padded.ticks[1]
    const upper = Math.ceil((max + Math.max(step * .1, max * .025)) / (step / 2)) * (step / 2)
    scale = axisScale(values, {...spec, range: 'custom', min: Math.min(0, extent(values).min || 0), max: upper || 1, ticks: spec.ticks === 'auto' ? 'step' : spec.ticks, tickValue: spec.ticks === 'auto' ? step : spec.tickValue})
  }
  const effective = spec.label?.trim() ? spec.label : label
  return {side, label: effective, defaultLabel: effective, spec, scale, minor: scale.minor, unit, format: format ? (value => formatTime(format === 'min' ? value * 60 : value, format)) : (value => formatTick(value, spec))}
}

function axisTitle(metrics: string[], unit: string, provenance: ModelData['provenance'], unitId: string | undefined): string {
  if (!metrics.length) return ''
  if (metrics.length === 1) return relabelUnit(metricAxisLabel(metrics[0], unit, provenance(metrics[0])), unit, unitId)
  const labels = metrics.map(metric => metricAxisLabel(metric, unit, provenance(metric)))
  const suffix = labels.map(label => label.match(/\(([^()]*)\)\s*$/)?.[1]).filter(Boolean)
  const choice = displayUnit(unit, unitId)
  const shared = choice?.label ?? (new Set(suffix).size === 1 ? suffix[0] : '')
  return `${metrics.map(metric => metricTitle(metric, provenance(metric))).join(', ')}${shared ? ` (${shared})` : ''}`
}

export function buildFigureModel(spec: FigureSpec, data: ModelData, {fontSize, hidden = () => false, labels = {}}: {fontSize: number; hidden?: (group: string) => boolean; labels?: FigureLabelOverrides}): FigureModel {
  const model = spec.type === 'xy' ? xyModel(spec, data, fontSize, hidden, labels) : timeModel(spec, data, fontSize, hidden, labels)
  // The studio's X / Y label fields apply to every panel (per-panel labels live in the axis specs).
  const pick = (value: string | undefined, axis: ModelAxis | undefined) => axis && value?.trim() ? {...axis, label: value} : axis
  model.panels = model.panels.map(panel => ({...panel, x: pick(labels.x, panel.x)!, left: pick(labels.y, panel.left)}))
  return model
}

// slot: position of the series on its axis (metric index × groups + group index), so a
// group keeps one colour and marker in every panel and metrics sharing an axis differ.
interface Draft {key: string; defaultLabel: string; metric: string; group: string; axis: AxisSide; slot: number; pageColor?: string; build: (style: ResolvedStyle) => ModelSeries}

const shortTitle = (metric: string, provenance: ModelData['provenance']) => metricTitle(metric, provenance(metric)).split(' — ')[0]

// Legend label: the group alone when its axis (in its panel) shows one metric and the axis or
// panel already names it; the metric alone with one group; otherwise both.
function seriesLabel({group, metric, groups, metricsOnAxis, named}: {group: string; metric: string; groups: number; metricsOnAxis: number; named: boolean}): string {
  if (groups <= 1) return metric
  if (metricsOnAxis <= 1 && named) return compactLabel(group)
  return `${compactLabel(group)} · ${metric}`
}

function finishStyles(drafts: Draft[][], spec: FigureSpec, fontSize: number, labels: FigureLabelOverrides): ModelSeries[][] {
  // Colours are assigned per axis across the whole figure, so a series keeps its colour
  // in every panel. Order: the user's order first, then the default order.
  const all = drafts.flat()
  const unique = [...new Map(all.map(item => [item.key, item])).values()]
  const dual = unique.some(item => item.axis === 'right') && unique.some(item => item.axis === 'left')
  const styles = new Map<string, ResolvedStyle>()
  for (const side of ['left', 'right'] as const) {
    const onAxis = unique.filter(item => item.axis === side)
    const count = Math.max(0, ...onAxis.map(item => item.slot + 1))
    onAxis.forEach(item => styles.set(item.key, resolveStyle({spec, key: item.key, side, index: item.slot, count, dual, pageColor: item.pageColor, fontSize})))
  }
  const rank = (key: string) => {const index = spec.order.indexOf(key); return index === -1 ? spec.order.length + unique.findIndex(item => item.key === key) : index}
  return drafts.map(panel => panel.map(item => {
    const series = item.build(styles.get(item.key)!)
    return {...series, label: labels.series?.[item.defaultLabel]?.trim() ? labels.series[item.defaultLabel] : item.defaultLabel}
  }).sort((a, b) => rank(a.key) - rank(b.key)))
}

// One entry per distinct label + look (a group drawn alike in several panels is listed once).
function legendOf(panels: ModelSeries[][], headerOf: (side: AxisSide) => string | undefined): FigureModel['legend'] {
  const seen = new Map<string, ModelSeries>()
  const look = (series: ModelSeries) => JSON.stringify([series.axis, series.label, series.style.color, series.style.marker, series.style.hollow, series.style.line])
  for (const panel of panels) for (const series of panel) if (!seen.has(look(series))) seen.set(look(series), series)
  const list = [...seen.values()]
  const dual = list.some(item => item.axis === 'right') && list.some(item => item.axis === 'left')
  const ordered = dual ? [...list.filter(item => item.axis === 'left'), ...list.filter(item => item.axis === 'right')] : list
  return ordered.map((series, index) => {
    const header = dual && (index === 0 || ordered[index - 1].axis !== series.axis) ? headerOf(series.axis) : undefined
    return header ? {axis: series.axis, series, header} : {axis: series.axis, series}
  })
}

function axisColor(series: ModelSeries[], side: AxisSide, dual: boolean): string | undefined {
  if (!dual) return undefined
  const on = series.filter(item => item.axis === side)
  return on.length === 1 ? on[0].style.color : undefined
}

function timeModel(spec: FigureSpec, data: ModelData, fontSize: number, hidden: (group: string) => boolean, labels: FigureLabelOverrides): FigureModel {
  const {rows, columns} = gridShape(spec.layout.grid)
  const panels = spec.panels.slice(0, rows * columns)
  const format = panels[0]?.x.time ?? 'min'
  const metricsUsed = [...new Set(panels.flatMap(panel => panel.metrics.map(item => item.metric)))]
  const groupList = [...new Set(metricsUsed.flatMap(metric => data.plot(metric).filter(series => !hidden(series.label)).map(series => series.label)))]
  const anyDual = panels.some(panel => panel.metrics.some(item => item.axis === 'right')) && panels.some(panel => panel.metrics.some(item => item.axis === 'left'))
  let fallback = false
  const drafts: Draft[][] = panels.map(panel => panel.metrics.flatMap(binding => {
    const curves = data.plot(binding.metric).filter(series => !hidden(series.label))
    const units = curves[0]?.unit ?? ''
    const factor = displayUnit(units, (binding.axis === 'right' ? panel.right : panel.left).unit)?.factor ?? 1
    const runInputs = data.runs?.(binding.metric)
    const sameAxis = [...new Set(panel.metrics.filter(item => item.axis === binding.axis).map(item => item.metric))]
    return curves.map((curve): Draft => {
      const group = curve.label
      const defaultLabel = metricsUsed.length > 1 ? seriesLabel({group, metric: shortTitle(binding.metric, data.provenance), groups: groupList.length, metricsOnAxis: sameAxis.length, named: panels.length > 1 || anyDual}) : compactLabel(group)
      const slot = sameAxis.indexOf(binding.metric) * groupList.length + Math.max(0, groupList.indexOf(group))
      return {key: seriesKey(binding.metric, group), defaultLabel, metric: binding.metric, group, axis: binding.axis, slot, pageColor: curve.color ? exportColor(curve.color) : undefined, build: style => {
        const perRun = runInputs?.filter(item => item.seriesLabel === group).map(item => item.series)
        const stats = spec.runMode !== 'mean' && perRun && perRun.length > 1 ? runStatistics(perRun) : undefined
        const runs = curve.runs ?? 1
        const step = (curve.stepMilliseconds ?? stats?.step ?? 15000) / 1000
        let t: number[], y: number[], low: number[] | undefined, high: number[] | undefined, runLines: number[][] | undefined
        const needsRuns = spec.runMode === 'iqr' || spec.runMode === 'spaghetti' || spec.runMode === 'runs'
        if (stats) {
          t = stats.t
          y = spec.runMode === 'iqr' ? stats.median : stats.mean
          const half = spec.runMode === 'ci' ? stats.ci : spec.runMode === 'sd' ? stats.sd : undefined
          if (half) {low = y.map((value, index) => value - half[index]); high = y.map((value, index) => value + half[index])}
          if (spec.runMode === 'iqr') {low = stats.q1; high = stats.q3}
          if (spec.runMode === 'spaghetti' || spec.runMode === 'runs') runLines = stats.runs
        } else {
          if (needsRuns && runs > 1) fallback = true
          t = curve.points.map(point => Date.parse(point.timestamp) / 1000)
          y = curve.points.map(point => point.value)
          const deviation = curve.deviation?.map(point => point.value)
          if (deviation && runs > 1 && (spec.runMode === 'ci' || spec.runMode === 'sd' || needsRuns)) {
            const mode = spec.runMode === 'sd' ? 'sd' : 'ci'
            const factor = mode === 'ci' ? tCritical(runs - 1) / Math.sqrt(runs) : 1
            low = y.map((value, index) => value - (deviation[index] ?? 0) * factor)
            high = y.map((value, index) => value + (deviation[index] ?? 0) * factor)
          }
        }
        const columns = [y, ...(low ? [low, high!] : []), ...(runLines ?? [])].map(column => column.map(value => value * factor))
        const gapped = withGaps(t, columns, step)
        const [my, ...rest] = gapped.columns
        const bands = low ? rest.splice(0, 2) : undefined
        return {key: seriesKey(binding.metric, group), defaultLabel, label: defaultLabel, metric: binding.metric, group, axis: binding.axis, style, kind: 'line',
          x: gapped.t.map(value => xValue(value, format)), y: my, low: bands?.[0], high: bands?.[1],
          runs: runLines ? rest.map((column, index) => ({label: perRun ? runInputs!.filter(item => item.seriesLabel === group)[index]?.runLabel ?? `run ${index + 1}` : `run ${index + 1}`, y: column})) : undefined,
          showMean: spec.runMode !== 'runs' || !runLines, n: stats?.n ?? runs}
      }}
    })
  }))
  const seriesByPanel = finishStyles(drafts, spec, fontSize, labels)
  const dual = seriesByPanel.flat().some(item => item.axis === 'right') && seriesByPanel.flat().some(item => item.axis === 'left')
  const values = (series: ModelSeries[], side: AxisSide) => series.filter(item => item.axis === side).flatMap(item => [...(item.showMean ? item.y : []), ...(item.low ?? []), ...(item.high ?? []), ...(item.runs?.flatMap(run => run.y) ?? [])])
  const sharedX = spec.layout.sharedX ? seriesByPanel.flat().flatMap(item => item.x) : undefined
  const sharedLeft = spec.layout.sharedY ? seriesByPanel.flatMap(series => values(series, 'left')) : undefined
  const unitOf = (metric: string) => data.plot(metric)[0]?.unit ?? ''
  const modelPanels: ModelPanel[] = panels.map((panel, index) => {
    const series = seriesByPanel[index]
    const metricsOn = (side: AxisSide) => panel.metrics.filter(item => item.axis === side).map(item => item.metric)
    const annotations = [...phaseAnnotations(data.profile, spec, format), ...userAnnotations(panel.annotations, format)]
    const sloAxis = (['left', 'right'] as const).find(side => metricsOn(side).some(metric => /^load\.(p\d+|mean)_response_time$/.test(metric)))
    if (spec.auto.slo && data.slo && sloAxis) {
      const unit = unitOf(metricsOn(sloAxis).find(metric => metric.endsWith('response_time'))!)
      const factor = displayUnit(unit, panel[sloAxis].unit)?.factor ?? 1
      annotations.push({kind: 'hline', axis: sloAxis, y: data.slo * factor, label: `SLO ${formatMetric(data.slo, 'milliseconds')}`, source: 'slo', color: '#b42318'})
    }
    const extra = (side: AxisSide) => annotations.filter(item => item.axis === side && (item.kind === 'hline' || item.kind === 'text')).map(item => item.y!)
    const yAxis = (side: AxisSide): ModelAxis | undefined => {
      const metrics = metricsOn(side)
      if (!metrics.length || !series.some(item => item.axis === side)) return undefined
      const unit = unitOf(metrics[0])
      const axis = axisModel(side, panel[side], [...(side === 'left' && sharedLeft ? sharedLeft : values(series, side)), ...extra(side)], axisTitle(metrics, unit, data.provenance, panel[side].unit), unit, {integer: unit === 'replicas' || unit === 'count'})
      return {...axis, color: axisColor(series, side, dual)}
    }
    const xs = [...(sharedX ?? series.flatMap(item => item.x)), ...annotations.filter(item => item.kind === 'vline' && item.source === 'user').map(item => item.x!)]
    const x = axisModel('x', panel.x, xs, timeAxisLabel(panel.x.time), 'seconds', {timeFormat: format})
    return {index, letter: panels.length > 1 && spec.layout.letters ? `(${letters[index]})` : undefined, x, left: yAxis('left'), right: yAxis('right'), series, annotations, title: panel.metrics.map(item => metricTitle(item.metric, data.provenance(item.metric))).join(', '), metrics: panel.metrics.map(item => item.metric)}
  })
  const axisMetrics = (side: AxisSide) => [...new Set(panels.flatMap(panel => panel.metrics.filter(item => item.axis === side).map(item => item.metric)))]
  const legend = legendOf(seriesByPanel, side => groupList.length > 1 ? axisMetrics(side).length === 1 ? `${shortTitle(axisMetrics(side)[0], data.provenance)} (${side}):` : `${side === 'left' ? 'Left' : 'Right'} axis:` : undefined)
  const first = metricsUsed[0] ?? ''
  const title = metricsUsed.length > 1 ? metricsUsed.map(metric => metricTitle(metric, data.provenance(metric))).join(' and ') : metricTitle(first, data.provenance(first))
  const all = seriesByPanel.flat()
  const n = [...new Set(all.map(item => item.n))]
  const mode = fallback ? 'ci' : spec.runMode
  const describePanel = (panel: ModelPanel) => {
    const side = (axis: AxisSide) => panel.metrics.filter((_, position) => panels[panel.index].metrics[position].axis === axis).map(metric => metricTitle(metric, data.provenance(metric)))
    const right = side('right')
    return right.length && side('left').length ? `${side('left').join(', ')} on the left axis; ${right.join(', ')} on the right axis (${panel.series.find(item => item.axis === 'right')?.style.line === 'dashed' ? 'dashed lines, hollow markers' : 'see legend'})` : panel.title
  }
  const caption = [
    `${modelPanels.length > 1 ? modelPanels.map(panel => `${panel.letter ? `${panel.letter} ` : ''}${describePanel(panel)}`).join('; ') + '.' : dual ? `${describePanel(modelPanels[0])}.` : ''} ${modelPanels.length > 1 || dual ? 'Measured window' : `${title} over the measured window`} (time 0 = start of the measured window).`.trim(),
    all.some(item => item.n > 1) ? `${mode === 'iqr' ? 'Lines: median' : mode === 'runs' ? 'Thin lines: individual repetitions' : 'Lines: mean'} across repetitions at each sampling window (n = ${n.join('/')} per series)${mode === 'ci' ? '; shaded band: 95% confidence interval of the mean (exact Student t, n − 1 df)' : mode === 'sd' ? '; shaded band: ±1 sample standard deviation' : mode === 'iqr' ? '; shaded band: interquartile range (25th–75th percentile, linear interpolation)' : mode === 'spaghetti' ? '; thin lines: individual repetitions' : ''}.` : '',
    fallback ? 'Individual repetitions are not available for this view; the 95% CI of the mean is shown instead.' : '',
    modelPanels.some(panel => panel.annotations.some(item => item.source === 'slo')) ? `Dashed red line: SLO of ${formatMetric(data.slo, 'milliseconds')}.` : '',
    modelPanels.some(panel => panel.annotations.some(item => item.source === 'phase')) ? 'Dotted vertical lines: load-profile phase boundaries.' : '',
    modelPanels.some(panel => panel.left?.spec.log || panel.right?.spec.log) ? 'Logarithmic y axis.' : ''
  ].filter(Boolean).join(' ')
  const methods = metricsUsed.map(metric => provenanceSentence(metric, data.provenance(metric))).join(' ')
  const csv = () => toCSV([['series', 'minute', 'value', 'band_low', 'band_high', 'n_runs', 'axis', 'metric', 'group', 'panel', 'run', 'display_unit'], ...modelPanels.flatMap(panel => panel.series.flatMap(series => {
    const unit = (series.axis === 'left' ? panel.left : panel.right)?.unit ?? ''
    const unitLabel = displayUnit(unit, panels[panel.index][series.axis].unit)?.label ?? unit
    const minute = (value: number) => format === 'min' ? value : value / 60
    const rows = series.x.flatMap((x, index) => Number.isFinite(x) ? [[series.label, minute(x), series.y[index], series.low?.[index], series.high?.[index], series.n, series.axis, series.metric, series.group, panel.index + 1, 'mean', unitLabel] as Array<string | number | undefined>] : [])
    const runs = (series.runs ?? []).flatMap(run => series.x.flatMap((x, index) => Number.isFinite(x) ? [[series.label, minute(x), run.y[index], undefined, undefined, 1, series.axis, series.metric, series.group, panel.index + 1, run.label, unitLabel]] : []))
    return [...rows, ...runs]
  }))])
  return {type: 'timeseries', rows, columns, panels: modelPanels, legend, dual, runMode: spec.runMode, runModeFallback: fallback, title, caption, methods, csv, empty: all.length ? undefined : 'Nothing to plot: every series is hidden or the metrics have no samples.'}
}

function xyModel(spec: FigureSpec, data: ModelData, fontSize: number, hidden: (group: string) => boolean, labels: FigureLabelOverrides): FigureModel {
  const panel = spec.panels[0]
  const xy = data.xy!
  const pairs = [{selection: xy.y, axis: 'left' as const}, ...(panel.y2?.metric ? [{selection: panel.y2, axis: 'right' as const}] : [])]
  const built = pairs.map(pair => ({...pair, data: buildXY(xy.source, xy.x, pair.selection)}))
  const xUnit = built[0].data.x.unit
  const xFactor = displayUnit(xUnit, panel.x.unit)?.factor ?? 1
  const metricKey = (selection: {metric: string; service?: string}) => `${selection.metric}${selection.service ? `@${selection.service}` : ''}`
  const groupList = [...new Set(built.flatMap(item => item.data.series.map(series => series.label)))]
  const groupCount = groupList.length
  const drafts: Draft[] = built.flatMap(item => {
    const factor = displayUnit(item.data.y.unit, panel[item.axis].unit)?.factor ?? 1
    return item.data.series.filter(series => !hidden(series.label) || groupCount <= 1).map((series): Draft => {
      const short = `${metricTitle(item.selection.metric, item.data.y.provenance).split(' — ')[0]}${item.selection.service ? ` (${item.selection.service})` : ''}`
      const defaultLabel = pairs.length > 1 ? seriesLabel({group: series.label, metric: short, groups: groupCount, metricsOnAxis: 1, named: true}) : compactLabel(series.label)
      return {key: seriesKey(metricKey(item.selection), series.label), defaultLabel, metric: item.selection.metric, group: series.label, axis: item.axis, slot: Math.max(0, groupList.indexOf(series.label)), pageColor: series.color ? exportColor(series.color) : undefined, build: style => {
        const points = series.runs.flatMap(run => run.points.filter(point => (!panel.x.log || point.x > 0) && (!panel[item.axis].log || point.y > 0)))
        const xs: number[] = []
        const ys: number[] = []
        series.runs.forEach((run, index) => {
          if (index) {xs.push(NaN); ys.push(NaN)}
          const step = run.step ?? Infinity
          run.points.forEach((point, position) => {
            if (position && Number.isFinite(step) && point.t - run.points[position - 1].t > step * 1.5) {xs.push(NaN); ys.push(NaN)}
            xs.push(point.x * xFactor); ys.push(point.y * factor)
          })
        })
        const fit = xy.options.fit ? fitXY(points.map(point => ({t: point.t, x: point.x * xFactor, y: point.y * factor})), {logX: panel.x.log, logY: panel[item.axis].log}) : undefined
        let fitLine: ModelSeries['fit']
        if (fit) {
          const low = Math.min(...points.map(point => point.x * xFactor))
          const high = Math.max(...points.map(point => point.x * xFactor))
          const fx = Array.from({length: 33}, (_, index) => panel.x.log ? 10 ** (Math.log10(low) + index / 32 * (Math.log10(high) - Math.log10(low))) : low + index / 32 * (high - low))
          const fy = fx.map(value => {const at = fit.intercept + fit.slope * (fit.logX ? Math.log10(value) : value); return fit.logY ? 10 ** at : at})
          fitLine = {x: fx, y: fy, summary: fitSummary(fit)}
        }
        const pathStyle = xy.options.path ? style : {...style, line: spec.series[seriesKey(metricKey(item.selection), series.label)]?.line ?? 'none' as const}
        return {key: seriesKey(metricKey(item.selection), series.label), defaultLabel, label: defaultLabel, metric: item.selection.metric, group: series.label, axis: item.axis, style: pathStyle, kind: 'scatter', x: xs, y: ys, showMean: true, n: series.runs.length, fit: fitLine}
      }}
    })
  })
  const [series] = finishStyles([drafts], spec, fontSize, labels)
  const dual = series.some(item => item.axis === 'right') && series.some(item => item.axis === 'left')
  const annotations = userAnnotations(panel.annotations, undefined)
  const axisValues = (side: AxisSide) => [...series.filter(item => item.axis === side).flatMap(item => [...item.y, ...(item.fit?.y ?? [])]), ...annotations.filter(item => item.axis === side && item.y !== undefined).map(item => item.y!)]
  const yAxis = (side: AxisSide): ModelAxis | undefined => {
    const entry = built.find(item => item.axis === side)
    if (!entry || !series.some(item => item.axis === side)) return undefined
    const unit = entry.data.y.unit
    const axis = axisModel(side, {...panel[side], range: panel[side].range === 'zero' && !panel[side].log ? 'zero' : panel[side].range}, axisValues(side), relabelUnit(entry.data.y.title, unit, panel[side].unit), unit, {integer: unit === 'replicas' || unit === 'count'})
    return {...axis, color: axisColor(series, side, dual)}
  }
  const xAxis = axisModel('x', panel.x, [...series.flatMap(item => item.x), ...annotations.filter(item => item.x !== undefined).flatMap(item => [item.x!, ...(item.x2 !== undefined ? [item.x2] : [])])], relabelUnit(built[0].data.x.title, xUnit, panel.x.unit), xUnit, {integer: xUnit === 'replicas' || xUnit === 'count'})
  const modelPanel: ModelPanel = {index: 0, x: xAxis, left: yAxis('left'), right: yAxis('right'), series, annotations, title: '', metrics: pairs.map(pair => pair.selection.metric)}
  const main = built[0].data
  const title = `${metricTitle(main.y.metric, main.y.provenance)}${built[1] ? ` and ${metricTitle(built[1].data.y.metric, built[1].data.y.provenance)}` : ''} vs ${metricTitle(main.x.metric, main.x.provenance)}`
  const fits = series.filter(item => item.fit).map(item => `${series.length > 1 ? `${item.label}: ` : ''}${item.fit!.summary}`)
  const caption = [
    `${main.y.title}${built[1] ? ` (left axis) and ${built[1].data.y.title} (right axis, hollow markers)` : ''} against ${main.x.title}. Each marker is one sampling window${main.level === 'mean' ? ' of the mean across repetitions' : ' of one run'}; X and Y are paired by timestamp within the measured window and never interpolated.`,
    xy.options.path ? 'Thin lines connect the windows of each run in time order (broken at missing windows).' : '',
    fits.length ? `Lines: ordinary least-squares fit per series${panel.x.log || panel.left.log ? ' on the log-scaled axes' : ''}.` : '',
    panel.x.log || panel.left.log || panel.right.log ? 'Logarithmic axes as labelled.' : ''
  ].filter(Boolean).join(' ')
  const methods = [fits.length ? `Fit: ordinary least squares; slope with 95% CI (Student t, n − 2 df, assuming independent windows — adjacent windows are autocorrelated, so the interval is optimistic). ${fits.join('; ')}.` : '', ...[...new Set([main.x, main.y, ...(built[1] ? [built[1].data.y] : [])].map(axis => axis.provenance ? provenanceSentence(axis.metric, axis.provenance) : ''))].filter(Boolean)].filter(Boolean).join(' ')
  const csv = () => toCSV([['series', 'axis', 'x_metric', 'x', 'y_metric', 'y', 'x_unit', 'y_unit'], ...series.flatMap(item => item.x.flatMap((x, index) => Number.isFinite(x) ? [[item.label, item.axis, main.x.metric, x, item.metric, item.y[index], displayUnit(xUnit, panel.x.unit)?.label ?? xUnit, (item.axis === 'left' ? modelPanel.left : modelPanel.right)?.unit ?? '']] : []))])
  return {type: 'xy', rows: 1, columns: 1, panels: [modelPanel], legend: legendOf([series], side => groupCount > 1 ? `${metricTitle(built.find(item => item.axis === side)!.selection.metric).split(' — ')[0]} (${side}):` : undefined), dual, runMode: 'mean', runModeFallback: false, title, caption, methods, csv, empty: series.length ? undefined : 'No sampling window has both metrics. Pick other metrics or services.'}
}

