import {cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import axe from 'axe-core'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {request} from '../../api'
import type {LoadedDataset} from '../../resultsData'
import type {Artifact, Experiment, MetricSeries, Session, TimeSeriesDataset} from '../../types'
import {ResultsView} from '../ResultsView'
import {rowScale, scaledInterval, scaledValue} from './format'
import {KpiCards, QualityLine} from './Overview'
import {resourceCards, resourceRun, resourceSummary} from './resources'
import {AcrossRunsTable, RunStatisticsTable, StatisticsTabs} from './StatisticsTables'
import {provenanceFor} from './runs'

vi.mock('./kubestudio/resultsLayout', async () => (await import('../../test/everySection')).everySection())
vi.mock('../../api', () => ({request: vi.fn(async () => ({items: []}))}))
afterEach(() => {cleanup(); vi.mocked(request).mockReset()})

const session = {authenticated: true} as Session
const start = Date.parse('2026-11-19T10:00:00Z')
const at = (seconds: number) => new Date(start + seconds * 1000).toISOString()
const series = (metric: string, unit: string, values: number[], labels: Record<string, string> = {}, aggregation: 'sum' | 'mean' = 'sum'): MetricSeries => ({metric, unit, aggregation, labels, points: values.map((value, index) => ({timestamp: at(index * 15), value}))})

function dataset(id: string, list: MetricSeries[]): LoadedDataset {
  const artifact: Artifact = {id, operationId: `op-${id}`, workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: `sha:${id}`, sizeBytes: 1, sensitive: false, verifiedAt: at(0)}
  return {artifact, label: id, dataset: {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: id, version: 'v1alpha1'}, spec: {applicationRef: 'app', clusterServer: 'https://cluster', namespace: 'ns', start: at(0), end: at(60), stepSeconds: 15, source: {kind: 'prometheus', version: '1', profile: 'p'}, summary: {metrics: 1, series: 1, samples: 1}, series: list}} as TimeSeriesDataset}
}

