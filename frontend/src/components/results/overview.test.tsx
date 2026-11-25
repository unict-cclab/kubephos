import {cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {downloadBlob} from '../../charts'
import type {AggregateSeries, RunLevelMetric, VariantComparison} from '../../resultsData'
import {KubeStudio} from './kubestudio/KubeStudio'
import type {StudioData} from './kubestudio/image'
import {defaultFigureSettings} from './figureSettings'
import type {ServiceHealth} from './insights'
import {Disclosure, KpiCards, ServicesTable, VerdictStrip} from './Overview'
import {comparisonVerdict, kpiCards, primaryPanels, rankServiceSummaries, significance} from './overview'
import {provenanceFor} from './runs'
import {TimeChart} from './TimeChart'

vi.mock('../../charts', async original => ({...await original<typeof import('../../charts')>(), downloadBlob: vi.fn()}))
afterEach(() => {cleanup(); vi.mocked(downloadBlob).mockReset()})

const group = (id: string, label: string, values: number[]) => ({id, label, values, artifactIds: values.map((_, index) => `${id}-${index}`)})
const metric = (name: string, unit: string, a: number[], b?: number[]): RunLevelMetric => ({metric: name, unit, groups: [group('a', 'Alpha', a), ...(b ? [group('b', 'Beta', b)] : [])]})
const curve = (label: string, values: number[], unit = 'requests_per_second'): AggregateSeries => ({artifactID: label, label, unit, stepMilliseconds: 15000, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value}))})
const provenance = (name: string) => provenanceFor([], name)

describe('KPI model', () => {
  const single = [metric('load.achieved_rps', 'requests_per_second', [163]), metric('load.offered_rps', 'requests_per_second', [250]), metric('load.p95_response_time', 'milliseconds', [2418]), metric('load.p50_response_time', 'milliseconds', [1627]), metric('load.failure_percentage', 'percent', [.022]), metric('load.slo_violation_percentage', 'percent', [100])]

  it('reports achieved vs offered, client P95 with P50/P99, errors and SLO compliance with tones', () => {
    const cards = kpiCards({runLevel: single, comparisons: [], slo: 350})
    expect(cards.map(card => card.id)).toEqual(['throughput', 'latency', 'errors', 'slo'])
    const [throughput, latency, errors, slo] = cards
    expect(throughput.entries[0]).toMatchObject({value: 163, tone: 'critical'})
    expect(throughput.detail(throughput.entries[0])).toBe('of 250 req/s offered (65 %)')
    expect(latency.entries[0].tone).toBe('warning')
    expect(latency.detail(latency.entries[0])).toBe('P50 1.63 s')
    expect(errors.entries[0].tone).toBe('ok')
    expect(slo.entries[0]).toMatchObject({value: 0, tone: 'critical'})
  })

  it('falls back to successful + failed for legacy runs and flags over-delivery', () => {
    const legacy = [metric('load.successful_rps', 'requests_per_second', [300]), metric('load.failed_rps', 'requests_per_second', [12]), metric('load.configured_rps', 'requests_per_second', [175])]
    const [throughput] = kpiCards({runLevel: legacy, comparisons: []})
    expect(throughput.entries[0].value).toBe(312)
    expect(throughput.entries[0].tone).toBe('warning')
    expect(throughput.detail(throughput.entries[0])).toContain('configured (178 %)')
  })

  it('computes the change vs the baseline and attaches the Holm-adjusted test of that pair', () => {
    const rows = [{metric: 'load.p95_response_time', unit: 'milliseconds', baselineId: 'a', baseline: 'Alpha', groupId: 'b', group: 'Beta', adjustedPValue: .004, pValue: .002, hedgesG: -3.1, insufficient: false, familySize: 4, groupCount: 3, baselineCount: 3} as unknown as VariantComparison]
    const cards = kpiCards({runLevel: [metric('load.p95_response_time', 'milliseconds', [2400, 2420, 2410], [720, 730, 725])], comparisons: rows, baseline: 'a'})
    const [alpha, beta] = cards[0].entries
    expect(alpha.baseline).toBe(true)
    expect(beta.change).toBeCloseTo(-69.9, 1)
    expect(significance(beta.comparison)).toMatchObject({label: 'p = 0.004, g -3.1', tone: 'significant'})
    expect(comparisonVerdict(cards, [])[0].title).toBe('Beta lowers latency P95 by 70 % against Alpha')
  })
})

