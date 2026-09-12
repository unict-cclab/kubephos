import {describe, expect, it} from 'vitest'
import type {Artifact, Experiment, TimeSeriesDataset} from '../types'
import {aggregateMetric, cohenD, experimentProgress, summarize, variantStatistics, type LoadedDataset} from './ResultsView'

const artifact: Artifact = {id: 'art_dataset', operationId: 'op_collect', workspaceId: 'ws_dev', name: 'dataset', type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:value', sizeBytes: 100, sensitive: false, verifiedAt: '2026-09-09T10:00:00Z'}

describe('aggregateMetric', () => {
  it('combines labeled series per dataset and preserves dataset boundaries', () => {
    const left = loaded('left', [[1, 2], [3, 4]])
    const right = loaded('right', [[5, 6], [7, 8]])
    const result = aggregateMetric([left, right], 'cpu.cores')
    expect(result).toHaveLength(2)
    expect(result[0].points.map(point => point.value)).toEqual([4, 6])
    expect(result[1].points.map(point => point.value)).toEqual([12, 14])
  })

  it('omits datasets without the selected metric', () => {
    const value = loaded('empty', [[1, 2]])
    value.dataset.spec.series[0].metric = 'memory.working_set'
    expect(aggregateMetric([value], 'cpu.cores')).toEqual([])
  })
})

describe('experimentProgress', () => {
  it('counts every terminal trial across variants', () => {
    const experiment = {variants: [
      {trials: [{status: 'succeeded'}, {status: 'running'}]},
      {trials: [{status: 'failed'}, {status: 'canceled'}, {status: 'queued'}]}
    ]} as Experiment
    expect(experimentProgress(experiment)).toEqual({completed: 3, total: 5})
  })
})

describe('summarize', () => {
  it('calculates sample statistics and interpolated percentiles', () => {
    const result = summarize([1, 2, 3, 4, 5])
    expect(result.count).toBe(5)
    expect(result.mean).toBe(3)
    expect(result.standardDeviation).toBeCloseTo(Math.sqrt(2.5))
    expect(result.min).toBe(1)
    expect(result.max).toBe(5)
    expect(result.median).toBe(3)
    expect(result.p95).toBeCloseTo(4.8)
    expect(result.confidence95Low).toBeLessThan(result.mean)
    expect(result.confidence95High).toBeGreaterThan(result.mean)
  })
})

describe('variantStatistics', () => {
  it('aggregates run means per strategy and reports missing runs', () => {
    const first = loaded('one', [[1, 3]])
    const second = loaded('two', [[3, 5]])
    const experiment = {variants: [{name: 'Strategy A', trials: [{resultArtifactId: first.artifact.id}, {resultArtifactId: second.artifact.id}, {resultArtifactId: ''}]}]} as Experiment
    const result = variantStatistics(experiment, [first, second], ['cpu.cores'])
    expect(result).toHaveLength(1)
    expect(result[0].summary.mean).toBe(3)
    expect(result[0].summary.count).toBe(2)
    expect(result[0].missing).toBe(1)
  })
})

describe('cohenD', () => {
  it('reports a standardized difference using pooled sample variance', () => {
    expect(cohenD([1, 2, 3], [3, 4, 5])).toBeCloseTo(2)
    expect(cohenD([2, 2], [2, 2])).toBe(0)
  })
})

function loaded(label: string, values: number[][]): LoadedDataset {
  const timestamps = ['2026-09-09T09:59:45Z', '2026-09-09T10:00:00Z']
  const dataset: TimeSeriesDataset = {
    apiVersion: 'artifacts.kubephos.dev/v1alpha1',
    kind: 'TimeSeriesDataset',
    metadata: {name: 'dev/workload-overview', version: 'v1alpha1'},
    spec: {
      applicationRef: 'app:example@1.0.0',
      clusterServer: 'https://10.0.0.1:6443',
      namespace: 'dev',
      start: '2026-09-09T09:55:00Z',
      end: '2026-09-09T10:00:00Z',
      stepSeconds: 15,
      source: {kind: 'prometheus-v1', version: '86.0.0', profile: 'kubernetes-workload-overview/v1'},
      summary: {metrics: 1, series: 2, samples: 4},
      series: values.map((seriesValues, index) => ({metric: 'cpu.cores', unit: 'cores', labels: {pod: `pod-${index}`}, points: seriesValues.map((value, point) => ({timestamp: timestamps[point], value}))}))
    }
  }
  return {artifact: {...artifact, id: `art_${label}`}, dataset, label}
}
