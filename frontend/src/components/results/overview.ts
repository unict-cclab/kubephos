import {aggregatePlotMetric, knownWorkloads, resolveWorkload, type AggregateSeries, type LoadedDataset, type RunLevelMetric, type VariantComparison} from '../../resultsData'
import {summarize, tCritical} from '../../statistics'
import type {Experiment} from '../../types'
import type {Insight, ServiceHealth} from './insights'
import type {ChartPanel, ChartPoint, ChartSeries} from './TimeChart'

// Pure models behind the results overview (KPI cards, primary chart, services table).
// They read only run-level values (one number per repetition), aggregated curves and
// per-service summaries, so the experiment page (raw datasets) and the comparison page
// (worker-prepared data) build the same overview.

export const overviewMetrics = ['load.achieved_rps', 'load.successful_rps', 'load.failed_rps', 'load.offered_rps', 'load.configured_rps', 'load.p50_response_time', 'load.p95_response_time', 'load.p99_response_time', 'load.failure_percentage', 'load.slo_violation_percentage']

// colorNote: why the display colour differs from the user's chosen colour (legend tooltip).
export interface OverviewGroup {id: string; label: string; color: string; colorNote?: string}

export const paletteColor = (index: number) => `var(--chart-${index % 8 + 1})`

// Identity colour per variant: the configured colour, else the palette slot of its position.
export function overviewGroups(runLevel: RunLevelMetric[]): OverviewGroup[] {
  const seen = new Map<string, OverviewGroup>()
  for (const metric of runLevel) metric.groups.forEach((group, index) => {
    if (!seen.has(group.id) && group.values.length) seen.set(group.id, {id: group.id, label: group.label, color: group.color || paletteColor(index)})
  })
  // Keep variant order (first metric that lists every group).
  const order = runLevel.find(metric => metric.groups.length)?.groups.map(group => group.id) ?? []
  return [...seen.values()].sort((a, b) => order.indexOf(a.id) - order.indexOf(b.id))
}

export type Tone = 'ok' | 'warning' | 'critical'
export type KpiId = 'throughput' | 'latency' | 'errors' | 'slo'

export interface KpiEntry {
  group: OverviewGroup
  value?: number
  n: number
  // Relative change vs the baseline group (percent); undefined for the baseline itself.
  change?: number
  baseline?: boolean
  comparison?: VariantComparison
  tone?: Tone
  note?: string
}

export interface KpiCard {
  id: KpiId
  title: string
  metric: string
  unit: string
  // Lower values are better (latency, errors) or higher (throughput, compliance).
  better: 'higher' | 'lower'
  entries: KpiEntry[]
  // Throughput: the offered rate per group; latency: P50/P99 per group.
  detail: (entry: KpiEntry) => string | undefined
}

const mean = (values: number[]) => values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : undefined

function valuesOf(runLevel: RunLevelMetric[], metric: string, groupId: string): {values: number[]; artifactIds: string[]} {
  const group = runLevel.find(item => item.metric === metric)?.groups.find(item => item.id === groupId)
  return {values: group?.values ?? [], artifactIds: group?.artifactIds ?? []}
}

// Achieved rate per run: load.achieved_rps, else successful + failed of the same run.
function achievedValues(runLevel: RunLevelMetric[], groupId: string): {values: number[]; metric: string} {
  const achieved = valuesOf(runLevel, 'load.achieved_rps', groupId)
  if (achieved.values.length) return {values: achieved.values, metric: 'load.achieved_rps'}
  const ok = valuesOf(runLevel, 'load.successful_rps', groupId)
  const failed = valuesOf(runLevel, 'load.failed_rps', groupId)
  return {values: ok.artifactIds.map((id, index) => ok.values[index] + (failed.values[failed.artifactIds.indexOf(id)] ?? 0)), metric: 'load.successful_rps'}
}

export interface KpiInput {
  runLevel: RunLevelMetric[]
  comparisons: VariantComparison[]
  baseline?: string
  slo?: number
}

