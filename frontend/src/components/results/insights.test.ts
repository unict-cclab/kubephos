import {describe, expect, it} from 'vitest'
import type {LoadedDataset} from '../../resultsData'
import type {Artifact, Experiment, LocustSummary, MetricSeries, TimeSeriesDataset} from '../../types'
import {insightThresholds, runInsights} from './insights'

const start = Date.parse('2026-11-19T10:00:00Z')
const at = (seconds: number) => new Date(start + seconds * 1000).toISOString()
const steps = 21

function series(metric: string, unit: string, values: number[] | ((index: number) => number), labels: Record<string, string> = {}, aggregation: 'sum' | 'mean' = 'mean'): MetricSeries {
  const value = typeof values === 'function' ? values : (index: number) => values[Math.min(index, values.length - 1)]
  return {metric, unit, aggregation, labels, points: Array.from({length: steps}, (_, index) => ({timestamp: at(60 + index * 15), value: value(index)}))}
}
const constant = (value: number) => () => value

function dataset(id: string, spec: Partial<TimeSeriesDataset['spec']> & {series: MetricSeries[]}): LoadedDataset {
  const artifact: Artifact = {id, operationId: `op-${id}`, workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: `sha:${id}`, sizeBytes: 1, sensitive: false, verifiedAt: at(0)}
  return {artifact, label: id, dataset: {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: id, version: 'v1alpha1'}, spec: {applicationRef: 'app', clusterServer: 'https://cluster', namespace: 'ns', start: at(0), workloadStartedAt: at(60), end: at(60 + (steps - 1) * 15), stepSeconds: 15, source: {kind: 'prometheus', version: '1', profile: 'p'}, summary: {metrics: 1, series: 1, samples: 1}, ...spec}}}
}

const service = (name: string, rps: number, p95: number, cpu: number | ((index: number) => number), extra: MetricSeries[] = []) => [
  series('application.request_rps', 'requests_per_second', constant(rps), {destination_workload: name}, 'sum'),
  series('application.p95_response_time', 'milliseconds', constant(p95), {destination_workload: name}),
  series('cpu.cores', 'cores', typeof cpu === 'function' ? cpu : constant(cpu), {pod: `${name}-7d4b9c6f8d-x2k9p`, workload: name, workload_kind: 'Deployment'}, 'sum'),
  series('deployment.ready_replicas', 'replicas', constant(1), {deployment: name, workload: name, workload_kind: 'Deployment'}, 'sum'),
  series('pod.restarts', 'count', constant(0), {pod: `${name}-7d4b9c6f8d-x2k9p`, workload: name, workload_kind: 'Deployment'}, 'sum'),
  ...extra
]

const endpoint = (method: string, name: string, requests: number, p50Ms: number, p95Ms: number) => ({method, name, requests, failures: 0, p50Ms, p95Ms})
const saturatedSummary: LocustSummary = {workers: 4, requests: 49691, failures: 11, measuredFrom: at(60), measuredTo: at(360),
  endpoints: [endpoint('GET', '/', 2339, 220, 440), endpoint('POST', '/setCurrency', 4330, 230, 440), endpoint('GET', '/cart', 6501, 1700, 2300), endpoint('POST', '/cart', 6451, 1800, 2400), endpoint('GET', '/product/A', 3190, 1700, 2300), endpoint('POST', '/cart/checkout', 2072, 1700, 2300)],
  total: {method: '', name: 'Aggregated', requests: 49691, failures: 11, p50Ms: 1600, p95Ms: 2300},
  errors: [{method: 'POST', name: '/cart/checkout', error: 'HTTP 500', occurrences: 9}],
  workerCpu: [{worker: 'w1', maxPercent: 3.8, warnings: 0}],
  schedule: {lateRequests: 3030, maxLagSeconds: .27, meanLagSeconds: .008}}

