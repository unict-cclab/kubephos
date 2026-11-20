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

const studio = () => screen.getByRole('dialog', {name: 'Figure studio'})
const figure = () => studio().querySelector('svg.pub-figure') as SVGSVGElement

beforeEach(() => {window.localStorage.clear()})
afterEach(() => {cleanup(); vi.mocked(downloadBlob).mockReset(); vi.mocked(exportChart).mockReset(); vi.unstubAllGlobals()})

describe('Figure studio', () => {
  it('switches figure type from the gallery and keeps the figure on print-white paper', () => {
    render(<Studio />)
    const gallery = within(studio()).getByRole('group', {name: 'Figure'})
    expect(within(gallery).getAllByRole('radio').map(radio => radio.closest('label')?.querySelector('.studio-kind-text span')?.textContent)).toEqual(['Time series', 'Run-level dots'])
    expect(within(studio()).getByRole('img', {name: /comparison chart/})).toBeInTheDocument()
    fireEvent.click(within(gallery).getByRole('radio', {name: /^Run-level dots/}))
    expect(within(gallery).getByRole('radio', {name: /^Run-level dots/})).toBeChecked()
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    expect(figure().querySelector('rect')).toHaveAttribute('fill', '#ffffff')
  })

  it('changes the exported page size with the size presets and custom W × H', () => {
    render(<Studio />)
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 252 /)
    expect(within(studio()).getByText(/^3\.5 × [\d.]+ in · Times 8 pt$/)).toBeInTheDocument()
    fireEvent.click(within(studio()).getByRole('radio', {name: /^Double column/}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 515.52 /)
    expect(svgDocument(figure())).toContain('width="7.16in"')
    fireEvent.click(within(studio()).getByRole('radio', {name: /^Slide 16:9/}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 720 /)
    fireEvent.click(within(within(studio()).getByRole('group', {name: 'Page size'})).getByRole('radio', {name: /^Custom/}))
    fireEvent.change(within(studio()).getByLabelText('Width (in)'), {target: {value: '4'}})
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 288 /)
    expect(svgDocument(figure())).toContain('width="4in"')
    fireEvent.click(within(studio()).getByRole('radio', {name: /^Sans/}))
    expect(within(studio()).getByText(/^4 × [\d.]+ in · Helvetica 8 pt$/)).toBeInTheDocument()
  })

  it('keeps provenance out of the short caption and embeds caption + methods in every export', async () => {
    render(<Studio />)
    const caption = within(studio()).getByLabelText('Caption') as HTMLTextAreaElement
    expect(caption.value).toMatch(/^P95 latency — client \(Locust\) over the measured window/)
    expect(caption.value).not.toMatch(/histogram_quantile|PromQL|rate window/i)
    const methods = within(studio()).getByText('Methods & provenance').closest('details')!
    expect(methods).not.toHaveAttribute('open')
    expect(methods.querySelector('p')?.textContent).toMatch(/Locust/)
    fireEvent.change(caption, {target: {value: 'Client P95 per configuration (n = 3 runs). Bands: 95 % CI.'}})
    fireEvent.change(within(studio()).getByLabelText('Figure name'), {target: {value: 'Fig 3 latency'}})
    fireEvent.click(within(studio()).getByRole('button', {name: 'Export PDF'}))
    await vi.waitFor(() => expect(exportChart).toHaveBeenCalled())
    const [id, , format, , options] = vi.mocked(exportChart).mock.calls[0]
    expect(id).toBe(figure().id)
    expect(format).toBe('pdf')
    expect(options).toMatchObject({title: 'Fig 3 latency', fileName: 'kubephos-fig-3-latency'})
    expect(options?.caption).toMatch(/^Client P95 per configuration \(n = 3 runs\)\. Bands: 95 % CI\.\n\nMethods and provenance: .*Locust/s)
    // CSV: plotted values preceded by a commented header with name, caption and methods.
    await vi.waitFor(() => expect(within(studio()).getByRole('button', {name: 'More export formats'})).toBeEnabled())
    fireEvent.click(within(studio()).getByRole('button', {name: 'More export formats'}))
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /Data CSV/}))
    await vi.waitFor(() => expect(downloadBlob).toHaveBeenCalled())
    const [name, blob] = vi.mocked(downloadBlob).mock.calls[0]
    expect(name).toBe('kubephos-fig-3-latency-data.csv')
    const text = await (blob as Blob).text()
    expect(text.split('\n').slice(0, 4)).toEqual(['# Fig 3 latency', '# Caption: Client P95 per configuration (n = 3 runs). Bands: 95 % CI.', expect.stringMatching(/^# Methods and provenance: /), expect.stringMatching(/^series,minute,value/)])
    // Insert into caption appends the methods; reset returns to the automatic caption.
    fireEvent.click(within(studio()).getByRole('button', {name: 'Insert into caption'}))
    expect(caption.value).toMatch(/Bands: 95 % CI\. .*Locust/)
    fireEvent.click(within(studio()).getByRole('button', {name: 'Reset to automatic'}))
    expect(caption.value).toMatch(/^P95 latency — client \(Locust\) over the measured window/)
  })

  it('offers every format in a keyboard-operable export menu', async () => {
    render(<Studio />)
    const toggle = within(studio()).getByRole('button', {name: 'More export formats'})
    expect(toggle).toHaveAttribute('aria-haspopup', 'menu')
    fireEvent.click(toggle)
    const menu = within(studio()).getByRole('menu', {name: 'Export formats'})
    const items = within(menu).getAllByRole('menuitem')
    expect(items.map(item => item.querySelector('strong')?.textContent)).toEqual(['PDF', 'SVG', 'PNG 300 dpi', 'PNG 600 dpi', 'Data CSV', 'Python (matplotlib)', 'pgfplots (LaTeX)'])
    expect(items[0]).toHaveFocus()
    fireEvent.keyDown(menu, {key: 'ArrowDown'})
    expect(items[1]).toHaveFocus()
    fireEvent.keyDown(menu, {key: 'End'})
    expect(items[6]).toHaveFocus()
    fireEvent.keyDown(menu, {key: 'Escape'})
    expect(within(studio()).queryByRole('menu')).not.toBeInTheDocument()
    expect(toggle).toHaveFocus()
    fireEvent.click(toggle)
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /PNG 600 dpi/}))
    await vi.waitFor(() => expect(exportChart).toHaveBeenCalledWith(figure().id, 'load.p95_response_time', 'png', undefined, expect.objectContaining({dpi: 600})))
  })

  it('edits title, axis and series labels with reset and moves the legend', () => {
    render(<Studio initial={{...defaultFigureSettings, showTitle: true}} />)
    fireEvent.change(within(studio()).getByLabelText('Title'), {target: {value: 'Client latency'}})
    expect(figure().querySelector('.figure-title')).toHaveTextContent('Client latency')
    fireEvent.change(within(studio()).getByLabelText('Y axis label'), {target: {value: 'Latency (ms)'}})
    fireEvent.change(within(studio()).getByLabelText('Series 2 label'), {target: {value: 'Raised limit'}})
    expect(figure()).toHaveTextContent('Latency (ms)')
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Raised limit')
    fireEvent.click(within(studio()).getByRole('button', {name: 'Reset series 2 label'}))
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Beta')
    const height = Number(figure().getAttribute('viewBox')!.split(' ')[3])
    const legend = within(studio()).getByRole('group', {name: 'Legend position'})
    fireEvent.click(within(legend).getByRole('radio', {name: 'None'}))
    expect(figure().querySelector('.figure-legend')).toBeNull()
    expect(Number(figure().getAttribute('viewBox')!.split(' ')[3])).toBeLessThan(height)
    fireEvent.click(within(legend).getByRole('radio', {name: 'Top'}))
    // Top legend: drawn above the plot with absolute coordinates (no transforms, so the PDF matches).
    expect(figure().querySelector('g[transform]')).toBeNull()
    expect(Number(figure().querySelector('.figure-legend text')!.getAttribute('y'))).toBeLessThan(Number(figure().querySelector('rect.axis')!.getAttribute('y')))
    fireEvent.click(within(legend).getByRole('radio', {name: 'Right'}))
    expect(Number(figure().querySelector('.figure-legend text')!.getAttribute('x'))).toBeGreaterThan(150)
  })

  it('remembers figure type, caption and zoom per experiment and survives broken storage', () => {
    const view = render(<Studio storageKey="experiment:e1" request={{}} />)
    fireEvent.click(within(studio()).getByRole('radio', {name: /^Run-level dots/}))
    fireEvent.change(within(studio()).getByLabelText('Caption'), {target: {value: 'Edited caption.'}})
    fireEvent.click(within(studio()).getByRole('button', {name: '100 %'}))
    view.unmount()
    expect(loadStudioMemory('experiment:e1')).toMatchObject({kind: 'runlevel', zoom: 1})
    render(<Studio storageKey="experiment:e1" request={{}} />)
    expect(within(studio()).getByRole('radio', {name: /^Run-level dots/})).toBeChecked()
    expect(within(studio()).getByLabelText('Caption')).toHaveValue('Edited caption.')
    expect(within(studio()).getByText('100 %', {selector: '.studio-zoom-value'})).toBeInTheDocument()
    cleanup()
    render(<Studio storageKey="experiment:other" request={{}} />)
    expect(within(studio()).getByRole('radio', {name: /^Time series/})).toBeChecked()
    cleanup()
    const getItem = vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => {throw new Error('denied')})
    const setItem = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {throw new Error('denied')})
    render(<Studio storageKey="experiment:e1" request={{}} />)
    fireEvent.click(within(studio()).getByRole('radio', {name: /^Run-level dots/}))
    expect(within(studio()).getByRole('img', {name: /per repetition/})).toBeInTheDocument()
    getItem.mockRestore()
    setItem.mockRestore()
  })

  it('saves the PDF to the experiment', async () => {
    const save = vi.fn<StudioSave>(async () => {})
    vi.mocked(exportChart).mockImplementation(async (_id, _metric, _format, onGenerated) => {await onGenerated?.(new Uint8Array([37, 80, 68, 70]))})
    render(<Studio onSave={save} />)
    fireEvent.click(within(studio()).getByRole('button', {name: 'Save to experiment'}))
    await vi.waitFor(() => expect(save).toHaveBeenCalledWith({metric: 'load.p95_response_time', format: 'pdf', bytes: expect.any(Uint8Array)}))
    expect(await within(studio()).findByText(/Saved to the experiment/, {selector: '[role="status"]'})).toBeInTheDocument()
  })

  it('turns the panels into Figure / Style / Preview tabs below 1000 px', () => {
    vi.stubGlobal('matchMedia', (query: string) => ({matches: query.includes('max-width'), media: query, addEventListener: () => {}, removeEventListener: () => {}}))
    render(<Studio />)
    const tabs = within(studio()).getByRole('tablist', {name: 'Figure studio panels'})
    expect(within(tabs).getAllByRole('tab').map(tab => tab.textContent)).toEqual(['Figure', 'Style', 'Preview'])
    expect(within(tabs).getByRole('tab', {name: 'Preview'})).toHaveAttribute('aria-selected', 'true')
    const panel = (name: string) => document.getElementById(within(tabs).getByRole('tab', {name}).getAttribute('aria-controls')!)!
    expect(panel('Preview')).toBeVisible()
    expect(panel('Preview')).toHaveAttribute('role', 'tabpanel')
    expect(panel('Style')).not.toBeVisible()
    fireEvent.click(within(tabs).getByRole('tab', {name: 'Style'}))
    expect(panel('Style')).toBeVisible()
    expect(panel('Preview')).not.toBeVisible()
    fireEvent.keyDown(tabs, {key: 'ArrowLeft'})
    expect(within(tabs).getByRole('tab', {name: 'Figure'})).toHaveAttribute('aria-selected', 'true')
    expect(within(studio()).getByRole('radio', {name: /^Time series/})).toBeVisible()
  })

  it('is free of axe violations (wide and narrow)', async () => {
    const audit = async () => {
      const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
      return results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))
    }
    render(<main><h1>Results</h1><Studio onSave={async () => {}} /></main>)
    fireEvent.click(within(studio()).getByText('Methods & provenance'))
    expect(await audit()).toEqual([])
    fireEvent.click(within(studio()).getByRole('button', {name: 'More export formats'}))
    expect(await audit()).toEqual([])
    cleanup()
    vi.stubGlobal('matchMedia', (query: string) => ({matches: true, media: query, addEventListener: () => {}, removeEventListener: () => {}}))
    render(<main><h1>Results</h1><Studio /></main>)
    await act(async () => {fireEvent.click(within(studio()).getByRole('tab', {name: 'Style'}))})
    expect(await audit()).toEqual([])
  })
})
