import {describe, expect, it} from 'vitest'
import type {Artifact, TimeSeriesDataset} from '../types'
import {aggregateMetric, type LoadedDataset} from './ResultsView'

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
