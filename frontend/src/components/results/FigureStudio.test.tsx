import {act, cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import axe from 'axe-core'
import {useState} from 'react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {downloadBlob, exportChart, svgDocument} from '../../charts'
import type {AggregateSeries, RunLevelMetric} from '../../resultsData'
import {FigureStudio, loadStudioMemory, type StudioData, type StudioSave} from './FigureStudio'
import {defaultFigureSettings, type FigureSettings} from './figureSettings'
import {provenanceFor} from './runs'

vi.mock('../../charts', async original => ({...await original<typeof import('../../charts')>(), downloadBlob: vi.fn(), exportChart: vi.fn(async () => {})}))

const group = (id: string, label: string, values: number[]) => ({id, label, values, artifactIds: values.map((_, index) => `${id}-${index}`)})
const runLevel = (name: string, unit: string): RunLevelMetric => ({metric: name, unit, groups: [group('a', 'Alpha', [1, 2, 3]), group('b', 'Beta', [2, 3, 4])]})
const curve = (label: string, values: number[]): AggregateSeries => ({artifactID: label, label, unit: 'milliseconds', stepMilliseconds: 15000, runs: 3, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value})), deviation: values.map((_, index) => ({timestamp: new Date(index * 15000).toISOString(), value: .5}))})
const data: StudioData = {metrics: ['load.p95_response_time'], plot: () => [curve('Alpha', [1, 2, 3, 4]), curve('Beta', [2, 3, 4, 5])], runLevel: [runLevel('load.p95_response_time', 'milliseconds')], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: true}
const groups = [{id: 'a', label: 'Alpha', color: '#1f77b4'}, {id: 'b', label: 'Beta', color: '#d55e00'}]

