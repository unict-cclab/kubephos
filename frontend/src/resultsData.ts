import {applyHolm, compareGroups, compareSamples, naturalCompare, summarize, summarizeTimeSeries, type ComparisonMode, type GroupComparison, type ScientificSummary} from './statistics'
import type {Artifact, Experiment, MetricPoint, MetricSeries, TimeSeriesDataset} from './types'

export {summarize, summarizeTimeSeries} from './statistics'
export type {ScientificSummary} from './statistics'

export interface LoadedDataset {
  artifact: Artifact
  dataset: TimeSeriesDataset
  label: string
  color?: string
  // Enabled only during immutable worker preparation; never shared with stored datasets.
  aggregateCache?: Map<string, AggregateSeries | undefined>
}

export interface AggregateSeries {
  artifactID: string
  label: string
  unit: string
  points: MetricPoint[]
  deviation?: MetricPoint[]
  stepMilliseconds?: number
  sloMilliseconds?: number
  color?: string
  // Number of runs averaged into each point (repetition means); absent for a single run.
  runs?: number
}

export function responseTimeSLO(experiment: Experiment | undefined, artifactID: string, sourceConfiguration?: Record<string, unknown>): number | undefined {
  const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === artifactID))
  const configuration = sourceConfiguration ?? variant?.configuration
  const components = Array.isArray(configuration?.components) ? configuration.components : []
  for (const value of components) {
    if (!value || typeof value !== 'object') continue
    const component = value as Record<string, unknown>
    if (!String(component.capability ?? '').startsWith('metrics.')) continue
    const settings = component.configuration
    const objective = settings && typeof settings === 'object' ? Number((settings as Record<string, unknown>).sloMilliseconds) : 0
    if (Number.isFinite(objective) && objective > 0) return objective
  }
  return undefined
}

export function addDerivedMetrics(dataset: TimeSeriesDataset, configuredSLO?: number) {
  const datasetSLO = Number(dataset.spec.sloMilliseconds)
  const sloMilliseconds = Number.isFinite(datasetSLO) && datasetSLO > 0
    ? datasetSLO
    : Number.isFinite(configuredSLO) && Number(configuredSLO) > 0 ? Number(configuredSLO) : undefined
  if (sloMilliseconds === undefined) return
  dataset.spec.sloMilliseconds = sloMilliseconds
  if (dataset.spec.series.some(series => series.metric === 'load.slo_violation_percentage')) return
  const response = dataset.spec.series.find(series => series.metric === 'load.p95_response_time')
  if (!response) return
  dataset.spec.series.push({metric: 'load.slo_violation_percentage', unit: 'percent', aggregation: 'mean', labels: {objective: `${sloMilliseconds}ms`}, points: response.points.map(point => ({timestamp: point.timestamp, value: point.value > sloMilliseconds ? 100 : 0}))})
}

// Resource metrics were queried namespace-wide, so stored datasets can contain the Locust load generator.
export const applicationResourceMetrics = ['cpu.cores', 'memory.working_set', 'pod.restarts', 'deployment.ready_replicas']

export function isLoadGeneratorSeries(series: MetricSeries): boolean {
  return series.labels['app.kubernetes.io/name'] === 'kubephos-load' || ['deployment', 'pod', 'destination_workload', 'service'].some(label => series.labels[label]?.startsWith('kubephos-load-'))
}

// Measurement window of the workload: [workloadStartedAt || start, end], both inclusive.
export function workloadWindow(dataset: TimeSeriesDataset): {start: number; end: number} {
  const start = Date.parse(dataset.spec.workloadStartedAt || dataset.spec.start)
  const end = Date.parse(dataset.spec.end)
  return {start: Number.isFinite(start) ? start : -Infinity, end: Number.isFinite(end) ? end : Infinity}
}

// The only source of samples for tables, CSV and plots, so they always describe the same data.
// Control metrics keep the whole collection window: pods are mostly scheduled during deployment.
export function analysisSeries(dataset: TimeSeriesDataset, metric: string): MetricSeries[] {
  const matching = dataset.spec.series.filter(series => series.metric === metric && !(applicationResourceMetrics.includes(metric) && isLoadGeneratorSeries(series)))
  if (isControlMetric(metric)) return matching
  const {start, end} = workloadWindow(dataset)
  const windowed = matching.map(series => ({...series, points: series.points.filter(point => {
    const timestamp = Date.parse(point.timestamp)
    return Number.isFinite(point.value) && timestamp >= start && timestamp <= end
  })}))
  if (metric === 'load.configured_rps') return windowed.map(series => withoutTrailingProfileZero(series, end))
  if (!cumulativeCounterMetrics.includes(metric)) return windowed
  // Every series of a metric comes from one range query, so they share its time grid.
  // A loop, not Math.min(...spread): long runs exceed the engine's argument limit.
  let gridStart = Infinity
  for (const series of windowed) for (const point of series.points) gridStart = Math.min(gridStart, Date.parse(point.timestamp))
  return windowed.map(series => counterIncrease(series, gridStart))
}