describe('summary', () => {
  it('lists every finding with its detail and a link to its section, then the measurement notes', () => {
    const navigate = vi.fn()
    const insights = ['critical', 'critical', 'warning', 'info'].map((severity, index) => ({id: `i${index}`, severity: severity as 'critical', title: `Finding ${index}`, detail: `Detail ${index}`, evidence: {}, target: index === 1 ? 'services' as const : undefined}))
    render(<VerdictStrip insights={insights} notices={[{kind: 'warning', text: 'Concurrent load'}]} onNavigate={navigate} />)
    const verdict = screen.getByRole('region', {name: 'Verdict'})
    expect(within(verdict).getAllByRole('listitem').map(item => item.textContent)).toEqual(['Critical: Finding 0.Detail 0', 'Critical: Finding 1. ServicesDetail 1', 'Warning: Finding 2.Detail 2', 'Note: Finding 3.Detail 3'])
    expect(within(verdict).getAllByRole('listitem')[0]).toHaveClass('summary-item', 'critical')
    fireEvent.click(within(verdict).getByRole('button', {name: 'Go to Services'}))
    expect(navigate).toHaveBeenCalledWith('services')
    expect(within(verdict).getByText('Concurrent load')).toHaveClass('validation-item', 'warning')
  })

  it('says so when nothing stands out', () => {
    render(<VerdictStrip insights={[]} notices={[]} onNavigate={() => {}} />)
    expect(screen.getByText('No issues detected.')).toBeInTheDocument()
  })
})

describe('key figures', () => {
  it('sets one large figure per metric for one variant, rows with Δ and significance for several, and the source as a footnote', () => {
    const cards = kpiCards({runLevel: [metric('load.achieved_rps', 'requests_per_second', [163]), metric('load.offered_rps', 'requests_per_second', [250])], comparisons: []})
    const {unmount} = render(<KpiCards cards={cards} provenance={provenance} />)
    const card = screen.getByRole('listitem', {name: 'Throughput'})
    expect(card).toHaveTextContent('163 req/s')
    expect(card).toHaveTextContent('(critical)')
    expect(card.querySelector('.sparkline')).toBeNull()
    expect(screen.getByText('Locust, client side')).toBeInTheDocument()
    expect(screen.getByRole('tooltip')).toHaveTextContent(/load generator/)
    unmount()
    const multi = kpiCards({runLevel: [metric('load.achieved_rps', 'requests_per_second', [160, 165, 164], [220, 225, 230])], comparisons: [{metric: 'load.achieved_rps', baselineId: 'a', groupId: 'b', insufficient: false, adjustedPValue: .2, groupCount: 3, baselineCount: 3, familySize: 1} as unknown as VariantComparison]})
    render(<KpiCards cards={multi} provenance={provenance} />)
    const rows = within(screen.getByRole('listitem', {name: 'Throughput'})).getAllByRole('listitem')
    expect(rows[0]).toHaveTextContent('Alpha163 req/sbaseline')
    expect(rows[1]).toHaveTextContent('+38 %')
    expect(rows[1]).toHaveTextContent('n.s.')
  })
})