// Settings are owned by the page (useFigureSettings); a tiny stateful harness mirrors that.
function Studio({storageKey, onSave, initial = defaultFigureSettings, request = {kind: 'timeseries' as const}}: {storageKey?: string; onSave?: StudioSave; initial?: FigureSettings; request?: Parameters<typeof FigureStudio>[0]['request']}) {
  const [settings, setSettings] = useState(initial)
  return <FigureStudio open onClose={() => {}} request={request} data={data} groups={groups} settings={settings} onSettings={setSettings} storageKey={storageKey} onSave={onSave} />
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
    expect(layers().getAllByRole('button').map(button => button.getAttribute('aria-label') ?? button.textContent)).toEqual(['Figure', 'Chart: P95 latency — client (Locust)', 'Title', 'Show the title', 'X axis', 'Y axis', 'Alpha', 'Hide Alpha', 'Beta', 'Hide Beta', 'Legend', 'Hide the legend', 'Add annotation', 'Add chart', 'Caption'])
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

  it('keeps provenance out of the short caption and embeds caption + methods in every export', async () => {
    render(<Studio />)
    const caption = within(studio()).getByLabelText('Caption', {selector: 'textarea'}) as HTMLTextAreaElement
    expect(caption.value).toMatch(/^P95 latency — client \(Locust\) over the measured window/)
    expect(caption.value).not.toMatch(/histogram_quantile|PromQL|rate window/i)
    // The caption lives in a bottom drawer, closed by default, opened from Layers.
    expect(caption).not.toBeVisible()
    layer('Caption')
    expect(caption).toBeVisible()
    const methods = within(studio()).getByText('Methods & provenance').closest('details')!
    expect(methods).not.toHaveAttribute('open')
    expect(methods.querySelector('p')?.textContent).toMatch(/Locust/)
    fireEvent.change(caption, {target: {value: 'Client P95 per configuration (n = 3 runs). Bands: 95 % CI.'}})
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
    fireEvent.click(within(studio()).getByRole('button', {name: 'Insert into caption'}))
    expect(caption.value).toMatch(/Bands: 95 % CI\. .*Locust/)
    fireEvent.click(within(studio()).getByRole('button', {name: 'Reset to automatic'}))
    expect(caption.value).toMatch(/^P95 latency — client \(Locust\) over the measured window/)
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

  it('edits title, axis and series labels from their layers and moves the legend', () => {
    render(<Studio />)
    layer('Title')
    fireEvent.click(props().getByRole('checkbox', {name: 'Show titles in the figure'}))
    fireEvent.change(props().getByRole('textbox', {name: 'Title'}), {target: {value: 'Client latency'}})
    expect(figure().querySelector('.figure-title')).toHaveTextContent('Client latency')
    layer('Y axis')
    fireEvent.change(props().getByLabelText('Label'), {target: {value: 'Latency (ms)'}})
    expect(figure()).toHaveTextContent('Latency (ms)')
    layer('Beta')
    fireEvent.change(props().getByLabelText('Label'), {target: {value: 'Raised limit'}})
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Raised limit')
    expect(layers().getByRole('button', {name: 'Raised limit'})).toHaveAttribute('aria-current', 'true')
    fireEvent.click(props().getByRole('button', {name: /Reset/}))
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Beta')
    const height = Number(figure().getAttribute('viewBox')!.split(' ')[3])
    layer('Legend')
    const position = within(props().getByRole('group', {name: 'Position'}))
    fireEvent.click(position.getByRole('radio', {name: 'None'}))
    expect(figure().querySelector('.figure-legend')).toBeNull()
    expect(Number(figure().getAttribute('viewBox')!.split(' ')[3])).toBeLessThan(height)
    fireEvent.click(position.getByRole('radio', {name: 'Top'}))
    expect(figure().querySelector('g[transform]')).toBeNull()
    expect(Number(figure().querySelector('.figure-legend text')!.getAttribute('y'))).toBeLessThan(Number(figure().querySelector('rect.axis')!.getAttribute('y')))
    fireEvent.click(position.getByRole('radio', {name: 'Right'}))
    expect(Number(figure().querySelector('.figure-legend text')!.getAttribute('x'))).toBeGreaterThan(150)
  })

  it('selects elements by clicking them on the figure; Esc and the Figure layer deselect', () => {
    render(<Studio />)
    expect(heading()).toBe('P95 latency — client (Locust)')
    fireEvent.click(figure().querySelector('.tick-left text')!)
    expect(heading()).toBe('Y axis')
    expect(layers().getByRole('button', {name: 'Y axis'})).toHaveAttribute('aria-current', 'true')
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

  it('adds an annotation from Layers and edits it in place', () => {
    render(<Studio />)
    fireEvent.click(layers().getByRole('button', {name: 'Add annotation'}))
    fireEvent.click(layers().getByRole('button', {name: 'Threshold line'}))
    expect(heading()).toBe('Threshold line')
    fireEvent.change(props().getByLabelText('Value (ms)'), {target: {value: '3'}})
    // The label follows the value, with the axis unit.
    expect(figure().querySelector('.annotation-user.annotation-hline')).toHaveTextContent('Threshold 3 ms')
    fireEvent.click(props().getByRole('button', {name: 'Delete threshold line'}))
    expect(figure().querySelector('.annotation-user')).toBeNull()
  })

  it('steps marker size and line width on a 0.25 pt grid with round numbers', () => {
    render(<Studio />)
    layer('Alpha')
    expect(props().getByLabelText('Line width (pt)')).toHaveValue(1)
    expect(props().getByLabelText('Marker size (pt)')).toHaveValue(2)
    fireEvent.click(props().getByRole('button', {name: 'Increase line width'}))
    expect(props().getByLabelText('Line width (pt)')).toHaveValue(1.25)
    expect(figure().querySelector('g.series[data-series-key="load.p95_response_time|Alpha"] path.series-line')).toHaveAttribute('stroke-width', '1.25')
    fireEvent.click(props().getByRole('button', {name: 'Decrease marker size'}))
    expect(props().getByLabelText('Marker size (pt)')).toHaveValue(1.75)
  })

  it('remembers the figure, caption and zoom per experiment, loads old saves and survives broken storage', () => {
    const view = render(<Studio storageKey="experiment:e1" request={{}} />)
    pickFigure('runlevel')
    fireEvent.change(within(studio()).getByLabelText('Caption', {selector: 'textarea'}), {target: {value: 'Edited caption.'}})
    fireEvent.click(within(studio()).getByRole('button', {name: /show at 100 %/}))
    expect(within(studio()).getByText('Saved')).toBeInTheDocument()
    view.unmount()
    expect(loadStudioMemory('experiment:e1')).toMatchObject({charts: [expect.objectContaining({kind: 'runlevel'})], zoom: 1})
    render(<Studio storageKey="experiment:e1" request={{}} />)
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(within(studio()).getByLabelText('Caption', {selector: 'textarea'})).toHaveValue('Edited caption.')
    expect(within(studio()).getByRole('button', {name: /^Zoom 100 %/})).toBeInTheDocument()
    cleanup()
    // Saved before the rename: same storage key, older spec format.
    window.localStorage.setItem('kubephos.figureStudio.v1:experiment:old', JSON.stringify({kind: 'timeseries', metric: 'load.p95_response_time', zoom: 'fit', figures: {'timeseries:load.p95_response_time': {name: 'Fig 2', caption: 'Old caption.'}}, specs: {timeseries: {version: 1, type: 'timeseries', runMode: 'mean', panels: [{metrics: [{metric: 'load.p95_response_time', axis: 'left'}], annotations: [{kind: 'hline', value: 3, label: 'Limit'}]}]}}}))
    render(<Studio storageKey="experiment:old" request={{}} />)
    expect(within(studio()).getByLabelText('Figure name')).toHaveValue('Fig 2')
    expect(within(studio()).getByLabelText('Caption', {selector: 'textarea'})).toHaveValue('Old caption.')
    expect(figure().querySelector('.deviation-band')).toBeNull()
    expect(figure().querySelector('.annotation-user.annotation-hline')).toHaveTextContent('Limit')
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

  it('is free of axe violations (wide and narrow)', async () => {
    const audit = async () => {
      const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
      return results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))
    }
    render(<main><h1>Results</h1><Studio onSave={async () => {}} /></main>)
    layer('Caption')
    fireEvent.click(within(studio()).getByText('Methods & provenance'))
    expect(await audit()).toEqual([])
    openExport()
    expect(await audit()).toEqual([])
    fireEvent.keyDown(within(studio()).getByRole('dialog', {name: 'Export figure'}), {key: 'Escape'})
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    expect(await audit()).toEqual([])
    fireEvent.keyDown(within(studio()).getByRole('dialog', {name: 'Choose a figure'}), {key: 'Escape'})
    fireEvent.click(figure().querySelector('.legend-item text')!)
    expect(await audit()).toEqual([])
    layer('Y axis')
    expect(await audit()).toEqual([])
    fireEvent.keyDown(document.body, {key: '?'})
    expect(await audit()).toEqual([])
    cleanup()
    vi.stubGlobal('matchMedia', (query: string) => ({matches: true, media: query, addEventListener: () => {}, removeEventListener: () => {}}))
    render(<main><h1>Results</h1><Studio /></main>)
    await act(async () => {fireEvent.click(within(studio()).getByRole('tab', {name: 'Properties'}))})
    expect(await audit()).toEqual([])
  })
})