// Legacy collectors evaluated the load profile at t = duration, where it is defined as 0, so
// the configured rate drops to 0 at the very end of the window although nothing changed.
// Only that artefact is removed: a final 0 sample exactly at the window end that follows a
// positive sample. A profile that genuinely ends at 0 earlier keeps its points.
function withoutTrailingProfileZero(series: MetricSeries, end: number): MetricSeries {
  const points = series.points
  const count = points.length
  if (count < 2) return series
  const last = points[count - 1]
  if (last.value !== 0 || Date.parse(last.timestamp) !== end || !(points[count - 2].value > 0)) return series
  return {...series, points: points.slice(0, -1)}
}

// Counters collected as lifetime totals (restarts since pod creation). Analysis uses their
// increase during the workload window, so earlier restarts are not attributed to the run.
export const cumulativeCounterMetrics = ['pod.restarts']

function counterIncrease(series: MetricSeries, gridStart: number): MetricSeries {
  const points = [...series.points].sort((left, right) => Date.parse(left.timestamp) - Date.parse(right.timestamp))
  if (!points.length) return series
  // A pod sampled at the first instant of the window keeps its earlier restarts as
  // baseline; a pod first sampled later was created during the run and starts from zero.
  let previous = Date.parse(points[0].timestamp) <= gridStart ? points[0].value : 0
  let increase = 0
  return {...series, points: points.map(point => {
    // A lower value means the counter was reset; the new value counts entirely.
    increase += point.value >= previous ? point.value - previous : point.value
    previous = point.value
    return {timestamp: point.timestamp, value: increase}
  })}
}

// Total increase of a cumulative counter during the run, summed over its series. Series
// that disappear (deleted pods) keep their last value.
export function counterRunTotal(dataset: TimeSeriesDataset, metric: string): number | undefined {
  const series = analysisSeries(dataset, metric).filter(item => item.points.length)
  return series.length ? series.reduce((sum, item) => sum + item.points[item.points.length - 1].value, 0) : undefined
}

export interface WorkloadRef {
  name: string
  // Deployment, StatefulSet, DaemonSet, Job, ...; absent when unknown.
  kind?: string
  // True when derived from the pod name instead of collector labels.
  inferred?: boolean
}

// Controller naming patterns of pod names (suffix after the workload name):
// Deployment `-<pod-template-hash>-<5>`, StatefulSet `-<ordinal>`, DaemonSet/Job `-<5>`.
// Matching against a KNOWN workload accepts any [a-z0-9] hash. Stripping a suffix from an
// UNKNOWN pod requires the hash alphabet Kubernetes really uses (k8s.io/apimachinery
// util/rand SafeEncodeString: no vowels, no 0/1/3), so DaemonSet `node-exporter-x7k2p`
// is not mistaken for Deployment `node` with hash `exporter`.
const kubernetesHash = '[bcdfghjklmnpqrstvwxz2456789]{5,10}'
const podSuffixes: Array<[kind: string, suffix: RegExp, strip: RegExp]> = [
  ['Deployment', /^-[a-z0-9]{5,10}-[a-z0-9]{5}$/, new RegExp(`^(.+)-${kubernetesHash}-[a-z0-9]{5}$`)],
  ['StatefulSet', /^-\d+$/, /^(.+)-\d+$/],
  ['DaemonSet', /^-[a-z0-9]{5}$/, /^(.+)-[a-z0-9]{5}$/],
  ['Job', /^-[a-z0-9]{5}$/, /^(.+)-[a-z0-9]{5}$/]
]

// Workloads known in one run: the collector's workload labels (any metric) and, for older
// datasets, the Deployments/StatefulSets reporting ready replicas. Longest name first, so
// `cart-db` wins over `cart` for pod `cart-db-0`.
export function knownWorkloads(dataset: TimeSeriesDataset): WorkloadRef[] {
  const known = new Map<string, string | undefined>()
  for (const series of dataset.spec.series) {
    if (isLoadGeneratorSeries(series)) continue
    if (series.labels.workload) known.set(series.labels.workload, series.labels.workload_kind || known.get(series.labels.workload))
    else if (series.metric === 'deployment.ready_replicas') {
      if (series.labels.deployment) known.set(series.labels.deployment, 'Deployment')
      else if (series.labels.statefulset) known.set(series.labels.statefulset, 'StatefulSet')
    }
  }
  return [...known].map(([name, kind]) => kind ? {name, kind} : {name}).sort((left, right) => right.name.length - left.name.length || (left.name < right.name ? -1 : left.name > right.name ? 1 : 0))
}

