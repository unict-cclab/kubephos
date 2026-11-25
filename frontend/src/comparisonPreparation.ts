import {request} from './api'
import {comparisonMetricCompatible, comparisonWarnings} from './comparisons'
import {addDerivedMetrics, aggregatePlotMetric, compareRunValues, knownWorkloads, resolveWorkload, isControlMetric, responseTimeSLO, runLevelValues, serviceMetricStatistics, serviceStatisticsMetrics, statisticsCSV, validateDataset, variantEffectSizes, variantStatistics, type LoadedDataset, type AggregateSeries, type RunLevelMetric, type VariantComparison, type VariantComparisonOptions} from './resultsData'
import {methodsParagraph, runContextSummary, type RunContextSummary} from './components/results/context'
import {serviceDetailMetrics} from './components/results/overview'
import {runInsights, type Insight, type ServiceHealth} from './components/results/insights'
import {fidelityPlot, loadFidelity, type FidelitySeries, type LoadFidelity} from './components/results/loadFidelity'
import {locustGroups, type LocustGroup} from './components/results/locust'
import {percentileLadder, type LadderSeries} from './components/results/percentiles'
import {executedPilotCount, provenanceFor, withoutPilots, type ResolvedProvenance} from './components/results/runs'
import {hasAutoscaler, resourceSummary, type ResourceSummary} from './components/results/resources'
import type {Artifact, Experiment, MetricSeries, TimeSeriesDataset} from './types'

export interface ComparisonJob {
  artifacts: Artifact[]
  experiment: Experiment
  configurations: Record<string, Record<string, unknown>>
  // Reference series (variant id) and mode of the between-series comparison table.
  comparison?: VariantComparisonOptions
}
export type ComparisonColumns = Array<{variant: Experiment['variants'][number]; rows: ReturnType<typeof variantStatistics>; available: number}>
export interface PreparedComparison {
  complete: boolean
  metrics: string[]
  statisticsMetrics: string[]
  columns: ComparisonColumns
  effects: ReturnType<typeof variantEffectSizes>
  // Every series vs the baseline (or all pairs) × statisticsMetrics, Holm-adjusted together.
  comparisons: VariantComparison[]
  // Run-level values (one per repetition) per metric: dot plots and re-computing the
  // comparison table for another baseline/mode without reloading the datasets.
  runLevel: RunLevelMetric[]
  provenance: Record<string, ResolvedProvenance>
  ladder: LadderSeries[]
  fidelity: FidelitySeries[]
  fidelityRows: Array<{label: string; fidelity: LoadFidelity}>
  locust: LocustGroup[]
  context: RunContextSummary
  // Methods paragraph without the comparison sentence (added by the page for its options).
  methods: string
  pilots: number
  plots: Record<string, ReturnType<typeof aggregatePlotMetric>>
  services: Record<string, ReturnType<typeof serviceMetricStatistics>>
  warnings: string[]
  csv: string
  omitSLO: boolean
  // Verdict and services table with experiment-page parity (absent in older payloads).
  overview?: PreparedServiceOverview
  // Scaling, CPU, memory and restarts per series (key-number row; absent in older payloads).
  resources?: ResourceSummary[]
}
// Down-sampled curve (mean across a series' runs, standard deviation across runs per point)
// as typed arrays: compact and transferable from the worker without copying.
export interface PackedSeries {
  label: string
  unit: string
  // Milliseconds since the start of the measured window (centre of each down-sampling bin).
  t: Float64Array
  v: Float32Array
  sd?: Float32Array
  step?: number
  runs?: number
}
// Per-service data of one comparison series (variant): the same ranking runInsights gives
// the experiment page, and the curves behind the services table (sparkline, expanded row).
export interface SeriesServices {
  id: string
  services: ServiceHealth[]
  // service → metric (serviceCurveMetrics) → curve; set once the plots are prepared.
  curves?: Record<string, Record<string, PackedSeries>>
}
export interface PreparedServiceOverview {
  insights: Insight[]
  series: SeriesServices[]
}
export type ComparisonProgress = 'Loading results…' | 'Preparing statistics…' | 'Preparing plots…'

