import {describe, expect, it} from 'vitest'
import {comparisonMetricCompatible, comparisonWarnings, selectableComparisonRuns} from './comparisons'
import {aggregatePlotMetric, variantStatistics, type LoadedDataset} from './components/ResultsView'
import type {Experiment, TimeSeriesDataset} from './types'

function dataset(id: string, values: number[]): LoadedDataset {
  return {artifact: {id, operationId: `op-${id}`, type: 'TimeSeriesDataset', version: 'v1alpha1', sensitive: false, verifiedAt: '2026-10-01T00:00:00Z'} as LoadedDataset['artifact'], label: id, dataset: {apiVersion: 'kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: id, version: '1'}, spec: {applicationRef: 'mubench', clusterServer: 'cluster', namespace: 'experiment', start: '2026-10-01T00:00:00Z', workloadStartedAt: '2026-10-01T00:00:00Z', end: '2026-10-01T00:02:00Z', stepSeconds: 60, source: {kind: 'prometheus', version: '1', profile: 'application'}, summary: {metrics: 1, samples: 2, series: 1}, series: [{metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {deployment: 'api'}, points: values.map((value, index) => ({timestamp: `2026-10-01T00:0${index + 1}:00Z`, value}))}]}} as TimeSeriesDataset}
}
const grouped = {id: 'suite', name: 'Suite', kind: 'suite', status: 'succeeded', variants: [{id: 'a', name: 'NAS', alias: 'NAS', color: '#123456', configuration: {}, trials: [{id: 'a1', position: 1, status: 'succeeded', resultArtifactId: 'a1'}, {id: 'a2', position: 2, status: 'succeeded', resultArtifactId: 'a2'}]}, {id: 'b', name: 'Netmarks', alias: 'Netmarks', color: '#abcdef', configuration: {}, trials: [{id: 'b1', position: 1, status: 'succeeded', resultArtifactId: 'b1'}]}]} as Experiment

describe('comparisons and suite regression', () => {
  it('uses exactly the same series, means, sample deviations and colors as a suite with unequal repetition counts', () => {
    const data = [dataset('a1', [10, 20]), dataset('a2', [30, 40]), dataset('b1', [100, 200])]
    const before = structuredClone(grouped)
    const comparison = {...structuredClone(grouped), id: 'comparison', kind: 'comparison' as const}
    expect(aggregatePlotMetric(data, 'cpu.cores', comparison)).toEqual(aggregatePlotMetric(data, 'cpu.cores', grouped))
    expect(variantStatistics(comparison, data, ['cpu.cores'])).toEqual(variantStatistics(grouped, data, ['cpu.cores']))
    const lines = aggregatePlotMetric(data, 'cpu.cores', comparison)
    expect(lines[0].points.map(point => point.value)).toEqual([20, 30])
    expect(lines[0].deviation?.[0].value).toBeCloseTo(Math.sqrt(200))
    expect(lines[1].points.map(point => point.value)).toEqual([100, 200])
    expect(lines[0].label).toBe('NAS')
    expect(lines[0].color).toBe('#123456')
    expect(grouped).toEqual(before)
  })
  it('retains missing-window behavior and never fills gaps with zeroes', () => {
    const data = [dataset('a1', [10, 20]), dataset('a2', [30]), dataset('b1', [100, 200])]
    expect(aggregatePlotMetric(data, 'cpu.cores', grouped)[0].points.map(point => point.value)).toEqual([20])
    data[1].dataset.spec.series = []
    expect(aggregatePlotMetric(data, 'cpu.cores', grouped).map(line => line.label)).toEqual(['Netmarks'])
  })
  it('warns about incompatible workloads and excludes metrics with conflicting units', () => {
    const a = dataset('a1', [1, 2]); const b = dataset('a2', [3, 4])
    expect(comparisonWarnings([a, b], {})).toEqual([])
    b.dataset.spec.stepSeconds = 30
    b.dataset.spec.applicationRef = 'redis'
    b.dataset.spec.series[0].unit = 'millicores'
    const warnings = comparisonWarnings([a, b], {a1: {components: [{capability: 'load.kubernetes', configuration: {rps: 10}}]}, a2: {components: [{capability: 'load.kubernetes', configuration: {rps: 20}}]}})
    expect(warnings.some(message => message.includes('resolution'))).toBe(true)
    expect(warnings.some(message => message.includes('Application'))).toBe(true)
    expect(warnings.some(message => message.includes('Load profiles'))).toBe(true)
    expect(comparisonMetricCompatible([a, b], 'cpu.cores')).toBe(false)
  })
  it('only offers successful runs with results from finished experiments', () => {
    expect(selectableComparisonRuns([grouped])).toHaveLength(3)
    expect(selectableComparisonRuns([{...grouped, status: 'running'}])).toEqual([])
    const failed = {...structuredClone(grouped), status: 'failed'}
    failed.variants[0].trials[0].status = 'failed'
    failed.variants[0].trials[1].resultArtifactId = ''
    expect(selectableComparisonRuns([failed]).map(run => run.trial.id)).toEqual(['b1'])
  })
})


it('checks metric units in linear time and ignores the number of pod series', () => {
  const a = dataset('a', [1])
  const b = dataset('b', [1])
  let reads = 0
  const series = a.dataset.spec.series[0]
  a.dataset.spec.series = Array.from({length: 2000}, () => ({...series, get metric() {reads++; return 'cpu.cores'}}))
  b.dataset.spec.series = [series]
  const warnings = comparisonWarnings([a, b], {})
  expect(warnings).toEqual([])
  expect(reads).toBeLessThan(12000)
  b.dataset.spec.series = [{...series, unit: 'seconds'}]
  expect(comparisonWarnings([a, b], {}).some(message => message.includes('inconsistent units'))).toBe(true)
})
