import {request} from './api'
import {comparisonMetricCompatible, comparisonWarnings} from './comparisons'
import {addDerivedMetrics, aggregatePlotMetric, isControlMetric, responseTimeSLO, serviceMetricStatistics, serviceStatisticsMetrics, statisticsCSV, validateDataset, variantEffectSizes, variantStatistics, type LoadedDataset} from './resultsData'
import type {Artifact, Experiment, TimeSeriesDataset} from './types'

export interface ComparisonJob {
  artifacts: Artifact[]
  experiment: Experiment
  configurations: Record<string, Record<string, unknown>>
}
export type ComparisonColumns = Array<{variant: Experiment['variants'][number]; rows: ReturnType<typeof variantStatistics>; available: number}>
export interface PreparedComparison {
  complete: boolean
  metrics: string[]
  statisticsMetrics: string[]
  columns: ComparisonColumns
  effects: ReturnType<typeof variantEffectSizes>
  plots: Record<string, ReturnType<typeof aggregatePlotMetric>>
  services: Record<string, ReturnType<typeof serviceMetricStatistics>>
  warnings: string[]
  csv: string
  omitSLO: boolean
}
export type ComparisonProgress = 'Loading results…' | 'Preparing statistics…' | 'Preparing plots…'

// Runs in a worker in the browser. Only small presentation data crosses back to React;
// raw metric samples stay in this scope and are released when preparation finishes.
export async function prepareComparison(job: ComparisonJob, signal: AbortSignal, progress: (message: ComparisonProgress) => void = () => {}, load = (artifact: Artifact) => request<TimeSeriesDataset>(`/artifacts/${artifact.id}/download`, {}, undefined, signal), statisticsReady: (value: PreparedComparison) => void = () => {}): Promise<PreparedComparison> {
  const datasets: LoadedDataset[] = []
  const byArtifact = new Map(job.experiment.variants.flatMap(variant => variant.trials.map(trial => [trial.resultArtifactId, variant] as const)))
  const sources = job.artifacts.filter(artifact => byArtifact.has(artifact.id))
  progress('Loading results…')
  for (let offset = 0; offset < sources.length; offset += 4) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    datasets.push(...await Promise.all(sources.slice(offset, offset + 4).map(async artifact => {
      const dataset = await load(artifact)
      if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
      addDerivedMetrics(dataset, responseTimeSLO(job.experiment, artifact.id, job.configurations[artifact.id]))
      validateDataset(dataset)
      const variant = byArtifact.get(artifact.id)!
      return {artifact, dataset, label: variant.alias || variant.name, color: variant.color, aggregateCache: new Map()}
    })))
  }
  if (!datasets.length) throw new Error('No comparison results are available.')
  progress('Preparing statistics…')
  const metrics = [...new Set(datasets.flatMap(item => [...item.dataset.spec.series.map(series => series.metric), ...(item.dataset.spec.controlMetrics ?? []).filter(summary => summary.status !== 'not_configured').map(summary => summary.metric)]))]
    .filter(metric => !['load.mean_response_time', 'load.p50_response_time', 'load.p99_response_time'].includes(metric) && (!metric.startsWith('application.') || metric === 'application.p95_response_time') && comparisonMetricCompatible(datasets, metric)).sort()
  const statisticsMetrics = [...new Set([...metrics, ...datasets.flatMap(item => item.dataset.spec.controlMetrics?.map(summary => summary.metric) ?? [])])].filter(metric => !['application.p95_response_time', 'redis.operation_p95_response_time'].includes(metric))
  const columns = job.experiment.variants.map(variant => ({variant: {...variant, trials: []}, rows: variantStatistics({...job.experiment, variants: [variant]}, datasets, statisticsMetrics), available: variant.trials.filter(trial => datasets.some(dataset => dataset.artifact.id === trial.resultArtifactId)).length}))
  const effects = variantEffectSizes(job.experiment, datasets, statisticsMetrics)
  const warnings = comparisonWarnings(datasets, job.configurations)
  const omitSLO = new Set(datasets.map(item => item.dataset.spec.sloMilliseconds)).size > 1
  statisticsReady({complete: false, metrics, statisticsMetrics, columns, effects, warnings, csv: '', omitSLO, plots: {}, services: {}})
  const csv = statisticsCSV(datasets, statisticsMetrics, job.experiment)
  progress('Preparing plots…')
  const plots: PreparedComparison['plots'] = {}
  const services: PreparedComparison['services'] = {}
  for (const metric of metrics.filter(metric => !isControlMetric(metric))) {
    if (signal.aborted) throw new DOMException('Aborted', 'AbortError')
    plots[metric] = aggregatePlotMetric(datasets, metric, job.experiment)
    if (serviceStatisticsMetrics.includes(metric)) services[metric] = serviceMetricStatistics(datasets, metric, job.experiment)
  }
  return {complete: true, metrics, statisticsMetrics, columns, effects, plots, services, warnings, csv, omitSLO}
}