export function kpiCards({runLevel, comparisons, baseline, slo}: KpiInput): KpiCard[] {
  const groups = overviewGroups(runLevel)
  if (!groups.length) return []
  const baselineId = groups.some(group => group.id === baseline) ? baseline! : groups[0].id
  const multiple = groups.length > 1
  const comparisonFor = (metrics: string[], groupId: string) => multiple ? comparisons.find(row => metrics.includes(row.metric) && row.groupId === groupId && row.baselineId === baselineId) : undefined
  const build = (id: KpiId, title: string, metric: string, unit: string, better: 'higher' | 'lower', read: (group: OverviewGroup) => number[], tone: (value: number, group: OverviewGroup) => Tone | undefined, detail: KpiCard['detail'], comparisonMetrics = [metric]): KpiCard | undefined => {
    const values = new Map(groups.map(group => [group.id, read(group)]))
    if (![...values.values()].some(list => list.length)) return undefined
    const baseValue = mean(values.get(baselineId) ?? [])
    const entries = groups.map(group => {
      const list = values.get(group.id) ?? []
      const value = mean(list)
      const change = multiple && group.id !== baselineId && value !== undefined && baseValue ? (value - baseValue) / Math.abs(baseValue) * 100 : undefined
      return {group, value, n: list.length, change, baseline: multiple && group.id === baselineId, comparison: group.id === baselineId ? undefined : comparisonFor(comparisonMetrics, group.id), tone: value === undefined ? undefined : tone(value, group)}
    })
    return {id, title, metric, unit, better, entries, detail}
  }
  const offered = (group: OverviewGroup) => {const list = valuesOf(runLevel, 'load.offered_rps', group.id).values; return list.length ? list : valuesOf(runLevel, 'load.configured_rps', group.id).values}
  const offeredLabel = runLevel.some(item => item.metric === 'load.offered_rps' && item.groups.some(group => group.values.length)) ? 'offered' : 'configured'
  const ratio = (group: OverviewGroup) => {const achieved = mean(achievedValues(runLevel, group.id).values); const target = mean(offered(group)); return achieved !== undefined && target ? achieved / target : undefined}
  const percentile = (metric: string, group: OverviewGroup) => mean(valuesOf(runLevel, metric, group.id).values)
  const cards = [
    build('throughput', 'Throughput', 'load.achieved_rps', 'requests_per_second', 'higher', group => achievedValues(runLevel, group.id).values, (_, group) => {const share = ratio(group); return share === undefined ? undefined : share < .8 ? 'critical' : share < .95 || share > 1.05 ? 'warning' : 'ok'}, entry => {
      const target = mean(offered(entry.group))
      const share = ratio(entry.group)
      return target === undefined ? undefined : `of ${Math.round(target).toLocaleString('en-US')} req/s ${offeredLabel}${share === undefined ? '' : ` (${Math.round(share * 100)} %)`}`
    }, ['load.achieved_rps', 'load.successful_rps']),
    build('latency', 'Latency P95', 'load.p95_response_time', 'milliseconds', 'lower', group => valuesOf(runLevel, 'load.p95_response_time', group.id).values, value => slo === undefined ? undefined : value > slo ? 'warning' : 'ok', entry => {
      const parts = [['P50', percentile('load.p50_response_time', entry.group)], ['P99', percentile('load.p99_response_time', entry.group)]].filter((item): item is [string, number] => item[1] !== undefined)
      return parts.length ? parts.map(([label, value]) => `${label} ${formatShortMs(value)}`).join(', ') : slo !== undefined ? `${entry.value !== undefined && entry.value > slo ? 'above' : 'within'} SLO ${formatShortMs(slo)}` : 'mean of window P95s'
    }),
    build('errors', 'Error rate', 'load.failure_percentage', 'percent', 'lower', group => valuesOf(runLevel, 'load.failure_percentage', group.id).values, value => value >= 5 ? 'critical' : value >= 1 ? 'warning' : 'ok', () => 'of all requests failed'),
    build('slo', 'SLO compliance', 'load.slo_violation_percentage', 'percent', 'higher', group => valuesOf(runLevel, 'load.slo_violation_percentage', group.id).values.map(value => 100 - value), value => value < 50 ? 'critical' : value < 95 ? 'warning' : 'ok', () => slo === undefined ? 'of windows met the P95 objective' : `of windows kept P95 under ${formatShortMs(slo)}`)
  ]
  return cards.filter((card): card is KpiCard => Boolean(card))
}