// Owning workload of a pod from its name: the longest known workload whose remaining
// suffix fits its controller's naming pattern; otherwise strip a controller suffix
// (StatefulSets and DaemonSets of older datasets are not in deployment.ready_replicas).
// Undefined when the name fits no controller pattern.
export function workloadFromPodName(pod: string, known: readonly WorkloadRef[]): WorkloadRef | undefined {
  for (const workload of known) {
    if (!pod.startsWith(`${workload.name}-`)) continue
    const suffix = pod.slice(workload.name.length)
    const ofKind = podSuffixes.filter(([kind]) => kind === workload.kind)
    // Unknown or unlabeled kinds (e.g. a bare ReplicaSet) accept any controller pattern.
    const patterns = ofKind.length ? ofKind : podSuffixes
    if (patterns.some(([, pattern]) => pattern.test(suffix))) return {...workload, inferred: true}
  }
  for (const [kind, , strip] of podSuffixes) {
    const match = strip.exec(pod)
    // A 5-character suffix cannot tell DaemonSet from Job pods.
    if (match) return kind === 'DaemonSet' ? {name: match[1], inferred: true} : {name: match[1], kind, inferred: true}
  }
  return undefined
}

// Collector labels first (`workload`/`workload_kind`), then explicit controller labels,
// then the pod name. Undefined when the series identifies no workload at all.
export function resolveWorkload(series: MetricSeries, known: readonly WorkloadRef[]): WorkloadRef | undefined {
  const labels = series.labels
  if (labels.workload) return labels.workload_kind ? {name: labels.workload, kind: labels.workload_kind} : {name: labels.workload}
  if (labels.deployment) return {name: labels.deployment, kind: 'Deployment'}
  if (labels.statefulset) return {name: labels.statefulset, kind: 'StatefulSet'}
  const named = labels.destination_workload || labels.service || labels.operation
  if (named) return {name: named}
  return labels.pod ? workloadFromPodName(labels.pod, known) : undefined
}

const isApplicationLatency = (metric: string) => metric.startsWith('application.') && metric.endsWith('response_time')

export function serviceMetricStatistics(datasets: LoadedDataset[], metric: string, experiment?: Experiment) {
  const groups = new Map<string, {service: string; kind?: string; group: string; groupID: string; color?: string; unit: string; runs: ScientificSummary[]}>()
  let unmatched = 0
  // Latencies are averaged over a service's series; rates and resources are summed.
  const averaged = metric.endsWith('response_time')
  for (const dataset of datasets) {
    const known = knownWorkloads(dataset.dataset)
    const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === dataset.artifact.id))
    const group = variant?.alias || variant?.name || plotLabel(dataset.label)
    const groupID = variant?.id || dataset.artifact.id
    const services = new Map<string, {unit: string; kind?: string; points: Map<number, number>; counts: Map<number, number>}>()
    for (const series of analysisSeries(dataset.dataset, metric)) {
      if (isLoadGeneratorSeries(series)) continue
      const workload = resolveWorkload(series, known)
      if (!workload) {unmatched++; continue}
      const service = workload.name
      // Mesh entry points (node proxy, gateways) are not application services.
      if (isApplicationLatency(metric) && (service === 'node-proxy' || service.startsWith('gateway-'))) continue
      const values = services.get(service) ?? {unit: series.unit, kind: workload.kind, points: new Map<number, number>(), counts: new Map<number, number>()}
      for (const point of series.points) if (Number.isFinite(point.value)) {
        const timestamp = Date.parse(point.timestamp)
        values.points.set(timestamp, (values.points.get(timestamp) ?? 0) + point.value)
        values.counts.set(timestamp, (values.counts.get(timestamp) ?? 0) + 1)
      }
      services.set(service, values)
    }
    for (const [service, values] of services) {
      const key = JSON.stringify([service, groupID])
      const row = groups.get(key) ?? {service, kind: values.kind, group, groupID, color: variant?.color ?? dataset.color, unit: values.unit, runs: []}
      // One run's series is autocorrelated: its CI uses the effective sample size.
      if (values.points.size) row.runs.push(summarizeTimeSeries([...values.points].sort((left, right) => left[0] - right[0]).map(([timestamp, value]) => averaged ? value / values.counts.get(timestamp)! : value)))
      groups.set(key, row)
    }
  }
  return {unmatched, rows: [...groups.values()].filter(row => row.runs.length).sort((a, b) => naturalCompare(a.service, b.service) || naturalCompare(a.group, b.group)).map(row => ({...row, summary: row.runs.length > 1 ? summarize(row.runs.map(run => run.mean)) : row.runs[0], acrossRuns: row.runs.length > 1}))}
}

