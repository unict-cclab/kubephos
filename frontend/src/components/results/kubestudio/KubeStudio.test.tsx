import {act, cleanup, createEvent, fireEvent, render, screen, within} from '@testing-library/react'
import axe from 'axe-core'
import {useState} from 'react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {downloadBlob, exportChart, svgDocument} from '../../../charts'
import type {AggregateSeries, RunLevelMetric} from '../../../resultsData'
import {KubeStudio, type StudioSave} from './KubeStudio'
import type {StudioData} from './image'
import {loadStudioMemory} from './memory'
import {defaultFigureSettings, type FigureSettings} from '../figureSettings'
import {provenanceFor} from '../runs'

vi.mock('../../../charts', async original => ({...await original<typeof import('../../../charts')>(), downloadBlob: vi.fn(), exportChart: vi.fn(async () => {})}))

const group = (id: string, label: string, values: number[]) => ({id, label, values, artifactIds: values.map((_, index) => `${id}-${index}`)})
const runLevel = (name: string, unit: string): RunLevelMetric => ({metric: name, unit, groups: [group('a', 'Alpha', [1, 2, 3]), group('b', 'Beta', [2, 3, 4])]})
const curve = (label: string, values: number[]): AggregateSeries => ({artifactID: label, label, unit: 'milliseconds', stepMilliseconds: 15000, runs: 3, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value})), deviation: values.map((_, index) => ({timestamp: new Date(index * 15000).toISOString(), value: .5}))})
const data: StudioData = {metrics: ['load.p95_response_time'], plot: () => [curve('Alpha', [1, 2, 3, 4]), curve('Beta', [2, 3, 4, 5])], runLevel: [runLevel('load.p95_response_time', 'milliseconds')], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: true}
const groups = [{id: 'a', label: 'Alpha', color: '#1f77b4'}, {id: 'b', label: 'Beta', color: '#d55e00'}]
// Results with findings, for the landing's suggestions: two variants × 3 repetitions, a bottleneck,
// a load shortfall, an SLO missed and autoscaling.
const series = (unit: string, values: number[], extra: Partial<AggregateSeries> = {}): AggregateSeries[] => [{...curve('Alpha', values), unit, ...extra}, {...curve('Beta', values.map(value => value * 1.1)), unit, ...extra}]
const guidedPlots: Record<string, AggregateSeries[]> = {
  'load.p95_response_time': series('milliseconds', [300, 340, 380, 420], {sloMilliseconds: 350}),
  'load.achieved_rps': series('requests_per_second', [100, 160, 220, 300 / 1.1]),
  'deployment.ready_replicas': series('replicas', [2, 3, 4, 5])
}
const serviceRow = (service: string, mean: number) => ({service, group: 'Alpha', summary: {mean}})
const guided: StudioData = {
  ...data, metrics: Object.keys(guidedPlots), plot: metric => guidedPlots[metric] ?? [], serviceMetrics: ['application.p95_response_time'], omitSLO: false,
  services: metric => metric === 'application.p95_response_time' ? {rows: [serviceRow('recommendationservice', 812), serviceRow('frontend', 120)]} as never : undefined,
  fidelity: [{...curve('Alpha', [200, 200]), role: 'offered'}, {...curve('Alpha', [130, 130]), role: 'achieved'}],
  xy: {metrics: ['load.p95_response_time', 'load.achieved_rps'], level: 'run', provenance: name => provenanceFor([], name), services: () => undefined, runs: () => []},
  guide: {variants: 2, repetitions: 3, services: [], insights: [
    {id: 'bottleneck', severity: 'critical', title: 'recommendationservice is the bottleneck', detail: '', evidence: {service: 'recommendationservice', value: 812, unit: 'milliseconds'}},
    {id: 'target-not-sustained', severity: 'critical', title: 'The target rate was not sustained', detail: '', evidence: {}},
    {id: 'slo', severity: 'warning', title: 'The 350 ms SLO was missed in 40 % of windows', detail: '', evidence: {}},
    {id: 'autoscaling', severity: 'info', title: 'Autoscaling was active: 7 services scaled, up to 5 replicas', detail: '', evidence: {}}
  ]}
}

// Settings are owned by the page (useFigureSettings); a tiny stateful harness mirrors that.
function Studio({storageKey, onSave, initial = defaultFigureSettings, request = {kind: 'timeseries' as const}, data: studioData = data}: {storageKey?: string; onSave?: StudioSave; initial?: FigureSettings; request?: Parameters<typeof KubeStudio>[0]['request']; data?: StudioData}) {
  const [settings, setSettings] = useState(initial)
  return <KubeStudio open onClose={() => {}} request={request} data={studioData} groups={groups} settings={settings} onSettings={setSettings} storageKey={storageKey} onSave={onSave} />
}

const studio = () => screen.getByRole('dialog', {name: 'KubeStudio'})
const figure = () => studio().querySelector('svg.pub-figure') as SVGSVGElement
const layers = () => within(within(studio()).getByRole('navigation', {name: 'Layers'}))
const props = () => within(studio().querySelector('.ks-right') as HTMLElement)
const layer = (name: string | RegExp) => fireEvent.click(layers().getByRole('button', {name}))
const openExport = () => {fireEvent.click(within(studio()).getByRole('button', {name: 'Export'})); return within(within(studio()).getByRole('dialog', {name: 'Export figure'}))}
const pickFigure = (kind: string) => {
  fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
  fireEvent.click(within(studio()).getByRole('dialog', {name: 'Choose a figure'}).querySelector(`[data-kind="${kind}"]`)!)
}
const heading = () => props().getAllByRole('heading')[0].textContent