export function formatShortMs(value: number): string {
  return value >= 1000 ? `${(value / 1000).toFixed(value >= 10000 ? 1 : 2).replace(/\.?0+$/, '')} s` : `${Math.round(value)} ms`
}

// Significance of a between-group difference, as a short badge.
export function significance(row: VariantComparison | undefined): {label: string; tone: 'significant' | 'neutral'; title: string} | undefined {
  if (!row) return undefined
  if (row.insufficient || row.adjustedPValue === undefined) return {label: `n = ${row.groupCount}/${row.baselineCount}`, tone: 'neutral', title: 'Fewer than 3 repetitions in a group: no significance test.'}
  const p = row.adjustedPValue
  const g = row.hedgesG
  const pText = p < .001 ? 'p < 0.001' : `p = ${p.toFixed(p < .01 ? 3 : 2)}`
  return {label: p < .05 ? `${pText}${g !== undefined ? `, g ${g.toFixed(1)}` : ''}` : 'n.s.', tone: p < .05 ? 'significant' : 'neutral', title: `Welch t-test on repetition means, Holm-adjusted ${pText} (family of ${row.familySize})${g !== undefined ? `; Hedges g = ${g.toFixed(2)}` : ''}.`}
}

// --- time series ---

export function toChartPoints(series: AggregateSeries): ChartPoint[] {
  return series.points.map(point => ({t: Date.parse(point.timestamp), v: point.value})).filter(point => Number.isFinite(point.t) && Number.isFinite(point.v))
}

// 95% CI half-width band across repetitions (exact t), when the curve is a mean of ≥ 2 runs.
export function confidenceBand(series: AggregateSeries): Array<[number, number]> | undefined {
  const runs = series.runs ?? 0
  if (!series.deviation || runs < 2 || series.deviation.length !== series.points.length) return undefined
  const factor = tCritical(runs - 1) / Math.sqrt(runs)
  return series.points.map((point, index) => [point.value - series.deviation![index].value * factor, point.value + series.deviation![index].value * factor])
}

export function chartSeries(series: AggregateSeries, id: string, label: string, color: string, options: {dashed?: boolean; band?: boolean} = {}): ChartSeries {
  return {id, label, color, points: toChartPoints(series), step: series.stepMilliseconds, dashed: options.dashed, band: options.band === false ? undefined : confidenceBand(series)}
}

// Colour of an aggregated curve: the group with the same label (curves carry the variant label).
export function colorOf(groups: OverviewGroup[], series: AggregateSeries, index: number): string {
  return groups.find(group => group.label === series.label)?.color ?? series.color ?? paletteColor(index)
}

export interface TimelineInput {
  groups: OverviewGroup[]
  plot: (metric: string) => AggregateSeries[]
  slo?: number
}