// Runs in a worker in the browser. Only small presentation data crosses back to React;
// raw metric samples stay in this scope and are released when preparation finishes.
export async function prepareComparison(job: ComparisonJob, signal: AbortSignal, progress: (message: ComparisonProgress) => void = () => {}, load = (artifact: Artifact) => request<TimeSeriesDataset>(`/artifacts/${artifact.id}/download`, {}, undefined, signal), statisticsReady: (value: PreparedComparison) => void = () => {}): Promise<PreparedComparison> {
  const datasets: LoadedDataset[] = []
  // Pilot (warm-up) repetitions never enter a comparison.
  const pilots = executedPilotCount(job.experiment)
  const experiment = withoutPilots(job.experiment)
  const byArtifact = new Map(experiment.variants.flatMap(variant => variant.trials.map(trial => [trial.resultArtifactId, variant] as const)))
  const sources = job.artifacts.filter(artifact => byArtifact.has(artifact.id))
  progress('Loading results…')
  for (let offset = 0; offset < sources.length; offset += 4) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    datasets.push(...await Promise.all(sources.slice(offset, offset + 4).map(async artifact => {
      const dataset = await load(artifact)
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      addDerivedMetrics(dataset, responseTimeSLO(experiment, artifact.id, job.configurations[artifact.id]))
      validateDataset(dataset)
      const variant = byArtifact.get(artifact.id)!
      return {artifact, dataset, label: variant.alias || variant.name, color: variant.color, aggregateCache: new Map()}
    })))
  }
  if (!datasets.length) throw new Error('No comparison results are available.')
  progress('Preparing statistics…')
  const metrics = [...new Set(datasets.flatMap(item => [...item.dataset.spec.series.map(series => series.metric), ...(item.dataset.spec.controlMetrics ?? []).filter(summary => summary.status !== 'not_configured').map(summary => summary.metric)]))]
    .filter(metric => comparisonMetricCompatible(datasets, metric)).sort()
  const statisticsMetrics = [...new Set([...metrics, ...datasets.flatMap(item => item.dataset.spec.controlMetrics?.map(summary => summary.metric) ?? [])])].filter(metric => metric !== 'redis.operation_p95_response_time')
  const columns = experiment.variants.map(variant => ({variant: {...variant, trials: []}, rows: variantStatistics({...experiment, variants: [variant]}, datasets, statisticsMetrics), available: variant.trials.filter(trial => datasets.some(dataset => dataset.artifact.id === trial.resultArtifactId)).length}))
  const effects = variantEffectSizes(experiment, datasets, statisticsMetrics)
  const runLevel = runLevelValues(experiment, datasets, statisticsMetrics)
  const comparisons = compareRunValues(runLevel, job.comparison)
  const warnings = comparisonWarnings(datasets, job.configurations)
  const omitSLO = new Set(datasets.map(item => item.dataset.spec.sloMilliseconds)).size > 1
  const provenance = Object.fromEntries(statisticsMetrics.map(metric => [metric, provenanceFor(datasets, metric)]))
  const variantOf = (item: LoadedDataset) => {const variant = byArtifact.get(item.artifact.id)!; return {id: variant.id, label: variant.alias || variant.name}}
  const runLabel = (item: LoadedDataset) => `${item.label} · ${item.artifact.name}`
  const extras = {
    runLevel, provenance, pilots,
    ladder: percentileLadder(datasets, experiment),
    fidelityRows: datasets.map(item => ({label: runLabel(item), fidelity: loadFidelity(item)})),
    locust: locustGroups(datasets.map(item => ({...item, label: runLabel(item)})), variantOf),
    context: runContextSummary(datasets.map(item => ({...item, label: runLabel(item)}))),
    methods: methodsParagraph({experiment, datasets, metrics: statisticsMetrics, configurations: job.configurations, pilots})
  }
  const seriesOf = experiment.variants.map(variant => ({id: variant.id, items: datasets.filter(item => byArtifact.get(item.artifact.id)?.id === variant.id)})).filter(entry => entry.items.length)
  const autoscaled = (variant: Experiment['variants'][number]) => hasAutoscaler(variant.configuration as Record<string, unknown> | undefined) || variant.trials.some(trial => hasAutoscaler(job.configurations[trial.resultArtifactId]))
  const resources = seriesOf.map(entry => resourceSummary(entry.id, entry.items, autoscaled(experiment.variants.find(variant => variant.id === entry.id)!)))
  const insightsOf = (items: LoadedDataset[]) => {
    try {return runInsights(items, experiment)} catch (cause) {console.error('Could not compute insights', cause); return {insights: [], services: []}}
  }
  const overview: PreparedServiceOverview = {insights: insightsOf(datasets).insights, series: seriesOf.map(entry => ({id: entry.id, services: insightsOf(entry.items).services}))}
  statisticsReady({complete: false, metrics, statisticsMetrics, columns, effects, comparisons, warnings, csv: '', omitSLO, plots: {}, services: {}, fidelity: [], overview, resources, ...extras})
  const csv = statisticsCSV(datasets, statisticsMetrics, experiment)
  progress('Preparing plots…')
  const plots: PreparedComparison['plots'] = {}
  const services: PreparedComparison['services'] = {}
  for (const metric of metrics.filter(metric => !isControlMetric(metric))) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    plots[metric] = aggregatePlotMetric(datasets, metric, experiment)
    if (serviceStatisticsMetrics.includes(metric)) services[metric] = serviceMetricStatistics(datasets, metric, experiment)
  }
  const curved: PreparedServiceOverview = {insights: overview.insights, series: overview.series.map(entry => {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    return {...entry, curves: serviceCurves(seriesOf.find(item => item.id === entry.id)!.items, entry.services.map(row => row.service), experiment)}
  })}
  return {complete: true, metrics, statisticsMetrics, columns, effects, comparisons, plots, services, warnings, csv, omitSLO, fidelity: fidelityPlot(datasets, experiment), overview: curved, resources, ...extras}
}