beforeEach(() => {window.localStorage.clear()})
afterEach(() => {cleanup(); vi.mocked(downloadBlob).mockReset(); vi.mocked(exportChart).mockReset(); vi.unstubAllGlobals()})

describe('KubeStudio', () => {
  it('opens straight on the best figure for the experiment, with its layers', () => {
    render(<Studio request={{}} />)
    expect(within(studio()).getByRole('img', {name: /comparison chart/})).toBeInTheDocument()
    expect(within(studio()).getByRole('button', {name: 'Figure: Latency over time by variant'})).toBeInTheDocument()
    expect(layers().getAllByRole('button').map(button => button.getAttribute('aria-label') ?? button.textContent)).toEqual(['Figure', 'Chart: P95 latency — client (Locust)', 'Title', 'Show the title', 'Alpha', 'Hide Alpha', 'Beta', 'Hide Beta', 'Legend', 'Hide the legend', 'Add a line in the graph', 'Add chart'])
    expect(heading()).toBe('P95 latency — client (Locust)')
    // One chart by default, with a single panel.
    expect(figure().querySelectorAll('.figure-panel')).toHaveLength(1)
    expect(figure().querySelector('rect')).toHaveAttribute('fill', '#ffffff')
  })

  it('switches figures from one list built from the data, one entry per figure type', () => {
    render(<Studio />)
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    const picker = within(within(studio()).getByRole('dialog', {name: 'Choose a figure'}))
    const recipes = picker.getAllByRole('button').filter(button => button.classList.contains('ks-figure-item'))
    expect(picker.getAllByRole('button').filter(button => button.classList.contains('ks-custom-form')).map(button => button.querySelector('strong')?.textContent)).toEqual(['Line over time', 'Per variant'])
    expect(recipes.map(button => button.querySelector('strong')?.textContent)).toEqual(['Latency over time by variant', 'Run-level comparison'])
    expect(recipes[0].querySelectorAll('svg.studio-thumb polyline')).toHaveLength(2)
    fireEvent.click(recipes[1])
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(within(studio()).queryByRole('dialog', {name: 'Choose a figure'})).not.toBeInTheDocument()
  })

  it('changes the exported page size and type in the figure properties', () => {
    render(<Studio />)
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 252 /)
    expect(within(studio()).getByText(/^3\.5 × [\d.]+ in · Times 8 pt$/)).toBeInTheDocument()
    fireEvent.click(props().getByRole('radio', {name: '2 col'}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 515.52 /)
    expect(svgDocument(figure())).toContain('width="7.16in"')
    fireEvent.click(props().getByRole('radio', {name: 'Slide'}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 720 /)
    fireEvent.click(props().getByRole('radio', {name: 'Custom'}))
    fireEvent.change(props().getByLabelText('Width (in)'), {target: {value: '4'}})
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 288 /)
    expect(svgDocument(figure())).toContain('width="4in"')
    fireEvent.click(props().getByRole('button', {name: 'Increase width'}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 306 /)
    fireEvent.click(props().getByRole('radio', {name: 'Sans'}))
    expect(within(studio()).getByText(/^4\.25 × [\d.]+ in · Helvetica 8 pt$/)).toBeInTheDocument()
  })

  it('adds an optional caption from the right-click menu and embeds caption + methods in every export', async () => {
    render(<Studio />)
    // No caption until the user writes one; exports then describe the figure on their own.
    expect(within(studio()).queryByLabelText('Caption')).not.toBeInTheDocument()
    fireEvent.click(openExport().getByRole('button', {name: 'Download PDF'}))
    await vi.waitFor(() => expect(exportChart).toHaveBeenCalled())
    const automatic = vi.mocked(exportChart).mock.calls[0][4]?.caption ?? ''
    expect(automatic).toMatch(/^P95 latency — client \(Locust\) over the measured window/)
    expect(automatic.split('\n\n')[0]).not.toMatch(/histogram_quantile|PromQL|rate window/i)
    await vi.waitFor(() => expect(within(studio()).getByRole('button', {name: 'Export'})).toBeEnabled())
    vi.mocked(exportChart).mockClear()
    fireEvent.contextMenu(studio().querySelector('.ks-cell')!)
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Add caption/}))
    const caption = within(studio()).getByLabelText('Caption', {selector: 'textarea'}) as HTMLTextAreaElement
    expect(caption).toHaveFocus()
    fireEvent.change(caption, {target: {value: 'Client P95 per configuration (n = 3 runs). Bands: 95 % CI.'}})
    fireEvent.click(within(studio()).getByRole('button', {name: 'Done'}))
    // The caption shows under the figure; clicking it edits it again.
    expect(within(studio()).getByRole('button', {name: 'Client P95 per configuration (n = 3 runs). Bands: 95 % CI.'})).toBeInTheDocument()
    fireEvent.change(within(studio()).getByLabelText('Figure name'), {target: {value: 'Fig 3 latency'}})
    const popover = openExport()
    expect(popover.getByRole('radio', {name: /^PDF/})).toBeChecked()
    expect(popover.getByText('kubephos-fig-3-latency.pdf')).toBeInTheDocument()
    fireEvent.click(popover.getByRole('button', {name: 'Download PDF'}))
    await vi.waitFor(() => expect(exportChart).toHaveBeenCalled())
    const [id, , format, , options] = vi.mocked(exportChart).mock.calls[0]
    expect(id).toBe(figure().id)
    expect(format).toBe('pdf')
    expect(options).toMatchObject({title: 'Fig 3 latency', fileName: 'kubephos-fig-3-latency', creator: 'KubePhos KubeStudio'})
    expect(options?.caption).toMatch(/^Client P95 per configuration \(n = 3 runs\)\. Bands: 95 % CI\.\n\nMethods and provenance: .*Locust/s)
    await vi.waitFor(() => expect(within(studio()).getByRole('button', {name: 'Export'})).toBeEnabled())
    const csvPopover = openExport()
    fireEvent.click(csvPopover.getByRole('radio', {name: /Data CSV/}))
    fireEvent.click(csvPopover.getByRole('button', {name: 'Download Data CSV'}))
    await vi.waitFor(() => expect(downloadBlob).toHaveBeenCalled())
    const [name, blob] = vi.mocked(downloadBlob).mock.calls[0]
    expect(name).toBe('kubephos-fig-3-latency-data.csv')
    const text = await (blob as Blob).text()
    expect(text.split('\n').slice(0, 4)).toEqual(['# Fig 3 latency', '# Caption: Client P95 per configuration (n = 3 runs). Bands: 95 % CI.', expect.stringMatching(/^# Methods and provenance: /), expect.stringMatching(/^series,minute,value/)])
    // Remove it from the same menu.
    fireEvent.contextMenu(studio().querySelector('.ks-cell')!)
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Edit caption/}))
    fireEvent.click(within(studio()).getByRole('button', {name: 'Remove caption'}))
    expect(within(studio()).queryByLabelText('Caption')).not.toBeInTheDocument()
  })

  it('duplicates, moves and deletes charts from the figure itself: right-click menu and drag handle', () => {
    render(<Studio />)
    const cells = () => [...studio().querySelectorAll('.ks-cell')].map(cell => cell.getAttribute('data-chart'))
    const menu = (index: number) => {fireEvent.contextMenu(studio().querySelectorAll('.ks-cell')[index]); return within(within(studio()).getByRole('menu'))}
    // One chart: it can be copied but not deleted, and there is nothing to drag.
    expect(menu(0).getByRole('menuitem', {name: /^Delete chart/})).toBeDisabled()
    expect(studio().querySelector('.ks-grip')).toBeNull()
    fireEvent.keyDown(within(studio()).getByRole('menu'), {key: 'Escape'})
    expect(within(studio()).queryByRole('menu')).not.toBeInTheDocument()
    fireEvent.click(menu(0).getByRole('menuitem', {name: /^Duplicate chart/}))
    expect(studio().querySelectorAll('svg.pub-figure')).toHaveLength(2)
    expect([...studio().querySelectorAll('.ks-letter')].map(item => item.textContent)).toEqual(['(a)', '(b)'])
    // Two charts side by side need the double-column page.
    expect(within(studio()).getByText(/^7\.16 × [\d.]+ in/)).toBeInTheDocument()
    const [a, b] = cells()
    fireEvent.keyDown(within(studio()).getByRole('button', {name: /^Move chart \(a\)/}), {key: 'ArrowRight'})
    expect(cells()).toEqual([b, a])
    expect(studio()).toHaveTextContent('Chart moved to position (b).')
    fireEvent.dragStart(within(studio()).getByRole('button', {name: /^Move chart \(b\)/}), {dataTransfer: {setData: () => {}, effectAllowed: ''}})
    const target = studio().querySelectorAll('.ks-cell')[0]
    fireEvent.dragOver(target)
    expect(target).toHaveAttribute('data-drop')
    fireEvent.drop(target)
    expect(cells()).toEqual([a, b])
    fireEvent.click(menu(1).getByRole('menuitem', {name: /^Delete chart/}))
    expect(cells()).toEqual([a])
  })

  it('adds charts guided: the type first, then X and Y', () => {
    render(<Studio request={{}} />)
    // Add chart opens the chart-type picker; leaving it adds nothing.
    fireEvent.click(layers().getByRole('button', {name: 'Add chart'}))
    const picker = within(studio()).getByRole('dialog', {name: 'Add chart (b): choose its type'})
    expect(picker).toHaveTextContent('first choose its type, then its X and Y on the right')
    fireEvent.keyDown(picker, {key: 'Escape'})
    expect(studio().querySelectorAll('svg.pub-figure')).toHaveLength(1)
    fireEvent.click(layers().getByRole('button', {name: 'Add chart'}))
    fireEvent.click(within(studio()).getByRole('dialog', {name: 'Add chart (b): choose its type'}).querySelector('[data-kind="runlevel"]')!)
    expect(studio().querySelectorAll('.ks-cell')).toHaveLength(2)
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    // The new chart is selected: its data and axes are on the right.
    expect(heading()).toMatch(/^Run-level|P95 latency/)
    expect(props().getByRole('group', {name: 'Chart form'})).toBeInTheDocument()
    expect(props().getByRole('button', {name: 'Y axis'})).toBeInTheDocument()
  })

  it('writes the legend from the Legend properties: each entry a colour and its text', () => {
    render(<Studio />)
    layer('Legend')
    const entries = within(props().getByRole('region', {name: 'Entries'}))
    fireEvent.change(entries.getByRole('textbox', {name: 'Text for Alpha'}), {target: {value: 'Default scheduler'}})
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Default scheduler')
    // The colour beside it is the line's colour, and changing it recolours the line.
    fireEvent.change(entries.getByLabelText('Colour of Default scheduler'), {target: {value: '#117733'}})
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] path.series-line')).toHaveAttribute('stroke', '#117733')
    expect(figure().querySelector('.legend-item[data-series-key="load.p95_response_time|Alpha"]')!.innerHTML).toContain('#117733')
    // The same text as the series' own label; the reset button restores the data's name.
    layer('Default scheduler')
    expect(props().getByLabelText('Label')).toHaveValue('Default scheduler')
    layer('Legend')
    fireEvent.click(props().getByRole('button', {name: 'Restore Alpha'}))
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Alpha')
  })

  it('draws the legend of a single line once its entry has a text', () => {
    render(<Studio />)
    // One line, mean only (a band would need explaining).
    fireEvent.click(layers().getAllByRole('button', {name: 'Hide Beta'})[0])
    fireEvent.click(layers().getAllByRole('button', {name: /^Chart/})[0])
    fireEvent.change(props().getByLabelText('Repeated runs'), {target: {value: 'mean'}})
    expect(figure().querySelector('.figure-legend')).toBeNull()
    layer('Legend')
    expect(props().getByText(/legend stays hidden until you give its entry a text/)).toBeInTheDocument()
    fireEvent.change(props().getByRole('textbox', {name: 'Text for Alpha'}), {target: {value: 'prova'}})
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('prova')
    expect(props().queryByText(/legend stays hidden/)).not.toBeInTheDocument()
  })


  it('shows Uncertainty only when a series has repetitions', () => {
    const chartLayer = () => fireEvent.click(layers().getAllByRole('button', {name: /^Chart/})[0])
    render(<Studio />)
    chartLayer()
    expect(props().getByRole('region', {name: 'Uncertainty'})).toBeInTheDocument()
    cleanup()
    const single = {...data, plot: () => [{...curve('Alpha', [1, 2, 3, 4]), runs: 1, deviation: undefined}, {...curve('Beta', [2, 3, 4, 5]), runs: 1, deviation: undefined}]}
    render(<KubeStudio open onClose={() => {}} request={{kind: 'timeseries'}} data={single} groups={groups} settings={defaultFigureSettings} onSettings={() => {}} />)
    chartLayer()
    expect(props().queryByRole('region', {name: 'Uncertainty'})).not.toBeInTheDocument()
    expect(props().queryByLabelText('Repeated runs')).not.toBeInTheDocument()
  })

  it('exports from one popover that shows the physical output and remembers the format', async () => {
    render(<Studio />)
    const button = within(studio()).getByRole('button', {name: 'Export'})
    expect(button).toHaveAttribute('aria-haspopup', 'dialog')
    let popover = openExport()
    const radios = popover.getAllByRole('radio')
    expect(radios.map(item => item.closest('label')?.querySelector('strong')?.textContent)).toEqual(['PDF', 'SVG', 'PNG 300 dpi', 'PNG 600 dpi', 'Data CSV', 'Python (matplotlib)', 'pgfplots (LaTeX)'])
    expect(radios[0]).toHaveFocus()
    expect(popover.getByText(/^3\.5 × [\d.]+ in \(89 × \d+ mm\)$/)).toBeInTheDocument()
    expect(popover.getByText('Times 8 pt, real text')).toBeInTheDocument()
    fireEvent.click(popover.getByRole('radio', {name: /PNG 600 dpi/}))
    expect(popover.getByText(/^2100 × \d+ px at 600 dpi$/)).toBeInTheDocument()
    expect(popover.getByRole('button', {name: 'Copy image'})).toBeInTheDocument()
    fireEvent.keyDown(popover.getByRole('radio', {name: /PNG 600 dpi/}), {key: 'Escape'})
    expect(within(studio()).queryByRole('dialog', {name: 'Export figure'})).not.toBeInTheDocument()
    expect(button).toHaveFocus()
    // The E shortcut reopens it on the remembered format.
    fireEvent.keyDown(document.body, {key: 'e'})
    popover = within(within(studio()).getByRole('dialog', {name: 'Export figure'}))
    expect(popover.getByRole('radio', {name: /PNG 600 dpi/})).toBeChecked()
    fireEvent.click(popover.getByRole('button', {name: 'Download PNG 600 dpi'}))
    await vi.waitFor(() => expect(exportChart).toHaveBeenCalledWith(figure().id, 'load.p95_response_time', 'png', undefined, expect.objectContaining({dpi: 600, creator: 'KubePhos KubeStudio'})))
    expect(window.localStorage.getItem('kubephos.kubestudio.exportFormat')).toBe('png600')
  })

  it('copies the figure to the clipboard as SVG text', async () => {
    const writeText = vi.fn(async () => {})
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText}})
    vi.mocked(exportChart).mockImplementation(async (_id, _metric, _format, onGenerated) => {await onGenerated?.(new TextEncoder().encode('<svg/>'))})
    render(<Studio />)
    const popover = openExport()
    fireEvent.click(popover.getByRole('radio', {name: /^SVG/}))
    fireEvent.click(popover.getByRole('button', {name: 'Copy SVG'}))
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith('<svg/>'))
    expect(await within(studio()).findByText('SVG copied to the clipboard.', {selector: '[role="status"]'})).toBeInTheDocument()
  })

  it('edits title and series labels from their layers, renames axes on the figure and moves the legend', () => {
    render(<Studio />)
    layer('Title')
    fireEvent.click(props().getByRole('checkbox', {name: 'Show titles in the figure'}))
    fireEvent.change(props().getByRole('textbox', {name: 'Title'}), {target: {value: 'Client latency'}})
    expect(figure().querySelector('.figure-title')).toHaveTextContent('Client latency')
    // Axes are not in Layers: a click on an axis title renames it in place.
    expect(layers().queryByRole('button', {name: /axis$/})).not.toBeInTheDocument()
    fireEvent.click(figure().querySelector('.axis-title-left text')!)
    const field = within(studio()).getByRole('textbox', {name: 'Y axis title'})
    expect(field).toHaveFocus()
    expect(field).toHaveValue(figure().querySelector('.axis-title-left text')!.textContent)
    expect(heading()).toBe('Y axis')
    fireEvent.change(field, {target: {value: 'Latency (ms)'}})
    fireEvent.keyDown(field, {key: 'Enter'})
    fireEvent.blur(field)
    expect(figure().querySelector('.axis-title-left')).toHaveTextContent('Latency (ms)')
    expect(within(studio()).queryByRole('textbox', {name: 'Y axis title'})).not.toBeInTheDocument()
    // Esc keeps the title; an empty one restores the automatic title.
    fireEvent.click(figure().querySelector('.axis-title-x text')!)
    fireEvent.change(within(studio()).getByRole('textbox', {name: 'X axis title'}), {target: {value: 'Elapsed'}})
    fireEvent.keyDown(within(studio()).getByRole('textbox', {name: 'X axis title'}), {key: 'Escape'})
    expect(figure().querySelector('.axis-title-x')).not.toHaveTextContent('Elapsed')
    fireEvent.click(figure().querySelector('.axis-title-left text')!)
    fireEvent.change(within(studio()).getByRole('textbox', {name: 'Y axis title'}), {target: {value: ' '}})
    fireEvent.blur(within(studio()).getByRole('textbox', {name: 'Y axis title'}))
    expect(figure().querySelector('.axis-title-left')).not.toHaveTextContent('Latency (ms)')
    layer('Beta')
    fireEvent.change(props().getByLabelText('Label'), {target: {value: 'Raised limit'}})
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Raised limit')
    expect(layers().getByRole('button', {name: 'Raised limit'})).toHaveAttribute('aria-current', 'true')
    fireEvent.click(props().getByRole('button', {name: /Reset/}))
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Beta')
    const height = Number(figure().getAttribute('viewBox')!.split(' ')[3])
    layer('Legend')
    fireEvent.click(props().getByRole('checkbox', {name: 'Show the legend'}))
    expect(figure().querySelector('.figure-legend')).toBeNull()
    expect(Number(figure().getAttribute('viewBox')!.split(' ')[3])).toBeLessThan(height)
    fireEvent.click(props().getByRole('checkbox', {name: 'Show the legend'}))
    fireEvent.change(props().getByLabelText('Text size (pt)'), {target: {value: '11'}})
    expect(figure().querySelector('.figure-legend .legend-label')).toHaveAttribute('font-size', '11')
    // Dragged with the mouse: above the plot it goes on top, inside the plot it stays where it is let go.
    const [width, pageHeight] = figure().getAttribute('viewBox')!.split(' ').slice(2).map(Number)
    vi.spyOn(figure(), 'getBoundingClientRect').mockReturnValue({left: 0, top: 0, width, height: pageHeight, right: width, bottom: pageHeight, x: 0, y: 0, toJSON: () => ({})})
    const pointer = (type: 'pointerDown' | 'pointerMove' | 'pointerUp', element: Element, x: number, y: number) => {
      const event = createEvent[type](element)
      for (const [key, value] of Object.entries({clientX: x, clientY: y, pointerId: 1, button: 0})) Object.defineProperty(event, key, {value})
      fireEvent(element, event)
    }
    const label = () => figure().querySelector('.figure-legend .legend-label')!
    const plotTop = () => Number(figure().querySelector('rect.axis')!.getAttribute('y'))
    pointer('pointerDown', label(), 20, pageHeight - 4)
    pointer('pointerMove', label(), 20, 2)
    expect(Number(label().getAttribute('y'))).toBeLessThan(plotTop())
    pointer('pointerMove', label(), width / 2, pageHeight / 2)
    pointer('pointerUp', label(), width / 2, pageHeight / 2)
    expect(figure().querySelector('.figure-legend rect')).not.toBeNull()
    expect(heading()).toBe('Legend')
  })

  it('selects elements by clicking them on the figure; Esc and the Figure layer deselect', () => {
    render(<Studio />)
    expect(heading()).toBe('P95 latency — client (Locust)')
    fireEvent.click(figure().querySelector('.tick-left text')!)
    expect(heading()).toBe('Y axis')
    fireEvent.click(props().getByRole('radio', {name: 'Custom'}))
    fireEvent.change(props().getByLabelText('Max'), {target: {value: '10'}})
    expect([...figure().querySelectorAll('.tick-left text')].at(-1)?.textContent).toBe('10')
    fireEvent.click(figure().querySelector('.tick-x text')!)
    expect(heading()).toBe('X axis')
    expect(props().getByLabelText('Time as')).toBeInTheDocument()
    fireEvent.click(figure().querySelector('.legend-item text')!)
    expect(heading()).toBe('Alpha')
    fireEvent.click(props().getByRole('radio', {name: 'Dashed'}))
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] path.series-line')!.getAttribute('stroke-dasharray')).toBeTruthy()
    // The selection hugs the series line on a layer that is not part of the figure.
    expect(studio().querySelector('.ks-overlay path.ks-halo.selected')?.getAttribute('d')).toBe(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] path.series-line')!.getAttribute('d'))
    expect(figure().querySelector('.ks-halo')).toBeNull()
    // Esc steps out: element → chart → page.
    fireEvent(studio(), new Event('cancel', {cancelable: true}))
    expect(props().getByRole('group', {name: 'Chart form'})).toBeInTheDocument()
    fireEvent(studio(), new Event('cancel', {cancelable: true}))
    expect(props().getByRole('group', {name: 'Page size'})).toBeInTheDocument()
    fireEvent.click(figure().querySelector('.legend-item text')!)
    layer('Figure')
    expect(props().getByRole('group', {name: 'Page size'})).toBeInTheDocument()
  })

  it('hides series, title and legend with the layer eye toggles', () => {
    render(<Studio />)
    fireEvent.click(layers().getByRole('button', {name: 'Hide Beta'}))
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Beta"]')).toBeNull()
    fireEvent.click(layers().getByRole('button', {name: 'Show Beta'}))
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Beta"]')).not.toBeNull()
    fireEvent.click(layers().getByRole('button', {name: 'Show the title'}))
    expect(figure().querySelector('.figure-title')).not.toBeNull()
    fireEvent.click(layers().getByRole('button', {name: 'Hide the legend'}))
    expect(figure().querySelector('.figure-legend')).toBeNull()
  })

  it('adds a line in the graph from Layers and edits it in place', () => {
    render(<Studio />)
    fireEvent.click(layers().getByRole('button', {name: 'Add a line in the graph'}))
    const options = within(layers().getByRole('group', {name: 'Line to add'})).getAllByRole('button')
    expect(options.map(option => option.querySelector('strong')?.textContent)).toEqual(['Threshold line', 'Shaded range', 'Vertical line', 'Text label'])
    // Each option says what it is for, with an example.
    expect(options[0]).toHaveTextContent(/SLO/)
    fireEvent.click(options[0])
    expect(heading()).toBe('Threshold line')
    fireEvent.change(props().getByLabelText('Value (ms)'), {target: {value: '3'}})
    // The label follows the value, with the axis unit.
    expect(figure().querySelector('.annotation-user.annotation-hline')).toHaveTextContent('Threshold 3 ms')
    fireEvent.click(props().getByRole('button', {name: 'Delete threshold line'}))
    expect(figure().querySelector('.annotation-user')).toBeNull()
  })

  it('steps line width on a 0.25 pt grid with round numbers, and keeps points to scatter charts', () => {
    render(<Studio />)
    layer('Alpha')
    expect(props().getByLabelText('Line width (pt)')).toHaveValue(1)
    expect(props().queryByLabelText(/^Point size/)).not.toBeInTheDocument()
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] circle')).toBeNull()
    fireEvent.click(props().getByRole('button', {name: 'Increase line width'}))
    expect(props().getByLabelText('Line width (pt)')).toHaveValue(1.25)
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] path.series-line')).toHaveAttribute('stroke-width', '1.25')
  })

  it('remembers the figure, caption and zoom per experiment, loads old saves and survives broken storage', () => {
    const view = render(<Studio storageKey="experiment:e1" request={{}} />)
    pickFigure('runlevel')
    fireEvent.contextMenu(studio().querySelector('.ks-cell')!)
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Add caption/}))
    fireEvent.change(within(studio()).getByLabelText('Caption', {selector: 'textarea'}), {target: {value: 'Edited caption.'}})
    fireEvent.click(within(studio()).getByRole('button', {name: /show at 100 %/}))
    expect(within(studio()).getByText('Saved')).toBeInTheDocument()
    view.unmount()
    expect(loadStudioMemory('experiment:e1')).toMatchObject({charts: [expect.objectContaining({kind: 'runlevel'})], zoom: 1})
    render(<Studio storageKey="experiment:e1" request={{}} />)
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(within(studio()).getByRole('button', {name: 'Edited caption.'})).toBeInTheDocument()
    expect(within(studio()).getByRole('button', {name: /^Zoom 100 %/})).toBeInTheDocument()
    cleanup()
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {throw new Error('denied')})
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {throw new Error('denied')})
    render(<Studio storageKey="experiment:e1" request={{kind: 'runlevel'}} />)
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(within(studio()).getByText('Not saved')).toBeInTheDocument()
    getItem.mockRestore()
    setItem.mockRestore()
  })

  it('undoes and redoes edits with buttons and Ctrl/Cmd+Z; text fields keep their own undo', async () => {
    render(<Studio />)
    const undo = within(studio()).getByRole('button', {name: 'Undo'})
    const redo = within(studio()).getByRole('button', {name: 'Redo'})
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 5))})
    expect(undo).toBeDisabled()
    fireEvent.click(props().getByRole('radio', {name: '2 col'}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 515.52 /)
    fireEvent.click(undo)
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 252 /)
    expect(redo).toBeEnabled()
    fireEvent.keyDown(document.body, {key: 'z', ctrlKey: true, shiftKey: true})
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 515.52 /)
    fireEvent.keyDown(document.body, {key: 'z', metaKey: true})
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 252 /)
    fireEvent.contextMenu(studio().querySelector('.ks-cell')!)
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Add caption/}))
    const caption = within(studio()).getByLabelText('Caption', {selector: 'textarea'})
    fireEvent.change(caption, {target: {value: 'Mine.'}})
    fireEvent.keyDown(caption, {key: 'z', ctrlKey: true})
    expect(caption).toHaveValue('Mine.')
    fireEvent.click(undo)
    expect(caption).not.toHaveValue('Mine.')
  })

  it('checks the figure and fixes problems in one click', () => {
    render(<Studio initial={{...defaultFigureSettings, legend: 'none'}} />)
    const check = within(props().getByRole('region', {name: 'Figure check'}))
    expect(check.getByText(/no legend/)).toBeInTheDocument()
    fireEvent.click(check.getByRole('button', {name: 'Show legend below'}))
    expect(figure().querySelector('.figure-legend')).not.toBeNull()
    expect(props().queryByRole('region', {name: 'Figure check'})).not.toBeInTheDocument()
  })

  it('shows the keyboard shortcuts with ? and brands the window', () => {
    render(<Studio />)
    expect(within(studio()).getByRole('heading', {name: 'KubeStudio'})).toBeInTheDocument()
    expect(studio().querySelector('.ks-brand svg.kubestudio-mark')).not.toBeNull()
    fireEvent.keyDown(document.body, {key: '?'})
    const sheet = within(within(studio()).getByRole('dialog', {name: 'Keyboard shortcuts'}))
    expect(sheet.getByText('Export')).toBeInTheDocument()
    fireEvent.click(sheet.getByRole('button', {name: 'Close shortcuts'}))
    expect(within(studio()).queryByRole('dialog', {name: 'Keyboard shortcuts'})).not.toBeInTheDocument()
    expect(openExport().getByText('Export figure').previousElementSibling).toHaveClass('kubestudio-mark')
  })

  it('saves the PDF to the experiment', async () => {
    const save = vi.fn<StudioSave>(async () => {})
    vi.mocked(exportChart).mockImplementation(async (_id, _metric, _format, onGenerated) => {await onGenerated?.(new Uint8Array([37, 80, 68, 70]))})
    render(<Studio onSave={save} />)
    fireEvent.click(openExport().getByRole('button', {name: 'Save PDF to the experiment'}))
    await vi.waitFor(() => expect(save).toHaveBeenCalledWith({metric: 'load.p95_response_time', format: 'pdf', bytes: expect.any(Uint8Array)}))
    expect(await within(studio()).findByText(/Saved to the experiment/, {selector: '[role="status"]'})).toBeInTheDocument()
  })

  it('turns the panels into Layers / Figure / Properties tabs below 1000 px', () => {
    vi.stubGlobal('matchMedia', (query: string) => ({matches: query.includes('max-width'), media: query, addEventListener: () => {}, removeEventListener: () => {}}))
    render(<Studio />)
    const tabs = within(studio()).getByRole('tablist', {name: 'KubeStudio panels'})
    expect(within(tabs).getAllByRole('tab').map(tab => tab.textContent)).toEqual(['Layers', 'Figure', 'Properties'])
    expect(within(tabs).getByRole('tab', {name: 'Figure'})).toHaveAttribute('aria-selected', 'true')
    const panel = (name: string) => document.getElementById(within(tabs).getByRole('tab', {name}).getAttribute('aria-controls')!)!
    expect(panel('Figure')).toBeVisible()
    expect(panel('Properties')).not.toBeVisible()
    // Selecting on the figure shows its properties.
    fireEvent.click(figure().querySelector('.legend-item text')!)
    expect(panel('Properties')).toBeVisible()
    fireEvent.keyDown(tabs, {key: 'Home'})
    expect(within(tabs).getByRole('tab', {name: 'Layers'})).toHaveAttribute('aria-selected', 'true')
    expect(panel('Layers')).toBeVisible()
  })

  it('is free of axe violations (wide and narrow, landing included)', async () => {
    const audit = async () => {
      const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
      return results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))
    }
    render(<main><h1>Results</h1><Studio onSave={async () => {}} /></main>)
    fireEvent.contextMenu(studio().querySelector('.ks-cell')!)
    expect(await audit()).toEqual([])
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Add caption/}))
    expect(await audit()).toEqual([])
    openExport()
    expect(await audit()).toEqual([])
    fireEvent.keyDown(within(studio()).getByRole('dialog', {name: 'Export figure'}), {key: 'Escape'})
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    expect(await audit()).toEqual([])
    fireEvent.keyDown(within(studio()).getByRole('dialog', {name: 'Choose a figure'}), {key: 'Escape'})
    fireEvent.click(figure().querySelector('.legend-item text')!)
    expect(await audit()).toEqual([])
    fireEvent.click(figure().querySelector('.axis-title-left text')!)
    expect(await audit()).toEqual([])
    fireEvent.keyDown(document.body, {key: '?'})
    expect(await audit()).toEqual([])
    cleanup()
    vi.stubGlobal('matchMedia', (query: string) => ({matches: true, media: query, addEventListener: () => {}, removeEventListener: () => {}}))
    render(<main><h1>Results</h1><Studio /></main>)
    await act(async () => {fireEvent.click(within(studio()).getByRole('tab', {name: 'Properties'}))})
    expect(await audit()).toEqual([])
    cleanup()
    render(<main><h1>Results</h1><Studio request={{choose: true}} data={guided} /></main>)
    expect(await audit()).toEqual([])
  })
})

