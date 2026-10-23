import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import {timeAxis} from '../charts'
import type {Artifact, Experiment, Operation, Session, TimeSeriesDataset} from '../types'
import {formatAxis, aggregateMetric, cohenD, compactLabel, experimentProgress, figureDimensions, niceAxis, ResultsView, serviceMetricStatistics, setPNGResolution, podSchedulingStatistics, statisticsCSV, summarize, svgToPDF, variantStatistics, type LoadedDataset} from './ResultsView'

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

describe('serviceMetricStatistics', () => {
  it('summarizes P95 windows separately for each application service', () => {
    const dataset = loaded('latency', [[100, 300], [1000, 3000], [9000]])
    dataset.dataset.spec.series.forEach((series, index) => {
      series.metric = 'application.p95_response_time'
      series.unit = 'milliseconds'
      series.labels = {destination_workload: ['m1', 'm2', 'gateway-node-1'][index]}
    })
    const result = serviceMetricStatistics([dataset], 'application.p95_response_time')
    expect(result.rows.map(row => row.service)).toEqual(['m1', 'm2'])
    expect(result.rows[0].summary.mean).toBe(200)
    expect(result.rows[0].summary.standardDeviation).toBeCloseTo(Math.sqrt(20000))
    expect(result.rows[0].summary.count).toBe(2)
    expect(result.rows[1].summary.mean).toBe(2000)
    expect(result.rows[1].summary.standardDeviation).toBeCloseTo(Math.sqrt(2000000))
  })

  it('sums pods per deployment before computing temporal variability', () => {
    const dataset = loaded('service', [[1, 3], [3, 5]])
    dataset.dataset.spec.series.forEach((series, index) => {series.labels = {pod: `api-abc123-pod${index}`}})
    dataset.dataset.spec.series.push({metric: 'deployment.ready_replicas', unit: 'replicas', labels: {deployment: 'api'}, points: dataset.dataset.spec.series[0].points.map(point => ({...point, value: 2}))})
    const result = serviceMetricStatistics([dataset], 'cpu.cores')
    expect(result.unmatched).toBe(0)
    expect(result.rows).toHaveLength(1)
    expect(result.rows[0].service).toBe('api')
    expect(result.rows[0].summary.mean).toBe(6)
    expect(result.rows[0].summary.standardDeviation).toBeCloseTo(Math.sqrt(8))
    expect(result.rows[0].acrossRuns).toBe(false)
  })

  it('gives each run equal weight and keeps configurations separate', () => {
    const first = loaded('first', [[1, 3]])
    const second = loaded('second', [[10]])
    const other = loaded('other', [[20, 30]])
    for (const dataset of [first, second, other]) dataset.dataset.spec.series[0].labels = {deployment: 'api'}
    const experiment = {variants: [
      {id: 'baseline', name: 'Baseline', trials: [{resultArtifactId: first.artifact.id}, {resultArtifactId: second.artifact.id}]},
      {id: 'custom', name: 'Custom', trials: [{resultArtifactId: other.artifact.id}]}
    ]} as Experiment
    const result = serviceMetricStatistics([first, second, other], 'cpu.cores', experiment)
    expect(result.rows).toHaveLength(2)
    expect(result.rows[0].summary.mean).toBe(6)
    expect(result.rows[0].summary.standardDeviation).toBeCloseTo(Math.sqrt(32))
    expect(result.rows[0].acrossRuns).toBe(true)
    expect(result.rows[1].summary.mean).toBe(25)
  })

  it.each(['deployment.ready_replicas', 'cpu.cores', 'memory.working_set'])('excludes the load driver from the service chart for %s', metric => {
    const dataset = loaded('loader', [[1, 3], [100, 200]])
    dataset.dataset.spec.series.forEach((series, index) => {
      series.metric = metric
      series.labels = metric === 'deployment.ready_replicas' ? {deployment: index ? 'kubephos-load-default-load' : 'api'} : {pod: index ? 'kubephos-load-default-load-abc123-pod01' : 'api-abc123-pod01'}
    })
    if (metric !== 'deployment.ready_replicas') dataset.dataset.spec.series.push({metric: 'deployment.ready_replicas', unit: 'replicas', labels: {deployment: 'api'}, points: [{timestamp: '2026-09-09T10:00:00Z', value: 1}]})
    const result = serviceMetricStatistics([dataset], metric)
    expect(result.rows).toHaveLength(1)
    expect(result.rows[0].service).toBe('api')
    expect(result.rows[0].summary.mean).toBe(2)
    expect(result.unmatched).toBe(0)
    expect(aggregateMetric([dataset], metric)[0].points.map(point => point.value)).toEqual([101, 203])
  })

  it('uses the most specific deployment and omits unidentifiable pods', () => {
    const dataset = loaded('names', [[1, 3], [5, 7]])
    dataset.dataset.spec.series[0].labels = {pod: 'api-worker-abc123-pod01'}
    dataset.dataset.spec.series[1].labels = {pod: 'unrelated-pod'}
    for (const deployment of ['api', 'api-worker']) dataset.dataset.spec.series.push({metric: 'deployment.ready_replicas', unit: 'replicas', labels: {deployment}, points: [{timestamp: '2026-09-09T10:00:00Z', value: 1}]})
    const result = serviceMetricStatistics([dataset], 'cpu.cores')
    expect(result.unmatched).toBe(1)
    expect(result.rows[0].service).toBe('api-worker')
    expect(result.rows[0].summary.mean).toBe(2)
    expect(serviceMetricStatistics([dataset], 'memory.working_set').rows).toEqual([])
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
    const experiment = {id: 'exp_01', ownerUserId: 'user_a', ownerUsername: 'alice', name: 'Scheduler comparison', description: '', status: 'running', resultType: 'TimeSeriesDataset', resultVersion: 'v1alpha1', variants: [{id: 'variant_01', name: 'Baseline', configuration: {components: [{capability: 'load.kubernetes', configuration: {steps: [{type: 'constant', durationSeconds: 60, rps: 20}]}}]}, trials: [{id: 'trial_01', position: 1, status: 'running', resultArtifactId: '', operationId: 'op_load'}, {id: 'trial_02', position: 2, status: 'queued', resultArtifactId: '', operationId: '', pipelineRunId: 'pipeline_waiting'}]}]} as unknown as Experiment
    const operation = {id: 'op_load', pluginId: 'io.kubephos.load.kubernetes.session', status: 'running', createdAt: new Date().toISOString()} as Operation
    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[operation]} workspaces={[]} session={{authenticated: true, user: {id: 'user_a', username: 'alice', role: 'user'}} as Session} changed={async () => {}} openOperation={() => {}} openPipelineRun={() => {}} focusedExperiment={experiment} />)
    expect(screen.getByLabelText('0 of 2 runs finished')).toBeInTheDocument()
    expect(screen.getByText('Runs in progress')).toBeInTheDocument()
    expect(screen.getByText('Created by').closest('dl')).toHaveTextContent('alice')
    expect(screen.getByText('Results and plots appear here as runs finish.')).toBeInTheDocument()
    expect(screen.getByText('Baseline · Run 1', {selector: '.run-load-progress-head strong'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Baseline · Run 1'}))
    expect(screen.getByRole('button', {name: 'Live logs'})).toBeInTheDocument()
    expect(screen.getAllByText('Load running').length).toBeGreaterThan(0)
    expect(screen.getByText(/of 1m · 20 RPS/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Baseline · Run 2'}))
    expect(screen.getByText('Run waiting to start')).toBeInTheDocument()
    expect(screen.getAllByText('Waiting to start').length).toBeGreaterThan(0)
    expect(screen.queryByText('Run completed')).not.toBeInTheDocument()
    expect(screen.queryByText('Runs in progress')).not.toBeInTheDocument()
    expect(screen.queryByText('Run in progress')).not.toBeInTheDocument()
  })

  it('stops an active suite from its detail page', async () => {
    vi.mocked(request).mockResolvedValue({items: []} as never)
    const changed = vi.fn(async () => {})
    const experiment = {id: 'exp_stop', kind: 'suite', name: 'Scheduler suite', description: '', status: 'running', variants: [{id: 'variant_stop', name: 'Baseline', trials: [{id: 'trial_stop', position: 1, status: 'running', resultArtifactId: '', operationId: 'op_stop'}]}]} as unknown as Experiment

    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'csrf', user: {role: 'admin'}} as Session} changed={changed} openOperation={() => {}} focusedExperiment={experiment} />)
    fireEvent.click(screen.getByRole('button', {name: 'Stop suite'}))
    fireEvent.click(screen.getByRole('button', {name: 'Stop'}))

    await waitFor(() => expect(request).toHaveBeenCalledWith('/experiments/exp_stop/cancel', {method: 'POST', body: '{}'}, 'csrf'))
    expect(await screen.findByRole('button', {name: 'Stopping suite…'})).toBeDisabled()
    expect(screen.getByText('stopping', {selector: '.status'})).toBeInTheDocument()
    expect(changed).toHaveBeenCalled()
  })

  it('keeps stop disabled when reopening a suite with cancellation already requested', async () => {
    vi.mocked(request).mockResolvedValue({items: []} as never)
    const experiment = {id: 'exp_stopping', kind: 'suite', name: 'Stopping suite', status: 'running', cancelRequested: true, variants: []} as unknown as Experiment
    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true, user: {role: 'admin'}} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect(screen.getByRole('button', {name: 'Stopping suite…'})).toBeDisabled()
    expect(screen.getByText('stopping', {selector: '.status'})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Stop suite'})).not.toBeInTheDocument()
  })

  it('compares strategies with unequal run counts and keeps individual details in their tabs', async () => {
    const baselineOne = loaded('baseline_one', [[1, 3]])
    const baselineTwo = loaded('baseline_two', [[3, 5]])
    const custom = loaded('custom', [[2, 2]])
    const datasets = [baselineOne, baselineTwo, custom]
    datasets.forEach(dataset => dataset.dataset.spec.series.forEach(series => {series.metric = 'load.p95_response_time'; series.unit = 'milliseconds'}))
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : datasets.find(dataset => path.includes(dataset.artifact.id))!.dataset as never)
    const trial = (dataset: LoadedDataset, position: number) => ({id: dataset.artifact.id, position, status: 'succeeded', resultArtifactId: dataset.artifact.id, operationId: ''})
    const experiment = {id: 'exp_comparison', kind: 'suite', name: 'Strategy suite', status: 'succeeded', resultType: 'TimeSeriesDataset', resultVersion: 'v1alpha1', variants: [
      {id: 'baseline', name: 'Baseline', trials: [trial(baselineOne, 1), trial(baselineTwo, 2)]},
      {id: 'custom', name: 'Custom', trials: [trial(custom, 1), {id: 'missing', position: 2, status: 'failed', resultArtifactId: '', operationId: ''}]}
    ]} as unknown as Experiment
    render(<ResultsView artifacts={datasets.map(dataset => dataset.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    const comparison = await screen.findByRole('table', {name: 'Strategy comparison'})
    expect(comparison).toHaveTextContent('Baseline · 2/2 runs')
    expect(comparison).toHaveTextContent('Custom · 1/2 runs')
    expect(comparison).toHaveTextContent('3.0 ms ± 1.4 ms')
    expect(comparison).toHaveTextContent('2.0 ms')
    expect(screen.queryByRole('table', {name: 'Statistics for Custom · Run 1'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Custom · Run 1'}))
    expect(await screen.findByRole('table', {name: 'Statistics for Custom · Run 1'})).toHaveTextContent('2.0 ms')
    expect(screen.queryByRole('table', {name: 'Strategy comparison'})).not.toBeInTheDocument()
  })

  it('compares single-run strategies without artificial deviation and preserves missing approaches', async () => {
    const first = loaded('first', [[1, 3]])
    const second = loaded('second', [[4, 6]])
    const datasets = [first, second]
    datasets.forEach(dataset => {dataset.dataset.spec.series[0].metric = 'load.p95_response_time'; dataset.dataset.spec.series[0].unit = 'milliseconds'})
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : datasets.find(dataset => path.includes(dataset.artifact.id))!.dataset as never)
    const experiment = {id: 'single_suite', kind: 'suite', name: 'Suite', status: 'failed', variants: [
      ...datasets.map((dataset, index) => ({id: dataset.artifact.id, name: `Strategy ${index + 1}`, trials: [{id: dataset.artifact.id, position: 1, status: 'succeeded', resultArtifactId: dataset.artifact.id}]})),
      {id: 'missing', name: 'Missing strategy', trials: [{id: 'failed', position: 1, status: 'failed', resultArtifactId: ''}]}
    ]} as unknown as Experiment
    render(<ResultsView artifacts={datasets.map(dataset => dataset.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    const table = await screen.findByRole('table', {name: 'Strategy comparison'})
    expect(table).toHaveTextContent('2.0 ms')
    expect(table).toHaveTextContent('5.0 ms')
    expect(table).toHaveTextContent('Missing strategy · 0/1 runs')
    expect(table).toHaveTextContent('—')
    expect(table).not.toHaveTextContent('±')
    expect(screen.queryByRole('heading', {name: 'Within each run'})).not.toBeInTheDocument()
  })

  it.each([1, 3])('keeps only within-run statistics when one of %i planned runs has data', async plannedRuns => {
    const dataset = loaded('single', [[1, 3]])
    dataset.dataset.spec.series[0].metric = 'load.p95_response_time'
    dataset.dataset.spec.series[0].unit = 'milliseconds'
    const trials = Array.from({length: plannedRuns}, (_, index) => ({id: `trial_${index}`, position: index + 1, status: index ? 'failed' : 'succeeded', resultArtifactId: index ? '' : dataset.artifact.id, operationId: ''}))
    const experiment = {id: 'exp_single', name: 'Single available run', status: 'succeeded', variants: [{id: 'v1', name: 'Baseline', trials}]} as Experiment
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : dataset.dataset as never)
    render(<ResultsView artifacts={[dataset.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    const statisticsButton = await screen.findByRole('button', {name: 'Statistics'})
    fireEvent.click(statisticsButton)
    expect(screen.queryByText('Across runs')).not.toBeInTheDocument()
    const table = screen.getByRole('table', {name: 'Statistics for Baseline · Run 1'})
    expect(within(table).queryByRole('columnheader', {name: 'P95 of time samples'})).not.toBeInTheDocument()
    expect(within(table).queryByRole('columnheader', {name: 'P99 of time samples'})).not.toBeInTheDocument()
    const cells = within(table).getAllByRole('row')[1].querySelectorAll('td')
    expect(cells[1]).toHaveTextContent('2')
    expect(cells[2]).toHaveTextContent('2.0 ms')
    expect(cells[3]).toHaveTextContent('1.4 ms')
    expect(cells[4]).toHaveTextContent('1.0 ms')
    expect(cells[6]).toHaveTextContent('3.0 ms')
  })

  it('separates aggregate results from the individual run tabs', async () => {
    const first = loaded('one', [[1, 3]])
    const second = loaded('two', [[3, 5]])
    second.dataset.spec.series[0].points[1].timestamp = '2026-09-09T10:40:00Z'
    for (const dataset of [first, second]) dataset.dataset.spec.series.forEach(series => {series.metric = 'load.p95_response_time'; series.unit = 'milliseconds'})
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : path.includes(first.artifact.id) ? first.dataset as never : second.dataset as never)
    const experiment = {id: 'exp_02', name: 'Scheduler comparison', description: '', status: 'succeeded', resultType: 'TimeSeriesDataset', resultVersion: 'v1alpha1', variants: [{id: 'variant_01', name: 'Baseline', trials: [{id: 'trial_01', position: 1, status: 'succeeded', resultArtifactId: first.artifact.id, operationId: 'op_01'}, {id: 'trial_02', position: 2, status: 'succeeded', resultArtifactId: second.artifact.id, operationId: 'op_02'}]}]} as Experiment
    render(<ResultsView artifacts={[first.artifact, second.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect(screen.getByRole('button', {name: 'All runs'})).toHaveAttribute('aria-current', 'page')
    expect(await screen.findByRole('heading', {name: 'Within each run'})).toBeInTheDocument()
    expect(screen.queryByText(/Based on .* run means/)).not.toBeInTheDocument()
    expect(screen.getByText('Mean across runs')).toBeInTheDocument()
    expect(screen.getAllByText('Std. dev.').length).toBeGreaterThan(0)
    expect(screen.getAllByText('3.0 ms').length).toBeGreaterThan(0)
    expect(screen.getAllByText('1.4 ms').length).toBeGreaterThan(0)
    expect(screen.queryByText(/±/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Plots'}))
    const chart = screen.getByRole('img', {name: 'P95 response time comparison chart'})
    expect(within(chart).getByText('Baseline · Run 1')).toBeInTheDocument()
    expect(within(chart).getByText('Baseline · Run 2')).toBeInTheDocument()
    expect(chart.querySelector('style')).toBeNull()
    expect(chart.querySelector('.axis')).toHaveAttribute('fill', 'none')
    expect(chart.querySelector('.axis')).toHaveAttribute('stroke', '#111')
    const frame = chart.querySelector('.axis')!
    const left = Number(frame.getAttribute('x'))
    const width = Number(frame.getAttribute('width'))
    const paths = chart.querySelectorAll('path[stroke-width="2.5"]')
    const endX = (path: Element) => Number(path.getAttribute('d')!.match(/L([\d.]+),[\d.]+$/)![1])
    expect(endX(paths[0])).toBeCloseTo(left + width * 5 / timeAxis(45, true).upper, 2)
    expect(endX(paths[1])).toBeCloseTo(left + width * 45 / timeAxis(45, true).upper, 2)
    const ticks = [...chart.querySelectorAll('text.tick[text-anchor="middle"]')]
    expect(ticks.at(-1)).toHaveTextContent('40')
    expect(Number(ticks.at(-1)!.getAttribute('x'))).toBeCloseTo(left + width * 40 / timeAxis(45, true).upper)
    expect(paths[1].getAttribute('d')!.match(/[ML]/g)).toHaveLength(2)

    fireEvent.click(screen.getByRole('button', {name: 'Statistics'}))
    expect(await screen.findByText('Across runs')).toBeInTheDocument()
    const runTables = screen.getAllByRole('table', {name: /^Statistics for /})
    expect(runTables).toHaveLength(2)
    expect(runTables.map(table => table.getAttribute('aria-label'))).toEqual(['Statistics for Baseline · Run 1', 'Statistics for Baseline · Run 2'])
    runTables.forEach(table => expect(table.querySelectorAll('tbody tr')).toHaveLength(1))
    fireEvent.click(screen.getByRole('button', {name: 'Baseline · Run 1'}))
    expect(screen.getByRole('button', {name: 'Baseline · Run 1'})).toHaveAttribute('aria-current', 'page')
    expect(screen.queryByText('Across runs')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /logs/i})).not.toBeInTheDocument()
  })

  it('adds a newly completed run without downloading the existing dataset again', async () => {
    vi.mocked(request).mockClear()
    const first = loaded('new_one', [[1, 3]])
    const second = loaded('new_two', [[3, 5]])
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : path.includes(first.artifact.id) ? first.dataset as never : second.dataset as never)
    const experiment = {id: 'exp_03', name: 'Load test', status: 'running', variants: [{id: 'variant_03', name: 'Default', trials: [{id: 'trial_11', position: 1, status: 'succeeded', resultArtifactId: first.artifact.id, operationId: 'op_11'}, {id: 'trial_12', position: 2, status: 'running', resultArtifactId: '', operationId: 'op_12'}]}]} as Experiment
    const props = {experiments: [experiment], operations: [], workspaces: [], session: {authenticated: true} as Session, changed: async () => {}, openOperation: () => {}}
    const view = render(<ResultsView {...props} artifacts={[first.artifact]} focusedExperiment={experiment} />)
    expect(await screen.findByRole('heading', {name: 'Within each run'})).toBeInTheDocument()
    const completed = {...experiment, variants: [{...experiment.variants[0], trials: [experiment.variants[0].trials[0], {...experiment.variants[0].trials[1], status: 'succeeded', resultArtifactId: second.artifact.id}]}]} as Experiment
    view.rerender(<ResultsView {...props} experiments={[completed]} artifacts={[first.artifact, second.artifact]} focusedExperiment={completed} />)
    expect(await screen.findByText('2 of 2', {selector: '.results-run-filter small'})).toBeInTheDocument()
    await waitFor(() => expect(vi.mocked(request).mock.calls.filter(([path]) => path.includes(second.artifact.id))).toHaveLength(1))
    expect(vi.mocked(request).mock.calls.filter(([path]) => path.includes(first.artifact.id))).toHaveLength(1)
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
    expect(within(screen.getByRole('table', {name: /^Statistics for /})).getByText('Ready replicas').closest('tr')).toHaveTextContent('4')
    fireEvent.click(screen.getByRole('button', {name: 'Plots'}))
    expect(screen.getByText(/SLO 250\.0 ms/)).toBeInTheDocument()
  })

  it('omits the SLO target and violation plot when no objective is configured', async () => {
    const result = loaded('without_slo', [[1, 2]])
    result.dataset.spec.series = [
      {metric: 'load.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: [{timestamp: '2026-09-09T09:59:45Z', value: 200}, {timestamp: '2026-09-09T10:00:00Z', value: 300}]},
      {metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels: {deployment: 'frontend'}, points: [{timestamp: '2026-09-09T09:59:45Z', value: 2}, {timestamp: '2026-09-09T10:00:00Z', value: 4}]}
    ]
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : result.dataset as never)
    const experiment = {id: 'exp_without_slo', name: 'Run without SLO', status: 'succeeded', variants: [{id: 'variant_without_slo', name: 'Default', configuration: {components: [{capability: 'metrics.timeseries.collect', configuration: {}}]}, trials: [{id: 'trial_without_slo', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id, operationId: 'op_without_slo'}]}]} as unknown as Experiment
    render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    expect(await screen.findByRole('heading', {name: 'Within each run'})).toBeInTheDocument()
    expect(screen.queryByText('SLO violation windows')).not.toBeInTheDocument()
    expect(screen.queryByText(/SLO \d/)).not.toBeInTheDocument()
  })

  it('shows successful Throughput in statistics and response time service plots', async () => {
    const result = loaded('throughput_summary', [[2, 4], [8, 6]])
    result.dataset.spec.series[0].metric = 'load.successful_rps'
    result.dataset.spec.series[0].unit = 'requests_per_second'
    result.dataset.spec.series[1].metric = 'load.failed_rps'
    result.dataset.spec.series[1].unit = 'requests_per_second'
    result.dataset.spec.series.push(
      {metric: 'application.request_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {destination_workload: 's0'}, points: result.dataset.spec.series[0].points},
      {metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: 's0'}, points: result.dataset.spec.series[0].points},
      {metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {pod: 's0'}, points: result.dataset.spec.series[0].points}
    )
    vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : result.dataset as never)
    const experiment = {id: 'exp_throughput', name: 'Throughput run', status: 'succeeded', variants: [{id: 'variant_throughput', name: 'Default', configuration: {components: []}, trials: [{id: 'trial_throughput', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id, operationId: 'op_throughput'}]}]} as unknown as Experiment
    render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)

    expect(await screen.findByText('Throughput')).toBeInTheDocument()
    expect(screen.getByText('Throughput').closest('tr')).toHaveTextContent('3.00 req/s')
    expect(screen.queryByText('CPU usage', {selector: '.key-metrics span'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Plots'}))
    expect(screen.queryByRole('button', {name: /Load/})).not.toBeInTheDocument()
    expect(screen.getByRole('img', {name: 'CPU usage comparison chart'})).toBeInTheDocument()
    expect(screen.queryByText(/Mean P95 across windows/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /Application/})).not.toBeInTheDocument()
    expect(screen.queryByText('Throughput per service')).not.toBeInTheDocument()
    expect(screen.getByText('Mean window P95 per service')).toBeInTheDocument()
    const serviceChart = screen.getByRole('img', {name: 'Mean window P95 per service: mean and standard deviation'})
    expect(serviceChart.querySelector('text[font-size="11"]')).toBeNull()
    expect(serviceChart).not.toHaveTextContent('± 1.4 ms')
    expect(document.querySelector('.chart-legend')).not.toHaveTextContent(/mean|last/)
    fireEvent.click(screen.getByRole('button', {name: 'Statistics'}))
    const serviceStats = screen.getByRole('table', {name: 'Service statistics for Default · Run 1'})
    expect(serviceStats).toHaveTextContent('3.0 ms')
    expect(serviceStats).toHaveTextContent('1.4 ms')
    expect(serviceStats.closest('.statistics-card')?.querySelector('h3')).toHaveTextContent('Default · Run 1')
    expect(screen.queryByRole('table', {name: 'Response time statistics by service'})).not.toBeInTheDocument()
    expect(screen.queryByRole('img', {name: 'Response time per service over time'})).not.toBeInTheDocument()
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
  it('keeps run numbers visible when long legend labels are shortened', () => {
    const first = compactLabel('Baseline scheduler configuration · Run 1')
    const second = compactLabel('Baseline scheduler configuration · Run 2')
    expect(first).toHaveLength(34)
    expect(first).toMatch(/ · Run 1$/)
    expect(second).toMatch(/ · Run 2$/)
    expect(first).not.toBe(second)
  })

  it('exports PNGs at 600 DPI at the same physical size as their vector PDF', () => {
    expect(figureDimensions(700, 525)).toEqual({pageWidth: 252, pageHeight: 189, pixelWidth: 2100, pixelHeight: 1575})
    expect(figureDimensions(900, 600)).toEqual({pageWidth: 504, pageHeight: 336, pixelWidth: 4200, pixelHeight: 2800})
  })

  it('preserves local SVG font sizes and SLO label colors in PDF', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', '0 0 700 525')
    svg.innerHTML = '<style>.tick{font-size:14px}.slo-label{fill:#b42318}</style><text class="tick slo-label" x="150" y="100">SLO 100 ms</text>'
    const text = new TextDecoder().decode(svgToPDF(svg))
    expect(text).toContain('/F1 5.040 Tf')
    expect(text).toContain('0.7059 0.1373 0.0941 rg')
  })

  it('preserves the plus-minus sign and its alignment in exported PDF labels', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', '0 0 700 525')
    svg.innerHTML = '<text x="350" y="100" text-anchor="middle" font-size="14">± 1.4 ms</text>'
    const text = new TextDecoder().decode(svgToPDF(svg))
    expect(text).toContain('/Encoding /WinAnsiEncoding')
    expect(text).toContain(String.raw`(\261 1.4 ms) Tj`)
    expect(text).toContain('1 0 0 1 115.920 153.000 Tm')
    expect(text).not.toContain('(- 1.4 ms)')
  })

  it('uses six human-readable ticks and leaves headroom at an exact maximum', () => {
    expect(niceAxis(10)).toEqual({upper: 10.5, ticks: [0, 2, 4, 6, 8, 10]})
    expect(niceAxis(3.2, true)).toEqual({upper: 5, ticks: [0, 1, 2, 3, 4, 5]})
  })

  it('preserves white backgrounds, short hex colors and rotated service labels in PDF', () => {
    const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg')
    svg.setAttribute('viewBox', '0 0 900 600')
    svg.innerHTML = '<rect width="900" height="600" fill="white"/><line x1="110" y1="385" x2="865" y2="385" stroke="#111"/><text x="200" y="405" transform="rotate(-45 200 405)" text-anchor="end">api-service</text>'
    const text = new TextDecoder().decode(svgToPDF(svg))
    expect(text).toContain('1.0000 1.0000 1.0000 rg')
    expect(text).not.toContain('0 0 0 rg\n0.000 0.000 504.000 336.000 re f')
    expect(text).toContain('0.0667 0.0667 0.0667 RG')
    expect(text).toContain('0.7071 0.7071 -0.7071 0.7071')
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
      applicationRef: 'app:example',
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

it('maps Redis operations and StatefulSet pods without calling operation metrics internal HTTP calls', () => {
  const dataset = loaded('redis', [[1, 3]])
  dataset.dataset.spec.series[0].labels = {pod: 'redis-primary-1-0'}
  dataset.dataset.spec.series.push({metric: 'deployment.ready_replicas', unit: 'replicas', labels: {statefulset: 'redis-primary-1'}, points: [{timestamp: '2026-09-09T10:00:00Z', value: 1}]})
  expect(serviceMetricStatistics([dataset], 'cpu.cores').rows[0].service).toBe('redis-primary-1')
  dataset.dataset.spec.series.push({metric: 'redis.operation_p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {operation: 'GET'}, points: [{timestamp: '2026-09-09T10:00:00Z', value: 2}]})
  expect(serviceMetricStatistics([dataset], 'redis.operation_p95_response_time').rows[0].service).toBe('GET')
})

it('integrates event-weighted cycle means and eviction totals into the common statistics table', async () => {
  const result = loaded('control', [[1, 3]])
  result.dataset.spec.start = '2026-09-09T09:59:00Z'
  result.dataset.spec.workloadStartedAt = '2026-09-09T10:00:00Z'
  result.dataset.spec.controlMetrics = [
    {metric: 'scheduler.cycle_duration', status: 'available', scope: 'cluster', count: 5, mean: 130},
    {metric: 'scheduler.pod_scheduling_latency', status: 'available', scope: 'experiment', count: 2, mean: 3000, timestampResolutionSeconds: 1},
    {metric: 'descheduler.cycle_duration', status: 'no_events', scope: 'experiment', count: 0},
    {metric: 'descheduler.evictions', status: 'available', scope: 'experiment', total: 8}
  ]
  result.dataset.spec.series.push(
    {metric: 'scheduler.cycle_duration', unit: 'milliseconds', aggregation: 'mean', labels: {scope: 'cluster'}, points: [{timestamp: '2026-09-09T09:59:00Z', value: 100}, {timestamp: '2026-09-09T10:00:00Z', value: 300}]},
    {metric: 'descheduler.evictions', unit: 'count', aggregation: 'sum', labels: {scope: 'experiment'}, points: [{timestamp: '2026-09-09T09:59:00Z', value: 2}, {timestamp: '2026-09-09T10:00:00Z', value: 8}]}
  )
  vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: []} as never : result.dataset as never)
  const experiment = {id: 'exp_control', name: 'Control metrics', status: 'succeeded', variants: [{id: 'control', name: 'Default', trials: [{id: 'trial_control', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id, operationId: ''}]}]} as Experiment
  render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  const summary = await screen.findByRole('table', {name: 'Statistics for Default · Run 1'})
  expect(screen.queryByRole('button', {name: /^Summary$/})).not.toBeInTheDocument()
  expect(screen.getByRole('button', {name: /^Export$/})).toBeInTheDocument()
  expect(screen.getByRole('navigation', {name: 'Result sections'}).querySelector('button')).toHaveTextContent('Statistics')
  expect(screen.queryByRole('table', {name: 'Scheduling and descheduling statistics'})).not.toBeInTheDocument()
  expect(within(summary).getByText('130.0 ms')).toBeInTheDocument()
  const schedulingLatency = within(summary).getByText('Scheduling latency').closest('tr')!
  expect(schedulingLatency).toHaveTextContent('3000.0 ms')
  expect(schedulingLatency).not.toHaveTextContent('Experiment')
  expect(schedulingLatency.querySelector('td')).toHaveAttribute('title', expect.stringContaining('Time from pod creation to node assignment'))
  expect(within(schedulingLatency).getByTitle('Mean per pod')).toBeInTheDocument()
  expect(within(summary).getByText('8')).toBeInTheDocument()
  expect(within(summary).queryByText(/Cluster/)).not.toBeInTheDocument()
  expect(within(summary).getByText('Scheduling cycle duration')).toHaveAttribute('title', expect.stringContaining('entire cluster'))
  expect(within(summary).getAllByText('—').length).toBeGreaterThan(0)
  expect(aggregateMetric([result], 'scheduler.cycle_duration')[0].points[0].timestamp).toBe('1970-01-01T00:00:00.000Z')
  expect(aggregateMetric([result], 'cpu.cores')[0].points[0].timestamp).toBe('1970-01-01T00:00:00.000Z')
  fireEvent.click(screen.getByRole('button', {name: 'Plots'}))
  expect(screen.queryByRole('img', {name: 'Scheduling cycle duration comparison chart'})).not.toBeInTheDocument()
  expect(screen.queryByRole('img', {name: 'Descheduler evictions comparison chart'})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Statistics'}))
  const temporal = screen.getByRole('table', {name: 'Statistics for Default · Run 1'})
  const eviction = within(temporal).getByText('Descheduler evictions').closest('tr')!
  expect(eviction).toHaveTextContent('8')
  expect(eviction).not.toHaveTextContent('5.0')
})

it('compares weighted cycle means and eviction totals across runs', () => {
  const first = loaded('control_first', [[1, 3]])
  const second = loaded('control_second', [[100, 300]])
  first.dataset.spec.controlMetrics = [{metric: 'scheduler.cycle_duration', status: 'available', scope: 'experiment', count: 5, mean: 130}, {metric: 'descheduler.evictions', status: 'available', scope: 'experiment', total: 8}]
  second.dataset.spec.controlMetrics = [{metric: 'scheduler.cycle_duration', status: 'available', scope: 'experiment', count: 1, mean: 10}, {metric: 'descheduler.evictions', status: 'available', scope: 'experiment', total: 2}]
  const experiment = {variants: [{name: 'Default', trials: [first, second].map(dataset => ({resultArtifactId: dataset.artifact.id}))}]} as Experiment
  const rows = variantStatistics(experiment, [first, second], ['scheduler.cycle_duration', 'descheduler.evictions'])
  expect(rows.find(row => row.metric === 'scheduler.cycle_duration')?.summary.mean).toBe(70)
  expect(rows.find(row => row.metric === 'descheduler.evictions')?.summary.mean).toBe(5)
})

it('keeps scheduler and descheduler metrics in statistics while omitting all their plots', async () => {
  const result = loaded('sparse_control', [])
  result.dataset.spec.start = '2026-09-09T09:55:00Z'
  result.dataset.spec.end = '2026-09-09T10:15:00Z'
  const at = (seconds: number) => new Date(Date.parse(result.dataset.spec.start) + seconds * 1000).toISOString()
  result.dataset.spec.controlMetrics = [
    {metric: 'scheduler.pod_scheduling_latency', status: 'available', scope: 'experiment', count: 15, mean: 600},
    {metric: 'scheduler.cycle_duration', status: 'available', scope: 'experiment', count: 15, mean: 408},
    {metric: 'descheduler.cycle_duration', status: 'available', scope: 'experiment', count: 82, mean: 1},
    {metric: 'descheduler.evictions', status: 'unavailable', scope: 'experiment', reason: 'Successful eviction counter unavailable'}
  ]
  result.dataset.spec.series = [
    ...Array.from({length: 15}, (_, index) => ({metric: 'scheduler.pod_scheduling_latency', unit: 'milliseconds', aggregation: 'mean' as const, labels: {pod_uid: `pod-${index}`, scheduled_at: at(60 + index)}, points: [{timestamp: at(index ? 75 : 60), value: index < 9 ? 1000 : 0}]})),
    {metric: 'scheduler.cycle_duration', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: [{timestamp: at(75), value: 350}, {timestamp: at(90), value: 1218}]},
    {metric: 'descheduler.cycle_duration', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: Array.from({length: 41}, (_, index) => ({timestamp: at(120 + index * 15 + (index >= 20 ? 30 : 0)), value: 1}))}
  ]
  const latency = aggregateMetric([result], 'scheduler.pod_scheduling_latency')[0]
  expect(latency.points).toHaveLength(15)
  expect(new Set(latency.points.map(point => point.timestamp)).size).toBe(15)
  expect(latency.points.reduce((total, point) => total + point.value, 0) / 15).toBe(600)
  vi.mocked(request).mockResolvedValue(result.dataset as never)
  const experiment = {id: 'sparse', name: 'Sparse scheduling', status: 'succeeded', variants: [{id: 'default', name: 'Default', trials: [{id: 'trial', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id}]}]} as Experiment
  render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  await screen.findByRole('table', {name: 'Statistics for Default · Run 1'})
  fireEvent.click(screen.getByRole('button', {name: 'Plots'}))
  expect(screen.queryByRole('img', {name: /Scheduling|Descheduling|evictions/i})).not.toBeInTheDocument()
  expect(document.querySelector('.chart-grid')?.children).toHaveLength(0)
  fireEvent.click(screen.getByRole('button', {name: 'Statistics'}))
  const table = screen.getByRole('table', {name: 'Statistics for Default · Run 1'})
  const row = within(table).getByText('Scheduling latency').closest('tr')!
  expect(row).toHaveTextContent('600.0 ms')
  expect(row).toHaveTextContent('507.1 ms')
  expect(within(table).getByText('Descheduler evictions')).toHaveAttribute('title', expect.stringContaining('unavailable'))
})

it('restores Export downloads and keeps CSV control means event weighted and service statistics separate', async () => {
  const result = loaded('export', [[2, 4]])
  result.dataset.spec.series[0].labels = {deployment: 'api'}
  result.dataset.spec.controlMetrics = [{metric: 'scheduler.cycle_duration', status: 'available', scope: 'experiment', count: 15, mean: 408}, {metric: 'descheduler.evictions', status: 'unavailable', scope: 'experiment'}]
  result.dataset.spec.series.push({metric: 'scheduler.cycle_duration', unit: 'milliseconds', aggregation: 'mean', labels: {}, points: [{timestamp: result.dataset.spec.start, value: 350}, {timestamp: result.dataset.spec.end, value: 1218}]})
  const csv = statisticsCSV([result], ['cpu.cores', 'scheduler.cycle_duration', 'descheduler.evictions'])
  expect(csv).toContain('scheduler.cycle_duration,,milliseconds,available,15,408,')
  expect(csv).toContain('descheduler.evictions,,count,unavailable,,,,')
  expect(csv).toContain('service_run,export,export,cpu.cores,api,cores,available,2,3,,1.4142135623730951')
  vi.mocked(request).mockImplementation(async path => path.endsWith('/figures') ? {items: [{id: 'saved', metric: 'cpu.cores', format: 'pdf', sourceArtifactIds: [result.artifact.id], createdAt: '2026-09-09T10:00:00Z'}]} as never : result.dataset as never)
  const experiment = {id: 'export', name: 'Export', status: 'succeeded', variants: [{id: 'default', name: 'Default', trials: [{id: 'trial', position: 1, status: 'succeeded', resultArtifactId: result.artifact.id}]}]} as Experiment
  render(<ResultsView artifacts={[result.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  await screen.findByRole('table', {name: 'Statistics for Default · Run 1'})
  fireEvent.click(screen.getByRole('button', {name: 'Export'}))
  expect(screen.getByRole('button', {name: 'Statistics CSV'})).toBeInTheDocument()
  expect(screen.getByRole('button', {name: 'Print statistics'})).toBeInTheDocument()
  expect(screen.getByRole('link', {name: /Metric samples/})).toHaveAttribute('href', `/api/v1/artifacts/${result.artifact.id}/download`)
  expect(await screen.findByRole('link', {name: /CPU usage · PDF/})).toHaveAttribute('href', '/api/v1/figures/saved/download')
})


it('deduplicates pod observations and refuses to reconstruct statistics from incomplete data', () => {
  const result = loaded('pod_stats', [])
  result.dataset.spec.controlMetrics = [{metric: 'scheduler.pod_scheduling_latency', status: 'available', scope: 'experiment', count: 2, mean: 500}]
  result.dataset.spec.series = [0, 1000].map((value, index) => ({metric: 'scheduler.pod_scheduling_latency', unit: 'milliseconds', labels: {pod_uid: `pod-${index}`}, points: [{timestamp: result.dataset.spec.start, value}]}))
  result.dataset.spec.series.push({...result.dataset.spec.series[0]})
  const summary = podSchedulingStatistics(result)!
  expect(summary.count).toBe(2)
  expect(summary.mean).toBe(500)
  expect(summary.standardDeviation).toBeCloseTo(Math.sqrt(500000))
  expect(statisticsCSV([result], ['scheduler.pod_scheduling_latency'])).toContain('available,2,500,,707.106')
  result.dataset.spec.series = [result.dataset.spec.series[0]]
  expect(podSchedulingStatistics(result)).toBeUndefined()
})

it('pads time axes without adding an irregular end-of-run tick', () => {
  const axis = timeAxis(20.2, true)
  expect(axis.upper).toBeGreaterThan(20.2)
  expect(axis.upper).toBeLessThanOrEqual(22)
  expect(axis.ticks).toEqual([0, 4, 8, 12, 16, 20])
  for (const end of [.5, 1, 20, 20.2, 45, 60, 150]) {
    const current = timeAxis(end, true)
    expect(current.upper).toBeGreaterThan(end)
    expect(current.ticks[0]).toBe(0)
    const step = current.ticks[1]
    current.ticks.slice(1).forEach((tick, index) => expect(tick).toBeCloseTo((index + 1) * step))
  }
})

it('orders service labels naturally and keeps CPU units explicit in statistics', async () => {
  const dataset = loaded('unit_review', [[0.1, 0.3], [1, 3], [0.01, 0.03]])
  dataset.dataset.spec.series.forEach((series, index) => {series.labels = {service: ['s10', 's2', 's1'][index]}})
  expect(serviceMetricStatistics([dataset], 'cpu.cores').rows.map(row => row.service)).toEqual(['s1', 's2', 's10'])
  vi.mocked(request).mockResolvedValue(dataset.dataset as never)
  render(<ResultsView artifacts={[dataset.artifact]} experiments={[]} operations={[]} workspaces={[]} session={{authenticated: true} as Session} changed={async () => {}} openOperation={() => {}} />)
  const table = await screen.findByRole('table', {name: /Statistics for/})
  expect(table.textContent).toContain('cores')
  const search = screen.getByRole('searchbox', {name: /Search services/})
  fireEvent.change(search, {target: {value: 's10'}})
  const services = screen.getByRole('table', {name: /Service statistics for/})
  expect(within(services).getByText('s10')).toBeInTheDocument()
  expect(within(services).queryByText('s2')).not.toBeInTheDocument()
})

it('keeps small adjacent axis ticks distinct', () => {
  const values = [0, 0.005, 0.01, 0.015, 0.02, 0.025]
  expect(new Set(values.map(formatAxis)).size).toBe(values.length)
  expect(formatAxis(0.005)).toBe('0.005')
})
