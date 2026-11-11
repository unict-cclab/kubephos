import type {Artifact, Experiment, MetricPoint, TimeSeriesDataset} from './types'

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

export function serviceMetricStatistics(datasets: LoadedDataset[], metric: string, experiment?: Experiment) {
  const groups = new Map<string, {service: string; group: string; groupID: string; color?: string; unit: string; runs: ScientificSummary[]}>()
  let unmatched = 0
  for (const dataset of datasets) {
    const deployments = [...new Set(dataset.dataset.spec.series.filter(series => series.metric === 'deployment.ready_replicas').map(series => series.labels.deployment || series.labels.statefulset).filter(Boolean))].sort((a, b) => b.length - a.length)
    const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === dataset.artifact.id))
    const group = variant?.alias || variant?.name || plotLabel(dataset.label)
    const groupID = variant?.id || dataset.artifact.id
    const services = new Map<string, {unit: string; points: Map<string, number>; counts: Map<string, number>}>()
    for (const series of dataset.dataset.spec.series.filter(series => series.metric === metric)) {
      if (series.labels['app.kubernetes.io/name'] === 'kubephos-load' || ['deployment', 'pod', 'destination_workload', 'service'].some(label => series.labels[label]?.startsWith('kubephos-load-'))) continue
      const pod = series.labels.pod
      // Older datasets contain pod labels only. Match against deployments actually present in the same run.
      const service = series.labels.deployment || series.labels.statefulset || series.labels.destination_workload || series.labels.service || series.labels.operation || deployments.find(name => pod?.startsWith(`${name}-`) && /^(?:[a-z0-9]+-[a-z0-9]+|[0-9]+)$/.test(pod.slice(name.length + 1)))
      if (!service) {unmatched++; continue}
      if (metric === 'application.p95_response_time' && (service === 'node-proxy' || service.startsWith('gateway-'))) continue
      const values = services.get(service) ?? {unit: series.unit, points: new Map<string, number>(), counts: new Map<string, number>()}
      for (const point of series.points) if (Number.isFinite(point.value)) {
        values.points.set(point.timestamp, (values.points.get(point.timestamp) ?? 0) + point.value)
        values.counts.set(point.timestamp, (values.counts.get(point.timestamp) ?? 0) + 1)
      }
      services.set(service, values)
    }
    for (const [service, values] of services) {
      const key = JSON.stringify([service, groupID])
      const row = groups.get(key) ?? {service, group, groupID, color: variant?.color ?? dataset.color, unit: values.unit, runs: []}
      if (values.points.size) row.runs.push(summarize([...values.points].map(([timestamp, value]) => metric.endsWith('p95_response_time') ? value / values.counts.get(timestamp)! : value)))
      groups.set(key, row)
    }
  }
  return {unmatched, rows: [...groups.values()].filter(row => row.runs.length).sort((a, b) => a.service.localeCompare(b.service, undefined, {numeric: true}) || a.group.localeCompare(b.group)).map(row => ({...row, summary: row.runs.length > 1 ? summarize(row.runs.map(run => run.mean)) : row.runs[0], acrossRuns: row.runs.length > 1}))}
}

