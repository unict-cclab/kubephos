import {describe, expect, it} from 'vitest'
import {addDerivedMetrics, aggregateMetric, aggregatePlotMetric, comparisonCSV, comparisonCSVHeader, counterRunTotal, knownWorkloads, legacyProvenanceMetrics, metricProvenance, podSchedulingStatistics, resolveWorkload, serviceMetricStatistics, statisticsCSV, statisticsCSVHeader, variantComparisons, variantEffectSizes, variantStatistics, workloadFromPodName, type LoadedDataset} from './resultsData'
import {studentTQuantile, summarize} from './statistics'
import type {Experiment, MetricSeries, TimeSeriesDataset} from './types'

// Deployment starts at 10:00, the workload at 10:01 and the measurement ends at 10:03.
const at = (seconds: number) => new Date(Date.parse('2026-11-01T10:00:00Z') + seconds * 1000).toISOString()
const before = [0, 30]
const inside = [60, 120, 180]
const after = [210]

function points(values: Record<number, number>) {
  return Object.entries(values).map(([seconds, value]) => ({timestamp: at(Number(seconds)), value}))
}

// Pre-workload and post-end samples are deliberately extreme so any leak changes every statistic.
function windowed(values: number[], noise = 1000) {
  return points(Object.fromEntries([...before.map(seconds => [seconds, noise]), ...inside.map((seconds, index) => [seconds, values[index]]), ...after.map(seconds => [seconds, noise])]))
}

