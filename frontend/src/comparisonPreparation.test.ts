import {expect, it, vi} from 'vitest'
import {comparisonTransferables, maxServicePoints, packSeries, prepareComparison, unpackSeries, type ComparisonJob} from './comparisonPreparation'
import {confidenceBand} from './components/results/overview'
import {aggregatePlotMetric, statisticsCSV, variantStatistics, type LoadedDataset} from './resultsData'
import type {Experiment, TimeSeriesDataset} from './types'

function source(id: string, values: number[]): LoadedDataset {
  return {artifact: {id, name:id, digest:id, type:'TimeSeriesDataset', version:'v1alpha1', sensitive:false} as LoadedDataset['artifact'], label:id, dataset:{apiVersion:'artifacts.kubephos.dev/v1alpha1',kind:'TimeSeriesDataset',metadata:{name:id,version:'v1alpha1'},spec:{applicationRef:'app',clusterServer:'https://cluster.example',source:{kind:'prometheus',version:'v1',profile:'application'},summary:{metrics:1,samples:values.length,series:1},namespace:'test',start:'2026-11-11T00:00:00Z',end:'2026-11-11T00:02:00Z',stepSeconds:60,series:[{metric:'cpu.cores',unit:'cores',aggregation:'sum',labels:{deployment:'api',pod:'raw-pod-label'},points:values.map((value,i)=>({timestamp:`2026-11-11T00:0${i}:00Z`,value}))}]}} as TimeSeriesDataset}
}
it('prepares the identical one-versus-three-run curves, statistics, colors and export outside the renderer', async () => {
  const datasets = [source('single',[10,20]), source('a',[2,4]), source('b',[4,6]), source('c',[6,8])]
  const experiment = {id:'comparison',kind:'comparison',variants:[{id:'one',name:'Same',alias:'Same',color:'#123456',trials:[{resultArtifactId:'single'}]},{id:'many',name:'Same',alias:'Same',color:'#abcdef',trials:datasets.slice(1).map(item=>({resultArtifactId:item.artifact.id}))}]} as Experiment
  const job: ComparisonJob = {artifacts:datasets.map(item=>item.artifact),experiment,configurations:{}}
  const progress=vi.fn(), partial=vi.fn()
  const result = await prepareComparison(job,new AbortController().signal,progress,async artifact=>structuredClone(datasets.find(item=>item.artifact.id===artifact.id)!.dataset),partial)
  const presented = datasets.map(item=>({...item,label:'Same'}))
  expect(result.plots['cpu.cores']).toEqual(aggregatePlotMetric(presented,'cpu.cores',experiment))
  expect(result.columns.map(column=>column.rows)).toEqual(experiment.variants.map(variant=>variantStatistics({...experiment,variants:[variant]},presented,['cpu.cores'])))
  expect(result.services['cpu.cores'].rows.map(row=>row.groupID)).toEqual(['one','many'])
  expect(result.csv).toBe(statisticsCSV(presented,['cpu.cores'],experiment))
  expect(result.complete).toBe(true)
  expect(partial).toHaveBeenCalledWith(expect.objectContaining({complete:false,plots:{},csv:''}))
  expect(progress.mock.calls.map(([stage])=>stage)).toEqual(['Loading results…','Preparing statistics…','Preparing plots…'])
  expect(JSON.stringify(result)).not.toContain('raw-pod-label')
  expect(result.columns.every(column=>column.variant.trials.length===0)).toBe(true)
})
it('stops preparation when the result view is abandoned', async () => {
  const item=source('one',[1,2])
  const controller=new AbortController()
  const job={artifacts:[item.artifact],experiment:{variants:[{id:'one',trials:[{resultArtifactId:'one'}]}]} as Experiment,configurations:{}}
  await expect(prepareComparison(job,controller.signal,()=>{},async()=>{controller.abort();return item.dataset})).rejects.toMatchObject({name:'AbortError'})
})