describe('services table', () => {
  const services: ServiceHealth[] = [
    {service: 'recommendationservice', rps: 141, p95Ms: 2310, cpuCores: .2, replicas: 1, restarts: 0, score: 2000, bottleneck: true, reasons: ['server P95 2.3 s × 87 %', 'CPU flat at 0.20 cores']},
    {service: 'cartservice', rps: 192, p95Ms: 61, cpuCores: .07, cpuLimitCores: .1, replicas: 1, restarts: 2, score: 61, bottleneck: false, reasons: []},
    ...Array.from({length: 8}, (_, index) => ({service: `svc${index}`, score: 8 - index, bottleneck: false, reasons: []}))
  ]

  it('keeps the ranking, highlights the bottleneck, shows CPU vs limit and expands a service on click', () => {
    const detail = vi.fn(() => [{title: 'Server P95', unit: 'milliseconds', series: [curve('Default', [2000, 2300, 2310], 'milliseconds')]}])
    render(<ServicesTable services={services} detail={detail} trend={() => curve('x', [1, 2, 3])} />)
    const table = screen.getByRole('table', {name: 'Services ranked by bottleneck score'})
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows).toHaveLength(8)
    expect(rows[0]).toHaveClass('bottleneck')
    expect(rows[0]).toHaveTextContent('Bottleneck')
    expect(rows[0]).toHaveTextContent('2,310')
    expect(rows[1]).toHaveTextContent('0.07 (70 %)')
    fireEvent.click(within(rows[0]).getByRole('button', {name: 'recommendationservice'}))
    expect(detail).toHaveBeenCalledWith('recommendationservice')
    expect(screen.getByText(/^Server P95 2\.3 s × 87 %; CPU flat at 0\.20 cores\.$/)).toHaveClass('service-detail-reasons')
    expect(screen.getByRole('img', {name: 'recommendationservice: Server P95 over time'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'All 10 services'}))
    expect(within(table).getAllByRole('row').filter(row => !row.classList.contains('service-detail-row'))).toHaveLength(11)
  })

  it('ranks saved-comparison summaries by server P95 and flags a dominant service', () => {
    const rows = (values: Record<string, number>) => ({rows: Object.entries(values).map(([service, mean]) => ({service, groupID: 'a', summary: {mean}}))})
    const ranked = rankServiceSummaries('a', {'application.p95_response_time': rows({cart: 60, rec: 2300, ad: 7}), 'cpu.cores': rows({rec: .2, cart: .07})})
    expect(ranked.map(row => row.service)).toEqual(['rec', 'cart', 'ad'])
    expect(ranked[0].bottleneck).toBe(true)
    expect(ranked[1].bottleneck).toBe(false)
  })
})

