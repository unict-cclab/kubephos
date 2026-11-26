import {useState} from 'react'
import {act, cleanup, createEvent, fireEvent, render, renderHook, screen, waitFor, within} from '@testing-library/react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {request} from '../../../api'
import type {AggregateSeries} from '../../../resultsData'
import {defaultFigureSettings} from '../figureSettings'
import {provenanceFor} from '../runs'
import {defaultXYState, type StudioData} from './image'
import {SectionsMenu} from '../SectionsMenu'
import {clickDelay, ResultCharts} from './ResultCharts'
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

// The charts with the page header's Sections menu and Reset (in the page's ⋯ menu), as on a results page.
function Page({onEdit = () => {}}: {onEdit?: (item: ChartItem) => void}) {
  const charts = useResultsLayout(scope, data, 'token')
  const [undo, setUndo] = useState<() => void>()
  return <>
    <SectionsMenu available={available} shown={charts.sections} onToggle={charts.toggleSection} onShowAll={() => charts.setSections(available)} />
    {charts.customized && <button type="button" onClick={() => setUndo(() => charts.reset())}>Reset charts and sections</button>}
    {undo && <button type="button" onClick={() => {undo(); setUndo(undefined)}}>Undo reset</button>}
    <ResultCharts items={charts.items} data={data} groups={groups} settings={defaultFigureSettings} loading={charts.loading} error={charts.error} onChange={charts.change} onEdit={onEdit} />
  </>
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
const openMenu = (index: number) => fireEvent.click(within(section().getAllByRole('figure')[index]).getByRole('button', {name: /^Actions for/}))
const grid = () => section().getByRole('list')
const cells = () => grid().querySelectorAll<HTMLLIElement>(':scope > li')
const sizes = () => [...cells()].map(cell => cell.dataset.size)
// jsdom has no PointerEvent: the coordinates are set on the event itself.
const pointer = (type: 'pointerDown' | 'pointerMove' | 'pointerUp', element: HTMLElement, clientX: number) => {const event = createEvent[type](element, {button: 0}); Object.defineProperty(event, 'clientX', {value: clientX}); fireEvent(element, event)}

beforeEach(() => vi.mocked(request).mockImplementation(async () => ({layout: null}) as never))
afterEach(() => {cleanup(); vi.mocked(request).mockReset()})

describe('Charts', () => {
  it('shows one chart per metric by default, without saving anything', async () => {
    render(<Page />)
    expect(section().getByText('Loading your charts…')).toBeInTheDocument()
    await waitFor(() => expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate']))
    expect(request).toHaveBeenCalledWith(path)
    expect(section().getAllByRole('img')).toHaveLength(2)
    // Nothing but the charts in their header: no Reset, no Sections (both moved to the page header).
    expect(section().queryByRole('button', {name: /Reset|Sections/})).not.toBeInTheDocument()
    expect(saves()).toEqual([])
  })

  it('hides a chart from its ⋯ menu with undo, shows it again from Hidden charts, and resets to the defaults', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    // No "Add chart" here: Create figure in the page header opens KubeStudio.
    expect(section().queryByRole('button', {name: /Add chart/})).not.toBeInTheDocument()
    openMenu(0)
    const menu = within(screen.getByRole('menu', {name: 'P95 latency — client (Locust)'}))
    expect(menu.getAllByRole('menuitem').map(item => item.querySelector('strong')!.textContent)).toEqual(['Full screen', 'Edit in KubeStudio', 'Rename', 'Duplicate', 'Download PDF', 'Download PNG', 'Download SVG', 'Hide'])
    // Sizes are the corner handle's job, not the menu's.
    expect(menu.queryAllByRole('menuitemcheckbox')).toEqual([])
    expect(menu.queryByRole('menuitem', {name: /Remove/})).not.toBeInTheDocument()
    fireEvent.click(menu.getByRole('menuitem', {name: /^Hide/}))
    expect(names()).toEqual(['Achieved request rate'])
    expect(saves()[0]).toEqual([path, expect.objectContaining({method: 'PUT'}), 'token'])
    // Hidden, not removed: kept in the saved layout and listed, collapsed, under the grid.
    expect(savedLayout().items!.map(item => [item.name, item.concealed])).toEqual([['P95 latency — client (Locust)', true], ['Achieved request rate', undefined]])
    const toggle = section().getByRole('button', {name: 'Hidden charts (1)'})
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(section().queryByRole('button', {name: /^Show P95/})).not.toBeInTheDocument()
    fireEvent.click(section().getByRole('button', {name: 'Undo'}))
    expect(names()).toHaveLength(2)
    expect(section().queryByRole('button', {name: /^Hidden charts/})).not.toBeInTheDocument()
    openMenu(1)
    fireEvent.click(screen.getByRole('menuitem', {name: /^Hide/}))
    fireEvent.click(section().getByRole('button', {name: 'Hidden charts (1)'}))
    fireEvent.click(section().getByRole('button', {name: 'Show Achieved request rate'}))
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate'])
    expect(savedLayout().items!.every(item => !item.concealed)).toBe(true)
    expect(section().getByText('Achieved request rate shown again.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Reset charts and sections'}))
    expect(saves().at(-1)).toEqual([path, {method: 'DELETE'}, 'token'])
    expect(screen.queryByRole('button', {name: 'Reset charts and sections'})).not.toBeInTheDocument()
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

  it('resizes a chart in steps from its corner (keys or drag); the grid reflows around it', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    // A six-column grid filled densely: the size is a span of it (CSS), so neighbours move to fill rows.
    expect(sizes()).toEqual(['third', 'third'])
    const corner = () => section().getByRole('button', {name: /^Resize P95 latency/})
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    expect(sizes()).toEqual(['half', 'third'])
    expect(section().getByText('P95 latency — client (Locust) is now ½ width.')).toBeInTheDocument()
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    fireEvent.keyDown(corner(), {key: 'ArrowRight'})
    expect(sizes()).toEqual(['full', 'third'])
    expect(savedLayout().items![0].size).toBe('full')
    expect(corner()).toHaveAccessibleName(/now Full width/)
    fireEvent.keyDown(corner(), {key: 'ArrowLeft'})
    expect(sizes()).toEqual(['two-thirds', 'third'])
    fireEvent.keyDown(section().getByRole('button', {name: /^Resize Achieved/}), {key: 'ArrowUp'})
    fireEvent.keyDown(corner(), {key: 'ArrowDown'})
    fireEvent.keyDown(corner(), {key: 'ArrowDown'})
    expect(sizes()).toEqual(['third', 'half'])
    expect(savedLayout().items!.map(item => item.size)).toEqual([undefined, 'half'])
    // Dragging the corner previews the nearest size live (the grid reflows), saves it when let go,
    // and never starts moving the card instead.
    Object.defineProperty(grid(), 'clientWidth', {configurable: true, value: 1200})
    vi.spyOn(cells()[0], 'getBoundingClientRect').mockReturnValue({left: 0, right: 380, width: 380, top: 0, bottom: 300, height: 300, x: 0, y: 0, toJSON: () => ({})})
    const before = saves().length
    pointer('pointerDown', corner(), 380)
    const drag = createEvent.dragStart(section().getAllByRole('figure')[0], {dataTransfer: {setData: () => {}, effectAllowed: ''}})
    fireEvent(section().getAllByRole('figure')[0], drag)
    expect(drag.defaultPrevented).toBe(true)
    pointer('pointerMove', corner(), 790)
    expect(sizes()).toEqual(['two-thirds', 'half'])
    expect(saves()).toHaveLength(before)
    pointer('pointerMove', corner(), 1180)
    expect(sizes()).toEqual(['full', 'half'])
    pointer('pointerUp', corner(), 1180)
    expect(savedLayout().items![0].size).toBe('full')
    // A grid too narrow for thirds steps between half and full width only.
    Object.defineProperty(grid(), 'clientWidth', {configurable: true, value: 700})
    fireEvent.keyDown(corner(), {key: 'ArrowLeft'})
    expect(sizes()).toEqual(['half', 'half'])
    fireEvent.keyDown(corner(), {key: 'ArrowLeft'})
    expect(sizes()).toEqual(['half', 'half'])
  })

  it('duplicates a chart from its ⋯ button and opens charts in KubeStudio by double-click, Enter or the menu', async () => {
    const onEdit = vi.fn()
    render(<Page onEdit={onEdit} />)
    await waitFor(() => expect(names()).toHaveLength(2))
    fireEvent.click(section().getByRole('button', {name: 'Actions for Achieved request rate'}))
    fireEvent.click(screen.getByRole('menuitem', {name: /^Duplicate/}))
    expect(names()).toEqual(['P95 latency — client (Locust)', 'Achieved request rate', 'Achieved request rate (copy)'])
    openMenu(0)
    fireEvent.click(screen.getByRole('menuitem', {name: /^Edit in KubeStudio/}))
    expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'P95 latency — client (Locust)'}))
    fireEvent.doubleClick(section().getAllByRole('figure')[2].querySelector('.result-chart-image')!)
    expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'Achieved request rate (copy)'}))
    // Keyboard: the card is focusable, Enter opens it; Enter inside its buttons does not.
    const second = section().getAllByRole('figure')[1]
    expect(second).toHaveAttribute('tabindex', '0')
    expect(second).toHaveAttribute('aria-keyshortcuts', 'Enter F Shift+F10')
    expect(second).toHaveAccessibleDescription(/Click for full screen, double-click to edit in KubeStudio, right-click for more/)
    fireEvent.keyDown(second, {key: 'Enter'})
    expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'Achieved request rate'}))
    fireEvent.keyDown(within(second).getByRole('button', {name: /^Actions for/}), {key: 'Enter'})
    expect(onEdit).toHaveBeenCalledTimes(3)
    // Double-clicking the title renames instead.
    fireEvent.doubleClick(within(second).getByText('Achieved request rate'))
    expect(within(second).getByRole('textbox', {name: 'Chart name'})).toBeInTheDocument()
    expect(onEdit).toHaveBeenCalledTimes(3)
  })

  it('opens a chart full screen on a single click, F or its ⤢ button; a double click opens KubeStudio instead', async () => {
    const onEdit = vi.fn()
    render(<Page onEdit={onEdit} />)
    await waitFor(() => expect(names()).toHaveLength(2))
    vi.useFakeTimers()
    try {
      const fullscreen = () => screen.queryByRole('dialog', {name: 'Achieved request rate'})
      const card = () => section().getAllByRole('figure')[1]
      const image = () => card().querySelector('.result-chart-image')!
      // A single click waits for a possible second one, then opens the full screen.
      fireEvent.click(image(), {detail: 1})
      expect(fullscreen()).not.toBeInTheDocument()
      act(() => {vi.advanceTimersByTime(clickDelay)})
      expect(fullscreen()).toBeInTheDocument()
      expect(within(fullscreen()!).getByRole('img')).toBeInTheDocument()
      expect(within(fullscreen()!).getByRole('button', {name: 'Close full screen'})).toHaveFocus()
      fireEvent.keyDown(fullscreen()!, {key: 'Escape'})
      expect(fullscreen()).not.toBeInTheDocument()
      // A double click never flashes it: KubeStudio opens.
      fireEvent.click(image(), {detail: 1})
      fireEvent.click(image(), {detail: 2})
      fireEvent.doubleClick(image())
      act(() => {vi.advanceTimersByTime(clickDelay * 2)})
      expect(fullscreen()).not.toBeInTheDocument()
      expect(onEdit).toHaveBeenCalledTimes(1)
      // Clicks on the card's controls are theirs.
      fireEvent.click(within(card()).getByRole('button', {name: /^Resize/}), {detail: 1})
      fireEvent.click(within(card()).getByRole('button', {name: /^Actions for/}), {detail: 1})
      fireEvent.click(screen.getByRole('menuitem', {name: /^Duplicate/}), {detail: 1})
      act(() => {vi.advanceTimersByTime(clickDelay * 2)})
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
      // F on the focused card, and the ⤢ button, open it at once.
      fireEvent.keyDown(card(), {key: 'f'})
      expect(fullscreen()).toBeInTheDocument()
      fireEvent.click(within(fullscreen()!).getByRole('button', {name: 'Close full screen'}))
      expect(fullscreen()).not.toBeInTheDocument()
      fireEvent.click(section().getByRole('button', {name: 'Full screen: Achieved request rate'}))
      fireEvent.click(within(fullscreen()!).getByRole('button', {name: 'Edit in KubeStudio'}))
      expect(fullscreen()).not.toBeInTheDocument()
      expect(onEdit).toHaveBeenLastCalledWith(expect.objectContaining({name: 'Achieved request rate'}))
    } finally {vi.useRealTimers()}
  })

  it('opens the chart’s menu on right-click at the pointer, or with Shift+F10 and the menu key', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    const card = section().getAllByRole('figure')[1]
    const right = createEvent.contextMenu(card)
    Object.defineProperty(right, 'clientX', {value: 120})
    Object.defineProperty(right, 'clientY', {value: 80})
    fireEvent(card, right)
    expect(right.defaultPrevented).toBe(true)
    const menu = screen.getByRole('menu', {name: 'Achieved request rate'})
    expect(menu.style.top).toBe('80px')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    fireEvent.click(within(menu).getByRole('menuitem', {name: /^Full screen/}))
    expect(screen.getByRole('dialog', {name: 'Achieved request rate'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Close full screen'}))
    fireEvent.keyDown(card, {key: 'F10', shiftKey: true})
    expect(screen.getByRole('menu', {name: 'Achieved request rate'})).toBeInTheDocument()
    fireEvent.keyDown(screen.getByRole('menu'), {key: 'Escape'})
    fireEvent.keyDown(card, {key: 'ContextMenu'})
    expect(screen.getByRole('menu', {name: 'Achieved request rate'})).toBeInTheDocument()
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
    expect(screen.getByRole('button', {name: 'Reset charts and sections'})).toBeInTheDocument()
    fireEvent.keyDown(section().getByRole('button', {name: /^Actions for Latency/}), {key: 'ArrowLeft', altKey: true})
    openMenu(0)
    fireEvent.click(screen.getByRole('menuitem', {name: /^Hide/}))
    expect(await section().findByText('The server is unavailable.')).toBeInTheDocument()
    expect(section().getByText(/Every chart is hidden/)).toBeInTheDocument()
    expect(section().getByRole('button', {name: 'Hidden charts (1)'})).toBeInTheDocument()
  })

  it('shows the default charts when the layout cannot be read', async () => {
    vi.mocked(request).mockImplementation(async () => {throw new Error('offline')})
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
  })
})