// Modelled on new-5min.json: 250 req/s offered, 163 achieved, recommendationservice slow at a flat 0.20 cores.
function saturatedRun(id = 'new', options: {limit?: number; recommendationP95?: number} = {}) {
  const recommendation = options.recommendationP95 ?? 2310
  return dataset(id, {sloMilliseconds: 350, rateWindowSeconds: 60, metrics: [{metric: 'load.offered_rps', source: 'locust', vantage: 'client'}],
    runContext: {loadSession: {clock: 'shared-start', warmupSeconds: 60, workers: 4}, concurrentLoad: []}, locustSummary: saturatedSummary,
    series: [
      series('load.offered_rps', 'requests_per_second', index => 187.5 + index * 6.25, {}, 'sum'),
      series('load.achieved_rps', 'requests_per_second', constant(163.3), {}, 'sum'),
      series('load.p50_response_time', 'milliseconds', constant(1627)),
      series('load.p95_response_time', 'milliseconds', constant(2418)),
      series('load.slo_violation_percentage', 'percent', constant(100), {objective: '350ms'}),
      ...service('recommendationservice', 141, recommendation, .2, options.limit ? [series('cpu.limit_cores', 'cores', constant(options.limit), {pod: 'recommendationservice-7d4b9c6f8d-x2k9p', workload: 'recommendationservice', workload_kind: 'Deployment'}, 'sum')] : []),
      ...service('cartservice', 192, 61, .067),
      ...service('checkoutservice', 7, 177, .018),
      ...service('currencyservice', 604, 15, index => .14 + (index % 3) * .01),
      ...service('productcatalogservice', 1077, 4, .086),
      series('cpu.cores', 'cores', constant(.196), {pod: 'frontend-6b9d5c7f4b-abcde', workload: 'frontend', workload_kind: 'Deployment'}, 'sum'),
      series('cpu.cores', 'cores', constant(.5), {pod: 'kubephos-load-storefront-6b9d5c7f4b-abcde'}, 'sum')
    ]})
}

// Modelled on old-1h-sinusoid.json: legacy (no provenance/runContext/summary), 311 achieved vs 175 configured, autoscaling.
function legacyRun(id = 'old') {
  const scaled = (name: string, max: number) => series('deployment.ready_replicas', 'replicas', index => index < 3 ? 1 : max, {deployment: name}, 'sum')
  return dataset(id, {sloMilliseconds: 350, series: [
    series('load.configured_rps', 'requests_per_second', constant(175), {scenario: 'storefront'}, 'sum'),
    series('load.successful_rps', 'requests_per_second', constant(311), {}, 'sum'),
    series('load.failed_rps', 'requests_per_second', constant(0), {}, 'sum'),
    series('load.p95_response_time', 'milliseconds', index => index % 3 === 0 ? 200 : 1800),
    series('load.slo_violation_percentage', 'percent', index => index % 3 === 0 ? 0 : 100, {objective: '350ms'}),
    series('application.request_rps', 'requests_per_second', constant(270), {destination_workload: 'recommendationservice'}, 'sum'),
    series('application.p95_response_time', 'milliseconds', constant(278), {destination_workload: 'recommendationservice'}),
    series('application.request_rps', 'requests_per_second', constant(362), {destination_workload: 'cartservice'}, 'sum'),
    series('application.p95_response_time', 'milliseconds', constant(128), {destination_workload: 'cartservice'}),
    series('cpu.cores', 'cores', index => .3 + index * .05, {pod: 'recommendationservice-869fdcf58c-4v27m'}, 'sum'),
    scaled('recommendationservice', 10), scaled('frontend', 10), scaled('cartservice', 4),
    series('deployment.ready_replicas', 'replicas', constant(1), {deployment: 'emailservice'}, 'sum')
  ]})
}