// Primary chart: offered vs achieved request rate (Locust side), requests that
// reach the ingress proxies (server side, when measured) and client latency.
export function primaryPanels({groups, plot, slo}: TimelineInput): ChartPanel[] {
  const multiple = groups.length > 1
  const achievedMetric = plot('load.achieved_rps').length ? 'load.achieved_rps' : 'load.successful_rps'
  const achieved = plot(achievedMetric)
  const offeredMetric = plot('load.offered_rps').length ? 'load.offered_rps' : 'load.configured_rps'
  const offered = plot(offeredMetric)
  const rate: ChartSeries[] = []
  if (offered[0]) rate.push({...chartSeries(offered[0], 'offered', offeredMetric === 'load.offered_rps' ? 'Offered (target)' : 'Configured (target)', 'var(--chart-target)', {dashed: true, band: false}), short: 'target'})
  achieved.forEach((series, index) => rate.push({...chartSeries(series, `achieved-${index}`, multiple ? series.label : achievedMetric === 'load.achieved_rps' ? 'Achieved' : 'Successful', colorOf(groups, series, index)), short: multiple ? series.label : achievedMetric === 'load.achieved_rps' ? 'achieved' : 'successful'}))
  const ingress = plot('ingress.request_rps').map((series, index) => ({...chartSeries(series, `ingress-${index}`, multiple ? series.label : 'Ingress proxies', colorOf(groups, series, index)), short: multiple ? series.label : 'ingress'}))
  const latency: ChartSeries[] = []
  const p95 = plot('load.p95_response_time')
  const p50 = multiple ? [] : plot('load.p50_response_time')
  p95.forEach((series, index) => latency.push({...chartSeries(series, `p95-${index}`, multiple ? `${series.label}, P95` : 'P95', colorOf(groups, series, index)), short: multiple ? series.label : 'P95'}))
  p50.forEach((series, index) => latency.push({...chartSeries(series, `p50-${index}`, 'P50', 'var(--chart-quiet)'), short: 'P50'}))
  const shortfall = !multiple && offered[0] && achieved[0] && ratioOf(achieved[0], offered[0]) < .95 ? {upper: 'offered', lower: 'achieved-0', label: 'achieved falls short'} : undefined
  return [
    {id: 'rate', title: 'Request rate, Locust side (req/s)', unit: 'requests_per_second', series: rate, shortfall},
    {id: 'ingress-rate', title: 'Request rate, server side (req/s)', unit: 'requests_per_second', series: ingress},
    {id: 'latency', title: 'Client latency', unit: 'milliseconds', series: latency, reference: slo ? {value: slo, label: `SLO ${formatShortMs(slo)}`} : undefined}
  ].filter(panel => panel.series.length)
}

const meanOf = (series: AggregateSeries) => mean(series.points.map(point => point.value).filter(Number.isFinite))
function ratioOf(achieved: AggregateSeries, offered: AggregateSeries): number {
  const a = meanOf(achieved)
  const o = meanOf(offered)
  return a !== undefined && o ? a / o : 1
}
function quantile(values: number[], q: number): number {
  const sorted = [...values].sort((a, b) => a - b)
  return sorted[Math.min(sorted.length - 1, Math.max(0, Math.round(q * (sorted.length - 1))))]
}
function roughRps(value: number): string {
  const step = value >= 200 ? 10 : value >= 50 ? 5 : 1
  return (Math.round(value / step) * step).toLocaleString('en-US')
}

