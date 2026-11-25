import type {ReactNode} from 'react'
import type {AggregateSeries} from '../../../resultsData'
import {formatMs, type Insight, type InsightSeverity} from '../insights'
import {metricTitle} from '../format'
import type {OverviewGroup} from '../overview'
import {defaultXYOptions} from '../xy'
import {defaultPanel, defaultSpec} from './figureSpec'
import {defaultXYState, timeMetrics, type StudioData} from './image'
import type {ChartState} from './layout'
import {buildRecipes, curvePoints, dotsThumb, lineThumb, placeholderThumb, smartTimeSeriesSpec} from './recipes'

// What KubeStudio suggests when it opens on a new figure: charts chosen from THIS
// experiment's results (its findings, load fidelity, variants, repetitions, autoscaling, SLO),
// each with a one-line reason tied to the data, ranked by how much they have to show.

export type Goal = 'latency' | 'load' | 'services' | 'scaling' | 'compare'
export const goals: Array<{id: Goal; label: string; question: string}> = [
  {id: 'load', label: 'Load and throughput', question: 'Did it get the load it was meant to, and how did it cope?'},
  {id: 'latency', label: 'Latency', question: 'How fast did it answer, and was the SLO met?'},
  {id: 'services', label: 'Bottlenecks', question: 'Which service holds it back?'},
  {id: 'scaling', label: 'Resources and scaling', question: 'CPU, replicas and placement'},
  {id: 'compare', label: 'Compare variants', question: 'Which variant or repetition does better?'}
]

export interface Suggestion {
  id: string
  goal: Goal
  title: string
  // Why this chart, from the data: "recommendationservice is the bottleneck → …".
  why: string
  score: number
  // The finding it rests on, when there is one.
  severity?: InsightSeverity
  chart: Omit<ChartState, 'id'>
  thumb: ReactNode
}

const severityBoost: Record<InsightSeverity, number> = {critical: 40, warning: 25, info: 10, ok: 0}
const mean = (values: number[]) => values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : undefined
const valuesOf = (series: AggregateSeries[]) => series.flatMap(item => item.points.map(point => point.value)).filter(Number.isFinite)
const percent = (ratio: number) => `${Math.round(ratio * 100)} %`
const rps = (value: number) => `${value >= 100 ? Math.round(value) : +value.toFixed(1)} RPS`
// Each series scaled to its own range, so two units share one thumbnail.
const ownScale = (series: AggregateSeries, dashed = false) => {
  const points = curvePoints(series)
  const high = Math.max(...points.map(point => point.y).filter(Number.isFinite), 1e-9)
  return {label: series.label, color: series.color, dashed, points: points.map(point => ({x: point.x, y: point.y / high}))}
}
const latencies = ['load.p95_response_time', 'application.p95_response_time', 'load.p99_response_time', 'load.p50_response_time']

