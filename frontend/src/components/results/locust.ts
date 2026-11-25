import {summarize} from '../../statistics'
import type {LoadedDataset} from '../../resultsData'
import type {LocustEndpointSummary, LocustSummary} from '../../types'

export const locustColumns = [
  ['meanMs', 'Mean'], ['p50Ms', 'P50'], ['p90Ms', 'P90'], ['p95Ms', 'P95'], ['p99Ms', 'P99'], ['p999Ms', 'P99.9'], ['maxMs', 'Max']
] as const
export type LocustLatencyKey = typeof locustColumns[number][0]

// Value across runs: mean and exact-t 95% CI (n ≥ 2), or the single run's value.
export interface AcrossRuns {mean: number; low?: number; high?: number; n: number}

export interface LocustRow {
  key: string
  method: string
  name: string
  aggregated: boolean
  requests: AcrossRuns
  failures: AcrossRuns
  // Exact failures / requests per run, in percent.
  failurePercent?: AcrossRuns
  latency: Partial<Record<LocustLatencyKey, AcrossRuns>>
}

export interface LocustGroup {
  id: string
  label: string
  runs: number
  rows: LocustRow[]
  errors: Array<{method: string; name: string; error: string; occurrences: number; runs: number}>
  cpuWarnings: Array<{run: string; worker: string; maxPercent: number; warnings: number}>
  workers?: number
}

function acrossRuns(values: Array<number | undefined>): AcrossRuns | undefined {
  const finite = values.filter((value): value is number => value !== undefined && Number.isFinite(value))
  if (!finite.length) return undefined
  if (finite.length === 1) return {mean: finite[0], n: 1}
  const summary = summarize(finite)
  return {mean: summary.mean, low: summary.confidence95Low, high: summary.confidence95High, n: finite.length}
}

const endpointKey = (endpoint: Pick<LocustEndpointSummary, 'method' | 'name'>) => `${endpoint.method} ${endpoint.name}`

export function exactFailurePercent(summary: Pick<LocustEndpointSummary, 'requests' | 'failures'> | undefined): number | undefined {
  return summary && summary.requests > 0 && Number.isFinite(summary.failures) ? 100 * summary.failures / summary.requests : undefined
}

function aggregatedRow(summary: LocustSummary): LocustEndpointSummary {
  return summary.total ?? {method: '', name: 'Aggregated', requests: summary.requests, failures: summary.failures}
}

// Per-endpoint Locust statistics of a group of runs (one variant). With one run the values
// are that run's exact statistics; with repetitions each cell is the mean ± 95% CI across runs.
export function locustGroup(id: string, label: string, runs: Array<{label: string; summary: LocustSummary}>): LocustGroup {
  const endpoints = new Map<string, {method: string; name: string}>()
  for (const {summary} of runs) for (const endpoint of summary.endpoints ?? []) endpoints.set(endpointKey(endpoint), {method: endpoint.method, name: endpoint.name})
  const row = (key: string, method: string, name: string, aggregated: boolean, pick: (summary: LocustSummary) => LocustEndpointSummary | undefined): LocustRow => {
    const values = runs.map(run => pick(run.summary))
    const latency: LocustRow['latency'] = {}
    for (const [column] of locustColumns) {
      const value = acrossRuns(values.map(item => item?.[column]))
      if (value) latency[column] = value
    }
    return {key, method, name, aggregated,
      requests: acrossRuns(values.map(item => item?.requests)) ?? {mean: 0, n: 0},
      failures: acrossRuns(values.map(item => item?.failures)) ?? {mean: 0, n: 0},
      failurePercent: acrossRuns(values.map(exactFailurePercent)),
      latency}
  }
  const rows = [...endpoints].sort((a, b) => a[0].localeCompare(b[0])).map(([key, endpoint]) => row(key, endpoint.method, endpoint.name, false, summary => summary.endpoints?.find(item => endpointKey(item) === key)))
  rows.push(row('aggregated', '', 'Aggregated', true, aggregatedRow))
  const errors = new Map<string, LocustGroup['errors'][number]>()
  for (const {summary} of runs) for (const error of summary.errors ?? []) {
    const key = `${error.method} ${error.name} ${error.error}`
    const current = errors.get(key) ?? {...error, occurrences: 0, runs: 0}
    current.occurrences += error.occurrences
    current.runs++
    errors.set(key, current)
  }
  const cpuWarnings = runs.flatMap(run => (run.summary.workerCpu ?? []).filter(worker => worker.warnings > 0).map(worker => ({run: run.label, ...worker})))
  const workers = [...new Set(runs.map(run => run.summary.workers).filter((value): value is number => value !== undefined))]
  return {id, label, runs: runs.length, rows, errors: [...errors.values()].sort((a, b) => b.occurrences - a.occurrences), cpuWarnings, ...(workers.length === 1 ? {workers: workers[0]} : {})}
}

// Groups datasets by variant (or one group per dataset) and keeps those with a Locust summary.
export function locustGroups(datasets: LoadedDataset[], groupOf: (item: LoadedDataset) => {id: string; label: string}): LocustGroup[] {
  const groups = new Map<string, {label: string; runs: Array<{label: string; summary: LocustSummary}>}>()
  for (const item of datasets) {
    const summary = item.dataset.spec.locustSummary
    if (!summary) continue
    const {id, label} = groupOf(item)
    const group = groups.get(id) ?? {label, runs: []}
    group.runs.push({label: item.label, summary})
    groups.set(id, group)
  }
  return [...groups].map(([id, group]) => locustGroup(id, group.label, group.runs))
}
