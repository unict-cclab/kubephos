import {analysisSeries, knownWorkloads, resolveWorkload, responseTimeSLO, type LoadedDataset} from '../../resultsData'
import type {Experiment, LocustEndpointSummary, MetricSeries} from '../../types'
import {exactFailurePercent} from './locust'
import {loadFidelity, type LoadFidelity} from './loadFidelity'

// Plain-language verdict of one experiment (or one variant): the few findings that matter,
// ordered by severity, each explaining the evidence it rests on. Pure and deterministic.

export type InsightSeverity = 'critical' | 'warning' | 'info' | 'ok'
export interface Insight {
  id: string
  severity: InsightSeverity
  title: string
  detail: string
  evidence: {metric?: string; service?: string; value?: number; unit?: string}
  target?: 'throughput' | 'latency' | 'services' | 'endpoints' | 'quality'
}

export interface ServiceHealth {
  service: string
  kind?: string
  rps?: number
  p95Ms?: number
  cpuCores?: number
  cpuLimitCores?: number
  replicas?: number
  restarts?: number
  score: number
  bottleneck: boolean
  reasons: string[]
}

// Every threshold used by the rules below.
export const insightThresholds = {
  // Achieved / offered request rate (load fidelity, see loadFidelity.ts): below → not sustained.
  sustained: .95,
  // Below this share the shortfall is critical even without latency evidence.
  criticalShortfall: .8,
  // Above this share the generator sent more than asked (legacy generator bug or extra load).
  overDelivery: 1.05,
  // Client P95 "high" without an SLO.
  highLatencyMs: 1000,
  // Client P95 "rising": mean of the last third of the window / mean of the first third.
  risingLatency: 1.5,
  // Share of the measured windows whose client P95 exceeded the SLO.
  sloCriticalPercent: 50,
  sloWarningPercent: 5,
  // Failed requests, percent of all requests.
  errorWarningPercent: 1,
  errorCriticalPercent: 5,
  // Bottleneck: highest score, at least this many times the runner-up, with server P95 ≥ minimum.
  bottleneckDominance: 2,
  bottleneckMinP95Ms: 100,
  // CPU ≥ this share of a KNOWN limit (cpu.limit_cores) counts as CPU-throttled.
  cpuAtLimit: .9,
  // CPU "flat": every pod's coefficient of variation ≤ this, at a mean ≥ cpuFlatMinCores.
  cpuFlatCV: .05,
  cpuFlatMinCores: .1,
  // Ready replicas at their maximum (after scaling) for at least this share of the window.
  replicasAtMaxShare: .5,
  // Client P50 more than this many times (and upstreamGapMs above) the highest server P95.
  upstreamGapRatio: 3,
  upstreamGapMs: 100,
  // Locust endpoints: P50 jump between consecutive endpoints (sorted) and slow-group minimum.
  endpointSplitRatio: 3,
  endpointSlowMs: 200,
  // Locust worker CPU (percent) considered saturated.
  generatorCpuPercent: 90,
  // Insights returned.
  maxInsights: 4
} as const

// Score multipliers for corroborating evidence (documented in ServiceHealth.reasons).
const cpuLimitWeight = 1.5
const replicasAtMaxWeight = 1.25

// Mesh/ingress infrastructure is listed but never named the bottleneck.
const isInfrastructure = (service: string) => service === 'node-proxy' || service === 'waypoint' || service === 'ztunnel' || service.startsWith('gateway-') || service.startsWith('istio-')

const average = (values: Array<number | undefined>): number | undefined => {
  const finite = values.filter((value): value is number => value !== undefined && Number.isFinite(value))
  return finite.length ? finite.reduce((sum, value) => sum + value, 0) / finite.length : undefined
}