function run(id: string, cpu: number[]): LoadedDataset {
  const loadDriver = (metric: string, unit: string, labels: Record<string, string>): MetricSeries => ({metric, unit, aggregation: 'sum', labels, points: windowed([50, 50, 50], 50)})
  const series: MetricSeries[] = [
    {metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {pod: 'api-5d8f7c9b6-abcde'}, points: windowed(cpu)},
    loadDriver('cpu.cores', 'cores', {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
    {metric: 'memory.working_set', unit: 'bytes', aggregation: 'sum', labels: {pod: 'api-5d8f7c9b6-abcde'}, points: windowed([10, 20, 30])},
    loadDriver('memory.working_set', 'bytes', {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
    {metric: 'pod.restarts', unit: 'count', aggregation: 'sum', labels: {pod: 'api-5d8f7c9b6-abcde'}, points: windowed([0, 0, 1])},
    loadDriver('pod.restarts', 'count', {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
    {metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels: {deployment: 'api'}, points: windowed([2, 2, 2], 0)},
    loadDriver('deployment.ready_replicas', 'replicas', {deployment: 'kubephos-load-default'}),
    {metric: 'load.configured_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {scenario: 'default'}, points: windowed([10, 20, 30], 0)},
    {metric: 'load.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: windowed([100, 300, 200], 900)},
    // Pods are mostly scheduled while the application deploys, before the workload starts.
    ...[0, 1000].map((value, index) => ({metric: 'scheduler.pod_scheduling_latency', unit: 'milliseconds', aggregation: 'mean' as const, labels: {pod_uid: `uid-${index}`, scheduled_at: at(10 + index * 10)}, points: [{timestamp: at(30), value}]})),
    {metric: 'scheduler.cycle_duration', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: points({0: 100, 30: 140, 60: 120})}
  ]
  const dataset: TimeSeriesDataset = {
    apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: `dev/${id}`, version: 'v1alpha1'},
    spec: {
      applicationRef: 'app:example', clusterServer: 'https://cluster.example', namespace: 'dev',
      start: at(0), workloadStartedAt: at(60), end: at(180), stepSeconds: 60,
      source: {kind: 'prometheus-v1', version: '1', profile: 'kubephos-experiment-evidence/v2'}, summary: {metrics: 1, series: series.length, samples: 0},
      controlMetrics: [
        {metric: 'scheduler.pod_scheduling_latency', status: 'available', scope: 'experiment', count: 2, mean: 500},
        {metric: 'scheduler.cycle_duration', status: 'available', scope: 'cluster', count: 3, mean: 120}
      ],
      series
    }
  }
  addDerivedMetrics(dataset, 250)
  return {artifact: {id, operationId: `op-${id}`, workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: id, sizeBytes: 1, sensitive: false, verifiedAt: at(0)}, dataset, label: id}
}

const suite = (...runs: LoadedDataset[]) => ({id: 'suite', name: 'Suite', kind: 'suite', status: 'succeeded', variants: [{id: 'approach', name: 'Approach', configuration: {}, trials: runs.map((item, index) => ({id: `t${index}`, position: index + 1, status: 'succeeded', resultArtifactId: item.artifact.id}))}]}) as unknown as Experiment
const values = (series: {points: {value: number}[]} | undefined) => series?.points.map(point => point.value)
const mean = (items: number[]) => items.reduce((sum, value) => sum + value, 0) / items.length
const csvRow = (csv: string, prefix: string) => csv.split('\n').find(line => line.startsWith(prefix))?.split(',')

describe('workload window', () => {
  it('excludes pre-workload and post-end samples from run statistics and plots alike', () => {
    const first = run('first', [1, 3, 5])
    const series = aggregateMetric([first], 'cpu.cores')[0]
    expect(values(series)).toEqual([1, 3, 5])
    expect(series.points.map(point => Date.parse(point.timestamp))).toEqual([0, 60000, 120000])
    expect(values(aggregatePlotMetric([first], 'cpu.cores')[0])).toEqual([1, 3, 5])
    expect(values(aggregatePlotMetric([first], 'cpu.cores', suite(first))[0])).toEqual([1, 3, 5])
  })

  it('computes across-run statistics from the same samples as the mean curve', () => {
    const first = run('first', [1, 3, 5])
    const second = run('second', [4, 6, 8])
    const experiment = suite(first, second)
    const curve = aggregatePlotMetric([first, second], 'cpu.cores', experiment)[0]
    expect(values(curve)).toEqual([2.5, 4.5, 6.5])
    const row = variantStatistics(experiment, [first, second], ['cpu.cores'])[0]
    expect(row.summary.count).toBe(2)
    expect(row.summary.mean).toBe(4.5)
    expect(row.summary.mean).toBeCloseTo(mean(values(curve)!))
  })

  it('exports the same samples in the run, service and across-run CSV rows', () => {
    const first = run('first', [1, 3, 5])
    const second = run('second', [4, 6, 8])
    const csv = statisticsCSV([first, second], ['cpu.cores'], suite(first, second))
    // count, mean, total, std, min, median, max
    expect(csvRow(csv, 'run,,first,cpu.cores,')?.slice(7, 14)).toEqual(['3', '3', '', '2', '1', '3', '5'])
    expect(csvRow(csv, 'service_run,first,first,cpu.cores,api,')?.slice(7, 14)).toEqual(['3', '3', '', '2', '1', '3', '5'])
    expect(csvRow(csv, 'across_runs,Approach,,cpu.cores,')?.slice(7, 9)).toEqual(['2', '4.5'])
    expect(csvRow(csv, 'service_across_runs,Approach,,cpu.cores,api,')?.slice(7, 9)).toEqual(['2', '4.5'])
  })

  it('restricts service statistics to the workload window', () => {
    const first = run('first', [1, 3, 5])
    const [row] = serviceMetricStatistics([first], 'cpu.cores').rows
    expect(row.service).toBe('api')
    expect(row.summary.count).toBe(3)
    expect(row.summary.mean).toBe(3)
    expect(row.summary.max).toBe(5)
  })

  it('trims configured input rate and SLO violations like the response time they derive from', () => {
    const first = run('first', [1, 3, 5])
    expect(values(aggregateMetric([first], 'load.configured_rps')[0])).toEqual([10, 20, 30])
    expect(values(aggregateMetric([first], 'load.p95_response_time')[0])).toEqual([100, 300, 200])
    expect(values(aggregateMetric([first], 'load.slo_violation_percentage')[0])).toEqual([0, 100, 0])
    const rows = variantStatistics(suite(first), [first], ['load.configured_rps', 'load.slo_violation_percentage'])
    expect(rows.map(row => row.summary.mean)).toEqual([20, 100 / 3])
  })
})

describe('legacy configured-rate end point', () => {
  // Legacy collectors sampled the profile every second and evaluated it at t = duration as 0.
  const legacy = (rates: [number, number, number]) => {
    const dataset = run('legacy', [1, 3, 5])
    dataset.dataset.spec.series.find(series => series.metric === 'load.configured_rps')!.points = points({60: rates[0], 120: rates[1], 180: rates[2]})
    return dataset
  }

  it('drops a final 0 exactly at the window end that follows a positive rate', () => {
    const dataset = legacy([175, 174, 0])
    expect(values(aggregateMetric([dataset], 'load.configured_rps')[0])).toEqual([175, 174])
    expect(variantStatistics(suite(dataset), [dataset], ['load.configured_rps'])[0].summary.mean).toBe(174.5)
  })

  it('keeps a profile that really is 0 before the end, or a 0 that is not the last window', () => {
    expect(values(aggregateMetric([legacy([175, 0, 0])], 'load.configured_rps')[0])).toEqual([175, 0, 0])
    const early = run('early', [1, 3, 5])
    early.dataset.spec.end = at(240)
    early.dataset.spec.series.find(series => series.metric === 'load.configured_rps')!.points = points({60: 10, 120: 20, 180: 0})
    expect(values(aggregateMetric([early], 'load.configured_rps')[0])).toEqual([10, 20, 0])
  })
})

describe('control metrics', () => {
  it('still count scheduling that happened before the workload started', () => {
    const first = run('first', [1, 3, 5])
    const latency = aggregateMetric([first], 'scheduler.pod_scheduling_latency')[0]
    expect(values(latency)).toEqual([0, 1000])
    expect(latency.points.map(point => Date.parse(point.timestamp))).toEqual([10000, 20000])
    expect(podSchedulingStatistics(first)?.count).toBe(2)
    expect(values(aggregateMetric([first], 'scheduler.cycle_duration')[0])).toEqual([100, 140, 120])
    const rows = variantStatistics(suite(first), [first], ['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration'])
    expect(rows.map(row => row.summary.mean)).toEqual([500, 120])
    const csv = statisticsCSV([first], ['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration'])
    expect(csvRow(csv, 'run,,first,scheduler.pod_scheduling_latency,')?.slice(6, 9)).toEqual(['available', '2', '500'])
    expect(csvRow(csv, 'run,,first,scheduler.cycle_duration,')?.slice(6, 9)).toEqual(['available', '3', '120'])
  })
})

describe('load generator', () => {
  it.each<[string, number[]]>([
    ['cpu.cores', [1, 3, 5]],
    ['memory.working_set', [10, 20, 30]],
    ['deployment.ready_replicas', [2, 2, 2]]
  ])('is excluded from application %s everywhere', (metric, expected) => {
    const first = run('first', [1, 3, 5])
    const second = run('second', [1, 3, 5])
    const experiment = suite(first, second)
    expect(values(aggregateMetric([first], metric)[0])).toEqual(expected)
    expect(values(aggregatePlotMetric([first, second], metric, experiment)[0])).toEqual(expected)
    expect(variantStatistics(experiment, [first, second], [metric])[0].summary.mean).toBeCloseTo(mean(expected))
    expect(Number(csvRow(statisticsCSV([first], [metric]), `run,,first,${metric},`)?.[8])).toBeCloseTo(mean(expected))
    expect(serviceMetricStatistics([first], metric).rows.map(row => row.service)).toEqual(['api'])
  })

  it('is excluded from application pod.restarts, which count their increase during the run', () => {
    const first = run('first', [1, 3, 5])
    const second = run('second', [1, 3, 5])
    const experiment = suite(first, second)
    expect(values(aggregateMetric([first], 'pod.restarts')[0])).toEqual([0, 0, 1])
    expect(values(aggregatePlotMetric([first, second], 'pod.restarts', experiment)[0])).toEqual([0, 0, 1])
    expect(variantStatistics(experiment, [first, second], ['pod.restarts'])[0].summary.mean).toBe(1)
    expect(Number(csvRow(statisticsCSV([first], ['pod.restarts']), 'run,,first,pod.restarts,')?.[9])).toBe(1)
    expect(serviceMetricStatistics([first], 'pod.restarts').rows.map(row => row.service)).toEqual(['api'])
  })
})

describe('pod restarts', () => {
  // Restarts are lifetime counters: only their increase during the workload belongs to the run.
  function withRestarts(id: string, restarts: Record<string, Record<number, number>>): LoadedDataset {
    const item = run(id, [1, 1, 1])
    item.dataset.spec.series = [...item.dataset.spec.series.filter(series => series.metric !== 'pod.restarts'), ...Object.entries(restarts).map(([pod, values]) => ({metric: 'pod.restarts', unit: 'count', aggregation: 'sum' as const, labels: {pod}, points: points(values)}))]
    return item
  }

  it('reports the increase during the run instead of the time-average of a lifetime counter', () => {
    // Crash-looped twice while deploying, then restarted once during the run.
    const item = withRestarts('crashloop', {'api-5d8f7c9b6-abcde': {0: 0, 30: 2, 60: 2, 120: 2, 180: 3}})
    expect(values(aggregateMetric([item], 'pod.restarts')[0])).toEqual([0, 0, 1])
    expect(counterRunTotal(item.dataset, 'pod.restarts')).toBe(1)
    expect(variantStatistics(suite(item), [item], ['pod.restarts'])[0].summary.mean).toBe(1)
    expect(csvRow(statisticsCSV([item], ['pod.restarts']), 'run,')?.slice(7, 10)).toEqual(['3', '', '1'])
  })

  it('counts pods created during the run from zero and keeps restarts of pods deleted before the end', () => {
    const item = withRestarts('churn', {
      'api-5d8f7c9b6-abcde': {60: 0, 120: 1},
      'api-5d8f7c9b6-fghij': {120: 1, 180: 2},
      'api-5d8f7c9b6-klmno': {60: 4, 120: 0, 180: 1}
    })
    // Deleted pod: 1. New pod: 2 since creation. Reset counter: 0 then 1 after the reset.
    expect(counterRunTotal(item.dataset, 'pod.restarts')).toBe(4)
    const first = withRestarts('first', {'api-5d8f7c9b6-abcde': {60: 5, 120: 5, 180: 6}})
    const second = withRestarts('second', {'api-5d8f7c9b6-abcde': {60: 0, 120: 2, 180: 3}})
    expect(variantStatistics(suite(first, second), [first, second], ['pod.restarts'])[0].summary.mean).toBe(2)
  })
})

describe('service grouping', () => {
  const cpu = (labels: Record<string, string>, value: number): MetricSeries => ({metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels, points: windowed([value, value, value])})
  const ready = (labels: Record<string, string>): MetricSeries => ({metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels, points: windowed([1, 1, 1], 1)})
  const withSeries = (series: MetricSeries[]) => {
    const item = run('services', [1, 1, 1])
    item.dataset.spec.series = series
    return item
  }
  const services = (item: LoadedDataset) => Object.fromEntries(serviceMetricStatistics([item], 'cpu.cores').rows.map(row => [row.service, row.summary.mean]))

  it('does not attribute the StatefulSet pod cart-db-0 to the Deployment cart (legacy dataset without workload labels)', () => {
    // Legacy ready_replicas only covered Deployments: cart-db is unknown.
    const item = withSeries([ready({deployment: 'cart'}), cpu({pod: 'cart-7d9f8b6c4-x2x9z'}, 1), cpu({pod: 'cart-db-0'}, 10), cpu({pod: 'cart-db-1'}, 20), cpu({pod: 'fluent-bit-x7k2p'}, 100)])
    expect(services(item)).toEqual({cart: 1, 'cart-db': 30, 'fluent-bit': 100})
    expect(serviceMetricStatistics([item], 'cpu.cores').unmatched).toBe(0)
  })

  it('matches the longest known workload whose suffix fits the controller pattern', () => {
    const known = knownWorkloads(withSeries([ready({deployment: 'cart'}), ready({statefulset: 'cart-db'})]).dataset)
    expect(known).toEqual([{name: 'cart-db', kind: 'StatefulSet'}, {name: 'cart', kind: 'Deployment'}])
    expect(workloadFromPodName('cart-db-0', known)).toEqual({name: 'cart-db', kind: 'StatefulSet', inferred: true})
    expect(workloadFromPodName('cart-7d9f8b6c4-x2x9z', known)).toEqual({name: 'cart', kind: 'Deployment', inferred: true})
    // A StatefulSet pod name never matches a Deployment's pattern and vice versa.
    expect(workloadFromPodName('cart-db-7d9f8b6c4-x2x9z', known)).toEqual({name: 'cart-db', kind: 'Deployment', inferred: true})
    expect(workloadFromPodName('cart-0', known)).toEqual({name: 'cart', kind: 'StatefulSet', inferred: true})
  })

  it('derives DaemonSet and unknown Deployment workloads by stripping controller suffixes', () => {
    expect(workloadFromPodName('fluent-bit-x7k2p', [])).toEqual({name: 'fluent-bit', inferred: true})
    expect(workloadFromPodName('payments-7d9f8b6c4-x2x9z', [])).toEqual({name: 'payments', kind: 'Deployment', inferred: true})
    // `exporter` is not a Kubernetes pod-template hash (it contains vowels).
    expect(workloadFromPodName('node-exporter-x7k2p', [])).toEqual({name: 'node-exporter', inferred: true})
    expect(workloadFromPodName('standalone', [])).toBeUndefined()
  })

  it('prefers the collector workload labels of new datasets', () => {
    const item = withSeries([
      ready({deployment: 'cart'}),
      cpu({pod: 'cart-db-0', workload: 'cart-db', workload_kind: 'StatefulSet'}, 10),
      cpu({pod: 'cart-7d9f8b6c4-x2x9z', workload: 'cart', workload_kind: 'Deployment'}, 1),
      cpu({pod: 'agent-x7k2p', workload: 'node-agent', workload_kind: 'DaemonSet'}, 5)
    ])
    expect(services(item)).toEqual({cart: 1, 'cart-db': 10, 'node-agent': 5})
    expect(serviceMetricStatistics([item], 'cpu.cores').rows.map(row => row.kind)).toEqual(['Deployment', 'StatefulSet', 'DaemonSet'])
    expect(resolveWorkload(item.dataset.spec.series[1], knownWorkloads(item.dataset))).toEqual({name: 'cart-db', kind: 'StatefulSet'})
    // Workload labels of one metric teach the pod-name fallback of another.
    expect(knownWorkloads(item.dataset).map(workload => workload.name)).toEqual(['node-agent', 'cart-db', 'cart'])
  })

  it('keeps excluding the node proxy and gateways from application latency', () => {
    const latency = (workload: string, value: number): MetricSeries => ({metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: workload}, points: windowed([value, value, value])})
    const item = withSeries([latency('cart', 10), latency('node-proxy', 1000), latency('gateway-ingress', 2000)])
    expect(serviceMetricStatistics([item], 'application.p95_response_time').rows.map(row => row.service)).toEqual(['cart'])
  })

  it('describes one run with an autocorrelation-aware CI and repetitions with plain n', () => {
    const item = withSeries([cpu({pod: 'cart-7d9f8b6c4-x2x9z'}, 1)])
    item.dataset.spec.series[0].points = windowed([1, 2, 3])
    const [row] = serviceMetricStatistics([item], 'cpu.cores').rows
    expect(row.summary.nEffective).toBeDefined()
    const second = run('second', [1, 1, 1])
    second.dataset.spec.series = [cpu({pod: 'cart-7d9f8b6c4-x2x9z'}, 5)]
    const across = serviceMetricStatistics([item, second], 'cpu.cores', suite(item, second)).rows[0]
    expect(across.acrossRuns).toBe(true)
    expect(across.summary).toEqual(summarize([2, 5]))
    expect(across.summary.nEffective).toBeUndefined()
  })
})

describe('unavailable samples', () => {
  // load.failure_percentage has no sample while no request is sent.
  function withFailures(id: string, values: Record<number, number>) {
    const item = run(id, [1, 1, 1])
    item.dataset.spec.series.push({metric: 'load.failure_percentage', unit: 'percent', aggregation: 'mean', labels: {}, points: points(values)})
    return item
  }

  it('averages only the available samples and never fills gaps with zero', () => {
    const item = withFailures('gaps', {60: 10, 180: 30})
    expect(values(aggregateMetric([item], 'load.failure_percentage')[0])).toEqual([10, 30])
    expect(variantStatistics(suite(item), [item], ['load.failure_percentage'])[0].summary.mean).toBe(20)
    expect(csvRow(statisticsCSV([item], ['load.failure_percentage']), 'run,,gaps,load.failure_percentage,')?.slice(6, 9)).toEqual(['available', '2', '20'])
  })

  it('reports a metric without samples as unavailable with empty values', () => {
    const item = run('silent', [1, 1, 1])
    const row = csvRow(statisticsCSV([item], ['load.failure_percentage']), 'run,,silent,load.failure_percentage,')
    expect(row?.[6]).toBe('unavailable')
    expect(row?.slice(7, 15).every(value => value === '')).toBe(true)
    expect(variantStatistics(suite(item), [item], ['load.failure_percentage'])).toEqual([])
  })
})

describe('statistics CSV', () => {
  it('appends provenance and CI columns after the historical ones', () => {
    expect(statisticsCSVHeader.slice(0, 15)).toEqual(['scope', 'strategy', 'dataset', 'metric', 'service', 'unit', 'status', 'count', 'mean', 'total', 'std', 'min', 'median', 'max', 'missing_runs'])
    expect(statisticsCSVHeader.slice(15)).toEqual(['source', 'vantage', 'ci95_low', 'ci95_high', 'n_effective', 'lag1_autocorrelation'])
    const first = run('first', [1, 3, 5])
    const second = run('second', [4, 6, 8])
    const csv = statisticsCSV([first, second], ['cpu.cores', 'load.p95_response_time', 'scheduler.cycle_duration'], suite(first, second))
    expect(csv.split('\n')[0]).toBe(statisticsCSVHeader.join(','))
    expect(csv.split('\n').every(line => line.split(',').length === statisticsCSVHeader.length)).toBe(true)
    expect(csvRow(csv, 'run,,first,cpu.cores,')?.slice(15, 17)).toEqual(['cadvisor', 'resource'])
    expect(csvRow(csv, 'run,,first,load.p95_response_time,')?.slice(15, 17)).toEqual(['locust', 'client'])
    expect(csvRow(csv, 'run,,first,scheduler.cycle_duration,')?.slice(15, 17)).toEqual(['kube-scheduler', 'control-plane'])
    // Across repetitions: plain Student t with n − 1 df, no effective sample size.
    const across = csvRow(csv, 'across_runs,Approach,,cpu.cores,')!
    const summary = summarize([3, 6])
    expect(Number(across[17])).toBeCloseTo(summary.mean - studentTQuantile(0.975, 1) * summary.standardDeviation / Math.sqrt(2), 10)
    expect(across[19]).toBe('')
    // Within one run: the CI uses n_effective.
    expect(csvRow(csv, 'run,,first,cpu.cores,')?.[19]).not.toBe('')
  })

  it('is deterministic', () => {
    const first = run('first', [1, 3, 5])
    expect(statisticsCSV([first], ['cpu.cores', 'memory.working_set'], suite(first))).toBe(statisticsCSV([run('first', [1, 3, 5])], ['cpu.cores', 'memory.working_set'], suite(first)))
  })
})

describe('provenance', () => {
  it('reads the dataset catalog when present', () => {
    const item = run('new', [1, 1, 1])
    item.dataset.spec.rateWindowSeconds = 30
    item.dataset.spec.metrics = [{metric: 'load.p95_response_time', source: 'locust', vantage: 'client', collector: 'prometheus', query: 'histogram_quantile(0.95, ...)', rateWindowSeconds: 30, description: 'p95 from Locust'}]
    expect(metricProvenance(item.dataset, 'load.p95_response_time')).toEqual({source: 'locust', vantage: 'client', collector: 'prometheus', query: 'histogram_quantile(0.95, ...)', rateWindowSeconds: 30, description: 'p95 from Locust', legacy: false})
    // Metrics missing from the catalog fall back, using the dataset's rate window.
    expect(metricProvenance(item.dataset, 'cpu.cores')).toMatchObject({source: 'cadvisor', vantage: 'resource', rateWindowSeconds: 30, legacy: true})
  })

  it('covers every known metric id for legacy datasets', () => {
    const item = run('legacy', [1, 1, 1])
    const expected: Record<string, [string, string]> = {
      'load.p95_response_time': ['locust', 'client'], 'load.failure_percentage': ['locust', 'client'], 'load.configured_rps': ['kubephos', 'configuration'],
      'load.slo_violation_percentage': ['kubephos', 'client'], 'application.p95_response_time': ['istio', 'server'], 'application.request_rps': ['istio', 'server'],
      'cpu.cores': ['cadvisor', 'resource'], 'memory.working_set': ['cadvisor', 'resource'], 'pod.restarts': ['kube-state-metrics', 'resource'],
      'deployment.ready_replicas': ['kube-state-metrics', 'resource'], 'network.rtt': ['mentat', 'network'], 'scheduler.pod_scheduling_latency': ['kube-scheduler', 'control-plane'],
      'descheduler.evictions': ['descheduler', 'control-plane'], 'redis.operation_rps': ['locust', 'client'], 'redis.operation_p95_response_time': ['locust', 'client']
    }
    for (const [metric, [source, vantage]] of Object.entries(expected)) expect(metricProvenance(item.dataset, metric)).toMatchObject({source, vantage, legacy: true})
    for (const metric of legacyProvenanceMetrics) expect(metricProvenance(item.dataset, metric).description).not.toBe('')
    expect(metricProvenance(item.dataset, 'load.p95_response_time').rateWindowSeconds).toBe(60)
    expect(metricProvenance(item.dataset, 'memory.working_set').rateWindowSeconds).toBeUndefined()
  })

  it('attributes inter-cluster network metrics of Cilium datasets to cilium', () => {
    const item = run('cilium', [1, 1, 1])
    item.dataset.spec.series.push({metric: 'network.rtt', unit: 'milliseconds', aggregation: 'mean', labels: {origin_cluster: 'a', destination_cluster: 'b'}, points: windowed([1, 1, 1])})
    expect(metricProvenance(item.dataset, 'network.rtt').source).toBe('cilium')
  })
})

describe('variant comparisons', () => {
  // cpu.cores run means equal the requested values (constant series).
  const experiment = (groups: number[][]) => {
    const datasets = groups.flatMap((group, variant) => group.map((value, index) => run(`v${variant}-${index}`, [value, value, value])))
    const definition = {id: 'experiment', name: 'Experiment', kind: 'suite', status: 'succeeded', variants: groups.map((group, variant) => ({id: `v${variant}`, name: `V${variant}`, configuration: {}, trials: group.map((_, index) => ({id: `t${variant}-${index}`, status: 'succeeded', resultArtifactId: `v${variant}-${index}`}))}))} as unknown as Experiment
    return {datasets, definition}
  }

  it('compares every variant with the baseline on run-level values and Holm-adjusts the whole table', () => {
    const {datasets, definition} = experiment([[12.1, 13.4, 11.8, 12.9, 13.0], [14.2, 15.1, 13.8, 14.9], [12, 13, 12.5]])
    const rows = variantComparisons(definition, datasets, ['cpu.cores', 'memory.working_set'])
    expect(rows.map(row => `${row.metric}:${row.baseline}->${row.group}`)).toEqual(['cpu.cores:V0->V1', 'cpu.cores:V0->V2', 'memory.working_set:V0->V1', 'memory.working_set:V0->V2'])
    // scipy.stats.ttest_ind(V1, V0, equal_var=False).pvalue
    expect(rows[0].pValue).toBeCloseTo(0.003436467554137229, 12)
    expect(rows[0].unit).toBe('cores')
    // memory.working_set is identical (20) in every run: zero variance, no p-value.
    expect(rows[2]).toMatchObject({zeroVariance: true, difference: 0})
    expect(rows[2].pValue).toBeUndefined()
    expect(rows[2].adjustedPValue).toBeUndefined()
    expect(rows.every(row => row.familySize === 2)).toBe(true)
    expect(rows[0].adjustedPValue).toBeCloseTo(Math.min(1, 2 * rows[0].pValue!), 12)
    expect(rows[1].insufficient).toBe(false)
    expect(variantComparisons(definition, datasets, ['cpu.cores'], {baseline: 'v1'}).map(row => row.group)).toEqual(['V0', 'V2'])
    expect(variantComparisons(definition, datasets, ['cpu.cores'], {mode: 'all-pairs'})).toHaveLength(3)
    const csv = comparisonCSV(rows)
    expect(csv.split('\n')[0]).toBe(comparisonCSVHeader.join(','))
    expect(csv.split('\n')).toHaveLength(5)
  })

  it('flags fewer than three repetitions as insufficient', () => {
    const {datasets, definition} = experiment([[1, 2], [3, 4]])
    const [row] = variantComparisons(definition, datasets, ['cpu.cores'])
    expect(row.insufficient).toBe(true)
    expect(row.pValue).toBeDefined()
  })

  it('keeps the legacy two-variant effect size table working', () => {
    const {datasets, definition} = experiment([[1, 2, 3], [2, 4, 6]])
    const [effect] = variantEffectSizes(definition, datasets, ['cpu.cores'])
    // Cohen's d = (4 − 2) / sqrt((1 + 4) / 2)
    expect(effect).toMatchObject({metric: 'cpu.cores', left: 'V0', right: 'V1', leftCount: 3, rightCount: 3})
    expect(effect.value).toBeCloseTo(2 / Math.sqrt(2.5), 12)
    expect(effect.hedgesG).toBeLessThan(effect.value)
    expect(variantEffectSizes(experiment([[1, 2], [3, 4], [5, 6]]).definition, datasets, ['cpu.cores'])).toEqual([])
  })
})

describe('long runs', () => {
  // 8 pods × 20,000 samples: more values than Math.min(...spread) accepts (RangeError).
  it('totals pod restarts and infers the plot step without spreading every sample into Math.min', () => {
    const item = run('long', [1, 2, 3])
    const count = 20000
    const long = (pod: string, value: number): MetricSeries => ({metric: 'pod.restarts', unit: 'count', aggregation: 'sum', labels: {pod}, points: Array.from({length: count}, (_, index) => ({timestamp: at(60 + index), value}))})
    item.dataset.spec.end = at(60 + count)
    item.dataset.spec.series = [...item.dataset.spec.series.filter(series => series.metric !== 'pod.restarts'), ...Array.from({length: 8}, (_, index) => long(`api-5d8f7c9b6-pod${index}`, 2))]
    expect(counterRunTotal(item.dataset, 'pod.restarts')).toBe(0)
    const second = {...item, artifact: {...item.artifact, id: 'long-2'}, dataset: {...item.dataset, spec: {...item.dataset.spec, stepSeconds: 0}}}
    const first = {...item, dataset: {...item.dataset, spec: {...item.dataset.spec, stepSeconds: 0}}}
    const [curve] = aggregatePlotMetric([first, second], 'pod.restarts', suite(first, second))
    expect(curve.stepMilliseconds).toBe(1000)
    expect(curve.points).toHaveLength(count)
  })
})

describe('gaps', () => {
  it('gives single-run curves their sampling step so plots do not bridge missing windows', () => {
    const item = run('gap', [1, 2, 3])
    expect(aggregateMetric([item], 'load.p95_response_time')[0].stepMilliseconds).toBe(60000)
    expect(aggregatePlotMetric([item], 'load.p95_response_time', suite(item))[0].stepMilliseconds).toBe(60000)
  })
})
