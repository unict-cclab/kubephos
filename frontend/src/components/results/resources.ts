import {analysisSeries, counterRunTotal, isLoadGeneratorSeries, type LoadedDataset} from '../../resultsData'
import type {MetricSeries} from '../../types'
import {displayValue} from './display'
import type {OverviewGroup} from './overview'

// Resource figures of the key-number row: scaling, CPU, memory and restarts of the
// application workloads (the load generator is excluded, see analysisSeries), measured
// over the workload window. One ResourceRun per execution, one ResourceSummary per group
// (variant), so the experiment page and the comparison worker build the same row.

export interface ResourceRun {
  replicas?: {start: number; min: number; max: number; events: number}
  cpu?: {mean: number; peak: number; limitShare?: number}
  memory?: {mean: number; peak: number; limitShare?: number}
  restarts?: number
}

export interface ResourceSummary {
  groupId: string
  runs: number
  autoscaled: boolean
  replicas?: {start: number; min: number; max: number; events: number}
  cpu?: {mean: number; peak: number; limitShare?: number}
  memory?: {mean: number; peak: number; limitShare?: number}
  restarts?: number
}

const time = (value: string) => Date.parse(value)

// Per-timestamp sum over series (all pods / workloads), in time order.
function totals(series: MetricSeries[], keep: (series: MetricSeries) => boolean = () => true): number[] {
  const sums = new Map<number, number>()
  for (const item of series) {
    if (!keep(item)) continue
    for (const point of item.points) {
      const at = time(point.timestamp)
      if (Number.isFinite(at) && Number.isFinite(point.value)) sums.set(at, (sums.get(at) ?? 0) + point.value)
    }
  }
  return [...sums].sort((left, right) => left[0] - right[0]).map(([, value]) => value)
}

const mean = (values: number[]) => values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : undefined

// Limit series are not filtered by analysisSeries (not application resource metrics).
const limitSeries = (item: LoadedDataset, metric: string) => analysisSeries(item.dataset, metric).filter(series => !isLoadGeneratorSeries(series) && !series.labels.workload?.startsWith('kubephos-load-'))

// Usage and its share of the limit. The share only counts the pods that have a limit, so
// unlimited pods do not inflate it; without pod labels every series is used.
function usage(item: LoadedDataset, metric: string, limitMetric: string): ResourceRun['cpu'] {
  const series = analysisSeries(item.dataset, metric)
  const values = totals(series)
  const average = mean(values)
  if (average === undefined) return undefined
  const limits = limitSeries(item, limitMetric)
  let limitShare: number | undefined
  if (limits.length) {
    const pods = new Set(limits.map(entry => entry.labels.pod).filter(Boolean))
    const limited = mean(totals(series, entry => !pods.size || pods.has(entry.labels.pod)))
    const limit = mean(totals(limits))
    if (limited !== undefined && limit) limitShare = limited / limit * 100
  }
  return {mean: average, peak: Math.max(...values), limitShare}
}

// Total ready replicas over time and the number of changes of any workload's ready count.
function replicas(item: LoadedDataset): ResourceRun['replicas'] {
  const series = analysisSeries(item.dataset, 'deployment.ready_replicas')
  const values = totals(series)
  if (!values.length) return undefined
  let events = 0
  for (const entry of series) {
    const ordered = entry.points.filter(point => Number.isFinite(point.value)).sort((left, right) => time(left.timestamp) - time(right.timestamp))
    for (let index = 1; index < ordered.length; index++) if (ordered[index].value !== ordered[index - 1].value) events++
  }
  return {start: values[0], min: Math.min(...values), max: Math.max(...values), events}
}

export function resourceRun(item: LoadedDataset): ResourceRun {
  return {
    replicas: replicas(item),
    cpu: usage(item, 'cpu.cores', 'cpu.limit_cores'),
    memory: usage(item, 'memory.working_set', 'memory.limit_bytes'),
    restarts: counterRunTotal(item.dataset, 'pod.restarts')
  }
}

// Configured autoscaler (any autoscaler.* component) in a variant configuration.
export function hasAutoscaler(configuration: Record<string, unknown> | undefined): boolean {
  const components = Array.isArray(configuration?.components) ? configuration.components : []
  return components.some(component => Boolean(component) && typeof component === 'object' && String((component as Record<string, unknown>).capability ?? '').startsWith('autoscaler.'))
}

const meanOf = <T>(runs: T[], read: (run: T) => number | undefined) => mean(runs.map(read).filter((value): value is number => value !== undefined && Number.isFinite(value)))