// --- formatting (titles are read by researchers: short, rounded, with units) ---
export function formatMs(value: number): string {
  return value >= 1000 ? `${+(value / 1000).toFixed(value >= 10000 ? 0 : 1)} s` : `${Math.round(value)} ms`
}
const formatRps = (value: number) => value >= 10 ? value.toFixed(0) : +value.toFixed(1) + ''
const formatCores = (value: number) => value.toFixed(2)
const formatPercent = (value: number) => value >= 10 || value === 0 ? `${Math.round(value)} %` : `${+value.toFixed(1)} %`
const list = (names: string[]) => names.length <= 1 ? names.join('') : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`

// --- per-run facts ---
interface ServiceFacts {
  kind?: string
  rps?: number
  p95Ms?: number
  cpuCores?: number
  cpuLimitCores?: number
  cpuRequestCores?: number
  // Per-pod CPU plateau (mean per pod) when every pod's CPU was flat.
  cpuPlateau?: number
  replicas?: number
  replicasMin?: number
  replicasMax?: number
  replicasAtMaxShare?: number
  restarts?: number
}

interface RunFacts {
  fidelity: LoadFidelity
  clientRps?: number
  clientP50?: number
  clientP95?: number
  clientP95Rising?: boolean
  sloMs?: number
  sloViolationPercent?: number
  failurePercent?: number
  topError?: string
  services: Map<string, ServiceFacts>
  legacy: boolean
  legacyClock: boolean
  concurrentLoad: Array<{namespace: string; maxPods: number}>
  generatorCpu?: {worker: string; maxPercent: number}
  endpoints: LocustEndpointSummary[]
}

const timestamps = (series: MetricSeries) => series.points.map(point => Date.parse(point.timestamp))

// Per-timestamp combination of series, then mean over time.
function timeMean(series: MetricSeries[], combine: 'sum' | 'mean'): number | undefined {
  const values = new Map<number, number[]>()
  for (const item of series) {
    const times = timestamps(item)
    item.points.forEach((point, index) => {
      if (Number.isFinite(point.value)) values.set(times[index], [...values.get(times[index]) ?? [], point.value])
    })
  }
  return average([...values.values()].map(group => combine === 'sum' ? group.reduce((sum, value) => sum + value, 0) : average(group)))
}

function byWorkload(item: LoadedDataset, metric: string): Map<string, {kind?: string; series: MetricSeries[]}> {
  const known = knownWorkloads(item.dataset)
  const result = new Map<string, {kind?: string; series: MetricSeries[]}>()
  for (const series of analysisSeries(item.dataset, metric)) {
    // cpu.limit_cores / cpu.request_cores are not filtered by analysisSeries.
    if (!series.points.length || series.labels['app.kubernetes.io/name'] === 'kubephos-load' || ['deployment', 'pod', 'destination_workload', 'workload'].some(label => series.labels[label]?.startsWith('kubephos-load-'))) continue
    const workload = resolveWorkload(series, known)
    if (!workload) continue
    const entry = result.get(workload.name) ?? {kind: workload.kind, series: []}
    entry.kind ??= workload.kind
    entry.series.push(series)
    result.set(workload.name, entry)
  }
  return result
}

function cpuPlateau(series: MetricSeries[]): number | undefined {
  const pods = series.map(item => item.points.map(point => point.value).filter(Number.isFinite)).filter(values => values.length >= 4)
  if (!pods.length) return undefined
  const means: number[] = []
  for (const values of pods) {
    const center = average(values)!
    const deviation = Math.sqrt(values.reduce((sum, value) => sum + (value - center) ** 2, 0) / values.length)
    if (center < insightThresholds.cpuFlatMinCores || deviation / center > insightThresholds.cpuFlatCV) return undefined
    means.push(center)
  }
  return average(means)
}

function seriesMean(item: LoadedDataset, metric: string): number | undefined {
  return timeMean(analysisSeries(item.dataset, metric), 'mean')
}

function runFacts(item: LoadedDataset, experiment?: Experiment): RunFacts {
  const spec = item.dataset.spec
  const fidelity = loadFidelity(item)
  const summary = spec.locustSummary
  const p95Series = analysisSeries(item.dataset, 'load.p95_response_time')
  const p95Points = p95Series.flatMap(series => series.points.map(point => point.value)).filter(Number.isFinite)
  const third = Math.floor(p95Points.length / 3)
  const firstThird = third ? average(p95Points.slice(0, third)) : undefined
  const lastThird = third ? average(p95Points.slice(-third)) : undefined
  const sloMs = Number(spec.sloMilliseconds) > 0 ? Number(spec.sloMilliseconds) : responseTimeSLO(experiment, item.artifact.id)
  const violationSeries = analysisSeries(item.dataset, 'load.slo_violation_percentage')
  const sloViolationPercent = sloMs === undefined ? undefined
    : violationSeries.length ? timeMean(violationSeries, 'mean')
      : p95Points.length ? 100 * p95Points.filter(value => value > sloMs).length / p95Points.length : undefined

  const services = new Map<string, ServiceFacts>()
  const service = (name: string, kind?: string) => {
    const entry = services.get(name) ?? {}
    entry.kind ??= kind
    services.set(name, entry)
    return entry
  }
  for (const [name, {kind, series}] of byWorkload(item, 'application.request_rps')) service(name, kind).rps = timeMean(series, 'sum')
  for (const [name, {kind, series}] of byWorkload(item, 'application.p95_response_time')) service(name, kind).p95Ms = timeMean(series, 'mean')
  for (const [name, {kind, series}] of byWorkload(item, 'cpu.cores')) {
    const entry = service(name, kind)
    entry.cpuCores = timeMean(series, 'sum')
    entry.cpuPlateau = cpuPlateau(series)
  }
  for (const [name, {kind, series}] of byWorkload(item, 'cpu.limit_cores')) service(name, kind).cpuLimitCores = timeMean(series, 'sum')
  for (const [name, {kind, series}] of byWorkload(item, 'cpu.request_cores')) service(name, kind).cpuRequestCores = timeMean(series, 'sum')
  for (const [name, {kind, series}] of byWorkload(item, 'deployment.ready_replicas')) {
    const totals = new Map<number, number>()
    for (const entry of series) {
      const times = timestamps(entry)
      entry.points.forEach((point, index) => {if (Number.isFinite(point.value)) totals.set(times[index], (totals.get(times[index]) ?? 0) + point.value)})
    }
    const values = [...totals.values()]
    if (!values.length) continue
    const entry = service(name, kind)
    entry.replicas = average(values)
    entry.replicasMin = Math.min(...values)
    entry.replicasMax = Math.max(...values)
    entry.replicasAtMaxShare = values.filter(value => value === entry.replicasMax).length / values.length
  }
  for (const [name, {kind, series}] of byWorkload(item, 'pod.restarts')) {
    // analysisSeries already turned the lifetime counter into its increase during the run.
    service(name, kind).restarts = series.reduce((sum, entry) => sum + entry.points[entry.points.length - 1].value, 0)
  }
  for (const name of [...services.keys()]) if (name === 'node-proxy' && !services.get(name)!.cpuCores) services.delete(name)

  const failurePercent = summary ? exactFailurePercent(summary.total ?? summary) : seriesMean(item, 'load.failure_percentage')
  const topError = summary?.errors?.length ? [...summary.errors].sort((a, b) => b.occurrences - a.occurrences)[0] : undefined
  const worker = summary?.workerCpu?.filter(entry => entry.warnings > 0 || entry.maxPercent >= insightThresholds.generatorCpuPercent).sort((a, b) => b.maxPercent - a.maxPercent)[0]
  return {
    fidelity,
    clientRps: fidelity.achievedMean ?? seriesMean(item, 'load.achieved_rps'),
    clientP50: seriesMean(item, 'load.p50_response_time') ?? summary?.total?.p50Ms,
    clientP95: average(p95Points) ?? summary?.total?.p95Ms,
    clientP95Rising: firstThird !== undefined && lastThird !== undefined && firstThird > 0 && lastThird / firstThird >= insightThresholds.risingLatency,
    sloMs,
    sloViolationPercent,
    failurePercent,
    topError: topError ? `${topError.method} ${topError.name}: ${topError.error} (${topError.occurrences}×)` : undefined,
    services,
    legacy: !spec.runContext && !spec.metrics?.length && !summary,
    legacyClock: Boolean(spec.runContext) && !spec.runContext?.loadSession?.clock,
    concurrentLoad: spec.runContext?.concurrentLoad ?? [],
    generatorCpu: worker ? {worker: worker.worker, maxPercent: worker.maxPercent} : undefined,
    endpoints: summary?.endpoints ?? []
  }
}

// --- groups (variants) ---
interface GroupFacts extends Omit<RunFacts, 'fidelity' | 'services' | 'endpoints' | 'clientP95Rising' | 'legacy' | 'legacyClock' | 'generatorCpu'> {
  label: string
  runs: number
  offered?: number
  achieved?: number
  ratio?: number
  offeredSource?: LoadFidelity['offeredSource']
  lateShare?: number
  meanLagSeconds?: number
  scheduleWarning?: string
  clientP95Rising: boolean
  services: Map<string, ServiceFacts>
  endpoints: Array<{key: string; name: string; requests: number; p50Ms: number}>
  legacy: boolean
  legacyClock: boolean
  generatorCpu?: {worker: string; maxPercent: number}
}

function mergeServices(runs: RunFacts[]): Map<string, ServiceFacts> {
  const names = [...new Set(runs.flatMap(run => [...run.services.keys()]))]
  return new Map(names.map(name => {
    const facts = runs.map(run => run.services.get(name)).filter((value): value is ServiceFacts => Boolean(value))
    const pick = <K extends keyof ServiceFacts>(key: K) => average(facts.map(entry => entry[key] as number | undefined))
    // A plateau must hold in every run that measured CPU.
    const withCpu = facts.filter(entry => entry.cpuCores !== undefined)
    const merged: ServiceFacts = {kind: facts.find(entry => entry.kind)?.kind, rps: pick('rps'), p95Ms: pick('p95Ms'), cpuCores: pick('cpuCores'), cpuLimitCores: pick('cpuLimitCores'), cpuRequestCores: pick('cpuRequestCores'),
      cpuPlateau: withCpu.length && withCpu.every(entry => entry.cpuPlateau !== undefined) ? pick('cpuPlateau') : undefined,
      replicas: pick('replicas'), replicasMin: pick('replicasMin'), replicasMax: pick('replicasMax'), replicasAtMaxShare: pick('replicasAtMaxShare'), restarts: pick('restarts')}
    return [name, merged]
  }))
}

function groupFacts(label: string, runs: RunFacts[]): GroupFacts {
  const endpoints = new Map<string, {name: string; requests: number[]; p50: number[]}>()
  for (const run of runs) for (const endpoint of run.endpoints) {
    if (endpoint.p50Ms === undefined) continue
    const key = `${endpoint.method} ${endpoint.name}`
    const entry = endpoints.get(key) ?? {name: endpoint.name, requests: [], p50: []}
    entry.requests.push(endpoint.requests)
    entry.p50.push(endpoint.p50Ms)
    endpoints.set(key, entry)
  }
  const schedule = runs.map(run => run.fidelity.schedule)
  return {
    label,
    runs: runs.length,
    offered: average(runs.map(run => run.fidelity.offeredMean)),
    achieved: average(runs.map(run => run.fidelity.achievedMean)),
    ratio: average(runs.map(run => run.fidelity.ratio)),
    offeredSource: runs.find(run => run.fidelity.offeredSource)?.fidelity.offeredSource,
    lateShare: average(schedule.map(item => item?.lateShare)),
    meanLagSeconds: average(schedule.map(item => item?.meanLagSeconds)),
    scheduleWarning: runs.flatMap(run => run.fidelity.warnings.filter(warning => warning.startsWith('Coordinated omission')))[0],
    clientRps: average(runs.map(run => run.clientRps)),
    clientP50: average(runs.map(run => run.clientP50)),
    clientP95: average(runs.map(run => run.clientP95)),
    clientP95Rising: runs.some(run => run.clientP95Rising),
    sloMs: runs.find(run => run.sloMs !== undefined)?.sloMs,
    sloViolationPercent: average(runs.map(run => run.sloViolationPercent)),
    failurePercent: average(runs.map(run => run.failurePercent)),
    topError: runs.find(run => run.topError)?.topError,
    services: mergeServices(runs),
    legacy: runs.some(run => run.legacy),
    legacyClock: runs.some(run => run.legacyClock),
    concurrentLoad: runs.flatMap(run => run.concurrentLoad),
    generatorCpu: runs.map(run => run.generatorCpu).filter(value => value !== undefined).sort((a, b) => b!.maxPercent - a!.maxPercent)[0],
    endpoints: [...endpoints].map(([key, entry]) => ({key, name: entry.name, requests: average(entry.requests)!, p50Ms: average(entry.p50)!}))
  }
}

// --- services ranking ---
// score = server P95 × share of client requests that reach the service (≈ its contribution to
// client latency, capped at 1 call per request), × 1.5 when CPU is at a known limit,
// × 1.25 when ready replicas sat at their maximum after scaling. Infrastructure scores 0.
export function rankServices(group: Pick<GroupFacts, 'services' | 'clientRps'>): ServiceHealth[] {
  const rows = [...group.services].map(([service, facts]): ServiceHealth => {
    const reasons: string[] = []
    let score = 0
    const share = facts.rps !== undefined && group.clientRps ? Math.min(1, facts.rps / group.clientRps) : undefined
    const cpuAtLimit = facts.cpuLimitCores !== undefined && facts.cpuCores !== undefined && facts.cpuLimitCores > 0 && facts.cpuCores >= insightThresholds.cpuAtLimit * facts.cpuLimitCores
    const atMax = facts.replicasMax !== undefined && facts.replicasMin !== undefined && facts.replicasMax > facts.replicasMin && (facts.replicasAtMaxShare ?? 0) >= insightThresholds.replicasAtMaxShare
    if (facts.p95Ms !== undefined && !isInfrastructure(service)) {
      score = facts.p95Ms * (share ?? 1)
      reasons.push(share !== undefined
        ? `server P95 ${formatMs(facts.p95Ms)} on ${formatPercent(share * 100)} of client requests, about ${formatMs(score)} of client latency`
        : `server P95 ${formatMs(facts.p95Ms)} (call share unknown)`)
      if (cpuAtLimit) score *= cpuLimitWeight
      if (atMax) score *= replicasAtMaxWeight
    }
    if (facts.cpuCores !== undefined) {
      if (facts.cpuLimitCores !== undefined && facts.cpuLimitCores > 0) reasons.push(`CPU ${formatCores(facts.cpuCores)} of ${formatCores(facts.cpuLimitCores)} cores limit (${formatPercent(100 * facts.cpuCores / facts.cpuLimitCores)})${cpuAtLimit ? ', at the limit: likely CPU-throttled' : ''}`)
      else reasons.push(`CPU ${formatCores(facts.cpuCores)} cores, no CPU limit recorded${facts.cpuRequestCores ? ` (${formatPercent(100 * facts.cpuCores / facts.cpuRequestCores)} of its request)` : ''}`)
      if (facts.cpuPlateau !== undefined) reasons.push(`CPU flat at ${formatCores(facts.cpuPlateau)} cores per pod`)
    }
    if (atMax) reasons.push(`ready replicas at their maximum (${Math.round(facts.replicasMax!)}) for ${formatPercent(100 * facts.replicasAtMaxShare!)} of the window`)
    if (facts.restarts) reasons.push(`${Math.round(facts.restarts)} restart${Math.round(facts.restarts) === 1 ? '' : 's'}`)
    if (isInfrastructure(service)) reasons.push('mesh/ingress infrastructure, not ranked')
    return {service, ...(facts.kind ? {kind: facts.kind} : {}), rps: facts.rps, p95Ms: facts.p95Ms, cpuCores: facts.cpuCores, cpuLimitCores: facts.cpuLimitCores, replicas: facts.replicas, restarts: facts.restarts,
      score: Math.round(score * 10) / 10, bottleneck: false, reasons}
  }).sort((a, b) => b.score - a.score || (b.cpuCores ?? 0) - (a.cpuCores ?? 0) || a.service.localeCompare(b.service))
  const [top, second] = rows
  if (top && top.score > 0 && (top.p95Ms ?? 0) >= insightThresholds.bottleneckMinP95Ms && (!second || top.score >= insightThresholds.bottleneckDominance * second.score)) {
    top.bottleneck = true
    top.reasons.push(second?.score ? `${top.score / second.score >= 10 ? Math.round(top.score / second.score) : (top.score / second.score).toFixed(1)}× the score of the next service (${second.service})` : 'the only service with measurable latency')
  }
  return rows
}

// --- rules ---
interface Candidate extends Insight {priority: number; badness: number}

const severityRank: Record<InsightSeverity, number> = {critical: 0, warning: 1, info: 2, ok: 3}

function groupInsights(group: GroupFacts, services: ServiceHealth[]): Candidate[] {
  const out: Candidate[] = []
  const add = (priority: number, badness: number, insight: Insight) => out.push({...insight, priority, badness})
  const target = group.offeredSource === 'configured' ? 'configured' : 'offered'
  const highLatency = group.clientP95 !== undefined && (group.sloMs !== undefined ? group.clientP95 > group.sloMs : group.clientP95 >= insightThresholds.highLatencyMs)
  const latencyEvidence = group.clientP95 !== undefined ? `client P95 ${formatMs(group.clientP95)}${group.clientP95Rising ? ', rising' : ''}` : undefined
  const bottleneck = services.find(item => item.bottleneck)

  // 1. Load fidelity: did the system receive the load the experiment describes?
  if (group.ratio !== undefined && group.offered !== undefined && group.achieved !== undefined) {
    const numbersOf = `${formatRps(group.achieved)} of the ${formatRps(group.offered)} req/s ${target} (${formatPercent(group.ratio * 100)})`
    if (group.ratio < insightThresholds.sustained) {
      const saturated = highLatency || group.clientP95Rising
      const late = group.lateShare !== undefined && group.lateShare >= .01 ? ` ${formatPercent(group.lateShare * 100)} of requests left later than scheduled (coordinated omission).` : ''
      add(0, 1 - group.ratio, {id: 'target-not-sustained', severity: saturated || group.ratio < insightThresholds.criticalShortfall ? 'critical' : 'warning',
        title: `The system handled only ${numbersOf}`,
        detail: `${saturated
          ? `With ${latencyEvidence}, the system was saturated: Locust users waited on slow responses instead of sending new requests${bottleneck ? `, mostly because of ${bottleneck.service}` : ''}.`
          : 'Latency stayed low, so the load generator (workers, CPU, users) is the first suspect, not the system.'}${late} Read every other number as the system at ${formatRps(group.achieved)} req/s, not at the configured load. Threshold: ${formatPercent(insightThresholds.sustained * 100)} of target.`,
        evidence: {metric: 'load.achieved_rps', value: group.ratio * 100, unit: 'percent'}, target: 'throughput'})
    } else if (group.ratio > insightThresholds.overDelivery) {
      add(0, group.ratio - 1, {id: 'target-exceeded', severity: group.legacy ? 'critical' : 'warning',
        title: group.legacy
          ? `The generator sent more than asked: ${formatRps(group.achieved)} against ${formatRps(group.offered)} req/s (${formatPercent(group.ratio * 100)}), a legacy generator bug, so the data are not comparable`
          : `The generator sent more than asked: ${formatRps(group.achieved)} against ${formatRps(group.offered)} req/s (${formatPercent(group.ratio * 100)})`,
        detail: group.legacy
          ? `Before the load fixes, every worker sent the full target instead of its share. Latency, CPU and scaling here describe a load the experiment never specified; compare this run only with other legacy runs.`
          : `Above the ${formatPercent(insightThresholds.overDelivery * 100)} threshold. Look for a concurrent generator or a misconfigured profile before reading the latency.`,
        evidence: {metric: 'load.achieved_rps', value: group.ratio * 100, unit: 'percent'}, target: 'throughput'})
    } else {
      add(0, 0, {id: 'target-sustained', severity: 'ok', title: `The system kept up with the load: ${numbersOf}`,
        detail: `Achieved and ${target} rates stayed within ${formatPercent((1 - insightThresholds.sustained) * 100)} of each other over the measured window.`,
        evidence: {metric: 'load.achieved_rps', value: group.ratio * 100, unit: 'percent'}, target: 'throughput'})
    }
  }

  // 2. Bottleneck: where the latency is, and a hypothesis for why (never claim a CPU limit without its series).
  if (bottleneck) {
    const facts = group.services.get(bottleneck.service)!
    const limit = facts.cpuLimitCores
    const cpu = facts.cpuCores
    const throttled = limit !== undefined && limit > 0 && cpu !== undefined && cpu >= insightThresholds.cpuAtLimit * limit
    const atMax = bottleneck.reasons.some(reason => reason.startsWith('ready replicas at their maximum'))
    const p95 = formatMs(bottleneck.p95Ms!)
    const cpuText = cpu === undefined ? 'CPU not measured' : limit ? `${formatCores(cpu)} of ${formatCores(limit)} cores limit` : `${formatCores(cpu)} cores, no CPU limit`
    const title = throttled
      ? `${bottleneck.service} is the bottleneck: its P95 reached ${p95} with CPU pinned at its limit (${formatCores(cpu!)} of ${formatCores(limit!)} cores), so it is being throttled`
      : cpu !== undefined ? `${bottleneck.service} is the bottleneck: its P95 reached ${p95} while CPU stayed at ${limit ? `${formatCores(cpu)} of ${formatCores(limit)} cores allowed` : `${formatCores(cpu)} cores`}, which points to queueing rather than CPU` : `${bottleneck.service} is the bottleneck: its P95 reached ${p95}`
    const others = services.filter(item => item !== bottleneck && item.p95Ms !== undefined && !isInfrastructure(item.service)).map(item => item.p95Ms!)
    const next = others.length ? Math.max(...others) : undefined
    const share = bottleneck.rps !== undefined && group.clientRps ? Math.min(1, bottleneck.rps / group.clientRps) : undefined
    const evidence = `No other instrumented service exceeds ${next !== undefined ? formatMs(next) : 'it'}${share !== undefined ? `; it serves ${formatRps(bottleneck.rps!)} req/s, about ${formatPercent(share * 100)} of client requests` : ''}.`
    const hypothesis = throttled
      ? ` CPU sits at ${formatPercent(100 * cpu! / limit!)} of its limit: raise the limit or add replicas.`
      : cpu !== undefined
        ? ` CPU (${cpuText}${facts.cpuPlateau !== undefined ? `, flat at ${formatCores(facts.cpuPlateau)} cores per pod` : ''}) is not saturated${limit ? '' : ', and with no limit series no CPU cap can be inferred'}. A concurrency limit (worker threads, connection pool) or a slow dependency is queueing requests; look at its server configuration and downstream calls.`
        : ' No CPU data were collected for it, so the cause cannot be narrowed further.'
    add(1, bottleneck.score, {id: 'bottleneck', severity: highLatency || (group.ratio !== undefined && group.ratio < insightThresholds.sustained) ? 'critical' : 'warning', title,
      detail: `${evidence}${hypothesis}${atMax ? ' Its ready replicas stayed at their maximum, possibly the autoscaler\'s maxReplicas.' : ''}`,
      evidence: {metric: 'application.p95_response_time', service: bottleneck.service, value: bottleneck.p95Ms, unit: 'milliseconds'}, target: 'services'})
  }

  // 3. SLO compliance of the client P95.
  if (group.sloMs !== undefined && group.sloViolationPercent !== undefined) {
    const percent = group.sloViolationPercent
    const severity: InsightSeverity = percent >= insightThresholds.sloCriticalPercent ? 'critical' : percent >= insightThresholds.sloWarningPercent ? 'warning' : 'ok'
    add(2, percent, {id: 'slo', severity,
      title: severity === 'ok' ? `The ${formatMs(group.sloMs)} SLO held (${latencyEvidence ?? 'client P95'})` : `The ${formatMs(group.sloMs)} SLO was missed in ${formatPercent(percent)} of windows${group.clientP95 !== undefined ? ` (client P95 ${formatMs(group.clientP95)})` : ''}`,
      detail: `Share of measured windows whose client P95 exceeded ${formatMs(group.sloMs)}; critical from ${insightThresholds.sloCriticalPercent} %, warning from ${insightThresholds.sloWarningPercent} %.`,
      evidence: {metric: 'load.slo_violation_percentage', value: percent, unit: 'percent'}, target: 'latency'})
  }

  // 4. Errors.
  if (group.failurePercent !== undefined && group.failurePercent >= insightThresholds.errorWarningPercent) {
    add(3, group.failurePercent, {id: 'errors', severity: group.failurePercent >= insightThresholds.errorCriticalPercent ? 'critical' : 'warning',
      title: `${formatPercent(group.failurePercent)} of requests failed`,
      detail: `${group.topError ? `Most frequent: ${group.topError}. ` : ''}Latency percentiles include these fast failures. Warning from ${insightThresholds.errorWarningPercent} %, critical from ${insightThresholds.errorCriticalPercent} %.`,
      evidence: {metric: 'load.failure_percentage', value: group.failurePercent, unit: 'percent'}, target: 'endpoints'})
  }

  // 5. Client latency not explained by the instrumented services.
  const serverP95 = services.filter(item => item.p95Ms !== undefined && !isInfrastructure(item.service)).map(item => item.p95Ms!)
  if (group.clientP50 !== undefined && serverP95.length) {
    const highest = Math.max(...serverP95)
    if (group.clientP50 > insightThresholds.upstreamGapRatio * highest && group.clientP50 - highest > insightThresholds.upstreamGapMs) {
      add(4, group.clientP50 - highest, {id: 'upstream-latency', severity: 'warning',
        title: `Most of the latency is upstream of the mesh: client P50 ${formatMs(group.clientP50)} against at most ${formatMs(highest)} at any service`,
        detail: 'The median request is slower than the worst server-side P95, so the time goes somewhere the mesh does not see: an uninstrumented frontend or ingress, the network, or the load generator.',
        evidence: {metric: 'load.p50_response_time', value: group.clientP50, unit: 'milliseconds'}, target: 'latency'})
    }
  }

  // 6. Restarts during the run.
  const restarted = services.filter(item => (item.restarts ?? 0) > 0)
  if (restarted.length) {
    const total = restarted.reduce((sum, item) => sum + item.restarts!, 0)
    add(5, total, {id: 'restarts', severity: 'warning', title: `Pods restarted during the run: ${list(restarted.slice(0, 3).map(item => `${item.service} (${Math.round(item.restarts!)})`))}${restarted.length > 3 ? ' and others' : ''}`,
      detail: 'Requests in flight were lost and the affected windows are not steady state (OOM kill, failed probe or crash).',
      evidence: {metric: 'pod.restarts', service: restarted[0].service, value: total, unit: 'count'}, target: 'services'})
  }

  // 7. Measurement quality.
  if (group.generatorCpu) add(6, group.generatorCpu.maxPercent, {id: 'generator-cpu', severity: 'warning',
    title: `The load generator ran out of CPU (${formatPercent(group.generatorCpu.maxPercent)} on ${group.generatorCpu.worker})`,
    detail: `Above ${insightThresholds.generatorCpuPercent} %, client latency includes time spent inside Locust and the offered rate may not have gone out. Add workers or CPU.`,
    evidence: {value: group.generatorCpu.maxPercent, unit: 'percent'}, target: 'quality'})
  if (group.concurrentLoad.length) add(7, group.concurrentLoad.length, {id: 'concurrent-load', severity: 'warning',
    title: `Another load test was running at the same time (${list([...new Set(group.concurrentLoad.map(item => item.namespace))])})`,
    detail: 'Shared nodes may have interfered with these results.',
    evidence: {value: group.concurrentLoad.length, unit: 'count'}, target: 'quality'})
  if (group.legacy) add(8, 1, {id: 'legacy-dataset', severity: 'warning', title: 'This is a legacy dataset, without provenance, run context or Locust summary',
    detail: 'The offered rate is the configured profile, the window was rebuilt from collection times (± one scrape interval), client percentiles come from histogram buckets and there are no per-endpoint statistics. Compare it only with other legacy runs.',
    evidence: {}, target: 'quality'})
  else if (group.legacyClock) add(10, 0, {id: 'legacy-clock', severity: 'info', title: 'The measured window has no recorded clock source',
    detail: 'Its boundaries were rebuilt from the collection time and may be off by one scrape interval.', evidence: {}, target: 'quality'})
  if (group.scheduleWarning && !(group.ratio !== undefined && group.ratio < insightThresholds.sustained)) add(9, group.lateShare ?? group.meanLagSeconds ?? 0, {id: 'schedule-lag', severity: 'warning',
    title: group.lateShare !== undefined ? `${formatPercent(group.lateShare * 100)} of requests left behind schedule` : 'Requests left behind schedule',
    detail: group.scheduleWarning, evidence: {metric: 'load.schedule_lag', value: group.meanLagSeconds, unit: 'seconds'}, target: 'quality'})

  // 8. Autoscaling.
  const scaled = [...group.services].filter(([service, facts]) => !isInfrastructure(service) && facts.replicasMax !== undefined && facts.replicasMin !== undefined && facts.replicasMax > facts.replicasMin)
    .sort((a, b) => b[1].replicasMax! - a[1].replicasMax! || a[0].localeCompare(b[0]))
  if (scaled.length) {
    const maxReplicas = Math.max(...scaled.map(([, facts]) => facts.replicasMax!))
    const describe = ([service, facts]: [string, ServiceFacts]) => `${service} ${Math.round(facts.replicasMin!)}→${Math.round(facts.replicasMax!)} (mean ${+facts.replicas!.toFixed(1)})`
    const pinned = scaled.filter(([, facts]) => (facts.replicasAtMaxShare ?? 0) >= insightThresholds.replicasAtMaxShare).map(([service]) => service)
    add(11, scaled.length, {id: 'autoscaling', severity: 'info', title: `Autoscaling was active: ${scaled.length} service${scaled.length === 1 ? '' : 's'} scaled, up to ${maxReplicas} replicas`,
      detail: `${scaled.map(describe).join('; ')}.${pinned.length ? ` ${list(pinned)} stayed at their maximum for most of the window (possibly the autoscaler's maxReplicas).` : ''} Window means therefore mix scaling states.`,
      evidence: {metric: 'deployment.ready_replicas', service: scaled[0][0], value: maxReplicas, unit: 'replicas'}, target: 'services'})
  }

  // 9. Endpoint split: a few endpoints fast, the rest slow — points at a path-specific bottleneck.
  const endpoints = group.endpoints.filter(item => item.requests > 0).sort((a, b) => a.p50Ms - b.p50Ms || a.key.localeCompare(b.key))
  if (endpoints.length >= 2) {
    let split = -1
    let jump = 0
    for (let index = 1; index < endpoints.length; index++) {
      const ratio = endpoints[index].p50Ms / Math.max(1, endpoints[index - 1].p50Ms)
      if (ratio > jump) {jump = ratio; split = index}
    }
    const fast = endpoints.slice(0, split)
    const slow = endpoints.slice(split)
    const slowMedian = slow[Math.floor(slow.length / 2)].p50Ms
    if (jump >= insightThresholds.endpointSplitRatio && slowMedian >= insightThresholds.endpointSlowMs) {
      const fastNames = [...new Set(fast.map(item => item.name))]
      const fastP50 = Math.max(...fast.map(item => item.p50Ms))
      add(12, jump, {id: 'endpoint-split', severity: 'info',
        title: `Endpoints fall into two groups: ${list(fastNames.slice(0, 3))}${fastNames.length > 3 ? ' and others' : ''} answer within ${formatMs(fastP50)}, the other ${slow.length === 1 ? 'one takes' : `${slow.length} take`} about ${formatMs(slowMedian)}`,
        detail: `Medians jump ${jump.toFixed(1)}× between the groups, so the slow endpoints share a dependency the fast ones avoid${bottleneck ? `, consistent with ${bottleneck.service} on their path` : ''}.`,
        evidence: {metric: 'load.p50_response_time', value: slowMedian, unit: 'milliseconds'}, target: 'endpoints'})
    }
  }
  return out
}

function groupsOf(datasets: LoadedDataset[], experiment?: Experiment): Array<{label: string; items: LoadedDataset[]}> {
  const groups = new Map<string, {label: string; items: LoadedDataset[]}>()
  for (const item of datasets) {
    const variant = experiment?.variants.find(entry => entry.trials.some(trial => trial.resultArtifactId === item.artifact.id))
    const key = variant?.id ?? item.artifact.id
    const group = groups.get(key) ?? {label: variant?.alias || variant?.name || item.label, items: []}
    group.items.push(item)
    groups.set(key, group)
  }
  return [...groups.values()]
}

// Sentence-case title after a leading clause (keeps acronyms such as SLO and service names).
const lowerFirst = (text: string) => /^[A-Z][a-z]/.test(text) ? text.charAt(0).toLowerCase() + text.slice(1) : text

// Insights for the experiment (worst variant per rule, the others named in the detail) and
// the services ranked by bottleneck score over all given runs. For per-variant services
// tables call it with one variant's datasets.
export function runInsights(datasets: LoadedDataset[], experiment?: Experiment): {insights: Insight[]; services: ServiceHealth[]} {
  if (!datasets.length) return {insights: [], services: []}
  const facts = new Map(datasets.map(item => [item, runFacts(item, experiment)]))
  const groups = groupsOf(datasets, experiment).map(group => groupFacts(group.label, group.items.map(item => facts.get(item)!)))
  const services = rankServices(groupFacts('all', [...facts.values()]))
  const candidates = groups.flatMap(group => groupInsights(group, rankServices(group)).map(candidate => ({candidate, group: group.label})))
  const byId = new Map<string, Array<{candidate: Candidate; group: string}>>()
  for (const entry of candidates) byId.set(entry.candidate.id, [...byId.get(entry.candidate.id) ?? [], entry])
  const merged = [...byId.values()].map(entries => {
    entries.sort((a, b) => severityRank[a.candidate.severity] - severityRank[b.candidate.severity] || b.candidate.badness - a.candidate.badness || a.group.localeCompare(b.group))
    const [worst, ...rest] = entries
    if (groups.length === 1) return worst.candidate
    const also = rest.filter(entry => entry.candidate.severity === worst.candidate.severity).map(entry => entry.group)
    return {...worst.candidate, title: `In ${worst.group}, ${lowerFirst(worst.candidate.title)}`, detail: also.length ? `${worst.candidate.detail} Also in ${list(also)}.` : worst.candidate.detail}
  })
  const insights = merged.sort((a, b) => severityRank[a.severity] - severityRank[b.severity] || a.priority - b.priority).slice(0, insightThresholds.maxInsights)
    .map((item): Insight => ({id: item.id, severity: item.severity, title: item.title, detail: item.detail, evidence: item.evidence, ...(item.target ? {target: item.target} : {})}))
  return {insights, services}
}