const MiB = 1024 ** 2
// Two application pods, one autoscaled workload (2 → 4 replicas), and the load generator,
// whose CPU, memory and restarts must never count.
const resourceRunData = (id: string, latency = 100) => dataset(id, [
  series('load.p95_response_time', 'milliseconds', [latency, latency, latency, latency, latency], {}, 'mean'),
  series('load.achieved_rps', 'requests_per_second', [10, 10, 10, 10, 10]),
  series('cpu.cores', 'cores', [0.2, 0.4, 0.6, 0.4, 0.4], {pod: 'api-7c9b6d8f5-x2b4c'}),
  series('cpu.cores', 'cores', [0.1, 0.1, 0.1, 0.1, 0.1], {pod: 'web-7c9b6d8f5-z2b4c'}),
  series('cpu.cores', 'cores', [5, 5, 5, 5, 5], {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
  series('cpu.limit_cores', 'cores', [1, 1, 1, 1, 1], {pod: 'api-7c9b6d8f5-x2b4c'}),
  series('cpu.limit_cores', 'cores', [4, 4, 4, 4, 4], {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
  series('memory.working_set', 'bytes', [100 * MiB, 200 * MiB, 300 * MiB, 200 * MiB, 200 * MiB], {pod: 'api-7c9b6d8f5-x2b4c'}),
  series('memory.working_set', 'bytes', [900 * MiB, 900 * MiB, 900 * MiB, 900 * MiB, 900 * MiB], {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'}),
  series('deployment.ready_replicas', 'replicas', [2, 2, 3, 4, 4], {deployment: 'api'}),
  series('deployment.ready_replicas', 'replicas', [1, 1, 1, 1, 1], {deployment: 'web'}),
  series('pod.restarts', 'count', [0, 0, 1, 1, 1], {pod: 'api-7c9b6d8f5-x2b4c'}),
  series('pod.restarts', 'count', [0, 3, 6, 9, 9], {pod: 'kubephos-load-default-7c9b6d8f5-xyz12'})
])

describe('resource key figures', () => {
  it('measures scaling, CPU, memory and restarts of the application only', () => {
    const run = resourceRun(resourceRunData('r1'))
    expect(run.replicas).toEqual({start: 3, min: 3, max: 5, events: 2})
    expect(run.cpu!.mean).toBeCloseTo(0.5)
    expect(run.cpu!.peak).toBeCloseTo(0.7)
    // Share of the limit counts only the pods that have one (api: 0.4 of 1 core).
    expect(run.cpu!.limitShare).toBeCloseTo(40)
    expect(run.memory).toEqual({mean: 200 * MiB, peak: 300 * MiB, limitShare: undefined})
    expect(run.restarts).toBe(1)
  })

  it('shows start → max for fixed deployments and min–max for autoscaled runs, — when unrecorded', () => {
    const groups = [{id: 'a', label: 'Fixed', color: 'var(--chart-1)'}, {id: 'b', label: 'HPA', color: 'var(--chart-2)'}, {id: 'c', label: 'Bare', color: 'var(--chart-3)'}]
    const cards = resourceCards(groups, [resourceSummary('a', [resourceRunData('a1')], false), resourceSummary('b', [resourceRunData('b1'), resourceRunData('b2')], true), resourceSummary('c', [dataset('c1', [series('load.achieved_rps', 'requests_per_second', [1, 1])])], false)])
    const scaling = cards.find(card => card.id === 'scaling')!
    expect(scaling.entries.map(entry => entry.value)).toEqual(['3 → 5', '3–5', '—'])
    expect(scaling.entries[0].detail).toBe('2 scaling events')
    expect(scaling.entries[1].detail).toBe('autoscaled · 2 scaling events per execution')
    const cpu = cards.find(card => card.id === 'cpu')!.entries[0]
    expect(cpu).toMatchObject({value: '500', unit: 'mCPU', detail: 'peak 700 mCPU · 40 % of limit'})
    const memory = cards.find(card => card.id === 'memory')!.entries[0]
    expect(memory).toMatchObject({value: '200', unit: 'MiB', detail: 'peak 300 MiB'})
    expect(cards.find(card => card.id === 'restarts')!.entries.map(entry => [entry.value, entry.tone])).toEqual([['1', 'warning'], ['1', 'warning'], ['—', undefined]])
    expect(resourceCards(groups.slice(2), [resourceSummary('c', [dataset('c1', [])], false)])).toEqual([])
  })

  it('renders the resource row in the key-number typography, per variant when several', () => {
    const groups = [{id: 'a', label: 'Fixed', color: 'var(--chart-1)'}, {id: 'b', label: 'HPA', color: 'var(--chart-2)'}]
    const {rerender} = render(<KpiCards cards={[]} provenance={() => undefined} resources={resourceCards(groups.slice(0, 1), [resourceSummary('a', [resourceRunData('a1')], false)])} />)
    const row = screen.getByRole('list', {name: 'Resource usage'})
    expect(within(row).getByRole('listitem', {name: 'Scaling'})).toHaveTextContent('3 → 5 replicas')
    expect(within(row).getByRole('listitem', {name: 'CPU usage'}).querySelector('.kpi-number')).toHaveTextContent('500')
    expect(within(row).getByRole('listitem', {name: 'Pod restarts'})).toHaveClass('tone-warning')
    rerender(<KpiCards cards={[]} provenance={() => undefined} resources={resourceCards(groups, [resourceSummary('a', [resourceRunData('a1')], false), resourceSummary('b', [resourceRunData('b1')], true)])} />)
    const memory = screen.getByRole('listitem', {name: 'Memory'})
    expect(within(memory).getAllByRole('listitem').map(item => item.textContent)).toEqual([expect.stringContaining('Fixed200 MiB'), expect.stringContaining('HPA200 MiB')])
  })
})

describe('single execution overview', () => {
  it('drops the summary paragraph and keeps measurement warnings as one quiet line', async () => {
    const item = resourceRunData('single')
    item.dataset.spec.series.push(series('load.offered_rps', 'requests_per_second', [20, 20, 20, 20, 20]))
    vi.mocked(request).mockImplementation(async path => String(path).endsWith('/figures') ? {items: []} as never : item.dataset as never)
    const experiment = {id: 'one', name: 'One run', kind: 'instance', status: 'succeeded', variants: [{id: 'v', name: 'Default', configuration: {}, trials: [{id: 't1', position: 1, status: 'succeeded', resultArtifactId: 'single', operationId: ''}]}]} as unknown as Experiment
    render(<ResultsView artifacts={[item.artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    await screen.findByRole('list', {name: 'Key results'})
    expect(screen.queryByRole('region', {name: 'Verdict'})).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', {name: 'Summary'})).not.toBeInTheDocument()
    const line = screen.getByRole('group', {name: 'Measurement quality'})
    expect(line.querySelector('summary')).toHaveTextContent(/^Measurement note: Achieved request rate was 50.0% of the offered rate over the measured window$/)
    expect(screen.getByRole('list', {name: 'Resource usage'})).toBeInTheDocument()
  })

  it('ignores notes that are not about the measurement', () => {
    const {container} = render(<QualityLine notices={[{kind: 'note', text: 'Cleanup failed'}]} />)
    expect(container).toBeEmptyDOMElement()
  })
})

describe('page navigation', () => {
  const suite = () => {
    const items = ['r1', 'r2', 'r3'].map((id, index) => resourceRunData(id, 100 + index * 10))
    vi.mocked(request).mockImplementation(async path => String(path).endsWith('/figures') ? {items: []} as never : structuredClone(items.find(item => String(path).includes(`/${item.artifact.id}/`))!.dataset) as never)
    const experiment = {id: 'suite', name: 'Suite', kind: 'suite', status: 'succeeded', variants: [{id: 'v', name: 'fg-01', configuration: {}, trials: items.map((item, index) => ({id: `t${index}`, position: index + 1, status: 'succeeded', resultArtifactId: item.artifact.id, operationId: ''}))}]} as unknown as Experiment
    render(<main><h1>KubePhos</h1><ResultsView artifacts={items.map(item => item.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} /></main>)
  }

  it('keeps the run tabs as pills with a status dot and marks the current one', async () => {
    suite()
    const tabs = await screen.findByRole('navigation', {name: 'Run executions'})
    const all = within(tabs).getByRole('button', {name: 'All executions'})
    expect(all).toHaveClass('run-tab', 'all-runs')
    expect(all).not.toHaveClass('summary')
    expect(all).toHaveAttribute('aria-current', 'page')
    const first = within(tabs).getByRole('button', {name: 'fg-01 · Execution 1'})
    expect(first.querySelector('.status-dot.succeeded')).not.toBeNull()
    fireEvent.click(first)
    expect(first).toHaveAttribute('aria-current', 'page')
    expect(all).not.toHaveAttribute('aria-current')
  })

  it('jumps to a section from the sticky bar, opening it, and highlights it', async () => {
    suite()
    const bar = await screen.findByRole('navigation', {name: 'Sections of the results'})
    expect(within(bar).getAllByRole('link').map(link => link.textContent)).toEqual(['Summary', 'Load and latency', 'Services', 'Charts', 'Statistics', 'Environment'])
    expect(within(bar).getAllByRole('link').filter(link => link.getAttribute('aria-current') === 'location')).toHaveLength(1)
    const statistics = within(bar).getByRole('link', {name: 'Statistics'})
    expect(statistics).toHaveAttribute('href', '#section-statistics')
    fireEvent.click(statistics)
    expect(statistics).toHaveAttribute('aria-current', 'location')
    expect(await screen.findByRole('tablist', {name: 'Statistics tables'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: /^Statistics/, expanded: true})).toBeInTheDocument()
    const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))).toEqual([])
  })

  it('shows one statistics table at a time, with keyboard tabs and a metric filter', async () => {
    suite()
    fireEvent.click(await screen.findByRole('button', {name: /^Statistics/}))
    const tabs = await screen.findAllByRole('tab')
    expect(tabs.map(tab => tab.textContent)).toEqual(['Across executions', 'Per execution', 'By service'])
    expect(tabs[0]).toHaveAttribute('aria-selected', 'true')
    expect(tabs[1]).toHaveAttribute('tabindex', '-1')
    expect(screen.getByRole('tabpanel')).toContainElement(screen.getByRole('table', {name: 'Statistics across executions'}))
    fireEvent.keyDown(tabs[0], {key: 'ArrowRight'})
    expect(tabs[1]).toHaveAttribute('aria-selected', 'true')
    expect(tabs[1]).toHaveFocus()
    expect(screen.queryByRole('table', {name: 'Statistics across executions'})).not.toBeInTheDocument()
    expect(screen.getByRole('table', {name: 'Statistics for fg-01 · Execution 1'})).toBeInTheDocument()
    fireEvent.keyDown(tabs[1], {key: 'End'})
    expect(tabs[2]).toHaveAttribute('aria-selected', 'true')
    fireEvent.keyDown(tabs[2], {key: 'Home'})
    const filter = screen.getByRole('searchbox', {name: 'Filter metrics'})
    fireEvent.change(filter, {target: {value: 'memory'}})
    const across = screen.getByRole('table', {name: 'Statistics across executions'})
    expect(within(across).getAllByRole('row').slice(1).map(row => row.querySelector('td')?.textContent)).toEqual(['Memory working set'])
    expect(within(across).getAllByRole('cell')[1]).toHaveTextContent('MiB')
  })
})

describe('statistics table format', () => {
  it('writes the unit once per row with one precision for every number', () => {
    expect(rowScale('milliseconds', 186.5)).toEqual({unit: 'ms', factor: 1, digits: 0})
    expect(scaledInterval(156.7, 216.4, rowScale('milliseconds', 186.5))).toBe('157–216')
    expect(rowScale('milliseconds', 24000).unit).toBe('s')
    expect(scaledValue(0.25, rowScale('cores', 0.25))).toBe('250')
    expect(rowScale('bytes', 300 * MiB)).toMatchObject({unit: 'MiB', digits: 0})
    expect(scaledValue(-1.234, rowScale('percent', 1.2))).toBe('−1.23')
    expect(scaledValue(undefined, rowScale('percent', 1))).toBe('—')
    expect(rowScale('count', 8).digits).toBe(0)
  })

  it('drops the Strategy column for one strategy and shows the source as plain text', () => {
    const items = ['a', 'b', 'c'].map((id, index) => dataset(id, [series('load.p95_response_time', 'milliseconds', [150 + index * 20, 170 + index * 20], {}, 'mean')]))
    const experiment = {id: 'e', variants: [{id: 'v', name: 'fg-01', trials: items.map((item, index) => ({id: `t${index}`, position: index + 1, resultArtifactId: item.artifact.id}))}]} as unknown as Experiment
    render(<AcrossRunsTable experiment={experiment} datasets={items} metrics={['load.p95_response_time']} provenance={metric => provenanceFor(items, metric)} />)
    const table = screen.getByRole('table', {name: 'Statistics across executions'})
    expect(within(table).getAllByRole('columnheader').map(header => header.textContent)).toEqual(['Metric', 'Unit', 'Source', 'n', 'Mean', 'SD', 'Min', 'Median', 'Max', 'CV', '95% CI'])
    const cells = within(table).getAllByRole('cell')
    expect(cells[0]).toHaveClass('metric-cell')
    expect(cells[2].querySelector('.provenance-badge')).toBeNull()
    expect(cells[2].querySelector('.provenance-text')).toHaveTextContent('Locust · client side')
    expect(cells.slice(4, 9).map(cell => cell.textContent)).toEqual(['180', '20', '160', '180', '200'])
    expect(cells[10].textContent).toMatch(/^\d+–\d+$/)
  })

  it('limits long tables to ten rows with a Show all link', () => {
    const metrics = Array.from({length: 14}, (_, index) => `custom.metric_${String(index).padStart(2, '0')}`)
    const item = dataset('long', metrics.map(metric => series(metric, 'count', [1, 2, 3], {}, 'mean')))
    render(<RunStatisticsTable dataset={item} metrics={metrics} />)
    const table = screen.getByRole('table', {name: 'Statistics for long'})
    expect(table.querySelectorAll('tbody tr')).toHaveLength(10)
    const more = screen.getByRole('button', {name: 'Show all 14 metrics'})
    expect(more).toHaveAttribute('aria-expanded', 'false')
    fireEvent.click(more)
    expect(table.querySelectorAll('tbody tr')).toHaveLength(14)
    expect(screen.getByRole('button', {name: 'Show first 10 only'})).toHaveAttribute('aria-expanded', 'true')
  })

  it('renders only the selected view, without a filter for views that list no metrics', () => {
    render(<StatisticsTabs views={[{id: 'one', title: 'One', render: () => <p>first</p>}, {id: 'two', title: 'Two', render: () => <p>second</p>}]} />)
    expect(screen.getByText('first')).toBeInTheDocument()
    expect(screen.queryByText('second')).not.toBeInTheDocument()
    expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
  })
})