describe('KubeStudio landing (a new figure)', () => {
  const landing = () => within(studio().querySelector('.ks-landing') as HTMLElement)
  const suggestions = () => landing().getAllByRole('button').filter(button => button.classList.contains('ks-suggestion'))
  const titles = () => suggestions().map(button => button.querySelector('strong')!.textContent)

  it('guides from the results: ranked suggestions, each with why, then the editor on one click', () => {
    render(<Studio request={{choose: true}} data={guided} />)
    // Not straight into a chart: the steps, the goals and the suggestions come first.
    expect(studio().querySelector('.ks-workspace')).toBeNull()
    expect(within(studio()).queryByRole('button', {name: 'Export'})).not.toBeInTheDocument()
    // Plain wording, no numbered stepper.
    expect(within(studio()).queryByRole('list', {name: /step/})).not.toBeInTheDocument()
    expect(landing().getByRole('heading', {name: 'What do you want to show?'})).toBeInTheDocument()
    expect(within(landing().getByRole('group', {name: 'What do you want to show?'})).getAllByRole('button').map(button => button.textContent)).toEqual(['Everything', 'Load and throughput', 'Latency', 'Bottlenecks', 'Resources and scaling', 'Compare variants'])
    expect(landing().getByRole('heading', {name: 'Suggested for this experiment'})).toBeInTheDocument()
    expect(landing().getByText('Pick one to open it in the editor — you can change data, style and layout there.')).toBeInTheDocument()
    expect(titles()).toEqual(['Best match Server P95 by service', 'Load fidelity', 'Latency against the SLO', 'P95 comparison with 95 % CI', 'Replicas over time vs load', 'Throughput vs latency'])
    expect(suggestions()[0]).toHaveFocus()
    const why = suggestions().map(button => button.querySelector('small')!.textContent)
    expect(why[0]).toBe('recommendationservice is the bottleneck (server P95 812 ms) → every service\'s P95 side by side')
    expect(why[1]).toBe('Achieved 65 % of the target rate → offered vs achieved load')
    expect(why[3]).toBe('2 variants × 3 repetitions → every repetition, mean and 95 % CI')
    expect(why[2]).toBe('The 350 ms SLO was missed in 40 % of windows → P95 over time with the SLO line')
    expect(why[4]).toBe('Autoscaling scaled 7 services, up to 5 replicas → replicas over time against the request rate')
    expect(why[5]).toBe('Load ranged from 100 RPS to 300 RPS → how latency grows with throughput')
    expect(suggestions()[0].querySelector('[data-severity="critical"]')).not.toBeNull()
    // Live thumbnails drawn from the data.
    expect(suggestions().slice(0, 5).every(button => button.querySelector('svg.studio-thumb polyline, svg.studio-thumb circle'))).toBe(true)
    // A goal chip narrows the list.
    const goal = (name: string) => within(landing().getByRole('group', {name: 'What do you want to show?'})).getByRole('button', {name})
    expect(goal('Everything')).toHaveAttribute('aria-pressed', 'true')
    fireEvent.click(goal('Load and throughput'))
    expect(goal('Load and throughput')).toHaveAttribute('aria-pressed', 'true')
    expect(titles()).toEqual(['Load fidelity', 'Throughput vs latency'])
    fireEvent.click(goal('Resources and scaling'))
    fireEvent.click(suggestions()[0])
    // The editor, on that chart, with a history of its own.
    expect(studio().querySelector('.ks-landing')).toBeNull()
    expect(within(studio()).getByRole('img', {name: /replicas.*comparison chart/i})).toBeInTheDocument()
    expect(figure().querySelectorAll('.axis-title-right').length).toBe(1)
    expect(within(studio()).getByRole('button', {name: 'Undo'})).toBeDisabled()
    expect(within(studio()).getByRole('status')).toHaveTextContent(/Replicas over time vs load\. Refine it/)
  })

  it('goes back to the suggestions from the editor with Home, keeping the figure as the last one', async () => {
    render(<Studio request={{choose: true}} data={guided} />)
    const undoButton = () => within(within(studio()).getByRole('group', {name: 'History'})).getByRole('button', {name: 'Undo'})
    // History starts recording once mounted.
    await act(async () => {await new Promise(resolve => setTimeout(resolve, 0))})
    fireEvent.click(suggestions()[1])
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    fireEvent.click(within(studio()).getByRole('dialog', {name: 'Choose a figure'}).querySelector('[data-kind="runlevel"]')!)
    expect(undoButton()).toBeEnabled()
    const home = within(studio()).getByRole('button', {name: 'Back to suggestions'})
    expect(home).toHaveAttribute('title', expect.stringMatching(/stays available as “Continue last figure”/))
    fireEvent.click(home)
    expect(studio().querySelector('.ks-workspace')).toBeNull()
    expect(suggestions()[0]).toHaveFocus()
    const resume = landing().getByRole('button', {name: /^Continue last figure/})
    expect(resume).toHaveTextContent(/P95 latency/)
    // Continue: the same figure, with its undo history.
    fireEvent.click(resume)
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(undoButton()).toBeEnabled()
    // A new suggestion instead starts a new figure with a fresh history.
    fireEvent.click(within(studio()).getByRole('button', {name: 'Back to suggestions'}))
    fireEvent.click(suggestions()[0])
    expect(undoButton()).toBeDisabled()
  })

  it('offers a custom chart and the last figure to experts, and survives results without findings', () => {
    window.localStorage.setItem('kubephos.figureStudio.v1:experiment:e9', JSON.stringify({version: 2, charts: [{id: 'old', kind: 'runlevel', metric: 'load.p95_response_time'}], name: 'Paper figure 3'}))
    render(<Studio storageKey="experiment:e9" request={{choose: true}} />)
    // Plain data (no findings): still a ranked list, and the memory is not overwritten by the landing.
    expect(titles()[0]).toMatch(/P95 comparison/)
    expect(loadStudioMemory('experiment:e9').name).toBe('Paper figure 3')
    fireEvent.click(landing().getByRole('button', {name: /^Continue last figure/}))
    expect(studio().querySelector('.ks-landing')).toBeNull()
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    cleanup()
    render(<Studio request={{choose: true}} />)
    expect(landing().queryByRole('button', {name: /^Continue last figure/})).not.toBeInTheDocument()
    fireEvent.click(landing().getByRole('button', {name: /^Line over time/}))
    expect(studio().querySelector('.ks-workspace')).not.toBeNull()
    expect(props().getByRole('group', {name: 'Chart form'})).toBeInTheDocument()
    // Esc on the landing closes KubeStudio.
    cleanup()
    const onClose = vi.fn()
    render(<KubeStudio open onClose={onClose} request={{choose: true}} data={data} groups={groups} settings={defaultFigureSettings} onSettings={() => {}} />)
    fireEvent(studio(), new Event('cancel', {cancelable: true}))
    expect(onClose).toHaveBeenCalled()
  })
})