export function aggregateMetric(datasets: LoadedDataset[], metric: string): AggregateSeries[] {
  return datasets.map(item => {
    if (item.aggregateCache?.has(metric)) return item.aggregateCache.get(metric)
    const matching = analysisSeries(item.dataset, metric)
    const values = new Map<number, number>()
    const counts = new Map<number, number>()
    for (const series of matching) {
      for (const point of series.points) {
        const timestamp = new Date(point.timestamp).getTime()
        values.set(timestamp, (values.get(timestamp) ?? 0) + point.value)
        counts.set(timestamp, (counts.get(timestamp) ?? 0) + 1)
      }
    }
    const aggregation = matching[0]?.aggregation ?? 'sum'
    const stepMilliseconds = Number(item.dataset.spec.stepSeconds) * 1000
    const startedAt = new Date(!isControlMetric(metric) && item.dataset.spec.workloadStartedAt ? item.dataset.spec.workloadStartedAt : item.dataset.spec.start).getTime()
    const result: AggregateSeries = {
      artifactID: item.artifact.id,
      label: item.label,
      unit: matching[0]?.unit ?? '',
      sloMilliseconds: item.dataset.spec.sloMilliseconds,
      color: item.color,
      // Sampling step, so plots break lines at missing windows instead of bridging them.
      ...(Number.isFinite(stepMilliseconds) && stepMilliseconds > 0 && metric !== 'scheduler.pod_scheduling_latency' ? {stepMilliseconds} : {}),
      points: (metric === 'scheduler.pod_scheduling_latency' ? matching.flatMap(series => series.points.map(point => [Number.isFinite(Date.parse(series.labels.scheduled_at)) ? Date.parse(series.labels.scheduled_at) : Date.parse(point.timestamp), point.value] as [number, number])) : [...values.entries()]).sort((left, right) => left[0] - right[0]).map(([timestamp, value]) => ({timestamp: new Date(Math.max(0, timestamp - startedAt)).toISOString(), value: aggregation === 'mean' && metric !== 'scheduler.pod_scheduling_latency' ? value / (counts.get(timestamp) ?? 1) : value}))
    }
    item.aggregateCache?.set(metric, result.points.length ? result : undefined)
    return result
  }).filter((item): item is AggregateSeries => Boolean(item?.points.length))
}

// Keep raw per-run statistics separate from the presentation of repeated trials.
export function aggregatePlotMetric(datasets: LoadedDataset[], metric: string, experiment?: Experiment): AggregateSeries[] {
  if (!experiment) return aggregateMetric(datasets, metric)
  const groups = new Map<string, {label: string; color?: string; datasets: LoadedDataset[]}>()
  datasets.forEach(dataset => {
    const index = experiment.variants.findIndex(variant => variant.trials.some(trial => trial.resultArtifactId === dataset.artifact.id))
    const variant = experiment.variants[index]
    const key = variant ? `variant-${index}` : dataset.artifact.id
    const group = groups.get(key) ?? {label: variant?.alias || variant?.name || plotLabel(dataset.label), color: variant?.color ?? dataset.color, datasets: []}
    group.datasets.push(dataset)
    groups.set(key, group)
  })
  return [...groups.entries()].flatMap(([key, group]) => {
    // aggregateMetric already restricts each run to its workload window (see analysisSeries).
    const runs = group.datasets.map(dataset => aggregateMetric([dataset], metric)[0])
    if (runs.length === 1) return runs[0] ? [{...runs[0], label: group.label, color: group.color}] : []
    // A missing run/metric is not a zero and cannot contribute to an N-run mean.
    if (runs.some(run => !run)) return []
    const intervals = group.datasets.map(dataset => dataset.dataset.spec.stepSeconds * 1000).filter(value => Number.isFinite(value) && value > 0)
    let inferred = Infinity
    for (const run of runs) for (let index = 1; index < run.points.length; index++) {
      const difference = Date.parse(run.points[index].timestamp) - Date.parse(run.points[index - 1].timestamp)
      if (difference > 0) inferred = Math.min(inferred, difference)
    }
    const step = intervals.length ? Math.max(...intervals) : Number.isFinite(inferred) ? inferred : 1000
    const windows = runs.map(run => {
      const bins = new Map<number, number[]>()
      run.points.forEach(point => {
        if (!Number.isFinite(point.value)) return
        const bin = Math.round(Date.parse(point.timestamp) / step)
        bins.set(bin, [...(bins.get(bin) ?? []), point.value])
      })
      return new Map([...bins].map(([bin, values]) => [bin, summarize(values).mean]))
    })
    // Only windows sampled by every included run are comparable; never interpolate gaps.
    const common = [...windows[0].keys()].filter(bin => windows.every(window => window.has(bin))).sort((a, b) => a - b)
    const summaries = common.map(bin => summarize(windows.map(window => window.get(bin)!)))
    return common.length ? [{...runs[0], artifactID: key, label: group.label, color: group.color, stepMilliseconds: step,
      points: common.map((bin, index) => ({timestamp: new Date(bin * step).toISOString(), value: summaries[index].mean})),
      deviation: common.map((bin, index) => ({timestamp: new Date(bin * step).toISOString(), value: summaries[index].standardDeviation})), runs: runs.length}] : []
  })
}

export function isControlMetric(metric: string): boolean {
  return ['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration', 'descheduler.cycle_duration', 'descheduler.evictions'].includes(metric)
}

export const serviceStatisticsMetrics = ['deployment.ready_replicas', 'cpu.cores', 'memory.working_set', 'application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time']

export function serviceStatisticsRows(datasets: LoadedDataset[], experiment?: Experiment) {
  return serviceStatisticsMetrics.flatMap(metric => serviceMetricStatistics(datasets, metric, experiment).rows.map(row => ({...row, metric})))
}

