import {cleanup, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import axe from 'axe-core'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {request} from '../../api'
import {downloadBlob, svgDocument, svgToPDF} from '../../charts'
import {prepareComparison} from '../../comparisonPreparation'
import {runLevelValues, variantComparisons, type LoadedDataset} from '../../resultsData'
import type {Artifact, Experiment, LocustSummary, MetricSeries, Session, TimeSeriesDataset} from '../../types'
import {ResultsView} from '../ResultsView'
import {methodsParagraph} from './context'
import {defaultFigureSettings, loadFigureSettings, saveFigureSettings, type FigureSettings} from './figureSettings'
import {TimeSeriesFigure} from './Figures'
import {formatDifference, formatPValue} from './format'
import {fidelityThresholds, loadFidelity} from './loadFidelity'
import {LocustSummaryPanel} from './LoadPanels'
import {locustGroup} from './locust'
import {executedPilotCount, provenanceFor} from './runs'
import {valueScale} from './scales'
import {intervalText, RunStatisticsTable} from './StatisticsTables'

vi.mock('./kubestudio/resultsLayout', async () => (await import('../../test/everySection')).everySection())
vi.mock('../../api', () => ({request: vi.fn(async () => ({items: []}))}))
vi.mock('../../charts', async original => ({...await original<typeof import('../../charts')>(), downloadBlob: vi.fn()}))
afterEach(() => {cleanup(); vi.mocked(request).mockReset(); vi.mocked(downloadBlob).mockReset()})

const session = {authenticated: true} as Session
const start = Date.parse('2026-11-19T10:00:00Z')
const at = (seconds: number) => new Date(start + seconds * 1000).toISOString()

function series(metric: string, unit: string, values: number[], labels: Record<string, string> = {}, aggregation: 'sum' | 'mean' = 'mean'): MetricSeries {
  return {metric, unit, aggregation, labels, points: values.map((value, index) => ({timestamp: at(index * 15), value}))}
}

function dataset(id: string, spec: Partial<TimeSeriesDataset['spec']> & {series: MetricSeries[]}): LoadedDataset {
  const artifact: Artifact = {id, operationId: `op-${id}`, workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: `sha:${id}`, sizeBytes: 1, sensitive: false, verifiedAt: at(0)}
  return {artifact, label: id, dataset: {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: id, version: 'v1alpha1'}, spec: {applicationRef: 'app', clusterServer: 'https://cluster', namespace: 'ns', start: at(0), end: at(60), stepSeconds: 15, source: {kind: 'prometheus', version: '1', profile: 'p'}, summary: {metrics: 1, series: 1, samples: 1}, ...spec}}}
}

const latencyRun = (id: string, value: number) => dataset(id, {series: [series('load.p95_response_time', 'milliseconds', [value - 1, value + 1, value - 1, value + 1, value])]})

function mockDownloads(items: LoadedDataset[]) {
  vi.mocked(request).mockImplementation(async (path: string) => path.endsWith('/figures') ? {items: []} as never : structuredClone(items.find(item => path.includes(`/${item.artifact.id}/`))!.dataset) as never)
}

async function openSection(name: string) {
  const button = await screen.findByRole('button', {name: new RegExp(`^${name}`)})
  if (button.getAttribute('aria-expanded') !== 'true') fireEvent.click(button)
}

const trial = (id: string, position: number, extra: Record<string, unknown> = {}) => ({id: `trial-${id}`, position, status: 'succeeded', resultArtifactId: id, operationId: '', ...extra})