describe('runInsights', () => {
  it('names the saturated target and a non-CPU bottleneck without asserting a CPU limit', () => {
    const {insights, services} = runInsights([saturatedRun()])
    expect(insights.length).toBeLessThanOrEqual(insightThresholds.maxInsights)
    expect(insights.map(item => item.id)).toEqual(['target-not-sustained', 'bottleneck', 'slo', 'endpoint-split'])
    expect(insights[0]).toMatchObject({severity: 'critical', title: 'The system handled only 163 of the 250 req/s offered (65 %)', target: 'throughput'})
    expect(insights[0].detail).toContain('saturated')
    expect(insights[0].detail).toContain('recommendationservice')
    expect(insights[1].severity).toBe('critical')
    expect(insights[1].title).toBe('recommendationservice is the bottleneck: its P95 reached 2.3 s while CPU stayed at 0.20 cores, which points to queueing rather than CPU')
    expect(insights[1].detail).toMatch(/concurrency limit .*slow dependency/)
    expect(insights[1].detail).not.toMatch(/CPU-throttled|possible CPU limit/)
    expect(insights[2].title).toBe('The 350 ms SLO was missed in 100 % of windows (client P95 2.4 s)')
    expect(insights[3].title).toBe('Endpoints fall into two groups: / and /setCurrency answer within 230 ms, the other 4 take about 1.7 s')
    expect(services[0]).toMatchObject({service: 'recommendationservice', bottleneck: true})
    expect(services.filter(item => item.bottleneck)).toHaveLength(1)
    // The load generator never appears among the services.
    expect(services.some(item => item.service.startsWith('kubephos-load'))).toBe(false)
    expect(services.find(item => item.service === 'frontend')).toMatchObject({score: 0, bottleneck: false})
  })

  it('calls the bottleneck CPU-throttled only with a known limit', () => {
    const {insights, services} = runInsights([saturatedRun('limited', {limit: .2})])
    expect(insights[1].title).toBe('recommendationservice is the bottleneck: its P95 reached 2.3 s with CPU pinned at its limit (0.20 of 0.20 cores), so it is being throttled')
    expect(services[0].cpuLimitCores).toBeCloseTo(.2)
    const below = runInsights([saturatedRun('unlimited', {limit: 2})]).insights[1]
    expect(below.title).toContain('while CPU stayed at 0.20 of 2.00 cores allowed, which points to queueing')
  })

  it('does not declare a bottleneck when no service dominates', () => {
    const {insights, services} = runInsights([saturatedRun('flat', {recommendationP95: 80})])
    expect(services.some(item => item.bottleneck)).toBe(false)
    expect(insights.some(item => item.id === 'bottleneck')).toBe(false)
  })

  it('flags legacy over-delivery as not comparable and shows autoscaling', () => {
    const {insights} = runInsights([legacyRun()])
    expect(insights.map(item => item.id)).toEqual(['target-exceeded', 'slo', 'legacy-dataset', 'autoscaling'])
    expect(insights[0]).toMatchObject({severity: 'critical', title: 'The generator sent more than asked: 311 against 175 req/s (178 %), a legacy generator bug, so the data are not comparable'})
    expect(insights[1].title).toBe('The 350 ms SLO was missed in 67 % of windows (client P95 1.3 s)')
    expect(insights[3].title).toBe('Autoscaling was active: 3 services scaled, up to 10 replicas')
    expect(insights[3].detail).toContain('recommendationservice 1→10')
  })

  it('reports a sustained target as ok and is deterministic', () => {
    const run = dataset('ok', {rateWindowSeconds: 60, metrics: [], runContext: {loadSession: {clock: 'shared-start', warmupSeconds: 60}}, series: [
      series('load.offered_rps', 'requests_per_second', constant(100), {}, 'sum'),
      series('load.achieved_rps', 'requests_per_second', constant(99), {}, 'sum'),
      series('load.p95_response_time', 'milliseconds', constant(40)),
      ...service('cartservice', 100, 20, .05)
    ]})
    const first = runInsights([run])
    expect(first.insights).toEqual([expect.objectContaining({id: 'target-sustained', severity: 'ok', title: 'The system kept up with the load: 99 of the 100 req/s offered (99 %)'})])
    expect(runInsights([run])).toEqual(first)
  })

  it('reports quality problems: upstream latency, restarts, generator CPU and concurrent load', () => {
    const run = dataset('quality', {rateWindowSeconds: 60, metrics: [], runContext: {loadSession: {clock: 'shared-start', warmupSeconds: 60}, concurrentLoad: [{namespace: 'other', maxPods: 2}]},
      locustSummary: {requests: 1000, failures: 0, workerCpu: [{worker: 'w1', maxPercent: 97, warnings: 3}]},
      series: [
        series('load.offered_rps', 'requests_per_second', constant(100), {}, 'sum'),
        series('load.achieved_rps', 'requests_per_second', constant(100), {}, 'sum'),
        series('load.p50_response_time', 'milliseconds', constant(900)),
        ...service('cartservice', 100, 20, .05).map(item => item.metric === 'pod.restarts' ? series('pod.restarts', 'count', index => index > 10 ? 2 : 0, item.labels, 'sum') : item)
      ]})
    const ids = runInsights([run]).insights.map(item => item.id)
    expect(ids).toEqual(['upstream-latency', 'restarts', 'generator-cpu', 'concurrent-load'])
  })

  it('reports the worst variant and names the others', () => {
    const experiment = {id: 'e', name: 'E', kind: 'comparison', status: 'succeeded', variants: [
      {id: 'a', name: 'Baseline', configuration: {}, trials: [{id: 't1', position: 0, status: 'succeeded', resultArtifactId: 'a1'}]},
      {id: 'b', name: 'Tuned', configuration: {}, trials: [{id: 't2', position: 0, status: 'succeeded', resultArtifactId: 'b1'}]}
    ]} as unknown as Experiment
    const {insights} = runInsights([saturatedRun('a1'), saturatedRun('b1', {recommendationP95: 2500})], experiment)
    expect(insights[0].title).toMatch(/^In Baseline, the system handled only/)
    expect(insights[0].detail).toContain('Also in Tuned.')
    expect(insights[1].title).toMatch(/^In Tuned, recommendationservice is the bottleneck: its P95 reached 2.5 s/)
  })
})