export const statisticsCSVHeader = ['scope', 'strategy', 'dataset', 'metric', 'service', 'unit', 'status', 'count', 'mean', 'total', 'std', 'min', 'median', 'max', 'missing_runs', 'source', 'vantage', 'ci95_low', 'ci95_high', 'n_effective', 'lag1_autocorrelation']

// Columns 0–14 keep their historical meaning; provenance and CI columns are appended.
// Missing measurements are exported as status "unavailable" with empty values, never 0.
// Run rows describe one autocorrelated time series: their CI uses n_effective.
export function statisticsCSV(datasets: LoadedDataset[], metrics: string[], experiment?: Experiment): string {
  const rows: Array<Array<string | number>> = [statisticsCSVHeader]
  const summaryValues = (summary: ScientificSummary) => [summary.count, summary.mean, '', summary.standardDeviation, summary.min, summary.median, summary.max]
  const interval = (summary?: ScientificSummary) => summary && summary.count > 1 ? [summary.confidence95Low, summary.confidence95High, summary.nEffective ?? '', summary.lag1Autocorrelation ?? ''] : ['', '', summary?.nEffective ?? '', summary?.lag1Autocorrelation ?? '']
  const provenance = (dataset: LoadedDataset | undefined, metric: string) => {
    const value = dataset ? metricProvenance(dataset.dataset, metric) : legacyMetricProvenance(metric)
    return [value.source, value.vantage]
  }
  const firstWith = (metric: string, items: LoadedDataset[]) => items.find(item => item.dataset.spec.series.some(series => series.metric === metric) || item.dataset.spec.metrics?.some(entry => entry.metric === metric)) ?? items[0]
  const unitOf = new Map<string, string>()
  for (const item of datasets) for (const series of item.dataset.spec.series) if (!unitOf.has(series.metric)) unitOf.set(series.metric, series.unit)
  for (const dataset of datasets) {
    for (const metric of metrics) {
      const series = aggregateMetric([dataset], metric)[0]
      const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
      const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
      const source = provenance(dataset, metric)
      if (podSummary) rows.push(['run', '', dataset.label, metric, '', 'milliseconds', 'available', ...summaryValues(podSummary), '', ...source, ...interval(podSummary)])
      else if (isControlMetric(metric) && control) rows.push(['run', '', dataset.label, metric, '', metric === 'descheduler.evictions' ? 'count' : 'milliseconds', control.status, control.count ?? '', control.mean ?? '', control.total ?? '', '', '', '', '', '', ...source, '', '', '', ''])
      else if (series && cumulativeCounterMetrics.includes(metric)) rows.push(['run', '', dataset.label, metric, '', series.unit, 'available', series.points.length, '', counterRunTotal(dataset.dataset, metric) ?? '', '', '', '', '', '', ...source, '', '', '', ''])
      else if (series) {
        const summary = summarizeTimeSeries(series.points.map(point => point.value))
        rows.push(['run', '', dataset.label, metric, '', series.unit, 'available', ...summaryValues(summary), '', ...source, ...interval(summary)])
      } else rows.push(['run', '', dataset.label, metric, '', unitOf.get(metric) ?? (isControlMetric(metric) ? metric === 'descheduler.evictions' ? 'count' : 'milliseconds' : ''), 'unavailable', '', '', '', '', '', '', '', '', ...source, '', '', '', ''])
    }
    for (const row of serviceStatisticsRows([dataset])) rows.push(['service_run', row.group, dataset.label, row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), '', ...provenance(dataset, row.metric), ...interval(row.summary)])
  }
  if (experiment) {
    for (const row of variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2)) rows.push(['across_runs', row.variant, '', row.metric, '', row.unit, 'available', ...summaryValues(row.summary), row.missing, ...provenance(firstWith(row.metric, datasets), row.metric), ...interval(row.summary)])
    for (const row of serviceStatisticsRows(datasets, experiment).filter(row => row.acrossRuns)) rows.push(['service_across_runs', row.group, '', row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), '', ...provenance(firstWith(row.metric, datasets), row.metric), ...interval(row.summary)])
  }
  return toCSV(rows)
}

// RFC 4180 quoting; numbers use String(), which is locale independent.
function toCSV(rows: Array<Array<string | number | boolean | undefined>>): string {
  return rows.map(row => row.map(value => {const text = value === undefined ? '' : String(value); return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text}).join(',')).join('\n')
}

export function podSchedulingStatistics(dataset: LoadedDataset): ScientificSummary | undefined {
  const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === 'scheduler.pod_scheduling_latency')
  if (control?.status !== 'available') return undefined
  const values = new Map<string, number>()
  for (const series of dataset.dataset.spec.series.filter(item => item.metric === control.metric)) {
    const uid = series.labels.pod_uid
    if (!uid) return undefined
    for (const point of series.points) {
      if (!Number.isFinite(point.value) || point.value < 0 || values.has(uid) && values.get(uid) !== point.value) return undefined
      values.set(uid, point.value)
    }
  }
  if (!values.size || values.size !== control.count) return undefined
  const summary = summarize([...values.values()])
  if (control.mean === undefined || Math.abs(summary.mean - control.mean) > Math.max(1, control.mean) * 1e-6) return undefined
  return summary
}