it('prepares worker statistics, plots and CSV from the workload window without the load generator', async () => {
  const datasets = [source('w1',[100,2,4]), source('w2',[100,6,8])]
  for (const item of datasets) {
    item.dataset.spec.workloadStartedAt = '2026-11-11T00:01:00Z'
    item.dataset.spec.series.push({metric:'cpu.cores',unit:'cores',aggregation:'sum',labels:{pod:'kubephos-load-default-7c9b6d8f5-xyz12'},points:item.dataset.spec.series[0].points.map(point=>({...point,value:50}))})
  }
  const experiment = {id:'window',kind:'comparison',variants:[{id:'w1',name:'First',trials:[{resultArtifactId:'w1'}]},{id:'w2',name:'Second',trials:[{resultArtifactId:'w2'}]}]} as Experiment
  const result = await prepareComparison({artifacts:datasets.map(item=>item.artifact),experiment,configurations:{}},new AbortController().signal,()=>{},async artifact=>structuredClone(datasets.find(item=>item.artifact.id===artifact.id)!.dataset))
  expect(result.plots['cpu.cores'].map(line=>line.points.map(point=>point.value))).toEqual([[2,4],[6,8]])
  expect(result.columns.map(column=>column.rows[0].summary.mean)).toEqual([3,7])
  expect(result.services['cpu.cores'].rows.map(row=>[row.service,row.summary.mean])).toEqual([['api',3],['api',7]])
  expect(result.csv).toContain('run,,First,cpu.cores,,cores,available,2,3,')
  expect(result.csv).toContain('run,,Second,cpu.cores,,cores,available,2,7,')
})

it('keeps every latency percentile, client and server, in comparisons and returns provenance and run-level values', async () => {
  const datasets = [source('x1',[1,2]), source('x2',[3,4])]
  for (const item of datasets) for (const metric of ['load.p50_response_time','load.p99_response_time','load.mean_response_time','application.p50_response_time','application.p99_response_time']) item.dataset.spec.series.push({metric,unit:'milliseconds',aggregation:'mean',labels:{},points:item.dataset.spec.series[0].points})
  const experiment = {id:'all',kind:'comparison',variants:[{id:'x1',name:'First',trials:[{resultArtifactId:'x1'}]},{id:'x2',name:'Second',trials:[{resultArtifactId:'x2'}]}]} as Experiment
  const result = await prepareComparison({artifacts:datasets.map(item=>item.artifact),experiment,configurations:{},comparison:{baseline:'x2'}},new AbortController().signal,()=>{},async artifact=>structuredClone(datasets.find(item=>item.artifact.id===artifact.id)!.dataset))
  expect(result.statisticsMetrics).toEqual(expect.arrayContaining(['load.p50_response_time','load.p99_response_time','load.mean_response_time','application.p50_response_time','application.p99_response_time']))
  expect(result.provenance['application.p99_response_time']).toMatchObject({source:'istio',vantage:'server',legacy:true})
  expect(result.comparisons.every(row=>row.baseline==='Second')).toBe(true)
  expect(result.runLevel.find(entry=>entry.metric==='cpu.cores')?.groups.map(group=>group.values)).toEqual([[1.5],[3.5]])
})

// Two series (A: two repetitions, B: one) of a small mesh, 900 samples at 15 s per run.
function meshRun(id: string, p95: number, restarts: number[]): LoadedDataset {
  const start = Date.parse('2026-11-11T00:00:00Z')
  const count = 900
  const points = (value: (index: number) => number) => Array.from({length: count}, (_, index) => ({timestamp: new Date(start + index * 15000).toISOString(), value: value(index)}))
  const series: TimeSeriesDataset['spec']['series'] = [
    {metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: 'rec'}, points: points(() => p95)},
    {metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: 'cart'}, points: points(() => 40)},
    {metric: 'application.request_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {destination_workload: 'rec'}, points: points(() => 120)},
    {metric: 'application.request_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {destination_workload: 'cart'}, points: points(() => 30)},
    {metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {workload: 'rec', pod: 'rec-1'}, points: points(index => index % 2 ? .19 : .21)},
    {metric: 'cpu.limit_cores', unit: 'cores', aggregation: 'sum', labels: {workload: 'rec', pod: 'rec-1'}, points: points(() => .2)},
    {metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels: {deployment: 'rec'}, points: points(() => 1)},
    {metric: 'pod.restarts', unit: 'count', aggregation: 'sum', labels: {workload: 'rec', pod: 'rec-1'}, points: points(index => restarts[Math.min(restarts.length - 1, Math.floor(index / (count / restarts.length)))])}
  ]
  const item = source(id, [1])
  item.dataset.spec.series = series
  item.dataset.spec.stepSeconds = 15
  item.dataset.spec.end = new Date(start + (count - 1) * 15000).toISOString()
  return item
}

