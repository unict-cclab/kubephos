import {legacyMetricProvenance, metricProvenance, type LoadedDataset, type MetricProvenance} from '../../resultsData'
import type {Experiment, ExperimentTrial} from '../../types'

// Pilot (warm-up) repetitions are shown individually but never enter statistics,
// aggregates or plots.
export const isPilot = (trial: Pick<ExperimentTrial, 'pilot'>) => trial.pilot === true

export function withoutPilots(experiment: Experiment): Experiment
export function withoutPilots(experiment: Experiment | undefined): Experiment | undefined
export function withoutPilots(experiment: Experiment | undefined): Experiment | undefined {
  if (!experiment || !experiment.variants.some(variant => variant.trials.some(isPilot))) return experiment
  return {...experiment, variants: experiment.variants.map(variant => ({...variant, trials: variant.trials.filter(trial => !isPilot(trial))}))}
}

// Pilots that actually ran (methods paragraph): queued or never-started pilots are not
// "executed first", e.g. while the experiment is still running.
export function executedPilotCount(experiment: Experiment | undefined): number {
  return (experiment?.variants ?? []).reduce((total, variant) => total + variant.trials.filter(trial => isPilot(trial) && (trial.status === 'succeeded' || Boolean(trial.resultArtifactId))).length, 0)
}

export function pilotArtifactIDs(experiment: Experiment | undefined): Set<string> {
  return new Set(experiment?.variants.flatMap(variant => variant.trials.filter(trial => isPilot(trial) && trial.resultArtifactId).map(trial => trial.resultArtifactId)) ?? [])
}

export function restingUntil(trial: Pick<ExperimentTrial, 'status' | 'scheduledFor'>, now = Date.now()): string | undefined {
  if (trial.status !== 'queued' || !trial.scheduledFor) return undefined
  const at = new Date(trial.scheduledFor)
  if (!Number.isFinite(at.getTime()) || at.getTime() <= now) return undefined
  return `Resting until ${at.toLocaleTimeString('en-GB', {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false})}`
}

export function cleanupWarning(trial: Pick<ExperimentTrial, 'cleanupError'>): string | undefined {
  return trial.cleanupError ? `Result kept — environment cleanup failed: ${trial.cleanupError}` : undefined
}

export interface ResolvedProvenance extends MetricProvenance {
  // Datasets disagree on source or vantage for this metric.
  mixed?: boolean
}

// Provenance of a metric across datasets: the first dataset that has it; flagged when they differ.
export function provenanceFor(datasets: readonly LoadedDataset[], metric: string): ResolvedProvenance {
  const having = datasets.filter(item => item.dataset.spec.series.some(series => series.metric === metric) || item.dataset.spec.metrics?.some(entry => entry.metric === metric))
  if (!having.length) return datasets.length ? metricProvenance(datasets[0].dataset, metric) : legacyMetricProvenance(metric)
  const all = having.map(item => metricProvenance(item.dataset, metric))
  const first = all.find(item => !item.legacy) ?? all[0]
  const mixed = new Set(all.map(item => `${item.source}/${item.vantage}`)).size > 1
  return mixed ? {...first, mixed} : {...first, legacy: all.every(item => item.legacy)}
}
