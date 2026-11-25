import {act, cleanup, fireEvent, render, renderHook, screen, within} from '@testing-library/react'
import axe from 'axe-core'
import {useState} from 'react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {exportChart} from '../../../charts'
import type {AggregateSeries} from '../../../resultsData'
import {defaultFigureSettings} from '../figureSettings'
import {provenanceFor} from '../runs'
import {defaultXYOptions, defaultXYSelection} from '../xy'
import type {StudioData} from './image'
import {KubeStudio, type StudioRequest} from './KubeStudio'
import {ResultFigures} from './ResultFigures'
import {useSavedFigures, type SavedFigure} from './savedFigures'

vi.mock('../../../charts', async original => ({...await original<typeof import('../../../charts')>(), downloadBlob: vi.fn(), exportChart: vi.fn(async () => {})}))

const curve = (label: string, values: number[]): AggregateSeries => ({artifactID: label, label, unit: 'milliseconds', stepMilliseconds: 15000, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value}))})
const data: StudioData = {metrics: ['load.p95_response_time'], plot: () => [curve('Alpha', [1, 2, 3, 4]), curve('Beta', [2, 3, 4, 5])], runLevel: [], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: true}
const groups = [{id: 'a', label: 'Alpha', color: '#1f77b4'}, {id: 'b', label: 'Beta', color: '#d55e00'}]
const chart = (id: string) => ({id, kind: 'timeseries' as const, metric: 'load.p95_response_time', xy: {...defaultXYSelection([]), options: defaultXYOptions}})
const saved = (id: string, name: string, charts = [chart(`${id}-c`)]): SavedFigure => ({id, name, caption: `${name} caption.`, charts, sharedLegend: false, hidden: [], settings: defaultFigureSettings})

function Figures({initial = [], onEdit = vi.fn(), onAdd = vi.fn()}: {initial?: SavedFigure[]; onEdit?: (figure: SavedFigure) => void; onAdd?: () => void}) {
  const [figures, setFigures] = useState(initial)
  const [deleted, setDeleted] = useState<{figure: SavedFigure; index: number}>()
  return <ResultFigures figures={figures} data={data} groups={groups} stored onAdd={onAdd} onEdit={onEdit}
    onDelete={figure => {setDeleted({figure, index: figures.indexOf(figure)}); setFigures(list => list.filter(item => item.id !== figure.id))}}
    deleted={deleted?.figure.name} onUndo={() => {if (deleted) setFigures(list => [...list.slice(0, deleted.index), deleted.figure, ...list.slice(deleted.index)]); setDeleted(undefined)}} />
}

const menu = () => within(screen.getByRole('menu'))

beforeEach(() => {window.localStorage.clear()})
afterEach(() => {cleanup(); vi.mocked(exportChart).mockReset()})