// Metrics of the services table's sparkline and expanded-row charts (serviceDetailMetrics).
export const serviceCurveMetrics = serviceDetailMetrics.map(item => item.metric)
export const maxServicePoints = 300

// One series' per-service curves: each run restricted to the service's series of a metric,
// then averaged across runs exactly like the main chart (aggregatePlotMetric).
function serviceCurves(items: LoadedDataset[], services: string[], experiment: Experiment): Record<string, Record<string, PackedSeries>> {
  const wanted = new Set(services)
  // run → metric → service → series, split once instead of filtering per service.
  const split = items.map(item => {
    const known = knownWorkloads(item.dataset)
    const byMetric = new Map<string, Map<string, MetricSeries[]>>(serviceCurveMetrics.map(metric => [metric, new Map()]))
    for (const series of item.dataset.spec.series) {
      const target = byMetric.get(series.metric)
      const name = target && series.points.length ? resolveWorkload(series, known)?.name : undefined
      if (!target || !name || !wanted.has(name)) continue
      target.set(name, [...target.get(name) ?? [], series])
    }
    return byMetric
  })
  const result: Record<string, Record<string, PackedSeries>> = {}
  for (const service of services) {
    const curves: Record<string, PackedSeries> = {}
    for (const metric of serviceCurveMetrics) {
      const runs = items.map((item, index) => ({...item, aggregateCache: undefined, dataset: {...item.dataset, spec: {...item.dataset.spec, series: split[index].get(metric)!.get(service) ?? []}}}))
      const curve = aggregatePlotMetric(runs, metric, experiment)[0]
      if (curve?.points.length) curves[metric] = packSeries(curve)
    }
    if (Object.keys(curves).length) result[service] = curves
  }
  return result
}

// Down-samples to at most `limit` points by averaging fixed time bins (a whole number of
// sampling steps wide, so gaps stay gaps); the deviation is pooled (root mean square).
export function packSeries(series: AggregateSeries, limit = maxServicePoints): PackedSeries {
  const times: number[] = []
  const values: number[] = []
  const deviations: number[] = []
  const deviation = series.deviation?.length === series.points.length ? series.deviation : undefined
  series.points.forEach((point, index) => {
    const time = Date.parse(point.timestamp)
    if (!Number.isFinite(time) || !Number.isFinite(point.value)) return
    times.push(time)
    values.push(point.value)
    if (deviation) deviations.push(Number.isFinite(deviation[index].value) ? deviation[index].value : 0)
  })
  const base = {label: series.label, unit: series.unit, ...(series.runs ? {runs: series.runs} : {})}
  if (times.length <= limit) return {...base, t: Float64Array.from(times), v: Float32Array.from(values), ...(deviation ? {sd: Float32Array.from(deviations)} : {}), ...(series.stepMilliseconds ? {step: series.stepMilliseconds} : {})}
  const span = times[times.length - 1] - times[0]
  const unit = series.stepMilliseconds && series.stepMilliseconds > 0 ? series.stepMilliseconds : span / (limit - 2)
  // floor(max/width) − floor(min/width) + 1 ≤ span/width + 2 ≤ limit bins.
  const width = unit * Math.max(1, Math.ceil(span / ((limit - 2) * unit)))
  const bins = new Map<number, {count: number; value: number; squares: number}>()
  times.forEach((time, index) => {
    const bin = Math.floor(time / width)
    const entry = bins.get(bin) ?? {count: 0, value: 0, squares: 0}
    entry.count++
    entry.value += values[index]
    if (deviation) entry.squares += deviations[index] ** 2
    bins.set(bin, entry)
  })
  const ordered = [...bins].sort((a, b) => a[0] - b[0])
  return {...base, step: width,
    t: Float64Array.from(ordered, ([bin]) => (bin + .5) * width),
    v: Float32Array.from(ordered, ([, entry]) => entry.value / entry.count),
    ...(deviation ? {sd: Float32Array.from(ordered, ([, entry]) => Math.sqrt(entry.squares / entry.count))} : {})}
}

export function unpackSeries(series: PackedSeries): AggregateSeries {
  const stamp = (index: number) => new Date(series.t[index]).toISOString()
  return {artifactID: series.label, label: series.label, unit: series.unit, stepMilliseconds: series.step, runs: series.runs,
    points: Array.from(series.v, (value, index) => ({timestamp: stamp(index), value})),
    ...(series.sd ? {deviation: Array.from(series.sd, (value, index) => ({timestamp: stamp(index), value}))} : {})}
}

// Buffers of every packed curve, for postMessage's transfer list.
export function comparisonTransferables(result: PreparedComparison): ArrayBuffer[] {
  const buffers: ArrayBuffer[] = []
  for (const entry of result.overview?.series ?? []) for (const curves of Object.values(entry.curves ?? {})) for (const curve of Object.values(curves)) {
    buffers.push(curve.t.buffer as ArrayBuffer, curve.v.buffer as ArrayBuffer)
    if (curve.sd) buffers.push(curve.sd.buffer as ArrayBuffer)
  }
  return buffers
}