export function buildSuggestions(data: StudioData, groups: OverviewGroup[]): Suggestion[] {
  const guide = data.guide
  const insights = guide?.insights ?? []
  const finding = (id: string): Insight | undefined => insights.find(item => item.id === id)
  const boost = (insight: Insight | undefined) => insight ? severityBoost[insight.severity] : 0
  const time = timeMetrics(data)
  const xy = defaultXYState(data)
  const recipes = buildRecipes(data, groups)
  const recipeThumb = (id: string) => recipes.find(recipe => recipe.id === id)?.thumb
  const variants = guide?.variants ?? groups.length
  const repetitions = guide?.repetitions ?? 0
  const out: Suggestion[] = []
  const add = (suggestion: Omit<Suggestion, 'chart'> & {chart: Partial<Omit<ChartState, 'id'>> & Pick<ChartState, 'kind'>}) => out.push({...suggestion, chart: {metric: '', xy, ...suggestion.chart}})
  const latency = latencies.find(metric => time.includes(metric))
  const latencySeries = latency ? data.plot(latency) : []
  const latencyName = latency ? metricTitle(latency, data.provenance(latency)) : ''

  // Latency, against the SLO when there is one.
  const slo = !data.omitSLO ? data.plot('load.p95_response_time').find(item => item.sloMilliseconds)?.sloMilliseconds : undefined
  if (latency) {
    const sloFinding = finding('slo')
    const timeChart = {kind: 'timeseries' as const, metric: latency, spec: smartTimeSeriesSpec(latencySeries)}
    const lines = latencySeries.map(item => ({label: item.label, color: item.color, points: curvePoints(item)}))
    if (slo !== undefined && latency === 'load.p95_response_time') {
      const missed = sloFinding && sloFinding.severity !== 'ok'
      const start = lines[0]?.points[0]?.x ?? 0
      const end = lines[0]?.points.at(-1)?.x ?? 1
      add({id: 'slo', goal: 'latency', title: 'Latency against the SLO', score: missed ? 60 + boost(sloFinding) : 38, severity: sloFinding?.severity,
        why: missed ? `${sloFinding.title} → P95 over time with the SLO line` : `The ${formatMs(slo)} SLO held → show the margin under the SLO line`,
        chart: timeChart, thumb: lineThumb([...lines, {label: 'SLO', color: 'var(--destructive, #b42318)', dashed: true, points: [{x: start, y: slo}, {x: end, y: slo}]}], groups)})
    } else {
      const average = mean(valuesOf(latencySeries))
      add({id: 'latency-time', goal: 'latency', title: variants > 1 ? 'Latency over time by variant' : 'Latency over time', score: 40,
        why: `${latencyName} averaged ${average === undefined ? 'n/a' : formatMs(average)} → its course over the measured window`, chart: timeChart, thumb: recipeThumb('latency-time') ?? lineThumb(lines, groups)})
    }
  }

  // Load fidelity: did the generator send what it was asked to?
  if (data.fidelity.length) {
    const offered = mean(valuesOf(data.fidelity.filter(item => item.role === 'offered')))
    const achieved = mean(valuesOf(data.fidelity.filter(item => item.role === 'achieved')))
    const ratio = offered && achieved !== undefined ? achieved / offered : undefined
    const short = ratio !== undefined && ratio < .95
    const over = ratio !== undefined && ratio > 1.05
    const off = finding(short ? 'target-not-sustained' : 'target-exceeded')
    add({id: 'fidelity', goal: 'load', title: 'Load fidelity', score: short ? 70 + boost(off) : over ? 50 + boost(off) : 30, severity: short || over ? off?.severity ?? 'warning' : undefined,
      why: ratio === undefined ? 'Offered and achieved request rate → did the generator keep up?'
        : short ? `Achieved ${percent(ratio)} of the target rate → offered vs achieved load`
        : over ? `Sent ${percent(ratio)} of the target rate, more than asked → offered vs achieved load`
        : `Achieved ${percent(ratio)} of the target rate → show the load was delivered`,
      chart: {kind: 'fidelity'}, thumb: recipeThumb('fidelity') ?? placeholderThumb('fidelity')})
  }

  // Throughput vs latency when the load varied enough to draw a curve.
  const throughput = ['load.achieved_rps', 'load.successful_rps'].find(metric => data.xy?.metrics.includes(metric))
  if (throughput && latency && data.xy?.metrics.includes(latency)) {
    const rates = valuesOf(data.plot(throughput))
    const low = Math.min(...rates)
    const high = Math.max(...rates)
    if (rates.length && high > 1.3 * Math.max(low, 1e-9)) add({id: 'xy', goal: 'load', title: 'Throughput vs latency', score: 45,
      why: `Load ranged from ${rps(low)} to ${rps(high)} → how latency grows with throughput`,
      chart: {kind: 'xy', xy: {x: {metric: throughput}, y: {metric: latency}, options: {...defaultXYOptions, fit: true}}}, thumb: recipeThumb('xy') ?? placeholderThumb('xy')})
  }

  // Errors over time.
  const errors = finding('errors')
  if (errors && time.includes('load.failure_percentage')) {
    const series = data.plot('load.failure_percentage')
    add({id: 'errors', goal: 'load', title: 'Failed requests over time', score: 50 + boost(errors), severity: errors.severity, why: `${errors.title} → when the failures happened`,
      chart: {kind: 'timeseries', metric: 'load.failure_percentage', spec: smartTimeSeriesSpec(series)}, thumb: lineThumb(series.map(item => ({label: item.label, color: item.color, points: curvePoints(item)})), groups)})
  }

  // Services: the bottleneck's latency against every other service, or where the CPU goes.
  const bottleneck = finding('bottleneck')
  const serviceRows = (metric: string) => data.serviceMetrics.includes(metric) ? data.services(metric)?.rows ?? [] : []
  const serviceThumb = (metric: string) => {
    const rows = serviceRows(metric)
    return dotsThumb([...new Set(rows.map(row => row.service))].map(service => ({label: service, values: rows.filter(row => row.service === service).map(row => row.summary.mean).filter(Number.isFinite)})), [])
  }
  const serverLatency = ['application.p95_response_time', 'redis.operation_p95_response_time'].find(metric => serviceRows(metric).length)
  if (serverLatency) {
    const service = bottleneck?.evidence.service
    add({id: 'services-latency', goal: 'services', title: 'Server P95 by service', score: bottleneck ? 75 + boost(bottleneck) : 34, severity: bottleneck?.severity,
      why: service ? `${service} is the bottleneck${bottleneck.evidence.value !== undefined ? ` (server P95 ${formatMs(bottleneck.evidence.value)})` : ''} → every service's P95 side by side` : `${new Set(serviceRows(serverLatency).map(row => row.service)).size} services → which one answers slowest`,
      chart: {kind: 'services', metric: serverLatency}, thumb: serviceThumb(serverLatency)})
  }
  if (serviceRows('cpu.cores').length) {
    const top = guide?.services.find(item => item.cpuCores !== undefined)
    add({id: 'services-cpu', goal: 'scaling', title: 'CPU by service', score: 30,
      why: top ? `${top.service} uses the most CPU (${+top.cpuCores!.toFixed(2)} cores) → where the work goes` : 'CPU per service → where the work goes',
      chart: {kind: 'services', metric: 'cpu.cores'}, thumb: serviceThumb('cpu.cores')})
  }
  const restarts = finding('restarts')
  if (restarts && time.includes('pod.restarts')) add({id: 'restarts', goal: 'services', title: 'Pod restarts over time', score: 40 + boost(restarts), severity: restarts.severity, why: `${restarts.title} → when they restarted`,
    chart: {kind: 'timeseries', metric: 'pod.restarts', spec: smartTimeSeriesSpec(data.plot('pod.restarts'))}, thumb: placeholderThumb('timeseries')})

  // Autoscaling: replicas over time against the load that drove them.
  const scaling = finding('autoscaling')
  const replicas = data.plot('deployment.ready_replicas')
  const replicaValues = valuesOf(replicas)
  const scaled = Boolean(scaling) || (replicaValues.length > 0 && Math.max(...replicaValues) > Math.min(...replicaValues))
  if (scaled && time.includes('deployment.ready_replicas')) {
    const load = ['load.achieved_rps', 'load.successful_rps', 'load.offered_rps'].find(metric => time.includes(metric))
    const spec = defaultSpec('timeseries')
    const withLoad = load ? {...spec, panels: [{...defaultPanel([{metric: 'deployment.ready_replicas', axis: 'left'}, {metric: load, axis: 'right'}]), right: {...spec.panels[0].right, range: 'zero' as const}}]} : spec
    add({id: 'scaling', goal: 'scaling', title: load ? 'Replicas over time vs load' : 'Replicas over time', score: 55 + boost(scaling),
      why: `${scaling ? scaling.title.replace(/^Autoscaling was active: (\d+ services?) scaled/, 'Autoscaling scaled $1') : `Ready replicas went from ${Math.min(...replicaValues)} to ${Math.max(...replicaValues)}`} → ${load ? 'replicas over time against the request rate' : 'replicas over time'}`,
      chart: {kind: 'timeseries', metric: 'deployment.ready_replicas', spec: withLoad},
      thumb: lineThumb([...replicas.map(item => ownScale(item)), ...(load ? data.plot(load).map(item => ownScale(item, true)) : [])], groups)})
  }

  // Variants and repetitions: the comparison, with confidence intervals when there are repetitions.
  const runMetric = data.runLevel.find(entry => entry.metric === latency && entry.groups.some(group => group.values.length > 0))
  if (runMetric && variants > 1) add({id: 'compare', goal: 'compare', title: repetitions > 1 ? 'P95 comparison with 95 % CI' : 'P95 comparison', score: 65 + (repetitions > 1 ? 10 : 0),
    why: `${variants} variants${repetitions > 1 ? ` × ${repetitions} repetitions → every repetition, mean and 95 % CI` : ' → their P95 side by side'}`,
    chart: {kind: 'runlevel', metric: runMetric.metric}, thumb: recipeThumb('runlevel') ?? placeholderThumb('runlevel')})
  else if (runMetric && repetitions > 1) add({id: 'repetitions', goal: 'compare', title: 'Run-to-run variation', score: 32,
    why: `${repetitions} repetitions → how much P95 moved between them`, chart: {kind: 'runlevel', metric: runMetric.metric}, thumb: recipeThumb('runlevel') ?? placeholderThumb('runlevel')})
  if (data.ladder.length) {
    const levels = data.ladder.find(item => item.vantage !== 'server')?.points ?? data.ladder[0].points
    const p50 = levels.find(point => point.level === 'p50')?.mean
    const p99 = levels.find(point => point.level === 'p99')?.mean
    add({id: 'ladder', goal: variants > 1 ? 'compare' : 'latency', title: 'Percentile ladder', score: p50 && p99 && p99 > 3 * p50 ? 44 : 28,
      why: p50 !== undefined && p99 !== undefined ? `P50 ${formatMs(p50)} vs P99 ${formatMs(p99)} → how heavy the tail is` : 'P50 → P99, client and server side → how heavy the tail is',
      chart: {kind: 'ladder'}, thumb: recipeThumb('ladder') ?? placeholderThumb('ladder')})
  }
  if (data.placement) add({id: 'placement', goal: 'scaling', title: 'Placement heatmap', score: 26, why: 'Where the pods ran → replicas per zone over time', chart: {kind: 'placement'}, thumb: placeholderThumb('placement')})
  return out.sort((a, b) => b.score - a.score || a.title.localeCompare(b.title))
}