// One value per run. The failure percentage uses the exact Locust count (failures/requests
// over the measured window) when the summary exists, not the mean of window percentages.
export function metricRunValue(dataset: LoadedDataset, metric: string): number | undefined {
  const locust = dataset.dataset.spec.locustSummary
  if (metric === 'load.failure_percentage' && locust && locust.requests > 0 && Number.isFinite(locust.failures)) return 100 * locust.failures / locust.requests
  if (isControlMetric(metric)) {
    const summary = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
    return summary?.total ?? summary?.mean
  }
  if (cumulativeCounterMetrics.includes(metric)) return counterRunTotal(dataset.dataset, metric)
  const series = aggregateMetric([dataset], metric)[0]
  return series?.points.length ? series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length : undefined
}

// Run-level values of one variant: one number per repetition (independent samples).
function variantRunValues(variant: Experiment['variants'][number], datasets: Map<string, LoadedDataset>, metric: string): {values: number[]; unit: string} {
  const values: number[] = []
  let unit = ''
  for (const trial of variant.trials) {
    // Pilot (warm-up) repetitions are never part of statistics.
    const dataset = trial.pilot ? undefined : datasets.get(trial.resultArtifactId)
    if (!dataset) continue
    const value = metricRunValue(dataset, metric)
    if (value === undefined || !Number.isFinite(value)) continue
    unit ||= aggregateMetric([dataset], metric)[0]?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
    values.push(value)
  }
  return {values, unit}
}

export function variantStatistics(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return experiment.variants.flatMap(variant => metrics.map(metric => {
    const {values, unit} = variantRunValues(variant, byArtifact, metric)
    return values.length ? {variant: variant.alias || variant.name, metric, unit, missing: variant.trials.length - values.length, summary: summarize(values)} : null
  }).filter((item): item is {variant: string; metric: string; unit: string; missing: number; summary: ScientificSummary} => Boolean(item)))
}

export interface VariantComparison extends GroupComparison {
  metric: string
  unit: string
  // Number of p-values in the Holm family this row was adjusted in.
  familySize: number
}

export interface VariantComparisonOptions {
  // Variant id of the reference group; defaults to the first variant.
  baseline?: string
  mode?: ComparisonMode
}

// Between-variant inference on run-level values (one value per repetition; see
// metricRunValue). Holm family: every row returned by ONE call, i.e. all non-baseline
// variants (or all pairs) × all metrics passed in. Pairs where either variant has no
// value for a metric are omitted.
export function variantComparisons(experiment: Experiment, datasets: LoadedDataset[], metrics: string[], options: VariantComparisonOptions = {}): VariantComparison[] {
  return compareRunValues(runLevelValues(experiment, datasets, metrics), options)
}

export interface RunLevelGroup {id: string; label: string; color?: string; values: number[]; artifactIds: string[]}
export interface RunLevelMetric {metric: string; unit: string; groups: RunLevelGroup[]}

// Run-level values (one per repetition) of every variant for each metric, in variant order.
// Serializable, so a worker can return it and the page can re-run compareRunValues.
export function runLevelValues(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]): RunLevelMetric[] {
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return metrics.map(metric => {
    let unit = ''
    const groups = experiment.variants.map(variant => {
      const values: number[] = []
      const artifactIds: string[] = []
      for (const trial of variant.trials) {
        const dataset = trial.pilot ? undefined : byArtifact.get(trial.resultArtifactId)
        if (!dataset) continue
        const value = metricRunValue(dataset, metric)
        if (value === undefined || !Number.isFinite(value)) continue
        unit ||= aggregateMetric([dataset], metric)[0]?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
        values.push(value)
        artifactIds.push(dataset.artifact.id)
      }
      return {id: variant.id, label: variant.alias || variant.name, color: variant.color, values, artifactIds}
    })
    return {metric, unit, groups}
  })
}

// Same inference as variantComparisons, from precomputed run-level values.
export function compareRunValues(table: RunLevelMetric[], options: VariantComparisonOptions = {}): VariantComparison[] {
  const rows = table.flatMap(({metric, unit, groups}) => compareGroups(groups.map(group => ({id: group.id, label: group.label, values: group.values})), options)
    .filter(row => row.baselineCount > 0 && row.groupCount > 0)
    .map(row => ({...row, metric, unit, familySize: 0})))
  const familySize = rows.filter(row => row.pValue !== undefined).length
  return applyHolm(rows).map(row => ({...row, familySize}))
}

export const comparisonCSVHeader = ['metric', 'unit', 'baseline', 'group', 'n_baseline', 'n_group', 'mean_baseline', 'mean_group', 'sd_baseline', 'sd_group', 'difference', 'relative_difference_percent', 'difference_ci95_low', 'difference_ci95_high', 'welch_t', 'welch_df', 'p_value', 'p_value_holm', 'holm_family_size', 'cohens_d', 'hedges_g', 'hedges_g_ci95_low', 'hedges_g_ci95_high', 'mann_whitney_u', 'mann_whitney_p', 'mann_whitney_method', 'insufficient', 'zero_variance']