// Title of the primary chart: what the curves show, in one sentence (the subtitle says what is plotted).
export function timelineHeadline({groups, plot, slo}: TimelineInput): string {
  const fallback = 'Load and latency over the measured window'
  const achieved = plot('load.achieved_rps').length ? plot('load.achieved_rps') : plot('load.successful_rps')
  const offered = plot('load.offered_rps').length ? plot('load.offered_rps') : plot('load.configured_rps')
  const p95 = plot('load.p95_response_time')
  if (groups.length > 1) {
    const latencies = p95.map(series => ({label: series.label, value: meanOf(series)})).filter((item): item is {label: string; value: number} => item.value !== undefined).sort((a, b) => a.value - b.value)
    if (latencies.length > 1) {
      const best = latencies[0]
      const worst = latencies[latencies.length - 1]
      if (worst.value > 1.2 * best.value) return `${best.label} keeps client P95 near ${formatShortMs(best.value)}, against ${formatShortMs(worst.value)} for ${worst.label}`
    }
    const rates = achieved.map(series => ({label: series.label, value: meanOf(series)})).filter((item): item is {label: string; value: number} => item.value !== undefined).sort((a, b) => b.value - a.value)
    if (rates.length > 1 && rates[0].value > 1.1 * rates[rates.length - 1].value) return `${rates[0].label} sustains ${roughRps(rates[0].value)} req/s, against ${roughRps(rates[rates.length - 1].value)} for ${rates[rates.length - 1].label}`
    return latencies.length > 1 || rates.length > 1 ? 'The variants track each other closely' : fallback
  }
  const a = achieved[0]
  const o = offered[0]
  const latency = p95[0] && meanOf(p95[0])
  if (a && o) {
    const ratio = ratioOf(a, o)
    const values = a.points.map(point => point.value).filter(Number.isFinite)
    const targets = o.points.map(point => point.value).filter(Number.isFinite)
    if (ratio < .95 && values.length && targets.length) {
      const top = Math.max(...targets)
      const level = roughRps(quantile(values, .9))
      return top > 1.1 * Math.min(...targets) ? `Achieved throughput levels off near ${level} req/s while the target climbs to ${roughRps(top)}` : `Achieved throughput stays near ${level} req/s, short of the ${roughRps(top)} req/s target`
    }
    if (ratio > 1.05) return `Achieved throughput overshoots the target by ${Math.round((ratio - 1) * 100)} %`
    if (latency !== undefined && slo !== undefined && latency > slo) return `Throughput follows the target, but client P95 sits above the ${formatShortMs(slo)} SLO`
    return latency !== undefined ? `Throughput follows the target; client P95 stays near ${formatShortMs(latency)}` : 'Throughput follows the target'
  }
  return latency !== undefined ? `Client P95 averages ${formatShortMs(latency)} over the measured window` : fallback
}

export function timelineSubtitle(variants: number, repetitions: number): string {
  const what = variants > 1 ? 'Achieved request rate (top) and client P95 latency (bottom) per variant' : 'Offered and achieved request rate (top), client P50 and P95 latency (bottom)'
  return `${what} over the measured window${repetitions > 1 ? `; lines are means of ${repetitions} repetitions, shading the 95 % confidence band` : ''}.`
}

// --- services ---

// Copy of a dataset restricted to one workload's series of a metric (others untouched).
function onlyService(item: LoadedDataset, metric: string, service: string): LoadedDataset {
  const known = knownWorkloads(item.dataset)
  return {...item, aggregateCache: undefined, dataset: {...item.dataset, spec: {...item.dataset.spec, series: item.dataset.spec.series.filter(series => series.metric !== metric || resolveWorkload(series, known)?.name === service)}}}
}

// One service's curve of a metric (mean across repetitions per variant).
export function serviceCurve(datasets: LoadedDataset[], metric: string, service: string, experiment?: Experiment): AggregateSeries[] {
  return aggregatePlotMetric(datasets.map(item => onlyService(item, metric, service)), metric, experiment)
}

export const serviceDetailMetrics: Array<{metric: string; title: string; unit: string}> = [
  {metric: 'application.p95_response_time', title: 'Server P95', unit: 'milliseconds'},
  {metric: 'application.request_rps', title: 'Requests (req/s)', unit: 'requests_per_second'},
  {metric: 'cpu.cores', title: 'CPU (cores)', unit: 'cores'},
  {metric: 'deployment.ready_replicas', title: 'Ready replicas', unit: 'replicas'}
]

