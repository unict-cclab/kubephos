import type {AggregateSeries} from '../../resultsData'
import {tCritical} from '../../statistics'
import type {MetricPoint} from '../../types'
import {metricAxisLabel, provenanceLabel} from './format'
import type {ResolvedProvenance} from './runs'

// X–Y relationship between two recorded metrics. Each point is one sampling window of one
// run: the X and Y samples taken at the same timestamp within that run's measured window.
// Only timestamps present in both series are paired; nothing is interpolated, so a window
// missing from either metric simply has no point.

export interface XYAxisSelection {metric: string; service?: string}

// One run's (or one series mean's) curve of a metric, from the page's own data.
export interface XYRunInput {runID: string; runLabel: string; seriesID: string; seriesLabel: string; color?: string; series: AggregateSeries}

export interface XYSource {
  metrics: string[]
  // Services that can filter a per-service metric; undefined when the metric is not per service.
  services: (metric: string) => string[] | undefined
  runs: (selection: XYAxisSelection) => XYRunInput[]
  provenance: (metric: string) => ResolvedProvenance
  // 'run': points are windows of individual repetitions; 'mean': windows of the mean curve
  // across repetitions (saved comparisons keep only those).
  level: 'run' | 'mean'
}

export interface XYPoint {t: number; x: number; y: number}
export interface XYRun {id: string; label: string; points: XYPoint[]; step?: number}
export interface XYSeries {id: string; label: string; color?: string; runs: XYRun[]}
export interface XYAxis extends XYAxisSelection {unit: string; title: string; provenance?: ResolvedProvenance}
export interface XYData {x: XYAxis; y: XYAxis; series: XYSeries[]; level: XYSource['level']}
export interface XYOptions {path: boolean; fit: boolean; logX: boolean; logY: boolean}
export const defaultXYOptions: XYOptions = {path: false, fit: true, logX: false, logY: false}

const time = (point: MetricPoint) => Date.parse(point.timestamp)

// Pairs samples with the same timestamp; non-finite values are dropped. Sorted by time.
export function alignByTimestamp(x: MetricPoint[], y: MetricPoint[]): XYPoint[] {
  const ys = new Map<number, number>()
  for (const point of y) if (Number.isFinite(point.value)) ys.set(time(point), point.value)
  const pairs: XYPoint[] = []
  for (const point of x) {
    const t = time(point)
    if (!Number.isFinite(point.value) || !ys.has(t)) continue
    pairs.push({t, x: point.value, y: ys.get(t)!})
  }
  return pairs.sort((a, b) => a.t - b.t)
}

export function axisTitle(selection: XYAxisSelection, unit: string, provenance?: ResolvedProvenance): string {
  const base = metricAxisLabel(selection.metric, unit, provenance)
  const origin = provenance ? ` · ${provenanceLabel(provenance)}` : ''
  return `${base}${selection.service ? ` — ${selection.service}` : ''}${origin}`
}

export function buildXY(source: XYSource, x: XYAxisSelection, y: XYAxisSelection): XYData {
  const xs = source.runs(x)
  const ys = new Map(source.runs(y).map(item => [item.runID, item]))
  const groups = new Map<string, XYSeries>()
  for (const input of xs) {
    const other = ys.get(input.runID)
    if (!other) continue
    const points = alignByTimestamp(input.series.points, other.series.points)
    if (!points.length) continue
    const group = groups.get(input.seriesID) ?? {id: input.seriesID, label: input.seriesLabel, color: input.color, runs: []}
    group.runs.push({id: input.runID, label: input.runLabel, points, step: input.series.stepMilliseconds ?? other.series.stepMilliseconds})
    groups.set(input.seriesID, group)
  }
  const unit = (runs: XYRunInput[], fallback?: Map<string, XYRunInput>) => runs[0]?.series.unit ?? [...(fallback?.values() ?? [])][0]?.series.unit ?? ''
  const xUnit = unit(xs)
  const yUnit = unit([], ys)
  const xProvenance = source.provenance(x.metric)
  const yProvenance = source.provenance(y.metric)
  return {
    x: {...x, unit: xUnit, title: axisTitle(x, xUnit, xProvenance), provenance: xProvenance},
    y: {...y, unit: yUnit, title: axisTitle(y, yUnit, yProvenance), provenance: yProvenance},
    series: [...groups.values()], level: source.level
  }
}

// Points that can be drawn on the chosen axes (log axes need positive values).
export function plottable(points: XYPoint[], options: Pick<XYOptions, 'logX' | 'logY'>): XYPoint[] {
  return points.filter(point => (!options.logX || point.x > 0) && (!options.logY || point.y > 0))
}