export function comparisonCSV(rows: VariantComparison[]): string {
  return toCSV([comparisonCSVHeader, ...rows.map(row => [row.metric, row.unit, row.baseline, row.group, row.baselineCount, row.groupCount, row.baselineMean, row.groupMean, row.baselineStandardDeviation, row.groupStandardDeviation, row.difference, row.relativeDifferencePercent, row.differenceLow, row.differenceHigh, row.welchT, row.welchDegreesOfFreedom, row.pValue, row.adjustedPValue, row.familySize, row.cohensD, row.hedgesG, row.hedgesGLow, row.hedgesGHigh, row.mannWhitneyU, row.mannWhitneyPValue, row.mannWhitneyMethod, row.insufficient, row.zeroVariance])])
}

export interface VariantEffectSize {
  metric: string
  left: string
  right: string
  // Cohen's d (right − left) / pooled SD; 0 when the pooled SD is 0 (historical behavior).
  value: number
  leftCount: number
  rightCount: number
  hedgesG?: number
  hedgesGLow?: number
  hedgesGHigh?: number
  pValue?: number
  adjustedPValue?: number
}

// Legacy two-variant table, now derived from variantComparisons.
export function variantEffectSizes(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]): VariantEffectSize[] {
  if (experiment.variants.length !== 2) return []
  return variantComparisons(experiment, datasets, metrics)
    .filter(row => row.baselineCount >= 2 && row.groupCount >= 2)
    .map(row => ({metric: row.metric, left: row.baseline, right: row.group, value: row.cohensD ?? 0, leftCount: row.baselineCount, rightCount: row.groupCount, hedgesG: row.hedgesG, hedgesGLow: row.hedgesGLow, hedgesGHigh: row.hedgesGHigh, pValue: row.pValue, adjustedPValue: row.adjustedPValue}))
}

export function cohenD(left: number[], right: number[]): number {
  return compareSamples(left, right).cohensD ?? 0
}

export interface MetricProvenance {
  source: string
  vantage: string
  description: string
  query?: string
  rateWindowSeconds?: number
  collector?: string
  // True when taken from the static catalog for datasets without spec.metrics.
  legacy: boolean
}

type ProvenanceDefault = {source: string; vantage: string; description: string; windowed?: boolean}
const client = (description: string, windowed = true): ProvenanceDefault => ({source: 'locust', vantage: 'client', description, windowed})
const server = (description: string): ProvenanceDefault => ({source: 'istio', vantage: 'server', description, windowed: true})
const latency = (statistic: string, vantage: 'client' | 'server') => vantage === 'client'
  ? client(`${statistic} of end-to-end response time measured by the Locust load generator (client side)`)
  : server(`${statistic} of request duration reported by the Istio data plane at the destination workload (server side)`)

// Static catalog for datasets collected before spec.metrics existed. All rate() and
// histogram_quantile() queries of those collectors used a 60 s window.
const legacyProvenanceCatalog: Record<string, ProvenanceDefault> = {
  'load.successful_rps': client('Rate of successful requests observed by the Locust load generator'),
  'load.failed_rps': client('Rate of failed requests observed by the Locust load generator'),
  'load.failure_percentage': client('Failed requests as a percentage of all requests sent by the load generator; unavailable when no request was sent'),
  'load.p50_response_time': latency('Median', 'client'),
  'load.p90_response_time': latency('90th percentile', 'client'),
  'load.p95_response_time': latency('95th percentile', 'client'),
  'load.p99_response_time': latency('99th percentile', 'client'),
  'load.mean_response_time': latency('Mean', 'client'),
  'load.offered_rps': client('Request rate the load generator tried to send (open-loop schedule target)', false),
  'load.achieved_rps': client('Total request rate actually sent by the load generator'),
  'load.schedule_lag': client('Mean delay between scheduled and actual request issue time (coordinated-omission evidence)'),
  'load.users': client('Concurrent Locust users', false),
  'load.configured_rps': {source: 'kubephos', vantage: 'configuration', description: 'Request rate configured in the load profile'},
  'load.slo_violation_percentage': {source: 'kubephos', vantage: 'client', description: 'Share of sampling windows whose client-side p95 response time exceeded the SLO', windowed: true},
  'application.request_rps': server('Request rate reported by the Istio data plane at the destination workload (server side)'),
  'application.p50_response_time': latency('Median', 'server'),
  'application.p90_response_time': latency('90th percentile', 'server'),
  'application.p95_response_time': latency('95th percentile', 'server'),
  'application.p99_response_time': latency('99th percentile', 'server'),
  'application.mean_response_time': latency('Mean', 'server'),
  'cpu.cores': {source: 'cadvisor', vantage: 'resource', description: 'CPU usage of application containers (cAdvisor)', windowed: true},
  'memory.working_set': {source: 'cadvisor', vantage: 'resource', description: 'Working-set memory of application containers (cAdvisor)'},
  'pod.restarts': {source: 'kube-state-metrics', vantage: 'resource', description: 'Container restarts; analysed as the increase during the workload window'},
  'deployment.ready_replicas': {source: 'kube-state-metrics', vantage: 'resource', description: 'Ready replicas of each application Deployment or StatefulSet'},
  'network.rtt': {source: 'mentat', vantage: 'network', description: 'Round-trip time between nodes measured by active probes', windowed: true},
  'network.packet_loss': {source: 'mentat', vantage: 'network', description: 'Packet loss between nodes measured by active probes', windowed: true},
  'network.bandwidth': {source: 'mentat', vantage: 'network', description: 'Available bandwidth between nodes measured by active probes'},
  'scheduler.pod_scheduling_latency': {source: 'kube-scheduler', vantage: 'control-plane', description: 'Time from pod creation to its PodScheduled condition'},
  'scheduler.cycle_duration': {source: 'kube-scheduler', vantage: 'control-plane', description: 'Duration of kube-scheduler scheduling algorithm runs'},
  'descheduler.cycle_duration': {source: 'descheduler', vantage: 'control-plane', description: 'Duration of descheduler loops'},
  'descheduler.evictions': {source: 'descheduler', vantage: 'control-plane', description: 'Pods evicted by the descheduler'},
  'redis.operation_rps': client('Rate of Redis operations sent by the load generator, per operation'),
  'redis.operation_p95_response_time': client('95th percentile of Redis operation latency measured by the load generator (client side)')
}

