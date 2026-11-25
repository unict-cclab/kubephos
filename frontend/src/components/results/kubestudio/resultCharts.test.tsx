import {act, cleanup, createEvent, fireEvent, render, renderHook, screen, waitFor, within} from '@testing-library/react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {request} from '../../../api'
import type {AggregateSeries} from '../../../resultsData'
import {defaultFigureSettings} from '../figureSettings'
import {provenanceFor} from '../runs'
import {defaultXYState, type StudioData} from './image'
import {ResultCharts} from './ResultCharts'
import {defaultCharts, useResultsLayout, type ChartItem, type LayoutScope, type PageSection} from './resultsLayout'

vi.mock('../../../api', () => ({request: vi.fn()}))

const curve = (label: string, unit: string, values: number[]): AggregateSeries => ({artifactID: label, label, unit, stepMilliseconds: 15000, runs: 3, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value})), deviation: values.map((_, index) => ({timestamp: new Date(index * 15000).toISOString(), value: .5}))})
const plots: Record<string, AggregateSeries[]> = {
  'load.p95_response_time': [curve('Alpha', 'milliseconds', [1, 2, 3, 4]), curve('Beta', 'milliseconds', [2, 3, 4, 5])],
  'load.achieved_rps': [curve('Alpha', 'requests_per_second', [10, 11, 12, 13]), curve('Beta', 'requests_per_second', [9, 10, 11, 12])]
}
const data: StudioData = {metrics: Object.keys(plots), plot: metric => plots[metric] ?? [], runLevel: [], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: true}
const groups = [{id: 'a', label: 'Alpha', color: '#1f77b4'}, {id: 'b', label: 'Beta', color: '#d55e00'}]
const scope: LayoutScope = {kind: 'experiments', id: 'exp/1'}
const path = '/account/result-layouts/experiments/exp%2F1'

const available: PageSection[] = ['summary', 'timeline', 'statistics']

function Page({onAdd = () => {}, onEdit = () => {}}: {onAdd?: () => void; onEdit?: (item: ChartItem) => void}) {
  const charts = useResultsLayout(scope, data, 'token')
  return <ResultCharts items={charts.items} data={data} groups={groups} settings={defaultFigureSettings} loading={charts.loading} customized={charts.customized} error={charts.error}
    sections={available} shown={charts.sections} onToggleSection={charts.toggleSection}
    onChange={charts.change} onReset={charts.reset} onAdd={onAdd} onEdit={onEdit} />
}

const section = () => within(screen.getByRole('region', {name: 'Charts'}))
const names = () => section().getAllByRole('figure').map(figure => figure.getAttribute('aria-label'))
const saves = () => vi.mocked(request).mock.calls.filter(([, init]) => init?.method)
const savedLayout = () => {
  const [, init] = saves().at(-1)!
  return (JSON.parse(String(init!.body)) as {layout: {version: number; items?: ChartItem[]; sections: PageSection[]}}).layout
}
const savedNames = () => savedLayout().items!.map(item => item.name)
// jsdom drops clientX from drag events, so it is set on the event itself.
const dragOverAt = (element: HTMLElement, clientX: number) => {const event = createEvent.dragOver(element); Object.defineProperty(event, 'clientX', {value: clientX}); fireEvent(element, event)}
const openMenu = (index: number) => fireEvent.contextMenu(section().getAllByRole('figure')[index])

beforeEach(() => vi.mocked(request).mockImplementation(async () => ({layout: null}) as never))
afterEach(() => {cleanup(); vi.mocked(request).mockReset()})

