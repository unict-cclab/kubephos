import {summarize} from '../../statistics'
import {metricRunValue, type LoadedDataset} from '../../resultsData'
import type {Experiment, LocustEndpointSummary} from '../../types'

export const ladderLevels = ['p50', 'p90', 'p95', 'p99'] as const
export type LadderLevel = typeof ladderLevels[number]

export interface LadderPoint {level: LadderLevel; mean: number; low?: number; high?: number; standardDeviation?: number; n: number}
export interface LadderSeries {
  id: string
  group: string
  color?: string
  vantage: 'client' | 'server'
  // exact: merged Locust histograms over the measured window; window: mean of windowed estimates.
  estimator: 'exact' | 'window'
  points: LadderPoint[]
}

const exactKey: Record<LadderLevel, keyof LocustEndpointSummary> = {p50: 'p50Ms', p90: 'p90Ms', p95: 'p95Ms', p99: 'p99Ms'}

function point(level: LadderLevel, values: number[]): LadderPoint | undefined {
  const finite = values.filter(Number.isFinite)
  if (!finite.length) return undefined
  if (finite.length === 1) return {level, mean: finite[0], n: 1}
  const summary = summarize(finite)
  return {level, mean: summary.mean, low: summary.confidence95Low, high: summary.confidence95High, standardDeviation: summary.standardDeviation, n: finite.length}
}

// Latency percentile ladder per group (variant, or run without experiment): client side from
// the exact Locust summary when every run has it (otherwise windowed load.* metrics), server
// side from the Istio application.* metrics. Each run contributes one value per level.
export function percentileLadder(datasets: LoadedDataset[], experiment?: Experiment): LadderSeries[] {
  const groups = new Map<string, {label: string; color?: string; runs: LoadedDataset[]}>()
  for (const item of datasets) {
    const variant = experiment?.variants.find(entry => entry.trials.some(trial => trial.resultArtifactId === item.artifact.id && !trial.pilot))
    if (experiment && !variant) continue
    const id = variant?.id ?? item.artifact.id
    const group = groups.get(id) ?? {label: variant ? variant.alias || variant.name : item.label, color: variant?.color ?? item.color, runs: []}
    group.runs.push(item)
    groups.set(id, group)
  }
  const series: LadderSeries[] = []
  for (const [id, group] of groups) {
    const exact = group.runs.every(run => ladderLevels.every(level => Number.isFinite(Number(run.dataset.spec.locustSummary?.total?.[exactKey[level]]))))
    const client = ladderLevels.flatMap(level => {
      const values = group.runs.map(run => exact ? Number(run.dataset.spec.locustSummary!.total![exactKey[level]]) : metricRunValue(run, `load.${level}_response_time`)).filter((value): value is number => value !== undefined)
      const result = point(level, values)
      return result ? [result] : []
    })
    if (client.length) series.push({id: `${id}:client`, group: group.label, color: group.color, vantage: 'client', estimator: exact ? 'exact' : 'window', points: client})
    const server = ladderLevels.flatMap(level => {
      const result = point(level, group.runs.map(run => metricRunValue(run, `application.${level}_response_time`)).filter((value): value is number => value !== undefined))
      return result ? [result] : []
    })
    if (server.length) series.push({id: `${id}:server`, group: group.label, color: group.color, vantage: 'server', estimator: 'window', points: server})
  }
  // A ladder needs at least two levels to be informative.
  return series.filter(item => item.points.length >= 2)
}