it('prepares per-series services, restarts as run increase and down-sampled curves with CI for the services table', async () => {
  const datasets = [meshRun('a1', 2000, [5, 5, 6, 7]), meshRun('a2', 2400, [3, 3, 3, 3]), meshRun('b1', 50, [0, 0, 0, 0])]
  const experiment = {id: 'mesh', kind: 'comparison', variants: [{id: 'A', name: 'Limited', trials: [{resultArtifactId: 'a1'}, {resultArtifactId: 'a2'}]}, {id: 'B', name: 'Unlimited', trials: [{resultArtifactId: 'b1'}]}]} as Experiment
  const run = () => prepareComparison({artifacts: datasets.map(item => item.artifact), experiment, configurations: {}}, new AbortController().signal, () => {}, async artifact => structuredClone(datasets.find(item => item.artifact.id === artifact.id)!.dataset))
  const partial = vi.fn()
  const result = await prepareComparison({artifacts: datasets.map(item => item.artifact), experiment, configurations: {}}, new AbortController().signal, () => {}, async artifact => structuredClone(datasets.find(item => item.artifact.id === artifact.id)!.dataset), partial)
  // Services are ready with the statistics; curves follow with the plots.
  expect(partial.mock.calls[0][0].overview.series.map((entry: {id: string; curves?: unknown}) => [entry.id, entry.curves])).toEqual([['A', undefined], ['B', undefined]])
  const [a, b] = result.overview!.series
  expect([a.id, b.id]).toEqual(['A', 'B'])
  const rec = a.services.find(row => row.service === 'rec')!
  expect(a.services[0].service).toBe('rec')
  expect(rec.bottleneck).toBe(true)
  expect(rec.p95Ms).toBeCloseTo(2200)
  expect(rec.rps).toBeCloseTo(120)
  expect(rec.cpuCores).toBeCloseTo(.2)
  expect(rec.cpuLimitCores).toBeCloseTo(.2)
  expect(rec.replicas).toBe(1)
  // Increase during the run (2 and 0), averaged over the series' repetitions.
  expect(rec.restarts).toBe(1)
  expect(b.services.find(row => row.service === 'rec')).toMatchObject({p95Ms: 50, restarts: 0, bottleneck: false})
  expect(result.overview!.insights.map(item => item.id)).toEqual(expect.arrayContaining(['bottleneck', 'restarts']))
  // Mean and across-run deviation per point, down-sampled to ≤ 300 points on whole sampling steps.
  const curve = a.curves!.rec['application.p95_response_time']
  expect(curve.t).toBeInstanceOf(Float64Array)
  expect(curve.v.length).toBeLessThanOrEqual(maxServicePoints)
  expect(curve.v.length).toBe(225)
  expect(curve.step).toBe(60000)
  expect(curve.runs).toBe(2)
  expect([...curve.v].every(value => Math.abs(value - 2200) < 1e-3)).toBe(true)
  expect(curve.sd![0]).toBeCloseTo(Math.SQRT2 * 200, 2)
  expect(Object.keys(a.curves!.rec)).toEqual(['application.p95_response_time', 'application.request_rps', 'cpu.cores', 'deployment.ready_replicas'])
  expect(a.curves!.rec['cpu.cores'].v[0]).toBeCloseTo(.2, 5)
  const single = b.curves!.rec['application.p95_response_time']
  expect(single.runs).toBeUndefined()
  expect(single.sd).toBeUndefined()
  const unpacked = unpackSeries(curve)
  expect(confidenceBand(unpacked)![0][0]).toBeCloseTo(2200 - 282.84 * 12.706 / Math.SQRT2, 0)
  expect(comparisonTransferables(result)).toHaveLength(Object.values(a.curves!).concat(Object.values(b.curves!)).reduce((sum, curves) => sum + Object.values(curves).reduce((count, item) => count + (item.sd ? 3 : 2), 0), 0))
  // Deterministic.
  expect(await run()).toEqual(result)
})

it('down-samples by averaging whole-step time bins and keeps short curves untouched', () => {
  const points = Array.from({length: 1000}, (_, index) => ({timestamp: new Date(index * 1000).toISOString(), value: index}))
  const packed = packSeries({artifactID: 'x', label: 'x', unit: 'cores', points, stepMilliseconds: 1000})
  expect(packed.v.length).toBeLessThanOrEqual(300)
  expect(packed.step).toBe(4000)
  expect([...packed.v.slice(0, 2)]).toEqual([1.5, 5.5])
  expect([...packed.t.slice(0, 2)]).toEqual([2000, 6000])
  const short = packSeries({artifactID: 'x', label: 'x', unit: 'cores', points: points.slice(0, 3), deviation: points.slice(0, 3), runs: 3, stepMilliseconds: 1000})
  expect(unpackSeries(short)).toEqual({artifactID: 'x', label: 'x', unit: 'cores', points: points.slice(0, 3), deviation: points.slice(0, 3), runs: 3, stepMilliseconds: 1000})
})