// Fallback ranking from per-service summaries (comparison payloads prepared before the
// worker returned per-series services). Score = server P95; bottleneck as in insights.ts (≥ 2× the runner-up
// and ≥ 100 ms), corroborated only by what the summaries show.
export interface ServiceSummaryRow {service: string; kind?: string; groupID: string; summary: {mean: number}}
export function rankServiceSummaries(groupID: string, byMetric: Record<string, {rows: ServiceSummaryRow[]} | undefined>): ServiceHealth[] {
  const value = (metric: string, service: string) => byMetric[metric]?.rows.find(row => row.groupID === groupID && row.service === service)?.summary.mean
  const services = [...new Set(Object.values(byMetric).flatMap(entry => entry?.rows.filter(row => row.groupID === groupID).map(row => row.service) ?? []))]
  const rows = services.map((service): ServiceHealth => {
    const p95Ms = value('application.p95_response_time', service)
    const cpuCores = value('cpu.cores', service)
    const reasons = [p95Ms !== undefined ? `server P95 ${formatShortMs(p95Ms)}` : '', cpuCores !== undefined ? `CPU ${cpuCores.toFixed(2)} cores` : ''].filter(Boolean)
    return {service, rps: value('application.request_rps', service), p95Ms, cpuCores, replicas: value('deployment.ready_replicas', service), score: Math.round((p95Ms ?? 0) * 10) / 10, bottleneck: false, reasons}
  }).sort((a, b) => b.score - a.score || (b.cpuCores ?? 0) - (a.cpuCores ?? 0) || a.service.localeCompare(b.service))
  const [top, second] = rows
  if (top && (top.p95Ms ?? 0) >= 100 && (!second || top.score >= 2 * second.score)) {
    top.bottleneck = true
    top.reasons.push(second?.score ? `${(top.score / second.score).toFixed(1)}× the next slowest service (${second.service})` : 'only service with measurable latency')
  }
  return rows
}

// Mean ± CI across repetitions, as text, for tooltips.
export function acrossRunsText(values: number[], format: (value: number) => string): string {
  if (values.length < 2) return values.length ? `${format(values[0])} (1 repetition)` : 'no data'
  const summary = summarize(values)
  return `${format(summary.mean)} mean of ${values.length} repetitions; 95% CI ${format(summary.confidence95Low)}–${format(summary.confidence95High)}`
}

// Verdict of a saved comparison (no raw samples on the page): significant differences on
// the key metrics (Holm-adjusted p < 0.05 on repetition means) and load-fidelity warnings.
export function comparisonVerdict(cards: KpiCard[], fidelityWarnings: Array<{label: string; text: string}>): Insight[] {
  const insights: Insight[] = []
  for (const {label, text} of fidelityWarnings.slice(0, 1)) insights.push({id: 'fidelity', severity: 'warning', title: `${label} did not reach its target load`, detail: text, evidence: {metric: 'load.achieved_rps'}, target: 'throughput'})
  for (const card of cards) for (const entry of card.entries) {
    const badge = significance(entry.comparison)
    if (!entry.comparison || badge?.tone !== 'significant' || entry.change === undefined) continue
    const better = card.better === 'higher' ? entry.change > 0 : entry.change < 0
    insights.push({id: `${card.id}-${entry.group.id}`, severity: better ? 'ok' : 'warning', title: `${entry.group.label} ${entry.change > 0 ? 'raises' : 'lowers'} ${card.title.charAt(0).toLowerCase()}${card.title.slice(1)} by ${formatChangeText(Math.abs(entry.change)).replace('+', '')} against ${entry.comparison.baseline}`, detail: `${badge.title} ${better ? 'Better' : 'Worse'} than the baseline.`, evidence: {metric: card.metric, value: entry.value, unit: card.unit}, target: card.id === 'latency' || card.id === 'slo' ? 'latency' : 'throughput'})
  }
  if (cards.some(card => card.entries.length > 1) && !insights.some(item => item.id !== 'fidelity')) insights.push({id: 'no-difference', severity: 'info', title: 'The variants do not differ significantly on the key metrics', detail: 'Throughput, latency, errors and SLO compliance stay within Holm-adjusted p ≥ 0.05 of the baseline, or there are fewer than 3 repetitions per series. Statistics has every metric.', evidence: {}})
  return insights.slice(0, 4)
}

function formatChangeText(change: number): string {
  return `${change > 0 ? '+' : '−'}${Math.abs(change) >= 10 ? Math.round(Math.abs(change)) : Math.abs(change).toFixed(1)} %`
}