describe('disclosure sections', () => {
  it('render their content only when opened', () => {
    const Body = vi.fn(() => <p>Heavy statistics</p>)
    const {rerender} = render(<Disclosure id="statistics" title="Statistics" summary="CSV" open={false} onToggle={() => {}}><Body /></Disclosure>)
    expect(screen.getByRole('button', {name: 'Statistics CSV'})).toHaveAttribute('aria-expanded', 'false')
    expect(Body).not.toHaveBeenCalled()
    rerender(<Disclosure id="statistics" title="Statistics" summary="CSV" open onToggle={() => {}}><Body /></Disclosure>)
    expect(screen.getByText('Heavy statistics')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Statistics CSV'})).toHaveAttribute('aria-controls', 'section-statistics-body')
  })
})

describe('time chart', () => {
  it('reads values with the keyboard and marks the SLO', () => {
    const panels = primaryPanels({groups: [{id: 'a', label: 'Default', color: 'var(--chart-1)'}], slo: 350, plot: name => name === 'load.achieved_rps' ? [curve('Default', [150, 160])] : name === 'load.offered_rps' ? [curve('Default', [175, 180])] : name === 'load.p95_response_time' ? [curve('Default', [2400, 2410], 'milliseconds')] : []})
    expect(panels.map(panel => panel.series.map(series => series.label))).toEqual([['Offered (target)', 'Achieved'], ['P95']])
    render(<TimeChart panels={panels} ariaLabel="Load" />)
    const plot = screen.getByRole('img', {name: 'Load'})
    expect(plot).toHaveTextContent('SLO 350 ms')
    fireEvent.keyDown(plot, {key: 'End'})
    expect(screen.getByText(/0:15: Request rate, Locust side \(req\/s\) Offered \(target\) 180 req\/s; .* Client latency P95 2.41 s/)).toBeInTheDocument()
  })

  it('shows the server-side rate and the ready pods under the Locust-side rate when they were measured', () => {
    const plot = (name: string) => name === 'load.achieved_rps' ? [curve('Default', [150, 160])] : name === 'ingress.request_rps' ? [curve('Default', [183, 195])] : name === 'deployment.ready_replicas' ? [curve('Default', [12, 15], 'replicas')] : name === 'load.p95_response_time' ? [curve('Default', [90, 95], 'milliseconds')] : []
    const panels = primaryPanels({groups: [{id: 'a', label: 'Default', color: 'var(--chart-1)'}], plot})
    expect(panels.map(panel => [panel.title, panel.series.map(series => series.label)])).toEqual([
      ['Request rate, Locust side (req/s)', ['Achieved']],
      ['Request rate, server side (req/s)', ['Ingress proxies']],
      ['Ready pods', ['Ready pods']],
      ['Client latency', ['P95']]
    ])
  })
})

describe('KubeStudio', () => {
  it('opens on the requested figure, filters series and exports the plotted data', async () => {
    const series = [curve('Alpha', [1, 2, 3], 'milliseconds'), curve('Beta', [2, 3, 4], 'milliseconds')]
    const data: StudioData = {metrics: ['load.p95_response_time'], plot: () => series, runLevel: [metric('load.p95_response_time', 'milliseconds', [1, 2, 3], [2, 3, 4])], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance, omitSLO: true}
    const groups = [{id: 'a', label: 'Alpha', color: 'red'}, {id: 'b', label: 'Beta', color: 'blue'}]
    render(<KubeStudio open onClose={() => {}} request={{kind: 'timeseries', metric: 'load.p95_response_time'}} data={data} groups={groups} settings={defaultFigureSettings} onSettings={() => {}} />)
    const dialog = screen.getByRole('dialog', {name: 'KubeStudio'})
    expect(within(dialog).getByRole('button', {name: /^Figure: Latency over time/})).toBeInTheDocument()
    const figure = within(dialog).getByRole('img', {name: 'P95 latency — client (Locust) comparison chart'})
    expect(figure).toHaveTextContent('Beta')
    fireEvent.click(within(dialog).getByRole('button', {name: 'Hide Beta'}))
    expect(within(dialog).getByRole('img', {name: 'P95 latency — client (Locust) comparison chart'})).not.toHaveTextContent('Beta')
    fireEvent.click(within(dialog).getByRole('button', {name: 'Export'}))
    const popover = within(within(dialog).getByRole('dialog', {name: 'Export figure'}))
    expect(popover.getAllByRole('radio').map(item => item.closest('label')?.querySelector('strong')?.textContent)).toEqual(['PDF', 'SVG', 'PNG 300 dpi', 'PNG 600 dpi', 'Data CSV', 'Python (matplotlib)', 'pgfplots (LaTeX)'])
    fireEvent.click(popover.getByRole('radio', {name: /Data CSV/}))
    fireEvent.click(popover.getByRole('button', {name: 'Download Data CSV'}))
    await vi.waitFor(() => expect(downloadBlob).toHaveBeenCalledWith(expect.stringContaining('p95-latency-client-locust'), expect.any(Blob)))
    fireEvent.click(within(dialog).getByRole('button', {name: /^Figure:/}))
    fireEvent.click(within(dialog).getByRole('dialog', {name: 'Choose a figure'}).querySelector('[data-kind="runlevel"]')!)
    expect(within(dialog).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
  })
})
