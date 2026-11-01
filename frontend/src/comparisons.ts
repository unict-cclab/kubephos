import type {Experiment, ExperimentTrial, ExperimentVariant, ResourceAttributionFields, Artifact} from './types'
import type {LoadedDataset} from './components/ResultsView'

export interface ComparisonRun {experimentId: string; trialId: string}
export interface ComparisonSeries {label: string; color: string; runs: ComparisonRun[]}
export interface ResultComparison extends ResourceAttributionFields {
  id: string; name: string; ownerUserId: string; series: ComparisonSeries[]; createdAt: string; updatedAt: string
}
export interface ComparisonView {
  comparison: ResultComparison; result: Experiment; sources: Experiment[]; artifacts: Artifact[]; sourceConfigurations: Record<string, Record<string, unknown>>
}
export interface SelectableRun {key: string; experiment: Experiment; variant: ExperimentVariant; trial: ExperimentTrial}
export const runKey = (run: ComparisonRun) => `${run.experimentId}/${run.trialId}`
export const comparisonColors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#332288', '#882255']
export function selectableComparisonRuns(experiments: Experiment[]): SelectableRun[] {
  return experiments.filter(item => item.kind !== 'comparison' && ['succeeded', 'failed', 'canceled'].includes(item.status)).flatMap(experiment => experiment.variants.flatMap(variant => variant.trials.filter(trial => trial.status === 'succeeded' && trial.resultArtifactId).map(trial => ({key: runKey({experimentId: experiment.id, trialId: trial.id}), experiment, variant, trial}))))
}

function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`
  if (value && typeof value === 'object') return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, entry]) => `${JSON.stringify(key)}:${canonical(entry)}`).join(',')}}`
  return JSON.stringify(value) ?? ''
}
export function comparisonWarnings(datasets: LoadedDataset[], configurations: Record<string, Record<string, unknown>>): string[] {
  if (datasets.length < 2) return []
  const warnings: string[] = []
  const different = (values: unknown[]) => new Set(values.map(canonical)).size > 1
  if (different(datasets.map(item => item.dataset.spec.stepSeconds))) warnings.push('Data resolution differs between runs. Plots use the common time windows.')
  const durations = datasets.map(item => Math.round((Date.parse(item.dataset.spec.end) - Date.parse(item.dataset.spec.workloadStartedAt || item.dataset.spec.start)) / 1000))
  if (durations.every(Number.isFinite) && Math.max(...durations) - Math.min(...durations) > Math.max(...datasets.map(item => item.dataset.spec.stepSeconds))) warnings.push('Run durations differ. Averaged lines cover only the time windows present in every run of that series.')
  const components = (id: string, prefix: string) => {
    const value = configurations[id]?.components
    return Array.isArray(value) ? value.filter(component => component && typeof component === 'object' && String(component.capability).startsWith(prefix)).map(component => ({pluginId: component.pluginId, configuration: component.configuration})) : []
  }
  if (different(datasets.map(item => ({application: item.dataset.spec.applicationRef, values: configurations[item.artifact.id]?.applicationValues})))) warnings.push('Application settings differ between runs.')
  if (different(datasets.map(item => ({target: item.dataset.spec.sloMilliseconds, settings: components(item.artifact.id, 'metrics.')})))) warnings.push('Monitoring settings or SLO targets differ. Violations use each run’s own target.')
  if (different(datasets.map(item => item.dataset.spec.clusterServer))) warnings.push('Runs were collected from different clusters.')
  if (different(datasets.map(item => components(item.artifact.id, 'load.')))) warnings.push('Load profiles differ between runs.')
  if (different(datasets.map(item => item.dataset.spec.series.map(series => `${series.metric}:${series.unit}`).sort()))) warnings.push('Available metrics differ between runs. Missing measurements are not treated as zero.')
  if (datasets.some(item => item.dataset.spec.series.some(series => !comparisonMetricCompatible(datasets, series.metric)))) warnings.push('Metrics with inconsistent units are excluded from this comparison.')
  return warnings
}

export function comparisonMetricCompatible(datasets: LoadedDataset[], metric: string): boolean {
  return new Set(datasets.flatMap(item => item.dataset.spec.series.filter(series => series.metric === metric).map(series => series.unit))).size <= 1
}
