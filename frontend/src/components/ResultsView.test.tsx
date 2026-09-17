import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Artifact, Experiment, Operation, Session, TimeSeriesDataset} from '../types'
import {aggregateMetric, cohenD, experimentProgress, niceAxis, ResultsView, setPNGResolution, summarize, svgToPDF, variantStatistics, type LoadedDataset} from './ResultsView'

vi.mock('../api', () => ({request: vi.fn(async () => ({items: []}))}))

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

  it('averages gauge series when requested by the dataset contract', () => {
    const value = loaded('network', [[10, 20], [30, 40]])
    value.dataset.spec.series.forEach(series => { series.aggregation = 'mean' })
    expect(aggregateMetric([value], 'cpu.cores')[0].points.map(point => point.value)).toEqual([20, 30])
  })

  it('propagates the suite plot color into every aggregated series', () => {
    const value = {...loaded('strategy', [[1, 2]]), color: '#345678'}
    expect(aggregateMetric([value], 'cpu.cores')[0].color).toBe('#345678')
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

describe('focused experiment results', () => {
  it('shows planned runs and a contextual empty state while runs are active', () => {
    const experiment = {id: 'exp_01', name: 'Scheduler comparison', description: '', status: 'running', resultType: 'TimeSeriesDataset', resultVersion: 'v1alpha1', variants: [{id: 'variant_01', name: 'Baseline', configuration: {components: [{capability: 'load.kubernetes', configuration: {steps: [{type: 'constant', durationSeconds: 60, rps: 20}]}}]}, trials: [{id: 'trial_01', position: 1, status: 'running', resultArtifactId: '', operationId: 'op_load'}, {id: 'trial_02', position: 2, status: 'queued', resultArtifactId: '', operationId: ''}]}]} as unknown as Experiment
    const operation = {id: 'op_load', pluginId: 'io.kubephos.load.kubernetes.session', status: 'running', createdAt: new Date().toISOString()} as Operation
    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[operation]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect(screen.getByLabelText('0 of 2 runs finished')).toBeInTheDocument()
    expect(screen.getByText('Runs in progress')).toBeInTheDocument()
    expect(screen.getByText('Results and plots appear here as runs finish.')).toBeInTheDocument()
    expect(screen.getByText('Baseline · Run 1', {selector: '.run-load-progress-head strong'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Baseline · Run 1'}))
    expect(screen.getByRole('button', {name: 'Live logs'})).toBeInTheDocument()
    expect(screen.getAllByText('Load running').length).toBeGreaterThan(0)
    expect(screen.getByText(/of 1m · 20 RPS/)).toBeInTheDocument()
  })

  it('stops an active suite from its detail page', async () => {
    vi.mocked(request).mockResolvedValue({items: []} as never)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const changed = vi.fn(async () => {})
    const experiment = {id: 'exp_stop', kind: 'suite', name: 'Scheduler suite', description: '', status: 'running', variants: [{id: 'variant_stop', name: 'Baseline', trials: [{id: 'trial_stop', position: 1, status: 'running', resultArtifactId: '', operationId: 'op_stop'}]}]} as unknown as Experiment

    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'csrf', user: {role: 'admin'}} as Session} changed={changed} openOperation={() => {}} focusedExperiment={experiment} />)
    fireEvent.click(screen.getByRole('button', {name: 'Stop suite'}))

    await waitFor(() => expect(request).toHaveBeenCalledWith('/experiments/exp_stop/cancel', {method: 'POST', body: '{}'}, 'csrf'))
    expect(await screen.findByRole('button', {name: 'Stopping suite…'})).toBeDisabled()
    expect(changed).toHaveBeenCalled()
    confirm.mockRestore()
  })

  it('separates aggregate results from the individual run tabs', async () => {
    const first = loaded('one', [[1, 3]])
    const second = loaded('two', [[3, 5]])
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : path.includes(first.artifact.id) ? first.dataset as never : second.dataset as never)
    const experiment = {id: 'exp_02', name: 'Scheduler comparison', description: '', status: 'succeeded', resultType: 'TimeSeriesDataset', resultVersion: 'v1alpha1', variants: [{id: 'variant_01', name: 'Baseline', trials: [{id: 'trial_01', position: 1, status: 'succeeded', resultArtifactId: first.artifact.id, operationId: 'op_01'}, {id: 'trial_02', position: 2, status: 'succeeded', resultArtifactId: second.artifact.id, operationId: 'op_02'}]}]} as Experiment
    render(<ResultsView artifacts={[first.artifact, second.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect(screen.getByRole('button', {name: 'Run summary'})).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByText('Key outcomes')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Statistics'}))
    expect(await screen.findByText('Across runs')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Baseline · Run 1'}))
    expect(screen.getByRole('button', {name: 'Baseline · Run 1'})).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByText('Across runs')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /logs/i})).not.toBeInTheDocument()
  })

  it('adds a newly completed run to the aggregate view automatically', async () => {
    const first = loaded('new_one', [[1, 3]])
    const second = loaded('new_two', [[3, 5]])
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : path.includes(first.artifact.id) ? first.dataset as never : second.dataset as never)
    const experiment = {id: 'exp_03', name: 'Load test', status: 'running', variants: [{id: 'variant_03', name: 'Default', trials: [{id: 'trial_11', position: 1, status: 'succeeded', resultArtifactId: first.artifact.id, operationId: 'op_11'}, {id: 'trial_12', position: 2, status: 'running', resultArtifactId: '', operationId: 'op_12'}]}]} as Experiment
    const props = {experiments: [experiment], operations: [], workspaces: [], session: {authenticated: true} as Session, changed: async () => {}, openOperation: () => {}}
    const view = render(<ResultsView {...props} artifacts={[first.artifact]} focusedExperiment={experiment} />)
    expect(await screen.findByText('Key outcomes')).toBeInTheDocument()
    const completed = {...experiment, variants: [{...experiment.variants[0], trials: [experiment.variants[0].trials[0], {...experiment.variants[0].trials[1], status: 'succeeded', resultArtifactId: second.artifact.id}]}]} as Experiment
    view.rerender(<ResultsView {...props} experiments={[completed]} artifacts={[first.artifact, second.artifact]} focusedExperiment={completed} />)
    expect(await screen.findByText('2 of 2', {selector: '.results-run-filter small'})).toBeInTheDocument()
  })

  it('shows SLO violation and replica statistics as run outcomes', async () => {
    const result = loaded('outcomes', [[1, 2]])
    result.dataset.spec.series = [
      {metric: 'load.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: [{timestamp: '2026-09-09T09:59:45Z', value: 200}, {timestamp: '2026-09-09T10:00:00Z', value: 300}]},
      {metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels: {deployment: 'frontend'}, points: [{timestamp: '2026-09-09T09:59:45Z', value: 2}, {timestamp: '2026-09-09T10:00:00Z', value: 4}]}
    ]
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : result.dataset as never)
    const experiment = {id: 'exp_outcomes', name: 'Autoscaler run', status: 'succeeded', variants: [{id: 'variant_outcomes', name: 'Autoscaler', configuration: {components: [{capability: 'metrics.timeseries.collect', configuration: {sloMilliseconds: 250}}]}, trials: [{id: 'trial_outcomes', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id, operationId: 'op_outcomes'}]}]} as unknown as Experiment
    render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect((await screen.findAllByText('50.00%')).length).toBeGreaterThan(0)
    expect(screen.getByText('Peak 4 · final 4')).toBeInTheDocument()
    expect(screen.getByText(/SLO 250\.0 ms/)).toBeInTheDocument()
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
    const experiment = {variants: [{name: 'Strategy A', alias: 'Paper label', trials: [{resultArtifactId: first.artifact.id}, {resultArtifactId: second.artifact.id}, {resultArtifactId: ''}]}]} as Experiment
    const result = variantStatistics(experiment, [first, second], ['cpu.cores'])
    expect(result).toHaveLength(1)
    expect(result[0].variant).toBe('Paper label')
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

describe('setPNGResolution', () => {
  it('adds a 600 DPI physical resolution chunk after IHDR', () => {
    const source = new Uint8Array([137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 73, 69, 78, 68, 0, 0, 0, 0])
    const result = setPNGResolution(source, 600)
    const marker = [...result].findIndex((_, index) => String.fromCharCode(...result.slice(index, index + 4)) === 'pHYs')
    expect(marker).toBe(37)
    const view = new DataView(result.buffer)
    expect(view.getUint32(marker + 4)).toBe(23622)
    expect(view.getUint32(marker + 8)).toBe(23622)
    expect(result[marker + 12]).toBe(1)
  })
})

describe('publication figures', () => {
  it('uses six human-readable ticks and leaves headroom at an exact maximum', () => {
    expect(niceAxis(10)).toEqual({upper: 10.5, ticks: [0, 2, 4, 6, 8, 10]})
    expect(niceAxis(3.2, true)).toEqual({upper: 5, ticks: [0, 1, 2, 3, 4, 5]})
  })

  it('creates a single-page vector PDF with publication dimensions', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', '0 0 700 525')
    svg.innerHTML = '<rect class="axis" x="112" y="28" width="560" height="355"/><path d="M112,383 L672,28" fill="none" stroke="#1f77b4" stroke-width="2.5"/><text class="axis-label" x="392" y="441" text-anchor="middle">Time (min)</text>'
    const text = new TextDecoder().decode(svgToPDF(svg))
    expect(text.startsWith('%PDF-1.4')).toBe(true)
    expect(text).toContain('/MediaBox [0 0 252 189]')
    expect(text).toContain('0.1216 0.4667 0.7059 RG')
    expect(text).toContain('(Time \\(min\\))')
    expect(text.endsWith('%%EOF\n')).toBe(true)
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
