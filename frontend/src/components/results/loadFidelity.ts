import {aggregatePlotMetric, analysisSeries, workloadWindow, type AggregateSeries, type LoadedDataset} from '../../resultsData'
import type {Experiment, MetricSeries} from '../../types'

// Thresholds of the load-fidelity check (coordinated omission / generator saturation).
export const fidelityThresholds = {
  // Achieved request rate below this share of the offered rate over the measured window.
  achievedRatio: .95,
  // Share of requests issued late with respect to the open-loop schedule.
  lateShare: .01,
  // Mean delay between scheduled and actual issue time.
  meanLagSeconds: .05
} as const

export interface LoadFidelity {
  offeredMean?: number
  achievedMean?: number
  ratio?: number
  offeredSource?: 'offered' | 'configured'
  achievedSource?: 'achieved' | 'successful+failed'
  schedule?: {lateRequests: number; requests?: number; lateShare?: number; meanLagSeconds?: number; maxLagSeconds?: number; source: 'locust-summary' | 'time-series'}
  warnings: string[]
}

function summedSeries(series: MetricSeries[]): Map<number, number> {
  const values = new Map<number, number>()
  for (const item of series) for (const point of item.points) {
    if (!Number.isFinite(point.value)) continue
    const timestamp = Date.parse(point.timestamp)
    values.set(timestamp, (values.get(timestamp) ?? 0) + point.value)
  }
  return values
}

const mean = (values: number[]) => values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : undefined

// Offered vs achieved request rate over the measured window, plus schedule-lag evidence.
export function loadFidelity(item: LoadedDataset): LoadFidelity {
  const dataset = item.dataset
  const offeredSeries = analysisSeries(dataset, 'load.offered_rps')
  const configuredSeries = offeredSeries.length ? [] : analysisSeries(dataset, 'load.configured_rps')
  const offered = summedSeries(offeredSeries.length ? offeredSeries : configuredSeries)
  const achievedSeries = analysisSeries(dataset, 'load.achieved_rps')
  const achieved = achievedSeries.length ? summedSeries(achievedSeries) : summedSeries([...analysisSeries(dataset, 'load.successful_rps'), ...analysisSeries(dataset, 'load.failed_rps')])
  const result: LoadFidelity = {warnings: []}
  if (offered.size) result.offeredSource = offeredSeries.length ? 'offered' : 'configured'
  if (achieved.size) result.achievedSource = achievedSeries.length ? 'achieved' : 'successful+failed'
  // Compare only instants observed in both series. The achieved rate is a trailing rate()
  // window: without a warm-up at least as long as that window, its first window still
  // covers time before the measured traffic started and underestimates the rate (legacy
  // runs had no warm-up), so those instants are not evidence of a shortfall.
  const rateWindowSeconds = Number(dataset.spec.rateWindowSeconds) > 0 ? Number(dataset.spec.rateWindowSeconds) : 60
  const warmupSeconds = Number(dataset.spec.runContext?.loadSession?.warmupSeconds ?? 0)
  const settledFrom = workloadWindow(dataset).start + (warmupSeconds >= rateWindowSeconds ? 0 : rateWindowSeconds * 1000)
  const common = [...offered.keys()].filter(timestamp => achieved.has(timestamp) && timestamp >= settledFrom)
  if (common.length) {
    result.offeredMean = mean(common.map(timestamp => offered.get(timestamp)!))
    result.achievedMean = mean(common.map(timestamp => achieved.get(timestamp)!))
    if (result.offeredMean !== undefined && result.achievedMean !== undefined && result.offeredMean > 0) {
      result.ratio = result.achievedMean / result.offeredMean
      if (result.ratio < fidelityThresholds.achievedRatio) result.warnings.push(`Achieved request rate was ${(result.ratio * 100).toFixed(1)}% of the ${result.offeredSource === 'offered' ? 'offered' : 'configured'} rate over the measured window (threshold ${fidelityThresholds.achievedRatio * 100}%): the load generator did not sustain the intended load.`)
    }
  }
  const schedule = dataset.spec.locustSummary?.schedule
  if (schedule) {
    const requests = dataset.spec.locustSummary?.requests
    result.schedule = {lateRequests: schedule.lateRequests, requests, lateShare: requests ? schedule.lateRequests / requests : undefined, meanLagSeconds: schedule.meanLagSeconds, maxLagSeconds: schedule.maxLagSeconds, source: 'locust-summary'}
  } else {
    const lag = mean(analysisSeries(dataset, 'load.schedule_lag').flatMap(series => series.points.map(point => point.value)).filter(Number.isFinite))
    if (lag !== undefined) result.schedule = {lateRequests: 0, meanLagSeconds: lag, source: 'time-series'}
  }
  const late = result.schedule
  if (late) {
    const reasons: string[] = []
    if (late.lateShare !== undefined && late.lateShare >= fidelityThresholds.lateShare) reasons.push(`${(late.lateShare * 100).toFixed(1)}% of requests were issued late (threshold ${fidelityThresholds.lateShare * 100}%)`)
    if (late.meanLagSeconds !== undefined && late.meanLagSeconds >= fidelityThresholds.meanLagSeconds) reasons.push(`mean schedule lag ${(late.meanLagSeconds * 1000).toFixed(0)} ms (threshold ${fidelityThresholds.meanLagSeconds * 1000} ms)`)
    if (reasons.length) result.warnings.push(`Coordinated omission risk: ${reasons.join('; ')}. Client-side latency may be underestimated because delayed requests were not sent on schedule.`)
  }
  return result
}

// Adds a derived `load.achieved_rps` (successful + failed) to datasets that lack it, without
// mutating the stored dataset.
export function withAchievedRate(item: LoadedDataset): LoadedDataset {
  const spec = item.dataset.spec
  if (spec.series.some(series => series.metric === 'load.achieved_rps')) return item
  const parts = spec.series.filter(series => series.metric === 'load.successful_rps' || series.metric === 'load.failed_rps')
  if (!parts.length) return item
  const values = summedSeries(parts)
  const derived: MetricSeries = {metric: 'load.achieved_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {derived: 'successful+failed'}, points: [...values].sort((a, b) => a[0] - b[0]).map(([timestamp, value]) => ({timestamp: new Date(timestamp).toISOString(), value}))}
  return {...item, aggregateCache: undefined, dataset: {...item.dataset, spec: {...spec, series: [...spec.series, derived]}}}
}

export interface FidelitySeries extends AggregateSeries {role: 'offered' | 'achieved'}

// Offered (or configured) and achieved curves per variant (or per run without experiment).
export function fidelityPlot(datasets: LoadedDataset[], experiment?: Experiment): FidelitySeries[] {
  const prepared = datasets.map(withAchievedRate)
  const offeredMetric = prepared.some(item => item.dataset.spec.series.some(series => series.metric === 'load.offered_rps')) ? 'load.offered_rps' : 'load.configured_rps'
  const offered = aggregatePlotMetric(prepared, offeredMetric, experiment)
  const achieved = aggregatePlotMetric(prepared, 'load.achieved_rps', experiment)
  if (!achieved.length || !offered.length) return []
  return achieved.flatMap(line => {
    const target = offered.find(item => item.artifactID === line.artifactID)
    return target ? [{...target, role: 'offered' as const, label: `${line.label} · ${offeredMetric === 'load.offered_rps' ? 'offered' : 'configured'}`}, {...line, role: 'achieved' as const, label: `${line.label} · achieved`}] : []
  })
}