function ranks(values: number[]): number[] {
  const order = values.map((value, index) => ({value, index})).sort((a, b) => a.value - b.value)
  const result = new Array<number>(values.length)
  for (let start = 0; start < order.length;) {
    let end = start
    while (end + 1 < order.length && order[end + 1].value === order[start].value) end++
    const rank = (start + end) / 2 + 1
    for (let index = start; index <= end; index++) result[order[index].index] = rank
    start = end + 1
  }
  return result
}

function pearson(xs: number[], ys: number[]): number | undefined {
  const n = xs.length
  if (n < 2) return undefined
  const mx = xs.reduce((sum, value) => sum + value, 0) / n
  const my = ys.reduce((sum, value) => sum + value, 0) / n
  let sxy = 0, sxx = 0, syy = 0
  for (let index = 0; index < n; index++) {
    const dx = xs[index] - mx, dy = ys[index] - my
    sxy += dx * dy; sxx += dx * dx; syy += dy * dy
  }
  return sxx > 0 && syy > 0 ? sxy / Math.sqrt(sxx * syy) : undefined
}

export function spearman(xs: number[], ys: number[]): number | undefined {
  return pearson(ranks(xs), ranks(ys))
}

export interface XYFit {
  n: number
  slope: number
  intercept: number
  // 95 % CI of the slope (Student t, n − 2 df), assuming independent windows.
  slopeCI: [number, number]
  r: number
  r2: number
  spearman?: number
  // The fit was computed on log10 of this axis (log axes: a straight line on the chart).
  logX: boolean
  logY: boolean
}

// Ordinary least squares y = a + b·x on the plotted scale; Spearman ρ on the raw values
// (rank-based, so the same on either scale). Needs 3 points and spread on both axes.
export function fitXY(points: XYPoint[], {logX = false, logY = false}: {logX?: boolean; logY?: boolean} = {}): XYFit | undefined {
  const usable = plottable(points, {logX, logY})
  const xs = usable.map(point => logX ? Math.log10(point.x) : point.x)
  const ys = usable.map(point => logY ? Math.log10(point.y) : point.y)
  const n = xs.length
  if (n < 3) return undefined
  const mx = xs.reduce((sum, value) => sum + value, 0) / n
  const my = ys.reduce((sum, value) => sum + value, 0) / n
  let sxy = 0, sxx = 0, syy = 0
  for (let index = 0; index < n; index++) {
    const dx = xs[index] - mx, dy = ys[index] - my
    sxy += dx * dy; sxx += dx * dx; syy += dy * dy
  }
  if (!(sxx > 0) || !(syy > 0)) return undefined
  const slope = sxy / sxx
  const intercept = my - slope * mx
  const r = sxy / Math.sqrt(sxx * syy)
  const residual = Math.max(0, syy - slope * sxy)
  const standardError = Math.sqrt(residual / (n - 2) / sxx)
  const half = tCritical(n - 2) * standardError
  return {n, slope, intercept, slopeCI: [slope - half, slope + half], r, r2: r * r, spearman: spearman(usable.map(point => point.x), usable.map(point => point.y)), logX, logY}
}

const precise = (value: number) => Number(value.toPrecision(3)).toString()

export function fitSummary(fit: XYFit): string {
  const slopeLabel = fit.logX || fit.logY ? `slope (log${fit.logX ? '–' : ''}${fit.logY ? 'log' : 'lin'})` : 'slope'
  return `n = ${fit.n} · r = ${fit.r.toFixed(2)} · R² = ${fit.r2.toFixed(2)} · ${slopeLabel} = ${precise(fit.slope)} [${precise(fit.slopeCI[0])}, ${precise(fit.slopeCI[1])}]${fit.spearman !== undefined ? ` · Spearman ρ = ${fit.spearman.toFixed(2)}` : ''}`
}

// Default pair: achieved rate vs client P95 when recorded, else the first two metrics.
export function defaultXYSelection(metrics: string[]): {x: XYAxisSelection; y: XYAxisSelection} {
  const pick = (preferred: string[], exclude?: string) => preferred.find(metric => metrics.includes(metric) && metric !== exclude) ?? metrics.find(metric => metric !== exclude) ?? metrics[0] ?? ''
  const x = pick(['load.achieved_rps', 'load.successful_rps', 'load.offered_rps'])
  return {x: {metric: x}, y: {metric: pick(['load.p95_response_time', 'application.p95_response_time'], x)}}
}

export interface XYState {x: XYAxisSelection; y: XYAxisSelection; options: XYOptions}