describe('stored layouts', () => {
  it('reads sizes, hidden charts and the column spans of older layouts', async () => {
    const chart = (id: string, extra: object) => ({id, name: id, caption: '', charts: [{id, kind: 'timeseries', metric: 'load.p95_response_time', xy: defaultXYState(data)}], sharedLegend: false, hidden: [], ...extra})
    vi.mocked(request).mockImplementation(async () => ({layout: {version: 1, items: [chart('wide', {span: 3}), chart('two', {span: 2}), chart('half', {size: 'half'}), chart('odd', {size: 'huge'}), chart('away', {concealed: true})]}}) as never)
    render(<Page />)
    await waitFor(() => expect(names()).toEqual(['wide', 'two', 'half', 'odd']))
    expect(sizes()).toEqual(['full', 'two-thirds', 'half', 'third'])
    expect(section().getByRole('button', {name: 'Hidden charts (1)'})).toBeInTheDocument()
  })
})

describe('the rest of the page', () => {
  it('is chosen from Display: one switch per section, the state at a glance, Show all', async () => {
    render(<Page />)
    await waitFor(() => expect(names()).toHaveLength(2))
    const button = () => screen.getByRole('button', {name: /^Display/})
    expect(button()).toHaveAccessibleName('Display: 1 of 4 sections shown')
    expect(button()).toHaveAttribute('title', 'Choose what this page shows (1 of 4 sections shown)')
    fireEvent.click(button())
    const menu = () => within(screen.getByRole('menu', {name: 'Sections of this page'}))
    const switches = () => menu().getAllByRole('menuitemcheckbox').map(item => [item.querySelector('strong')!.textContent, item.getAttribute('aria-checked'), item.querySelector('small')!.textContent])
    expect(switches()).toEqual([['Charts', 'true', 'Always shown'], ['Summary', 'false', 'Verdict and key numbers'], ['Load and latency', 'false', 'Request rate and latency over time'], ['Statistics', 'false', 'Tables per metric and comparisons']])
    expect(menu().getByRole('menuitemcheckbox', {name: /^Charts/})).toBeDisabled()
    // Switching keeps the menu open, so several can be changed in a row; each change is saved.
    fireEvent.click(menu().getByRole('menuitemcheckbox', {name: /^Load and latency/}))
    expect(savedLayout()).toEqual({version: 1, sections: ['timeline']})
    expect(menu().getByRole('menuitemcheckbox', {name: /^Load and latency/})).toHaveAttribute('aria-checked', 'true')
    fireEvent.click(menu().getByRole('menuitemcheckbox', {name: /^Summary/}))
    expect(savedLayout().sections).toEqual(['summary', 'timeline'])
    expect(button()).toHaveAccessibleName('Display: 3 of 4 sections shown')
    fireEvent.click(menu().getByRole('menuitem', {name: /^Show all/}))
    expect(savedLayout().sections).toEqual(['summary', 'timeline', 'statistics'])
    fireEvent.click(button())
    expect(menu().getByRole('menuitem', {name: /^Show all/})).toBeDisabled()
    fireEvent.keyDown(screen.getByRole('menu'), {key: 'Escape'})
    // Reset (from the page's ⋯ menu) restores the default page, with undo.
    fireEvent.click(screen.getByRole('button', {name: 'Reset charts and sections'}))
    expect(saves().at(-1)).toEqual([path, {method: 'DELETE'}, 'token'])
    fireEvent.click(screen.getByRole('button', {name: 'Undo reset'}))
    expect(savedLayout()).toEqual({version: 1, sections: ['summary', 'timeline', 'statistics']})
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