describe('Charts', () => {
  it('shows one chart per metric by default, without saving anything', async () => {
    render(<Page />)
    expect(section().getByText('Loading your charts…')).toBeInTheDocument()
    await waitFor(() => expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate']))
    expect(request).toHaveBeenCalledWith(path)
    expect(section().getAllByRole('img')).toHaveLength(2)
    expect(section().queryByRole('button', {name: 'Reset'})).not.toBeInTheDocument()
    expect(saves()).toEqual([])
  })

  it('removes a chart from its right-click menu with undo, and resets to the defaults', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    openMenu(0)
    const menu = within(screen.getByRole('menu', {name: 'P95 latency — client (Locust)'}))
    expect(menu.getAllByRole('menuitem').map(item => item.textContent)).toEqual([expect.stringMatching(/^Edit in KubeStudio/), expect.stringMatching(/^Rename/), expect.stringMatching(/^Duplicate/), expect.stringMatching(/^Download PDF/), expect.stringMatching(/^Download PNG/), expect.stringMatching(/^Download SVG/), expect.stringMatching(/^Remove from this page/)])
    fireEvent.click(menu.getByRole('menuitem', {name: /^Remove from this page/}))
    expect(names()).toEqual(['Achieved request rate'])
    expect(saves()[0]).toEqual([path, expect.objectContaining({method: 'PUT'}), 'token'])
    expect(savedNames()).toEqual(['Achieved request rate'])
    fireEvent.click(section().getByRole('button', {name: 'Undo'}))
    expect(names()).toHaveLength(2)
    expect(savedNames()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate'])
    fireEvent.click(section().getByRole('button', {name: 'Reset'}))
    expect(saves().at(-1)).toEqual([path, {method: 'DELETE'}, 'token'])
    expect(section().getByText(/Default page restored/)).toBeInTheDocument()
    expect(section().queryByRole('button', {name: 'Reset'})).not.toBeInTheDocument()
  })

  it('moves and swaps charts by dragging them, or moves them with Alt + arrow keys on their ⋯ button', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    // There is no separate drag handle: the whole card is dragged.
    expect(section().queryByRole('button', {name: /^Move /})).not.toBeInTheDocument()
    fireEvent.keyDown(section().getByRole('button', {name: /^Actions for P95 latency/}), {key: 'ArrowRight'})
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate'])
    fireEvent.keyDown(section().getByRole('button', {name: /^Actions for P95 latency/}), {key: 'ArrowRight', altKey: true})
    expect(names()).toEqual(['Achieved request rate', 'P95 latency — client (Locust)'])
    expect(section().getByText('P95 latency — client (Locust) moved to position 2 of 2.')).toBeInTheDocument()
    expect(savedNames()).toEqual(['Achieved request rate', 'P95 latency — client (Locust)'])
    // Drag a chart onto the left quarter of another to move it before; onto its middle to swap the two.
    const charts = () => section().getAllByRole('listitem')
    for (const item of charts()) vi.spyOn(item, 'getBoundingClientRect').mockReturnValue({left: 0, right: 400, width: 400, top: 0, bottom: 300, height: 300, x: 0, y: 0, toJSON: () => ({})})
    fireEvent.dragStart(section().getAllByRole('figure')[1], {dataTransfer: {setData: () => {}, effectAllowed: ''}})
    dragOverAt(charts()[0], 20)
    expect(charts()[0]).toHaveAttribute('data-drop-before')
    fireEvent.drop(charts()[0])
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate'])
    fireEvent.dragStart(section().getAllByRole('figure')[0], {dataTransfer: {setData: () => {}, effectAllowed: ''}})
    dragOverAt(charts()[1], 200)
    expect(charts()[1]).toHaveAttribute('data-drop-swap')
    fireEvent.drop(charts()[1])
    expect(names()).toEqual(['Achieved request rate', 'P95 latency — client (Locust)'])
    expect(section().getByText('P95 latency — client (Locust) swapped with Achieved request rate.')).toBeInTheDocument()
  })

  it('renames a chart from its ⋯ menu or by double-clicking its title', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    fireEvent.click(section().getByRole('button', {name: /^Actions for Achieved/}))
    fireEvent.click(screen.getByRole('menuitem', {name: /^Rename/}))
    const input = section().getByRole('textbox', {name: 'Chart name'})
    expect(input).toHaveFocus()
    expect(input).toHaveValue('Achieved request rate')
    fireEvent.change(input, {target: {value: '  Throughput  '}})
    fireEvent.keyDown(input, {key: 'Enter'})
    fireEvent.blur(input)
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Throughput'])
    expect(savedNames()).toEqual(['P95 latency — client (Locust)', 'Throughput'])
    // Esc and an empty name keep the old one.
    fireEvent.doubleClick(section().getByText('Throughput'))
    fireEvent.change(section().getByRole('textbox', {name: 'Chart name'}), {target: {value: 'Other'}})
    fireEvent.keyDown(section().getByRole('textbox', {name: 'Chart name'}), {key: 'Escape'})
    fireEvent.doubleClick(section().getByText('Throughput'))
    fireEvent.change(section().getByRole('textbox', {name: 'Chart name'}), {target: {value: '   '}})
    fireEvent.blur(section().getByRole('textbox', {name: 'Chart name'}))
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Throughput'])
  })

  it('widens and narrows a chart from its corner, up to the columns there are', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    const [first] = section().getAllByRole('listitem')
    const corner = () => section().getByRole('button', {name: /^Resize P95 latency/})
    expect(first.style.gridColumn).toBe('span 1')
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    expect(first.style.gridColumn).toBe('span 2')
    expect(section().getByText('P95 latency — client (Locust) now spans 2 columns.')).toBeInTheDocument()
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    expect(first.style.gridColumn).toBe('span 3')
    expect(JSON.parse(String(saves().at(-1)![1]!.body)).layout.items[0].span).toBe(3)
    fireEvent.keyDown(corner(), {key: 'ArrowLeft'})
    fireEvent.keyDown(corner(), {key: 'ArrowLeft'})
    expect(first.style.gridColumn).toBe('span 1')
    expect(JSON.parse(String(saves().at(-1)![1]!.body)).layout.items[0].span).toBeUndefined()
  })

  it('duplicates a chart from its ⋯ button and opens charts in KubeStudio', async () => {
    const onEdit = vi.fn()
    const onAdd = vi.fn()
    render(<Page onEdit={onEdit} onAdd={onAdd} />)
    await waitFor(() => expect(names()).toHaveLength(2))
    fireEvent.click(section().getByRole('button', {name: 'Actions for Achieved request rate'}))
    fireEvent.click(screen.getByRole('menuitem', {name: /^Duplicate/}))
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate', 'Achieved request rate (copy)'])
    openMenu(0)
    fireEvent.click(screen.getByRole('menuitem', {name: /^Edit in KubeStudio/}))
    expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'P95 latency — client (Locust)'}))
    fireEvent.doubleClick(section().getAllByRole('figure')[2].querySelector('.result-chart-image')!)
    expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'Achieved request rate (copy)'}))
    fireEvent.click(section().getByRole('button', {name: 'Add chart'}))
    expect(onAdd).toHaveBeenCalled()
  })

  it('loads the user’s saved arrangement, drops unreadable entries and reports failed saves', async () => {
    const mine: ChartItem = {id: 'mine', name: 'Latency for the paper', caption: 'P95 per strategy.', charts: [{id: 'mine', kind: 'timeseries', metric: 'load.p95_response_time', xy: defaultXYState(data)}], sharedLegend: false, hidden: []}
    vi.mocked(request).mockImplementation(async (_path, init) => {
      if (init?.method) throw new Error('The server is unavailable.')
      return {layout: {version: 1, items: [mine, {id: 'broken'}, 'nonsense']}} as never
    })
    render(<Page />)
    await waitFor(() => expect(names()).toEqual(['Latency for the paper']))
    expect(section().getByText('P95 per strategy.')).toBeInTheDocument()
    expect(section().getByRole('button', {name: 'Reset'})).toBeInTheDocument()
    fireEvent.keyDown(section().getByRole('button', {name: /^Actions for Latency/}), {key: 'ArrowLeft', altKey: true})
    openMenu(0)
    fireEvent.click(screen.getByRole('menuitem', {name: /^Remove from this page/}))
    expect(await section().findByText('The server is unavailable.')).toBeInTheDocument()
    expect(section().getByText(/No charts here/)).toBeInTheDocument()
  })

  it('shows the default charts when the layout cannot be read', async () => {
    vi.mocked(request).mockImplementation(async () => {throw new Error('offline')})
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
  })
})

