import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {AggregateSeries} from '../../resultsData'
import {AllMetrics} from './AllMetrics'
import {defaultFigureSettings} from './figureSettings'
import type {ResolvedProvenance} from './runs'
import {XYChart, XYFigure} from './XYChart'
import {alignByTimestamp, buildXY, defaultXYOptions, defaultXYSelection, fitAt, fitXY, pathSegments, spearman, xyCSV, type XYRunInput, type XYSource} from './xy'

const iso = (seconds: number) => new Date(seconds * 1000).toISOString()
const curve = (unit: string, values: Array<[number, number]>, extra: Partial<AggregateSeries> = {}): AggregateSeries => ({artifactID: 'a', label: 'a', unit, points: values.map(([t, value]) => ({timestamp: iso(t), value})), stepMilliseconds: 15000, ...extra})
const provenance = (metric: string): ResolvedProvenance => ({metric, source: metric.startsWith('load.') ? 'locust' : 'cadvisor', vantage: metric.startsWith('load.') ? 'client' : 'resource', description: ''} as unknown as ResolvedProvenance)

// Two runs of a baseline variant and one run of a tuned variant; run 2 misses a Y window.
const data: Record<string, Record<string, AggregateSeries>> = {
  r1: {'load.achieved_rps': curve('requests_per_second', [[0, 100], [15, 150], [30, 200], [45, 250]]), 'load.p95_response_time': curve('milliseconds', [[0, 210], [15, 300], [30, 410], [45, 500]])},
  r2: {'load.achieved_rps': curve('requests_per_second', [[0, 110], [15, 160], [30, 210], [45, 260]]), 'load.p95_response_time': curve('milliseconds', [[0, 220], [30, 420], [45, 520]])},
  r3: {'load.achieved_rps': curve('requests_per_second', [[0, 90], [15, 140], [30, 190]]), 'load.p95_response_time': curve('milliseconds', [[0, 100], [15, 120], [30, 150], [60, 999]])}
}
const variants: Record<string, [string, string, string | undefined]> = {r1: ['base', 'Baseline', undefined], r2: ['base', 'Baseline', undefined], r3: ['tuned', 'Tuned', '#8e44ad']}
const source: XYSource = {
  metrics: ['cpu.cores', 'load.achieved_rps', 'load.p95_response_time'], level: 'run', provenance,
  services: metric => metric === 'cpu.cores' ? ['cartservice', 'frontend'] : undefined,
  runs: ({metric}) => Object.entries(data).flatMap(([run, metrics]): XYRunInput[] => metrics[metric] ? [{runID: run, runLabel: `run ${run}`, seriesID: variants[run][0], seriesLabel: variants[run][1], color: variants[run][2], series: metrics[metric]}] : [])
}

describe('X–Y pairing', () => {
  it('pairs only timestamps present in both series and never interpolates', () => {
    const pairs = alignByTimestamp(curve('', [[0, 1], [15, 2], [30, 3]]).points, curve('', [[0, 10], [30, 30], [45, 40]]).points)
    expect(pairs).toEqual([{t: 0, x: 1, y: 10}, {t: 30000, x: 3, y: 30}])
  })

  it('groups runs by variant and keeps units and provenance in the axis titles', () => {
    const xy = buildXY(source, {metric: 'load.achieved_rps'}, {metric: 'load.p95_response_time'})
    expect(xy.series.map(series => [series.label, series.runs.map(run => run.points.length)])).toEqual([['Baseline', [4, 3]], ['Tuned', [3]]])
    expect(xy.x.title).toBe('Achieved rate (req/s) · Locust · client side')
    expect(xy.y.title).toBe('P95 latency, client (ms) · Locust · client side')
    expect(buildXY(source, {metric: 'cpu.cores', service: 'cartservice'}, {metric: 'load.achieved_rps'}).x.title).toContain('— cartservice')
  })

  it('breaks the time-ordered path at a missing window', () => {
    const run = buildXY(source, {metric: 'load.achieved_rps'}, {metric: 'load.p95_response_time'}).series[0].runs[1]
    expect(pathSegments(run).map(segment => segment.map(point => point.t / 1000))).toEqual([[0], [30, 45]])
  })

  it('picks achieved rate vs client P95 by default', () => {
    expect(defaultXYSelection(source.metrics)).toEqual({x: {metric: 'load.achieved_rps'}, y: {metric: 'load.p95_response_time'}})
  })
})