describe('Figures kept on the results page', () => {
  it('invites to add a figure when there is none', () => {
    const onAdd = vi.fn()
    render(<Figures onAdd={onAdd} />)
    expect(screen.getByText('Build a figure in KubeStudio and keep it here, next to the results.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Add figure'}))
    expect(onAdd).toHaveBeenCalled()
  })

  it('draws each figure, numbered after Figure 1, with a menu on right click and on its ⋯ button', async () => {
    const onEdit = vi.fn()
    render(<Figures initial={[saved('f1', 'Latency'), saved('f2', 'Two charts', [chart('a'), chart('b')])]} onEdit={onEdit} />)
    const first = screen.getByRole('figure', {name: 'Figure 2: Latency'})
    expect(first.querySelector('svg.pub-figure')).not.toBeNull()
    expect(within(first).getByText('Latency caption.')).toBeInTheDocument()
    const second = screen.getByRole('figure', {name: 'Figure 3: Two charts'})
    expect(second.querySelectorAll('svg.pub-figure')).toHaveLength(2)
    expect([...second.querySelectorAll('.result-figure-letter')].map(item => item.textContent)).toEqual(['(a)', '(b)'])
    fireEvent.contextMenu(first)
    expect(menu().getAllByRole('menuitem').map(item => item.querySelector('strong')?.textContent)).toEqual(['Edit in KubeStudio', 'Download PDF', 'Download PNG', 'Download SVG', 'Delete figure'])
    expect(document.activeElement).toBe(menu().getByRole('menuitem', {name: /Edit in KubeStudio/}))
    fireEvent.keyDown(screen.getByRole('menu'), {key: 'Escape'})
    expect(screen.queryByRole('menu')).toBeNull()
    fireEvent.click(within(first).getByRole('button', {name: 'Actions for figure 2'}))
    fireEvent.click(menu().getByRole('menuitem', {name: /Edit in KubeStudio/}))
    expect(onEdit).toHaveBeenCalledWith(expect.objectContaining({id: 'f1'}))
    const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(results.violations.map(item => item.id)).toEqual([])
  })

  it('downloads a figure, a page of several charts as one image', async () => {
    let exported = ''
    vi.mocked(exportChart).mockImplementation(async id => {exported = document.getElementById(id)!.outerHTML})
    render(<Figures initial={[saved('f2', 'Two charts', [chart('a'), chart('b')])]} />)
    fireEvent.contextMenu(screen.getByRole('figure', {name: 'Figure 2: Two charts'}))
    await act(async () => {fireEvent.click(menu().getByRole('menuitem', {name: /Download PDF/}))})
    expect(exportChart).toHaveBeenCalledWith('result-figure-f2-export', 'Two charts', 'pdf', undefined, expect.objectContaining({title: 'Two charts', fileName: 'kubephos-two-charts'}))
    expect(exported).toContain('data-export-size="physical"')
    expect((exported.match(/translate\(/g) ?? []).length).toBeGreaterThanOrEqual(2)
    expect(document.getElementById('result-figure-f2-export')).toBeNull()
  })

  it('deletes a figure from its menu, with an undo', () => {
    render(<Figures initial={[saved('f1', 'Latency'), saved('f2', 'Throughput')]} />)
    fireEvent.contextMenu(screen.getByRole('figure', {name: 'Figure 2: Latency'}))
    fireEvent.click(menu().getByRole('menuitem', {name: /Delete figure/}))
    expect(screen.queryByRole('figure', {name: /Latency/})).toBeNull()
    expect(screen.getByRole('figure', {name: 'Figure 2: Throughput'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Undo'}))
    expect(screen.getAllByRole('figure').map(item => item.getAttribute('aria-label'))).toEqual(['Figure 2: Latency', 'Figure 3: Throughput'])
  })
})

describe('Saved figures storage', () => {
  it('keeps figures per experiment in this browser', () => {
    const {result, rerender} = renderHook(({key}) => useSavedFigures(key), {initialProps: {key: 'experiment:e1'}})
    let kept: SavedFigure | undefined
    act(() => {kept = result.current.add({name: 'Latency', caption: '', charts: [chart('c1')], sharedLegend: false, hidden: [], settings: defaultFigureSettings})})
    act(() => {result.current.update({...kept!, name: 'Renamed'})})
    expect(result.current.figures.map(item => item.name)).toEqual(['Renamed'])
    rerender({key: 'experiment:e2'})
    expect(result.current.figures).toEqual([])
    rerender({key: 'experiment:e1'})
    expect(result.current.figures.map(item => item.name)).toEqual(['Renamed'])
    act(() => {result.current.remove(kept!.id)})
    expect(JSON.parse(window.localStorage.getItem('kubephos.resultFigures.v1:experiment:e1') ?? 'null')).toEqual([])
  })

  it('ignores stored entries without a drawable chart', () => {
    window.localStorage.setItem('kubephos.resultFigures.v1:experiment:e3', JSON.stringify([{id: 'x', name: 'Broken', charts: [{kind: 'unknown'}]}, {id: 'y', name: 'Fine', charts: [chart('c1')]}]))
    const {result} = renderHook(() => useSavedFigures('experiment:e3'))
    expect(result.current.figures.map(item => item.name)).toEqual(['Fine'])
  })
})

describe('KubeStudio and the results page', () => {
  function Studio({request = {}, onKeep}: {request?: StudioRequest; onKeep: (figure: Omit<SavedFigure, 'id'>, id?: string) => SavedFigure}) {
    const [settings, setSettings] = useState(defaultFigureSettings)
    return <KubeStudio open onClose={() => {}} request={request} data={data} groups={groups} settings={settings} onSettings={setSettings} storageKey="experiment:e1" onKeep={onKeep} />
  }

  it('adds the image to the results, then updates that same figure', () => {
    const onKeep = vi.fn((figure: Omit<SavedFigure, 'id'>, id?: string) => ({...figure, id: id ?? 'kept'}))
    render(<Studio onKeep={onKeep} />)
    const studio = within(screen.getByRole('dialog', {name: 'KubeStudio'}))
    fireEvent.click(studio.getByRole('button', {name: 'Add to results'}))
    expect(onKeep).toHaveBeenLastCalledWith(expect.objectContaining({name: expect.any(String), charts: [expect.objectContaining({kind: 'timeseries'})], settings: defaultFigureSettings}), undefined)
    fireEvent.click(studio.getByRole('button', {name: 'Update in results'}))
    expect(onKeep).toHaveBeenLastCalledWith(expect.anything(), 'kept')
  })

  it('opens a kept figure as it was saved, with its own page style', () => {
    const figure = {...saved('f1', 'My figure'), settings: {...defaultFigureSettings, fontSize: 10}}
    render(<Studio request={{figure}} onKeep={vi.fn()} />)
    const studio = within(screen.getByRole('dialog', {name: 'KubeStudio'}))
    expect(studio.getByLabelText('Figure name')).toHaveValue('My figure')
    expect(studio.getByRole('button', {name: 'Update in results'})).toBeInTheDocument()
    expect(studio.getByText(/Times 10 pt/)).toBeInTheDocument()
    // Editing a kept figure leaves the remembered image alone.
    expect(window.localStorage.getItem('kubephos.figureStudio.v1:experiment:e1')).toBeNull()
  })
})