export const legacyProvenanceMetrics = Object.keys(legacyProvenanceCatalog)

export function legacyMetricProvenance(metric: string, rateWindowSeconds = 60): MetricProvenance {
  const entry = legacyProvenanceCatalog[metric]
  if (entry) return {source: entry.source, vantage: entry.vantage, description: entry.description, rateWindowSeconds: entry.windowed ? rateWindowSeconds : undefined, legacy: true}
  const prefix = metric.split('.')[0]
  const byPrefix: Record<string, Pick<ProvenanceDefault, 'source' | 'vantage'>> = {load: {source: 'locust', vantage: 'client'}, redis: {source: 'locust', vantage: 'client'}, application: {source: 'istio', vantage: 'server'}, network: {source: 'mentat', vantage: 'network'}, scheduler: {source: 'kube-scheduler', vantage: 'control-plane'}, descheduler: {source: 'descheduler', vantage: 'control-plane'}}
  const guess = byPrefix[prefix]
  return guess ? {...guess, description: '', legacy: true} : {source: 'unknown', vantage: 'unknown', description: '', legacy: true}
}

// Where a metric was measured: the dataset's own catalog (spec.metrics) when present,
// otherwise the static catalog for legacy datasets. Multi-cluster Cilium datasets measure
// the network between clusters (origin_cluster/destination_cluster labels).
export function metricProvenance(dataset: TimeSeriesDataset, metric: string): MetricProvenance {
  const window = Number.isFinite(dataset.spec.rateWindowSeconds) && Number(dataset.spec.rateWindowSeconds) > 0 ? Number(dataset.spec.rateWindowSeconds) : 60
  const fallback = legacyMetricProvenance(metric, window)
  const entry = dataset.spec.metrics?.find(item => item.metric === metric)
  if (entry) {
    const result: MetricProvenance = {source: entry.source, vantage: entry.vantage, description: entry.description || fallback.description, legacy: false}
    if (entry.query) result.query = entry.query
    const rateWindowSeconds = entry.rateWindowSeconds ?? fallback.rateWindowSeconds
    if (rateWindowSeconds !== undefined) result.rateWindowSeconds = rateWindowSeconds
    if (entry.collector) result.collector = entry.collector
    return result
  }
  if (metric.startsWith('network.') && dataset.spec.series.some(series => series.metric === metric && (series.labels.origin_cluster !== undefined || series.labels.measurement_cluster !== undefined))) return {...fallback, source: 'cilium', description: fallback.description.replace('between nodes', 'between clusters')}
  return fallback
}

export function plotLabel(value: string): string {
  return value.replace(/ · (?:Run|Execution) \d+$/i, '')
}

export function validateDataset(dataset: TimeSeriesDataset) {
  if (dataset.apiVersion !== 'artifacts.kubephos.dev/v1alpha1' || dataset.kind !== 'TimeSeriesDataset' || dataset.metadata?.version !== 'v1alpha1' || !dataset.spec?.applicationRef || !dataset.spec?.namespace || !Array.isArray(dataset.spec?.series) || !dataset.spec.series.length) throw new Error('This run has no compatible metric data.')
  for (const series of dataset.spec.series) {
    if (!series.metric || !series.unit || !Array.isArray(series.points) || !series.points.length || series.points.some(point => !Number.isFinite(point.value) || !Number.isFinite(new Date(point.timestamp).getTime()))) throw new Error('This run contains invalid metric samples.')
  }
}