describe('X–Y fit', () => {
  it('recovers an exact line with r = 1 and a zero-width slope interval', () => {
    const fit = fitXY([1, 2, 3, 4, 5].map(x => ({t: x, x, y: 3 + 2 * x})))!
    expect(fit.slope).toBeCloseTo(2)
    expect(fit.intercept).toBeCloseTo(3)
    expect(fit.r).toBeCloseTo(1)
    expect(fit.r2).toBeCloseTo(1)
    expect(fit.slopeCI[1] - fit.slopeCI[0]).toBeCloseTo(0)
    expect(fit.spearman).toBeCloseTo(1)
  })

  it('matches a textbook regression (slope CI with Student t, n − 2 df)', () => {
    // x = 1..5, y = 2, 4, 5, 4, 5: b = 0.6, a = 2.2, r = 0.7746, SE(b) = 0.2828, t(3) = 3.1824.
    const fit = fitXY([2, 4, 5, 4, 5].map((y, index) => ({t: index, x: index + 1, y})))!
    expect(fit.slope).toBeCloseTo(.6)
    expect(fit.intercept).toBeCloseTo(2.2)
    expect(fit.r).toBeCloseTo(.7746, 3)
    expect(fit.slopeCI[0]).toBeCloseTo(.6 - 3.1824 * .2828, 2)
    expect(fit.slopeCI[1]).toBeCloseTo(.6 + 3.1824 * .2828, 2)
  })

  it('computes Spearman ρ with tied ranks and fits power laws on log–log axes', () => {
    expect(spearman([1, 2, 2, 3], [10, 20, 20, 30])).toBeCloseTo(1)
    expect(spearman([1, 2, 3, 4], [4, 3, 2, 1])).toBeCloseTo(-1)
    const fit = fitXY([1, 10, 100, 1000].map(x => ({t: x, x, y: 5 * x ** 2})), {logX: true, logY: true})!
    expect(fit.slope).toBeCloseTo(2)
    expect(fitAt(fit, 10)).toBeCloseTo(500)
    expect(fitXY([{t: 0, x: 1, y: 1}, {t: 1, x: 1, y: 2}, {t: 2, x: 1, y: 3}])).toBeUndefined()
  })

  it('exports every pair with both metrics, run and fit columns', () => {
    const csv = xyCSV(buildXY(source, {metric: 'load.achieved_rps'}, {metric: 'load.p95_response_time'}), defaultXYOptions).split('\n')
    expect(csv[0]).toMatch(/^series,run,seconds_since_window_start,x_metric,x_service,x_unit,x,y_metric/)
    expect(csv).toHaveLength(1 + 4 + 3 + 3)
    expect(csv[1]).toMatch(/^Baseline,run r1,0,load.achieved_rps,,requests_per_second,100,load.p95_response_time,,milliseconds,210,run,7,/)
  })
})

describe('X–Y chart', () => {
  const xy = buildXY(source, {metric: 'load.achieved_rps'}, {metric: 'load.p95_response_time'})

  it('renders one mark per window, fit statistics in the legend and a table alternative', () => {
    const {container} = render(<XYChart data={xy} options={{...defaultXYOptions, path: true}} />)
    expect(container.querySelectorAll('.xy-dot')).toHaveLength(10)
    expect(container.querySelectorAll('.xy-fit')).toHaveLength(2)
    expect(container.querySelectorAll('.xy-path').length).toBeGreaterThan(0)
    const legend = screen.getByRole('list', {name: 'Series'})
    expect(within(legend).getByText(/Baseline/)).toBeInTheDocument()
    expect(legend.textContent).toMatch(/n = 7 · r = 1\.00 · R² = 1\.00 · slope = [\d.]+ \[[\d.]+, [\d.]+\] · Spearman ρ = 1\.00/)
    fireEvent.click(screen.getByRole('button', {name: 'View as table'}))
    const table = screen.getByRole('table')
    expect(within(table).getAllByRole('row')).toHaveLength(11)
  })

  it('reads the nearest point with the keyboard: timestamp and both values', () => {
    render(<XYChart data={xy} options={defaultXYOptions} />)
    const plot = screen.getByRole('img')
    expect(plot.getAttribute('aria-label')).toMatch(/10 sampling windows in 2 series/)
    fireEvent.keyDown(plot, {key: 'ArrowRight'})
    expect(document.querySelector('.xy-tooltip')?.textContent).toMatch(/0:00 into the measured window.*run r1.*100 req\/s.*210 ms/)
  })

  it('drops non-positive values on log axes and says so', () => {
    const zero = buildXY({...source, runs: selection => source.runs(selection).map(run => run.runID === 'r1' && selection.metric === 'load.achieved_rps' ? {...run, series: curve('requests_per_second', [[0, 0], [15, 150], [30, 200], [45, 250]])} : run)}, {metric: 'load.achieved_rps'}, {metric: 'load.p95_response_time'})
    const {container} = render(<XYChart data={zero} options={{...defaultXYOptions, logX: true}} />)
    expect(container.querySelectorAll('.xy-dot')).toHaveLength(9)
    expect(screen.getByText(/1 windows with non-positive values are not shown/)).toBeInTheDocument()
  })

  it('exports a publication figure that keeps the user colour on white', () => {
    const {container} = render(<XYFigure data={xy} options={defaultXYOptions} settings={defaultFigureSettings} />)
    expect(container.querySelector('svg.pub-figure')).not.toBeNull()
    expect(container.innerHTML).toContain('#8e44ad')
    expect(screen.getByText(/paired by timestamp within the measured window and never interpolated/)).toBeInTheDocument()
    expect(screen.getByRole('button', {name: /CSV/})).toBeInTheDocument()
  })

  it('is reachable from All metrics with metric and service pickers', async () => {
    render(<AllMetrics metrics={source.metrics} plot={() => []} groups={[]} provenance={provenance} xy={source} />)
    fireEvent.click(screen.getByRole('button', {name: /Plot one metric against another/}))
    expect((await screen.findByLabelText('X axis') as HTMLSelectElement).value).toBe('load.achieved_rps')
    expect((screen.getByLabelText('Y axis') as HTMLSelectElement).value).toBe('load.p95_response_time')
    fireEvent.change(screen.getByLabelText('X axis'), {target: {value: 'cpu.cores'}})
    expect(screen.getByLabelText('X service')).toBeInTheDocument()
  })
})