// Real datasets from a local study (never committed): KUBEPHOS_REAL_DATASETS=<dir with new-5min.json, old-1h-sinusoid.json>.
const env = (globalThis as {process?: {env: Record<string, string | undefined>}}).process?.env ?? {}
const realDirectory = env.KUBEPHOS_REAL_DATASETS
async function real(name: string): Promise<LoadedDataset | undefined> {
  if (!realDirectory) return undefined
  const fsModule = 'node:fs'
  const fs = await import(/* @vite-ignore */ fsModule) as {existsSync(path: string): boolean; readFileSync(path: string, encoding: string): string}
  const path = `${realDirectory}/${name}`
  if (!fs.existsSync(path)) return undefined
  const parsed = JSON.parse(fs.readFileSync(path, 'utf8')) as TimeSeriesDataset
  return {artifact: {id: name} as Artifact, dataset: parsed, label: name}
}

describe.skipIf(!realDirectory)('runInsights on real datasets', () => {
  it('new-5min.json: saturation with a recommendationservice bottleneck', async () => {
    const item = await real('new-5min.json')
    if (!item) return
    const {insights, services} = runInsights([item])
    expect(insights[0]).toMatchObject({id: 'target-not-sustained', severity: 'critical'})
    expect(insights[0].title).toMatch(/16\d of the 250 req\/s offered \(6\d %\)/)
    expect(insights.find(entry => entry.id === 'bottleneck')?.title).toMatch(/^recommendationservice is the bottleneck: its P95 reached 2\.3 s while CPU stayed/)
    expect(insights.find(entry => entry.id === 'slo')?.title).toMatch(/^The 350 ms SLO was missed in 100 %/)
    expect(services[0]).toMatchObject({service: 'recommendationservice', bottleneck: true})
    expect(services[0].cpuCores).toBeCloseTo(.2, 1)
  })

  it('old-1h-sinusoid.json: legacy over-delivery and autoscaling', async () => {
    const item = await real('old-1h-sinusoid.json')
    if (!item) return
    const {insights} = runInsights([item])
    expect(insights[0]).toMatchObject({id: 'target-exceeded', severity: 'critical'})
    expect(insights[0].title).toContain('legacy generator bug, so the data are not comparable')
    expect(insights.map(entry => entry.id)).toContain('legacy-dataset')
    expect(insights.map(entry => entry.id)).toContain('autoscaling')
  })
})