export function aggregateMetric(datasets: LoadedDataset[], metric: string): AggregateSeries[] {
  return datasets.map(item => {
    if (item.aggregateCache?.has(metric)) return item.aggregateCache.get(metric)
    const matching = item.dataset.spec.series.filter(series => series.metric === metric)
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
    const startedAt = new Date(!isControlMetric(metric) && item.dataset.spec.workloadStartedAt ? item.dataset.spec.workloadStartedAt : item.dataset.spec.start).getTime()
    const result: AggregateSeries = {
      artifactID: item.artifact.id,
      label: item.label,
      unit: matching[0]?.unit ?? '',
      sloMilliseconds: item.dataset.spec.sloMilliseconds,
      color: item.color,
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
    const runs = group.datasets.map(dataset => {
      const origin = Date.parse(dataset.dataset.spec.workloadStartedAt || dataset.dataset.spec.start)
      const end = Date.parse(dataset.dataset.spec.end)
      const series = dataset.dataset.spec.series.filter(series => series.metric === metric).map(series => ({...series, points: series.points.filter(point => Number.isFinite(point.value) && Date.parse(point.timestamp) >= origin && (!Number.isFinite(end) || Date.parse(point.timestamp) <= end))}))
      return aggregateMetric([{...dataset, aggregateCache: undefined, dataset: {...dataset.dataset, spec: {...dataset.dataset.spec, series}}}], metric)[0]
    })
    if (runs.length === 1) return runs[0] ? [{...runs[0], label: group.label, color: group.color}] : []
    // A missing run/metric is not a zero and cannot contribute to an N-run mean.
    if (runs.some(run => !run)) return []
    const intervals = group.datasets.map(dataset => dataset.dataset.spec.stepSeconds * 1000).filter(value => Number.isFinite(value) && value > 0)
    const inferred = runs.flatMap(run => run.points.slice(1).map((point, index) => Date.parse(point.timestamp) - Date.parse(run.points[index].timestamp))).filter(value => value > 0)
    const step = intervals.length ? Math.max(...intervals) : inferred.length ? Math.min(...inferred) : 1000
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
      deviation: common.map((bin, index) => ({timestamp: new Date(bin * step).toISOString(), value: summaries[index].standardDeviation}))}] : []
  })
}

export interface ScientificSummary {
  count: number
  mean: number
  standardDeviation: number
  min: number
  max: number
  median: number
  p05: number
  p25: number
  p75: number
  p95: number
  p99: number
  coefficientOfVariation: number
  confidence95Low: number
  confidence95High: number
}

export function summarize(values: number[]): ScientificSummary {
  const sorted = values.filter(Number.isFinite).sort((left, right) => left - right)
  const count = sorted.length
  if (!count) return {count: 0, mean: 0, standardDeviation: 0, min: 0, max: 0, median: 0, p05: 0, p25: 0, p75: 0, p95: 0, p99: 0, coefficientOfVariation: 0, confidence95Low: 0, confidence95High: 0}
  const mean = sorted.reduce((sum, value) => sum + value, 0) / count
  const variance = count > 1 ? sorted.reduce((sum, value) => sum + (value - mean) ** 2, 0) / (count - 1) : 0
  const standardDeviation = Math.sqrt(variance)
  const margin = count > 1 ? tCritical95(count - 1) * standardDeviation / Math.sqrt(count) : 0
  return {count, mean, standardDeviation, min: sorted[0], max: sorted[count - 1], median: percentile(sorted, .5), p05: percentile(sorted, .05), p25: percentile(sorted, .25), p75: percentile(sorted, .75), p95: percentile(sorted, .95), p99: percentile(sorted, .99), coefficientOfVariation: mean === 0 ? 0 : standardDeviation / Math.abs(mean), confidence95Low: mean - margin, confidence95High: mean + margin}
}

function tCritical95(degrees: number): number {
  const values = [0, 12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228, 2.201, 2.179, 2.16, 2.145, 2.131, 2.12, 2.11, 2.101, 2.093, 2.086, 2.08, 2.074, 2.069, 2.064, 2.06, 2.056, 2.052, 2.048, 2.045, 2.042]
  return degrees < values.length ? values[Math.max(1, degrees)] : degrees < 40 ? 2.021 : degrees < 60 ? 2 : degrees < 120 ? 1.98 : 1.96
}

function percentile(sorted: number[], ratio: number): number {
  if (sorted.length === 1) return sorted[0]
  const position = (sorted.length - 1) * ratio
  const lower = Math.floor(position)
  const fraction = position - lower
  return sorted[lower] + (sorted[Math.min(lower + 1, sorted.length - 1)] - sorted[lower]) * fraction
}

export function isControlMetric(metric: string): boolean {
  return ['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration', 'descheduler.cycle_duration', 'descheduler.evictions'].includes(metric)
}

export const serviceStatisticsMetrics = ['deployment.ready_replicas', 'cpu.cores', 'memory.working_set', 'application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time']

export function serviceStatisticsRows(datasets: LoadedDataset[], experiment?: Experiment) {
  return serviceStatisticsMetrics.flatMap(metric => serviceMetricStatistics(datasets, metric, experiment).rows.map(row => ({...row, metric})))
}