describe('pilot repetitions', () => {
  it('excludes pilots from every statistic and plot but keeps them viewable with a badge', async () => {
    const pilot = latencyRun('pilot', 900)
    const runs = [latencyRun('r1', 10), latencyRun('r2', 20)]
    mockDownloads([pilot, ...runs])
    const experiment = {id: 'exp-pilot', name: 'Pilot study', kind: 'instance', status: 'succeeded', firstTrialPilot: true, variants: [{id: 'v', name: 'Baseline', configuration: {}, trials: [trial('pilot', 0, {pilot: true}), trial('r1', 1), trial('r2', 2)]}]} as unknown as Experiment
    render(<ResultsView artifacts={[pilot, ...runs].map(item => item.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    await openSection('Statistics')
    const across = await screen.findByRole('table', {name: 'Statistics across executions'})
    expect(within(across).getByText('P95 latency — client (Locust)').closest('tr')).toHaveTextContent(/ms.*15\.0/)
    expect(across).not.toHaveTextContent('900')
    fireEvent.click(screen.getByRole('tab', {name: 'Per execution'}))
    expect(within(screen.getByRole('combobox', {name: 'Execution'})).getAllByRole('option')).toHaveLength(2)
    expect(vi.mocked(request).mock.calls.some(([path]) => String(path).includes('/pilot/'))).toBe(false)
    const tab = screen.getByRole('button', {name: /Baseline · Warm-up \(excluded\)/})
    expect(within(tab).getByText('Pilot')).toBeInTheDocument()
    fireEvent.click(tab)
    expect(await screen.findByText(/excluded from every statistic, aggregate and plot/)).toBeInTheDocument()
    expect(await screen.findByRole('table', {name: 'Statistics for Baseline · Warm-up (excluded)'})).toHaveTextContent('900')
  })

  it('skips pilot trials in run-level values and in comparison preparation', async () => {
    const items = [latencyRun('p', 500), latencyRun('a', 10), latencyRun('b', 12), latencyRun('c', 30), latencyRun('d', 32)]
    const experiment = {id: 'cmp', kind: 'comparison', variants: [{id: 'one', name: 'One', configuration: {}, trials: [trial('p', 0, {pilot: true}), trial('a', 1), trial('b', 2)]}, {id: 'two', name: 'Two', configuration: {}, trials: [trial('c', 1), trial('d', 2)]}]} as unknown as Experiment
    expect(runLevelValues(experiment, items, ['load.p95_response_time'])[0].groups[0].values).toEqual([10, 12])
    const result = await prepareComparison({artifacts: items.map(item => item.artifact), experiment, configurations: {}}, new AbortController().signal, () => {}, async artifact => structuredClone(items.find(item => item.artifact.id === artifact.id)!.dataset))
    expect(result.pilots).toBe(1)
    expect(result.runLevel.find(entry => entry.metric === 'load.p95_response_time')!.groups.map(group => group.values)).toEqual([[10, 12], [30, 32]])
    expect(result.methods).toContain('1 pilot repetition was executed first and excluded')
  })

  it('shows cleanup failures and resting trials in the run list', () => {
    const resting = new Date(Date.now() + 3_600_000).toISOString()
    const experiment = {id: 'exp-rest', name: 'Resting', status: 'running', variants: [{id: 'v', name: 'Baseline', configuration: {}, trials: [trial('x', 1, {cleanupError: 'namespace stuck terminating'}), {...trial('', 2), status: 'queued', scheduledFor: resting}]}]} as unknown as Experiment
    render(<ResultsView artifacts={[]} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    const first = screen.getByRole('button', {name: /Baseline · Execution 1/})
    expect(first).toHaveTextContent('Cleanup failed')
    expect(first).toHaveAttribute('title', 'Result kept — environment cleanup failed: namespace stuck terminating')
    expect(screen.getByRole('button', {name: /Baseline · Execution 2/})).toHaveTextContent(/Resting until \d\d:\d\d:\d\d/)
    fireEvent.click(first)
    expect(screen.getByText('Result kept — environment cleanup failed: namespace stuck terminating')).toBeInTheDocument()
  })
})

describe('provenance', () => {
  const recorded = () => dataset('recorded', {
    rateWindowSeconds: 30,
    metrics: [
      {metric: 'load.p95_response_time', source: 'locust', vantage: 'client', collector: 'prometheus', query: 'histogram_quantile(0.95, sum by (le) (rate(kubephos_load_duration_milliseconds_bucket[30s])))', rateWindowSeconds: 30, description: '95th percentile of end-to-end latency measured by Locust'},
      {metric: 'application.p95_response_time', source: 'istio', vantage: 'server', query: 'histogram_quantile(0.95, istio)', rateWindowSeconds: 30, description: '95th percentile at the destination proxy'}
    ],
    series: [series('load.p95_response_time', 'milliseconds', [10, 12, 11]), series('application.p95_response_time', 'milliseconds', [4, 5, 6], {destination_workload: 'api'})]
  })

  it('labels client and server latency distinctly with their recorded query and window', () => {
    const item = recorded()
    render(<RunStatisticsTable dataset={item} metrics={['load.p95_response_time', 'application.p95_response_time']} />)
    const table = screen.getByRole('table', {name: 'Statistics for recorded'})
    const client = within(table).getByText('P95 latency — client (Locust)').closest('tr')!
    const server = within(table).getByText('P95 latency — server (Istio)').closest('tr')!
    expect(client).toHaveTextContent('Locust · client side')
    expect(server).toHaveTextContent('Istio · server side')
    expect(client).toHaveTextContent('kubephos_load_duration_milliseconds_bucket[30s]')
    expect(client).toHaveTextContent('30 s')
    expect(client).not.toHaveTextContent('catalog')
  })

  it('falls back to the metric catalog for legacy datasets and says so', () => {
    const legacy = dataset('legacy', {series: [series('cpu.cores', 'cores', [1, 2, 3], {pod: 'api-7c9b6d8f5-x2b4c'}, 'sum'), series('network.rtt', 'milliseconds', [1, 2, 3])]})
    expect(provenanceFor([legacy], 'cpu.cores')).toMatchObject({source: 'cadvisor', legacy: true})
    render(<RunStatisticsTable dataset={legacy} metrics={['cpu.cores', 'network.rtt']} />)
    const cpu = screen.getByText('CPU usage').closest('tr')!
    expect(cpu).toHaveTextContent('cAdvisor · catalog')
    expect(cpu).toHaveTextContent('predates recorded provenance')
    expect(screen.getByText('Network RTT').closest('tr')).toHaveTextContent('Mentat')
  })
})

describe('statistics tables', () => {
  it('never shows a zero-width interval for fewer than two samples', () => {
    expect(intervalText({count: 1, confidence95Low: 5, confidence95High: 5}, 'milliseconds')).toBe('n/a')
    expect(intervalText({count: 3, confidence95Low: 4, confidence95High: 6, nEffective: 3}, 'milliseconds')).toBe('4.0 ms–6.0 ms')
    render(<RunStatisticsTable dataset={dataset('single', {series: [series('load.p95_response_time', 'milliseconds', [7])]})} metrics={['load.p95_response_time']} />)
    const cells = screen.getByText('P95 latency — client (Locust)').closest('tr')!.querySelectorAll('td')
    expect(cells[3]).toHaveTextContent('1')
    expect(cells[11]).toHaveTextContent('n/a')
  })

  it('reports n and the autocorrelation-adjusted n_eff for a run', () => {
    render(<RunStatisticsTable dataset={dataset('ar', {end: at(200), series: [series('load.p95_response_time', 'milliseconds', [1, 2, 3, 4, 5, 6, 7, 8])]})} metrics={['load.p95_response_time']} />)
    const cells = screen.getByText('P95 latency — client (Locust)').closest('tr')!.querySelectorAll('td')
    expect(cells[3]).toHaveTextContent('8')
    // A trend has ρ₁ > 0: n_eff is smaller than n but at least 2.
    expect(Number(cells[4].textContent)).toBeGreaterThanOrEqual(2)
    expect(Number(cells[4].textContent)).toBeLessThan(8)
    expect(screen.getByRole('columnheader', {name: /n\s*eff/})).toHaveAttribute('title', expect.stringContaining('lag-1 autocorrelation'))
  })

  it('fills the comparison table from variantComparisons and follows the baseline selector', async () => {
    const items = [latencyRun('a1', 10), latencyRun('a2', 12), latencyRun('a3', 11), latencyRun('b1', 20), latencyRun('b2', 23), latencyRun('b3', 21), latencyRun('c1', 15), latencyRun('c2', 16)]
    mockDownloads(items)
    const variant = (id: string, name: string, runs: string[]) => ({id, name, configuration: {}, trials: runs.map((run, index) => trial(run, index + 1))})
    const experiment = {id: 'suite', name: 'Suite', kind: 'suite', status: 'succeeded', variants: [variant('a', 'Alpha', ['a1', 'a2', 'a3']), variant('b', 'Beta', ['b1', 'b2', 'b3']), variant('c', 'Gamma', ['c1', 'c2'])]} as unknown as Experiment
    render(<ResultsView artifacts={items.map(item => item.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
    await openSection('Statistics')
    fireEvent.click(await screen.findByRole('tab', {name: 'Differences'}))
    const table = await screen.findByRole('table', {name: 'Differences between strategies'})
    const labelled = items.map(item => ({...item, label: item.artifact.id}))
    const expected = variantComparisons(experiment, labelled, ['load.p95_response_time'])
    expect(expected).toHaveLength(2)
    const row = within(table).getByText('Beta vs Alpha').closest('tr')!
    const beta = expected.find(item => item.group === 'Beta')!
    expect(row).toHaveTextContent(formatDifference(beta.difference, 'milliseconds'))
    expect(row).toHaveTextContent(formatPValue(beta.pValue))
    expect(row).toHaveTextContent(formatPValue(beta.adjustedPValue))
    expect(row).toHaveTextContent(formatPValue(beta.mannWhitneyPValue))
    expect(row).toHaveTextContent(`${beta.hedgesG!.toFixed(2)} [`)
    expect(row).toHaveTextContent('3 / 3')
    expect(within(table).getByText('Gamma vs Alpha').closest('tr')).toHaveTextContent('n < 3')
    expect(screen.getByText(/family of 2 Welch tests/)).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: /^Baseline/}), {target: {value: 'b'}})
    expect(within(screen.getByRole('table', {name: 'Differences between strategies'})).getByText('Alpha vs Beta')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: /^Comparisons/}), {target: {value: 'all-pairs'}})
    expect(within(screen.getByRole('table', {name: 'Differences between strategies'})).getAllByRole('row')).toHaveLength(4)
    expect(screen.getByText(/family of 3 Welch tests/)).toBeInTheDocument()
    expect(screen.queryByText("Cohen's d")).not.toBeInTheDocument()
  })
})

const locustSummary = (requests: number, failures: number, p95: number, extra: Partial<LocustSummary> = {}): LocustSummary => ({
  workers: 2, requests, failures,
  endpoints: [{method: 'GET', name: '/cart', requests, failures, meanMs: 10, p50Ms: 8, p90Ms: 15, p95Ms: p95, p99Ms: p95 * 2, p999Ms: p95 * 3, maxMs: p95 * 4}],
  total: {method: '', name: 'Aggregated', requests, failures, meanMs: 10, p50Ms: 8, p90Ms: 15, p95Ms: p95, p99Ms: p95 * 2, p999Ms: p95 * 3, maxMs: p95 * 4},
  errors: failures ? [{method: 'GET', name: '/cart', error: 'HTTPError 503', occurrences: failures}] : [],
  ...extra
})

describe('Locust summary', () => {
  it('reports exact failure percentages, endpoint percentiles and across-run intervals', () => {
    const group = locustGroup('v', 'Baseline', [{label: 'r1', summary: locustSummary(1000, 3, 20)}, {label: 'r2', summary: locustSummary(2000, 2, 30)}])
    const aggregated = group.rows.find(row => row.aggregated)!
    expect(aggregated.failurePercent!.mean).toBeCloseTo((0.3 + 0.1) / 2)
    expect(aggregated.latency.p95Ms).toMatchObject({mean: 25, n: 2})
    // Exact t with 1 df: 25 ± 12.706 · 7.071/√2.
    expect(aggregated.latency.p95Ms!.high! - 25).toBeCloseTo(12.7062 * Math.sqrt(50) / Math.sqrt(2), 2)
    expect(group.errors).toEqual([{method: 'GET', name: '/cart', error: 'HTTPError 503', occurrences: 5, runs: 2}])
  })

  it('renders the endpoint table with an Aggregated row, errors and the CPU saturation badge', () => {
    const group = locustGroup('v', 'Baseline', [{label: 'r1', summary: locustSummary(4000, 1, 42, {workerCpu: [{worker: 'kubephos-load-a', maxPercent: 96, warnings: 2}]})}])
    render(<LocustSummaryPanel groups={[group]} />)
    const table = screen.getByRole('table', {name: 'Locust endpoints for Baseline'})
    const rows = within(table).getAllByRole('row')
    expect(rows.at(-1)).toHaveTextContent('Aggregated')
    expect(rows.at(-1)).toHaveTextContent('0.025')
    expect(rows[1]).toHaveTextContent('/cart')
    expect(rows[1]).toHaveTextContent('42')
    expect(within(table).getAllByRole('columnheader').map(cell => cell.textContent)).toEqual(['Endpoint', 'Requests', 'Failures', 'Failure %', 'Mean (ms)', 'P50 (ms)', 'P90 (ms)', 'P95 (ms)', 'P99 (ms)', 'P99.9 (ms)', 'Max (ms)'])
    expect(screen.getByRole('table', {name: 'Locust errors for Baseline'})).toHaveTextContent('HTTPError 503')
    expect(screen.getByText('Load generator saturated — latency may be inflated')).toBeInTheDocument()
  })
})

describe('load fidelity', () => {
  const run = (offered: number, achieved: number, extra: Partial<TimeSeriesDataset['spec']> = {}) => dataset('fidelity', {runContext: {loadSession: {warmupSeconds: 60}}, series: [series('load.offered_rps', 'requests_per_second', [offered, offered, offered], {}, 'sum'), series('load.achieved_rps', 'requests_per_second', [achieved, achieved, achieved], {}, 'sum')], ...extra})

  it('warns below 95% of the offered rate only', () => {
    expect(fidelityThresholds.achievedRatio).toBe(.95)
    expect(loadFidelity(run(100, 96)).warnings).toEqual([])
    const low = loadFidelity(run(100, 94))
    expect(low.ratio).toBeCloseTo(.94)
    expect(low.warnings[0]).toContain('94.0% of the offered rate')
    expect(low.warnings[0]).toContain('threshold 95%')
  })

  it('flags coordinated omission from late requests or schedule lag', () => {
    expect(loadFidelity(run(100, 100, {locustSummary: locustSummary(10000, 0, 10, {schedule: {lateRequests: 99, maxLagSeconds: .2, meanLagSeconds: .001}})})).warnings).toEqual([])
    const late = loadFidelity(run(100, 100, {locustSummary: locustSummary(10000, 0, 10, {schedule: {lateRequests: 100, maxLagSeconds: .2, meanLagSeconds: .001}})}))
    expect(late.warnings[0]).toContain('1.0% of requests were issued late (threshold 1%)')
    const lag = loadFidelity(run(100, 100, {locustSummary: locustSummary(10000, 0, 10, {schedule: {lateRequests: 0, maxLagSeconds: .9, meanLagSeconds: .06}})}))
    expect(lag.warnings[0]).toContain('mean schedule lag 60 ms (threshold 50 ms)')
  })

  it('derives the achieved rate from successful and failed requests and the offered rate from the configuration', () => {
    const legacy = dataset('legacy', {end: at(120), series: [series('load.configured_rps', 'requests_per_second', Array(9).fill(50), {}, 'sum'), series('load.successful_rps', 'requests_per_second', Array(9).fill(40), {}, 'sum'), series('load.failed_rps', 'requests_per_second', Array(9).fill(4), {}, 'sum')]})
    const result = loadFidelity(legacy)
    expect(result).toMatchObject({offeredSource: 'configured', achievedSource: 'successful+failed', offeredMean: 50, achievedMean: 44})
    expect(result.warnings[0]).toContain('88.0% of the configured rate')
  })

  it('ignores the first rate window of runs without a warm-up (legacy): rate() still covers time before traffic started', () => {
    // Constant 50 req/s from t = 0; a 60 s rate() window only reaches it after one window.
    const achieved = [6.25, 18.75, 31.25, 43.75, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50, 50]
    const legacy = dataset('legacy', {end: at(300), series: [series('load.configured_rps', 'requests_per_second', achieved.map(() => 50), {}, 'sum'), series('load.successful_rps', 'requests_per_second', achieved, {}, 'sum')]})
    const result = loadFidelity(legacy)
    expect(result.ratio).toBeCloseTo(1)
    expect(result.warnings).toEqual([])
    // With a warm-up covering the rate window every instant is comparable.
    const warmed = dataset('warm', {end: at(300), runContext: {loadSession: {warmupSeconds: 60}}, rateWindowSeconds: 60, series: [series('load.offered_rps', 'requests_per_second', achieved.map(() => 50), {}, 'sum'), series('load.achieved_rps', 'requests_per_second', achieved.map(() => 40), {}, 'sum')]})
    expect(loadFidelity(warmed).ratio).toBeCloseTo(.8)
  })
})

describe('methods paragraph', () => {
  it('states only what the recorded data supports', () => {
    const context = {loadSession: {warmupSeconds: 60, drainSeconds: 30, clock: 'shared-start', workers: 4}, prometheus: {version: '2.53.0', scrapeIntervalSeconds: 15}}
    const run = (id: string) => dataset(id, {rateWindowSeconds: 30, scrapeIntervalSeconds: 15, workloadStartedAt: at(0), end: at(600), runContext: context, series: [series('load.p95_response_time', 'milliseconds', [1, 2]), series('application.p95_response_time', 'milliseconds', [1, 2], {destination_workload: 'api'}), series('load.p50_response_time', 'milliseconds', [1, 2])]})
    const items = ['a', 'b', 'c', 'd'].map(run)
    const configuration = {components: [{capability: 'load.kubernetes', configuration: {steps: [{type: 'constant', durationSeconds: 600, rps: 200}]}}]}
    const experiment = {id: 'm', kind: 'suite', variants: [{id: 'x', name: 'Default', configuration, trials: [trial('a', 1), trial('b', 2)]}, {id: 'y', name: 'Custom', configuration, trials: [trial('c', 1), trial('d', 2)]}]} as unknown as Experiment
    const text = methodsParagraph({experiment, datasets: items, metrics: ['load.p95_response_time'], pilots: 2, comparison: {familySize: 1, mode: 'baseline', baseline: 'Default'}})
    expect(text).toContain('We compared 2 configurations (Default: 2 repetitions; Custom: 2 repetitions).')
    expect(text).toContain('2 pilot repetitions were executed first and excluded')
    expect(text).toContain('a 60 s warm-up excluded from analysis and a measured window of 10 min')
    expect(text).toContain('a constant 200 req/s for 10 min')
    expect(text).toContain('4 workers')
    expect(text).toContain('a scrape interval of 15 s, a 30 s window for rate() and histogram_quantile() and a query resolution of 15 s')
    expect(text).toContain('Client-side latency percentiles (P50, P95) were measured by the Locust load generator')
    expect(text).toContain('Server-side latency percentiles (P95) were reported by the Istio data plane')
    expect(text).toContain('exact Student t quantile with n − 1 degrees of freedom')
    expect(text).toContain('Holm–Bonferroni procedure over a family of 1 test')
    expect(text).toContain("Hedges' g")
    expect(text).toContain('AR(1) effective sample size')
    const minimal = methodsParagraph({datasets: [dataset('old', {series: [series('cpu.cores', 'cores', [1, 2], {}, 'sum')]})], metrics: ['cpu.cores']})
    expect(minimal).not.toContain('scrape interval')
    expect(minimal).not.toContain('warm-up')
    expect(minimal).not.toContain('Welch')
    expect(minimal).toContain('collector default of 60 s')
  })
})

describe('publication figures', () => {
  const plot = (settings: FigureSettings) => {
    const item = latencyRun('fig', 10)
    const view = render(<TimeSeriesFigure name="p95" metric="load.p95_response_time" title="P95 latency" axisLabel="P95 latency (ms)" series={[{artifactID: 'fig', label: 'Baseline', unit: 'milliseconds', points: item.dataset.spec.series[0].points.map((point, index) => ({timestamp: new Date(index * 15000).toISOString(), value: point.value})), deviation: item.dataset.spec.series[0].points.map((_, index) => ({timestamp: new Date(index * 15000).toISOString(), value: 1})), runs: 3}]} settings={settings} provenance={[{metric: 'load.p95_response_time', provenance: provenanceFor([item], 'load.p95_response_time')}]} />)
    return {view, svg: view.container.querySelector('svg') as SVGSVGElement}
  }

  it('exports SVG and PDF at the physical size with the chosen font family and point size', () => {
    const {svg} = plot({...defaultFigureSettings, size: 'double', font: 'sans', fontSize: 9})
    const document = svgDocument(svg, {title: 'P95 latency', caption: 'Caption with provenance'})
    expect(document).toContain('width="7.16in"')
    expect(document.match(/xmlns="http:\/\/www\.w3\.org\/2000\/svg"/g)).toHaveLength(1)
    expect(document).toMatch(/viewBox="0 0 515.52 \d+(\.\d+)?"/)
    expect(document).toContain('<title>P95 latency</title>')
    expect(document).toContain('<desc>Caption with provenance</desc>')
    const ticks = [...svg.querySelectorAll('text.tick')]
    expect(ticks.length).toBeGreaterThan(3)
    for (const tick of ticks) {
      expect(tick).toHaveAttribute('font-size', '9')
      expect(tick.getAttribute('font-family')).toContain('Helvetica')
    }
    const pdf = new TextDecoder().decode(svgToPDF(svg, {title: 'P95 latency', caption: 'Locust · client side'}))
    expect(pdf).toMatch(/\/MediaBox \[0 0 515\.52 [\d.]+\]/)
    expect(pdf).toContain('/F1 9.000 Tf')
    expect(pdf).not.toContain('/F2 9.000 Tf')
    expect(pdf).toContain('/BaseFont /Helvetica ')
    expect(pdf).toContain('/Title (P95 latency)')
    expect(pdf).toContain('/Subject (Locust \\267 client side)')
    cleanup()
    const serif = plot({...defaultFigureSettings, size: 'single', font: 'serif', fontSize: 8})
    expect(svgDocument(serif.svg)).toContain('width="3.5in"')
    expect(new TextDecoder().decode(svgToPDF(serif.svg))).toContain('/F2 8.000 Tf')
  })

  it('draws a 95% CI band with the exact t quantile and exports the plotted data as CSV', async () => {
    const {svg} = plot({...defaultFigureSettings, uncertainty: 'ci'})
    expect(svg.querySelector('.deviation-band path')).not.toBeNull()
    expect(screen.getByText(/95% confidence interval of the mean across repetitions \(exact Student t/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Data CSV'}))
    await waitFor(() => expect(downloadBlob).toHaveBeenCalled())
    const [name, blob] = vi.mocked(downloadBlob).mock.calls[0]
    expect(name).toBe('kubephos-p95-data.csv')
    const lines = (await (blob as Blob).text()).split('\n')
    expect(lines[0]).toBe('series,minute,value,sd_across_runs,band_low,band_high,n_runs,metric,source,vantage,rate_window_seconds')
    // t(0.975, 2) = 4.3027; half-width = 4.3027 · 1 / √3.
    const [, minute, value, sd, low, high, runs, metric, source, vantage] = lines[1].split(',')
    expect([minute, value, sd, runs, metric, source, vantage]).toEqual(['0', '9', '1', '3', 'load.p95_response_time', 'locust', 'client'])
    expect(Number(high) - Number(value)).toBeCloseTo(4.302653 / Math.sqrt(3), 4)
    expect(Number(value) - Number(low)).toBeCloseTo(4.302653 / Math.sqrt(3), 4)
  })

  it('persists figure settings per page and survives unavailable storage', () => {
    saveFigureSettings('experiment:a', {...defaultFigureSettings, size: 'slide', fontSize: 11, grayscale: true})
    expect(loadFigureSettings('experiment:a')).toMatchObject({size: 'slide', fontSize: 11, grayscale: true})
    expect(loadFigureSettings('experiment:b')).toEqual(defaultFigureSettings)
    const spy = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {throw new Error('blocked')})
    expect(loadFigureSettings('experiment:a')).toEqual(defaultFigureSettings)
    spy.mockRestore()
  })
})

describe('accessibility', () => {
  it('keeps the overview, every disclosure section, the charts and KubeStudio free of axe violations', async () => {
    const items = ['a1', 'a2', 'a3', 'b1', 'b2', 'b3'].map((id, index) => {
      const item = latencyRun(id, 10 + index)
      item.dataset.spec.locustSummary = locustSummary(1000, 1, 20 + index, {workerCpu: [{worker: 'w', maxPercent: 95, warnings: 1}]})
      item.dataset.spec.runContext = {loadSession: {warmupSeconds: 30}, kubernetes: {serverVersion: 'v1.31.2'}, nodes: [{name: 'n1', zone: 'z1'}], concurrentLoad: [{namespace: 'other', maxPods: 2}]}
      item.dataset.spec.series.push(series('load.offered_rps', 'requests_per_second', [10, 10, 10, 10, 10], {}, 'sum'), series('load.achieved_rps', 'requests_per_second', [9, 9, 9, 9, 9], {}, 'sum'))
      return item
    })
    mockDownloads(items)
    const experiment = {id: 'a11y', name: 'A11y', kind: 'suite', status: 'succeeded', variants: [{id: 'a', name: 'Alpha', configuration: {}, trials: ['a1', 'a2', 'a3'].map((id, index) => trial(id, index + 1))}, {id: 'b', name: 'Beta', configuration: {}, trials: ['b1', 'b2', 'b3'].map((id, index) => trial(id, index + 1))}]} as unknown as Experiment
    render(<main><h1>KubePhos</h1><ResultsView artifacts={items.map(item => item.artifact)} experiments={[experiment]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} /></main>)
    await screen.findByRole('list', {name: 'Key results'})
    expect(screen.getAllByText(/Achieved request rate was 90.0% of the offered rate/).length).toBeGreaterThan(0)
    expect(screen.getAllByText(/Other KubePhos load generators were active in namespace other/)).toHaveLength(6)
    const audit = async (section: string) => {
      const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
      expect({section, violations: results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))}).toEqual({section, violations: []})
    }
    await audit('Overview')
    for (const section of ['Endpoints', 'Statistics', 'Environment & methods']) {
      await openSection(section)
      await audit(section)
    }
    fireEvent.click(screen.getByRole('tab', {name: 'Differences'}))
    await screen.findByRole('table', {name: 'Differences between strategies'})
    await audit('Statistics, differences tab')
    const charts = within(screen.getByRole('region', {name: 'Charts'}))
    fireEvent.click(charts.getAllByRole('button', {name: /^Actions for /})[0])
    expect(screen.getByRole('menu')).toBeInTheDocument()
    await audit('Charts and a chart menu')
    fireEvent.keyDown(screen.getByRole('menu'), {key: 'Escape'})
    fireEvent.click(screen.getByRole('button', {name: 'Create figure'}))
    const dialog = await screen.findByRole('dialog', {name: 'KubeStudio'})
    const pick = (kind: string) => {fireEvent.click(within(dialog).getByRole('button', {name: /^Figure:/})); fireEvent.click(within(dialog).getByRole('dialog', {name: 'Choose a figure'}).querySelector(`[data-kind="${kind}"]`)!)}
    // A new figure opens on the chart types.
    await audit('KubeStudio figure switcher')
    fireEvent.click(within(dialog).getByRole('dialog', {name: 'Choose a figure'}).querySelector('[data-kind="fidelity"]')!)
    expect(within(dialog).getByRole('img', {name: 'Offered versus achieved request rate chart'})).toBeInTheDocument()
    pick('runlevel')
    // Clicking the Y axis title selects the axis and opens its rename field.
    fireEvent.click(dialog.querySelector('svg [data-select^="axis:left"] .axis-label')!)
    expect(within(dialog).getByRole('textbox', {name: 'Y axis title'})).toHaveFocus()
    fireEvent.change(within(dialog).getByRole('combobox', {name: 'Metric'}), {target: {value: 'load.p95_response_time'}})
    expect(within(dialog).getByRole('img', {name: 'P95 latency — client (Locust) per repetition: individual repetitions with mean and 95% confidence interval'})).toBeInTheDocument()
    pick('ladder')
    expect(within(dialog).getByRole('img', {name: 'Latency percentile ladder: client and server percentiles per configuration'})).toBeInTheDocument()
    fireEvent.click(within(dialog).getByRole('button', {name: 'Export'}))
    await audit('KubeStudio export popover')
  }, 20000)
})

describe('long runs and gaps in figures', () => {
  const settings = {...defaultFigureSettings, uncertainty: 'ci' as const}
  it('scales and draws 4 × 20,000-sample curves with CI bands without exceeding the argument limit', () => {
    expect(valueScale(Array.from({length: 300000}, (_, index) => index % 100), {zero: true, log: false}).upper).toBe(100)
    expect(valueScale(Array.from({length: 300000}, (_, index) => 1 + index % 100), {zero: false, log: true}).lower).toBe(1)
    const curve = (index: number) => ({artifactID: `v${index}`, label: `Variant ${index}`, unit: 'milliseconds', runs: 3, stepMilliseconds: 1000,
      points: Array.from({length: 20000}, (_, point) => ({timestamp: new Date(point * 1000).toISOString(), value: 10 + index})),
      deviation: Array.from({length: 20000}, (_, point) => ({timestamp: new Date(point * 1000).toISOString(), value: 1}))})
    const item = latencyRun('long', 10)
    const view = render(<TimeSeriesFigure name="long" metric="load.p95_response_time" title="P95" axisLabel="P95 (ms)" series={[0, 1, 2, 3].map(curve)} settings={settings} provenance={[{metric: 'load.p95_response_time', provenance: provenanceFor([item], 'load.p95_response_time')}]} />)
    expect(view.container.querySelectorAll('.series-line')).toHaveLength(4)
  })

  it('breaks a single run\'s line at missing windows (e.g. failure % unavailable while idle)', () => {
    const at = (seconds: number) => new Date(seconds * 1000).toISOString()
    const points = [0, 15, 30, 300, 315].map(seconds => ({timestamp: at(seconds), value: 1}))
    const item = latencyRun('gap', 10)
    const view = render(<TimeSeriesFigure name="gap" metric="load.failure_percentage" title="Failures" axisLabel="Failed requests (%)" series={[{artifactID: 'gap', label: 'Run', unit: 'percent', points, stepMilliseconds: 15000}]} settings={settings} provenance={[{metric: 'load.failure_percentage', provenance: provenanceFor([item], 'load.failure_percentage')}]} />)
    expect(view.container.querySelector('.series-line')?.getAttribute('d')?.match(/M/g)).toHaveLength(2)
  })
})

describe('executed pilots', () => {
  it('counts only pilots that ran in the methods paragraph', () => {
    const experiment = {variants: [{trials: [trial('p1', 0, {pilot: true}), trial('r1', 1)]}, {trials: [trial('p2', 0, {pilot: true, status: 'queued', resultArtifactId: ''})]}]} as unknown as Experiment
    expect(executedPilotCount(experiment)).toBe(1)
  })
})