// Repetitions: usage figures are means across executions; the replica range spans them all.
export function resourceSummary(groupId: string, items: LoadedDataset[], autoscaled: boolean): ResourceSummary {
  const runs = items.map(resourceRun)
  const scaled = runs.flatMap(run => run.replicas ? [run.replicas] : [])
  const usageOf = (key: 'cpu' | 'memory') => {
    const list = runs.flatMap(run => run[key] ? [run[key]!] : [])
    return list.length ? {mean: meanOf(list, entry => entry.mean)!, peak: meanOf(list, entry => entry.peak)!, limitShare: meanOf(list, entry => entry.limitShare)} : undefined
  }
  return {
    groupId, runs: items.length, autoscaled,
    replicas: scaled.length ? {start: meanOf(scaled, entry => entry.start)!, min: Math.min(...scaled.map(entry => entry.min)), max: Math.max(...scaled.map(entry => entry.max)), events: meanOf(scaled, entry => entry.events)!} : undefined,
    cpu: usageOf('cpu'), memory: usageOf('memory'),
    restarts: meanOf(runs, run => run.restarts)
  }
}

// --- presentation ---

export type ResourceId = 'scaling' | 'cpu' | 'memory' | 'restarts'
export interface ResourceEntry {group: OverviewGroup; value: string; unit: string; detail?: string; tone?: 'warning'}
export interface ResourceCard {id: ResourceId; title: string; entries: ResourceEntry[]}

const whole = (value: number) => Math.round(value).toLocaleString('en-US')
const count = (value: number) => Number.isInteger(value) ? whole(value) : value.toLocaleString('en-US', {maximumFractionDigits: 1})
const plural = (value: number, word: string) => `${count(value)} ${word}${Math.round(value * 10) === 10 ? '' : 's'}`
const share = (value: number | undefined, what: string) => value === undefined ? '' : ` · ${value >= 10 ? whole(value) : value.toFixed(1)} % of ${what}`

function scalingEntry(summary: ResourceSummary): Omit<ResourceEntry, 'group'> {
  const range = summary.replicas
  if (!range) return {value: '—', unit: '', detail: 'ready replicas not recorded'}
  const per = summary.runs > 1 ? ' per execution' : ''
  if (summary.autoscaled) return {value: range.min === range.max ? whole(range.min) : `${whole(range.min)}–${whole(range.max)}`, unit: 'replicas', detail: `autoscaled · ${plural(range.events, 'scaling event')}${per}`}
  return {value: Math.round(range.start) === range.max ? whole(range.max) : `${whole(range.start)} → ${whole(range.max)}`, unit: 'replicas', detail: range.events ? `${plural(range.events, 'scaling event')}${per}` : 'no scaling events'}
}

function usageEntry(value: ResourceSummary['cpu'], unit: 'cores' | 'bytes', missing: string): Omit<ResourceEntry, 'group'> {
  if (!value) return {value: '—', unit: '', detail: missing}
  const shown = displayValue(value.mean, unit)
  return {value: shown.value, unit: shown.unit, detail: `peak ${displayValue(value.peak, unit).text}${share(value.limitShare, 'limit')}`, tone: value.limitShare !== undefined && value.limitShare >= 90 ? 'warning' : undefined}
}

export function resourceCards(groups: OverviewGroup[], summaries: ResourceSummary[] | undefined): ResourceCard[] {
  if (!groups.length || !summaries) return []
  const of = (group: OverviewGroup) => summaries.find(item => item.groupId === group.id) ?? {groupId: group.id, runs: 0, autoscaled: false}
  // Nothing recorded at all (e.g. a load-only dataset): no resource row.
  if (!groups.some(group => {const item = of(group); return item.replicas || item.cpu || item.memory || item.restarts !== undefined})) return []
  const card = (id: ResourceId, title: string, entry: (summary: ResourceSummary) => Omit<ResourceEntry, 'group'>): ResourceCard => ({id, title, entries: groups.map(group => ({group, ...entry(of(group))}))})
  return [
    card('scaling', 'Scaling', scalingEntry),
    card('cpu', 'CPU usage', summary => usageEntry(summary.cpu, 'cores', 'CPU usage not recorded')),
    card('memory', 'Memory', summary => usageEntry(summary.memory, 'bytes', 'memory not recorded')),
    card('restarts', 'Pod restarts', summary => summary.restarts === undefined ? {value: '—', unit: '', detail: 'restarts not recorded'} : {value: count(summary.restarts), unit: '', detail: summary.runs > 1 ? 'per execution, measured window' : 'during the measured window', tone: summary.restarts > 0 ? 'warning' : undefined})
  ]
}