export function statisticsCSV(datasets: LoadedDataset[], metrics: string[], experiment?: Experiment): string {
  const rows: Array<Array<string | number>> = [['scope', 'strategy', 'dataset', 'metric', 'service', 'unit', 'status', 'count', 'mean', 'total', 'std', 'min', 'median', 'max', 'missing_runs']]
  const summaryValues = (summary: ScientificSummary) => [summary.count, summary.mean, '', summary.standardDeviation, summary.min, summary.median, summary.max]
  for (const dataset of datasets) {
    for (const metric of metrics) {
      const series = aggregateMetric([dataset], metric)[0]
      const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
      const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
      if (podSummary) rows.push(['run', '', dataset.label, metric, '', 'milliseconds', 'available', ...summaryValues(podSummary), ''])
      else if (isControlMetric(metric) && control) rows.push(['run', '', dataset.label, metric, '', metric === 'descheduler.evictions' ? 'count' : 'milliseconds', control.status, control.count ?? '', control.mean ?? '', control.total ?? '', '', '', '', '', ''])
      else if (series) rows.push(['run', '', dataset.label, metric, '', series.unit, 'available', ...summaryValues(summarize(series.points.map(point => point.value))), ''])
    }
    for (const row of serviceStatisticsRows([dataset])) rows.push(['service_run', row.group, dataset.label, row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), ''])
  }
  if (experiment) {
    for (const row of variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2)) rows.push(['across_runs', row.variant, '', row.metric, '', row.unit, 'available', ...summaryValues(row.summary), row.missing])
    for (const row of serviceStatisticsRows(datasets, experiment).filter(row => row.acrossRuns)) rows.push(['service_across_runs', row.group, '', row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), ''])
  }
  return rows.map(row => row.map(value => {const text = String(value); return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text}).join(',')).join('\n')
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

function metricRunValue(dataset: LoadedDataset, metric: string): number | undefined {
  if (isControlMetric(metric)) {
    const summary = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
    return summary?.total ?? summary?.mean
  }
  const series = aggregateMetric([dataset], metric)[0]
  return series?.points.length ? series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length : undefined
}

export function variantStatistics(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return experiment.variants.flatMap(variant => metrics.map(metric => {
    const values: number[] = []
    let unit = ''
    for (const trial of variant.trials) {
      const dataset = byArtifact.get(trial.resultArtifactId)
      if (!dataset) continue
      const value = metricRunValue(dataset, metric)
      if (value === undefined) continue
      unit ||= aggregateMetric([dataset], metric)[0]?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
      values.push(value)
    }
    return values.length ? {variant: variant.alias || variant.name, metric, unit, missing: variant.trials.length - values.length, summary: summarize(values)} : null
  }).filter((item): item is {variant: string; metric: string; unit: string; missing: number; summary: ScientificSummary} => Boolean(item)))
}

export function variantEffectSizes(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  if (experiment.variants.length !== 2) return []
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return metrics.map(metric => {
    const left = variantRunMeans(experiment.variants[0], byArtifact, metric)
    const right = variantRunMeans(experiment.variants[1], byArtifact, metric)
    if (left.length < 2 || right.length < 2) return null
    return {metric, left: experiment.variants[0].alias || experiment.variants[0].name, right: experiment.variants[1].alias || experiment.variants[1].name, value: cohenD(left, right), leftCount: left.length, rightCount: right.length}
  }).filter((item): item is {metric: string; left: string; right: string; value: number; leftCount: number; rightCount: number} => Boolean(item))
}

function variantRunMeans(variant: Experiment['variants'][number], datasets: Map<string, LoadedDataset>, metric: string): number[] {
  const values: number[] = []
  for (const trial of variant.trials) {
    const dataset = datasets.get(trial.resultArtifactId)
    if (!dataset) continue
    const value = metricRunValue(dataset, metric)
    if (value !== undefined) values.push(value)
  }
  return values
}

export function cohenD(left: number[], right: number[]): number {
  const leftSummary = summarize(left)
  const rightSummary = summarize(right)
  const degrees = leftSummary.count + rightSummary.count - 2
  if (degrees <= 0) return 0
  const pooled = Math.sqrt(((leftSummary.count - 1) * leftSummary.standardDeviation ** 2 + (rightSummary.count - 1) * rightSummary.standardDeviation ** 2) / degrees)
  return pooled === 0 ? 0 : (rightSummary.mean - leftSummary.mean) / pooled
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