describe('the rest of the page', () => {
  it('starts hidden and is added from the Sections menu, which saves it with the default charts', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    fireEvent.click(section().getByRole('button', {name: 'Sections'}))
    const menu = within(screen.getByRole('menu', {name: 'Sections'}))
    expect(menu.getAllByRole('menuitemcheckbox').map(item => [item.querySelector('strong')!.textContent, item.getAttribute('aria-checked')])).toEqual([['Summary', 'false'], ['Load and latency', 'false'], ['Statistics', 'false']])
    fireEvent.click(menu.getByRole('menuitemcheckbox', {name: /^Load and latency/}))
    expect(savedLayout()).toEqual({version: 1, sections: ['timeline']})
    fireEvent.click(section().getByRole('button', {name: 'Sections'}))
    expect(screen.getByRole('menuitemcheckbox', {name: /^Load and latency/})).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(screen.getByRole('menuitemcheckbox', {name: /^Summary/}))
    expect(savedLayout().sections).toEqual(['summary', 'timeline'])
    fireEvent.click(section().getByRole('button', {name: 'Reset'}))
    expect(saves().at(-1)).toEqual([path, {method: 'DELETE'}, 'token'])
    fireEvent.click(section().getByRole('button', {name: 'Undo'}))
    expect(savedLayout()).toEqual({version: 1, sections: ['summary', 'timeline']})
  })
})

describe('default charts', () => {
  it('put the placement heatmap first, then each metric over time followed by its services', () => {
    const services: StudioData = {...data, metrics: ['cpu.cores', 'application.p95_response_time', ...data.metrics], serviceMetrics: ['cpu.cores', 'application.p95_response_time'], placement: <svg />,
      plot: metric => plots[metric] ?? [curve('Alpha', 'cores', [1, 2])]}
    expect(defaultCharts(services).map(item => item.id)).toEqual(['placement', 'timeseries:load.p95_response_time', 'timeseries:load.achieved_rps', 'services:application.p95_response_time', 'timeseries:cpu.cores', 'services:cpu.cores'])
  })
})

describe('useResultsLayout', () => {
  it('keeps KubeStudio images: replaces the chart they were opened from, or adds them at the end', async () => {
    const {result} = renderHook(() => useResultsLayout(scope, data, 'token'))
    await waitFor(() => expect(result.current.loading).toBe(false))
    const [first] = result.current.items
    act(() => {result.current.keep({...first, name: 'Edited'}, first.id)})
    expect(result.current.items.map(item => item.name)).toEqual(['Edited', 'Achieved request rate'])
    let added: ChartItem | undefined
    act(() => {added = result.current.keep({...first, name: 'New figure'})})
    expect(result.current.items.map(item => item.name)).toEqual(['Edited', 'Achieved request rate', 'New figure'])
    expect(added!.id).not.toBe(first.id)
    expect(result.current.customized).toBe(true)
  })

  it('keeps changes only in the page without a scope', () => {
    const {result} = renderHook(() => useResultsLayout(undefined, data))
    expect(result.current.loading).toBe(false)
    act(() => result.current.change(result.current.items.slice(1)))
    expect(result.current.items).toHaveLength(1)
    expect(request).not.toHaveBeenCalled()
  })
})
