import {cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import {useState} from 'react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {downloadBlob, exportChart, svgToPDF} from '../../charts'
import type {AggregateSeries} from '../../resultsData'
import {matplotlibScript, pgfplotsSnippet, plotData} from './figureCode'
import {axisScale, buildFigureModel, runStatistics, type ModelData} from './figureModel'
import {defaultFigureSettings, type FigureSettings} from './figureSettings'
import {blendWhite, dashArray, defaultAxis, defaultSpec, formatTick, normalizeSpec, paperContrast, parseSpec, relabelUnit, resolveStyle, serializeSpec, type FigureSpec} from './figureSpec'
import {FigureStudio, type StudioData} from './FigureStudio'
import {provenanceFor} from './runs'
import type {XYRunInput} from './xy'

vi.mock('../../charts', async original => ({...await original<typeof import('../../charts')>(), downloadBlob: vi.fn(), exportChart: vi.fn(async () => {})}))

const at = (index: number) => new Date(index * 15000).toISOString()
const curve = (label: string, unit: string, values: number[], color?: string, runs = 3): AggregateSeries => ({artifactID: label, label, unit, color, stepMilliseconds: 15000, runs, points: values.map((value, index) => ({timestamp: at(index), value})), deviation: values.map((_, index) => ({timestamp: at(index), value: values[index] * .1}))})
const runCurve = (label: string, unit: string, values: number[]): AggregateSeries => ({...curve(label, unit, values, undefined, 1), deviation: undefined})
const offsets = [-1, 0, 2]
const series = {
  'load.p95_response_time': {unit: 'milliseconds', Alpha: [100, 120, 140, 160, 150, 130], Beta: [200, 210, 230, 250, 240, 220]},
  'load.achieved_rps': {unit: 'requests_per_second', Alpha: [1000, 1100, 1200, 1300, 1250, 1150], Beta: [900, 950, 1000, 1050, 1020, 980]}
} as const
type Metric = keyof typeof series
const plot = (metric: string) => metric in series ? (['Alpha', 'Beta'] as const).map(group => {const entry = series[metric as Metric]; return {...curve(group, entry.unit, [...entry[group]]), sloMilliseconds: metric === 'load.p95_response_time' ? 180 : undefined}}) : []
const runs = (metric: string): XYRunInput[] => metric in series ? (['Alpha', 'Beta'] as const).flatMap(group => offsets.map((offset, run) => {const entry = series[metric as Metric]; return {runID: `${group}-${run}`, runLabel: `${group} run ${run + 1}`, seriesID: group, seriesLabel: group, series: runCurve(group, entry.unit, entry[group].map(value => value + offset * value * .05))}})) : []
const data: StudioData = {
  metrics: ['load.p95_response_time', 'load.achieved_rps'], plot, runLevel: [], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: false,
  xy: {metrics: ['load.p95_response_time', 'load.achieved_rps'], level: 'run', provenance: name => provenanceFor([], name), services: () => undefined, runs: ({metric}) => runs(metric)},
  profile: {warmupSeconds: 60, steps: [{label: 'ramp 10→20 req/s', durationSeconds: 30}, {label: '20 req/s', durationSeconds: 30}]}
}
const groups = [{id: 'a', label: 'Alpha', color: '#1f77b4'}, {id: 'b', label: 'Beta', color: '#d55e00'}]

function Studio({storageKey = 'experiment:spec', kind = 'timeseries' as const}: {storageKey?: string; kind?: 'timeseries' | 'xy'}) {
  const [settings, setSettings] = useState<FigureSettings>(defaultFigureSettings)
  return <FigureStudio open onClose={() => {}} request={{kind}} data={data} groups={groups} settings={settings} onSettings={setSettings} storageKey={storageKey} />
}
const studio = () => screen.getByRole('dialog', {name: 'Figure studio'})
const figure = () => studio().querySelector('svg.pub-figure') as SVGSVGElement
const left = () => within(studio().querySelector('.studio-left') as HTMLElement)
const right = () => within(studio().querySelector('.studio-right') as HTMLElement)
const seriesGroup = (key: string) => figure().querySelector(`g.series[data-series-key="${key}"]`) as SVGGElement
const addRight = (metric = 'load.achieved_rps') => {
  fireEvent.change(left().getByLabelText('Add metric'), {target: {value: metric}})
  fireEvent.click(left().getByRole('button', {name: 'Add to right axis'}))
}

beforeEach(() => window.localStorage.clear())
afterEach(() => {cleanup(); vi.mocked(downloadBlob).mockReset(); vi.mocked(exportChart).mockReset(); vi.unstubAllGlobals()})

describe('FigureSpec model', () => {
  it('normalises stored or pasted specs and round-trips through JSON', () => {
    const spec = normalizeSpec({version: 1, type: 'timeseries', runMode: 'spaghetti', palette: 'tol-muted', layout: {grid: '2x2'}, series: {'m|A': {color: '#ABCDEF', marker: 'diamond', lineWidth: 99, opacity: 0, bogus: 1}, 'm|B': {color: 'red'}}, panels: [{metrics: [{metric: 'm', axis: 'right'}, {metric: 3}], left: {range: 'custom', min: 1, max: 5, ticks: 'step', tickValue: -1}, annotations: [{kind: 'hline', value: 5, label: 'SLO'}, {kind: 'xrange', from: 9, to: 3}, {kind: 'text', at: 1}]}]})
    expect(spec.panels).toHaveLength(4)
    expect(spec.panels[0].metrics).toEqual([{metric: 'm', axis: 'right'}])
    expect(spec.series).toEqual({'m|A': {color: '#abcdef', marker: 'diamond', lineWidth: 6, opacity: .1}})
    expect(spec.panels[0].left).toMatchObject({range: 'custom', min: 1, max: 5, ticks: 'step'})
    expect(spec.panels[0].left.tickValue).toBeUndefined()
    expect(spec.panels[0].annotations).toEqual([{id: 'a1', kind: 'hline', axis: 'left', value: 5, label: 'SLO'}, {id: 'a2', kind: 'xrange', from: 3, to: 9}])
    expect(parseSpec(serializeSpec(spec))).toEqual(spec)
    expect(() => parseSpec('nope')).toThrow(/invalid JSON/)
    expect(() => parseSpec('{"version": 2, "type": "timeseries"}')).toThrow(/version 1/)
    expect(normalizeSpec(null)).toEqual(defaultSpec())
  })

  it('gives right-axis series another hue family, hollow markers and dashes by default', () => {
    const spec = defaultSpec()
    const left = resolveStyle({spec, key: 'a', side: 'left', index: 0, count: 1, dual: true, fontSize: 8})
    const right = resolveStyle({spec, key: 'b', side: 'right', index: 0, count: 1, dual: true, fontSize: 8})
    expect(left).toMatchObject({color: '#0072b2', hollow: false, line: 'solid', marker: 'circle'})
    expect(right).toMatchObject({color: '#d55e00', hollow: true, line: 'dashed'})
    expect(resolveStyle({spec: {...spec, series: {b: {color: '#123456', line: 'dotted'}}}, key: 'b', side: 'right', index: 0, count: 1, dual: true, fontSize: 8})).toMatchObject({color: '#123456', line: 'dotted', hollow: true})
    expect(dashArray('dashdot', 1)).toBe('4 1.6 1 1.6')
    expect(blendWhite('#000000', .5)).toBe('#808080')
    expect(paperContrast('#f0e442').ok).toBe(false)
    expect(paperContrast('#0072b2').ok).toBe(true)
  })

  it('formats ticks, converts units and builds custom, stepped, counted and log scales', () => {
    expect(formatTick(12345.678, {decimals: 1, thousands: true})).toBe('12,345.7')
    expect(formatTick(0.05, {thousands: false})).toBe('0.05')
    expect(relabelUnit('P95 latency, client (ms)', 'milliseconds', 's')).toBe('P95 latency, client (s)')
    expect(relabelUnit('CPU usage (cores)', 'cores', 'millicores')).toBe('CPU usage (millicores)')
    expect(axisScale([3, 47], {...defaultAxis('custom'), min: 0, max: 50, ticks: 'step', tickValue: 10})).toMatchObject({lower: 0, upper: 50, ticks: [0, 10, 20, 30, 40, 50]})
    expect(axisScale([3, 47], {...defaultAxis('custom'), min: 0, max: 60, ticks: 'count', tickValue: 4}).ticks).toEqual([0, 20, 40, 60])
    const log = axisScale([3, 470], {...defaultAxis('auto'), log: true})
    expect(log).toMatchObject({lower: 1, upper: 1000, log: true})
    expect(log.ticks).toEqual([1, 2, 5, 10, 20, 50, 100, 200, 500, 1000])
    expect(axisScale([3, 47], defaultAxis('zero')).lower).toBe(0)
  })

  it('summarises repetitions on common windows: mean, CI, median and IQR', () => {
    const stats = runStatistics([runCurve('a', 'ms', [1, 2, 3]), runCurve('b', 'ms', [3, 4, 5]), runCurve('c', 'ms', [5, 6])])!
    expect(stats.t).toEqual([0, 15])
    expect(stats.mean).toEqual([3, 4])
    expect(stats.median).toEqual([3, 4])
    expect(stats.q1).toEqual([2, 3])
    expect(stats.q3).toEqual([4, 5])
    expect(stats.runs).toEqual([[1, 2], [3, 4], [5, 6]])
    expect(stats.ci[0]).toBeCloseTo(2 * 4.302653 / Math.sqrt(3), 4)
  })
})

describe('Spec-driven figure studio', () => {
  it('puts a second metric on a distinct right axis, with axis-grouped legend and real PDF dashes', () => {
    render(<Studio />)
    expect(figure().querySelector('.tick-right')).toBeNull()
    addRight()
    expect(figure().querySelectorAll('.tick-right').length).toBeGreaterThan(2)
    expect(figure().querySelector('.axis-title-right')).toHaveTextContent('Achieved rate (req/s)')
    const leftLine = seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')!
    const rightLine = seriesGroup('load.achieved_rps|Alpha').querySelector('path.series-line')!
    expect(leftLine.getAttribute('stroke')).toBe('#0072b2')
    expect(leftLine.getAttribute('stroke-dasharray')).toBeNull()
    expect(rightLine.getAttribute('stroke')).toBe('#d55e00')
    expect(rightLine.getAttribute('stroke-dasharray')).toMatch(/^[\d.]+ [\d.]+$/)
    expect(seriesGroup('load.achieved_rps|Alpha').querySelector('circle')).toHaveAttribute('fill', '#ffffff')
    expect([...figure().querySelectorAll('.legend-header')].map(item => item.textContent)).toEqual(['P95 latency (left):', 'Achieved request rate (right):'])
    const pdf = new TextDecoder().decode(svgToPDF(figure()))
    expect(pdf).toMatch(/\[[\d. ]+\] 0 d/)
    // One series per axis: axis titles and ticks wear the series colour.
    fireEvent.click(left().getByRole('checkbox', {name: 'Beta'}))
    expect(figure().querySelector('.axis-title-right text')).toHaveAttribute('fill', '#d55e00')
    expect(figure().querySelector('.axis-title-left text')).toHaveAttribute('fill', '#0072b2')
  })

  it('edits one series from its legend entry: colour, marker, line, width, opacity, label and order', () => {
    render(<Studio />)
    fireEvent.click(figure().querySelector('.legend-item[data-series-key="load.p95_response_time|Beta"] text')!)
    const editor = within(left().getByRole('group', {name: 'Style of Beta'}))
    fireEvent.click(editor.getByRole('radio', {name: 'Diamond'}))
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('polygon')).not.toBeNull()
    fireEvent.change(editor.getByLabelText('Hex'), {target: {value: '#f0e442'}})
    expect(editor.getByRole('status')).toHaveTextContent(/below 3:1/)
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')).toHaveAttribute('stroke', '#f0e442')
    fireEvent.click(editor.getByRole('radio', {name: 'Dotted'}))
    fireEvent.change(editor.getByLabelText('Line width (pt)'), {target: {value: '2'}})
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')).toHaveAttribute('stroke-dasharray', '2 3.2')
    fireEvent.change(editor.getByLabelText(/^Opacity/), {target: {value: '50'}})
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')).toHaveAttribute('stroke', blendWhite('#f0e442', .5))
    fireEvent.change(editor.getByLabelText('Label'), {target: {value: 'Raised'}})
    expect(figure().querySelector('.figure-legend')).toHaveTextContent('Raised')
    // Order: Beta first in the legend and drawn first.
    fireEvent.click(left().getByRole('button', {name: 'Move Raised up'}))
    expect([...figure().querySelectorAll('.legend-label')].map(item => item.textContent)).toEqual(['Raised', 'Alpha'])
    expect(figure().querySelector('g.series')).toHaveAttribute('data-series-key', 'load.p95_response_time|Beta')
    fireEvent.click(editor.getByRole('button', {name: 'Reset this series'}))
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')).toHaveAttribute('stroke', '#d55e00')
  })

  it('switches palettes and draws every repeated-run mode', () => {
    render(<Studio />)
    fireEvent.change(right().getByLabelText('Palette'), {target: {value: 'grayscale'}})
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).toHaveAttribute('stroke', '#000000')
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')!.getAttribute('stroke-dasharray')).toBeTruthy()
    const mode = (name: RegExp) => fireEvent.click(left().getByRole('radio', {name}))
    expect(figure().querySelectorAll('.deviation-band path')).toHaveLength(2)
    mode(/^All runs \+ mean/)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelectorAll('path.series-run')).toHaveLength(3)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).not.toBeNull()
    mode(/^Runs only/)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelectorAll('path.series-run')).toHaveLength(3)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).toBeNull()
    mode(/^Median \+ IQR/)
    expect(figure().querySelectorAll('.deviation-band path')).toHaveLength(2)
    expect((within(studio()).getByLabelText('Caption') as HTMLTextAreaElement).value).toMatch(/interquartile range/)
    mode(/^Mean only/)
    expect(figure().querySelector('.deviation-band')).toBeNull()
  })

  it('applies axis ranges, ticks, units, number format, time format and grid', () => {
    render(<Studio />)
    const axes = within(right().getByRole('region', {name: 'Axes'}))
    fireEvent.change(axes.getByLabelText('Unit'), {target: {value: 's'}})
    fireEvent.change(axes.getByLabelText('Decimals'), {target: {value: '2'}})
    expect(figure().querySelector('.axis-title-left')).toHaveTextContent('(s)')
    expect([...figure().querySelectorAll('.tick-left text')].map(item => item.textContent)).toEqual(['0.00', '0.06', '0.12', '0.18', '0.24', '0.30'])
    fireEvent.change(axes.getByLabelText('Unit'), {target: {value: 'ms'}})
    fireEvent.change(axes.getByLabelText('Decimals'), {target: {value: ''}})
    fireEvent.click(axes.getByRole('radio', {name: 'Custom'}))
    fireEvent.change(axes.getByLabelText('Min'), {target: {value: '0'}})
    fireEvent.change(axes.getByLabelText('Max'), {target: {value: '400'}})
    fireEvent.change(axes.getByLabelText('Ticks'), {target: {value: 'step'}})
    fireEvent.change(axes.getByLabelText('Tick step'), {target: {value: '100'}})
    expect([...figure().querySelectorAll('.tick-left text')].map(item => item.textContent)).toEqual(['0', '100', '200', '300', '400'])
    fireEvent.click(axes.getByRole('radio', {name: 'X'}))
    fireEvent.change(axes.getByLabelText('Time as'), {target: {value: 'mmss'}})
    expect([...figure().querySelectorAll('.tick-x text')].map(item => item.textContent)).toContain('0:30')
    expect(figure()).toHaveTextContent('Time (mm:ss)')
    fireEvent.click(axes.getByRole('radio', {name: 'Major + minor'}))
    expect(figure().querySelectorAll('.grid-minor').length).toBeGreaterThan(0)
    fireEvent.click(axes.getByRole('radio', {name: 'None'}))
    expect(figure().querySelector('.grid-major')).toBeNull()
  })

  it('draws automatic and user annotations', () => {
    render(<Studio />)
    expect(figure().querySelector('.annotation-phase')).toHaveTextContent('20 req/s')
    expect(figure().querySelector('.annotation-slo')).toHaveTextContent('SLO 180.0 ms')
    fireEvent.click(left().getByRole('checkbox', {name: 'Warm-up end'}))
    expect(figure().querySelector('.annotation-warmup')).toHaveTextContent('after 60 s warm-up')
    fireEvent.click(left().getByRole('button', {name: '+ Reference line'}))
    fireEvent.change(left().getByLabelText('Value (axis units)'), {target: {value: '120'}})
    expect(figure().querySelector('.annotation-user.annotation-hline')).toHaveTextContent('Reference')
    fireEvent.click(left().getByRole('button', {name: '+ Shaded range'}))
    expect(figure().querySelector('.annotation-xrange rect')).toHaveAttribute('fill-opacity', '0.07')
    fireEvent.click(left().getByRole('button', {name: '+ Text'}))
    fireEvent.change(left().getByLabelText('Text'), {target: {value: 'GC pause'}})
    expect(figure().querySelector('.annotation-text')).toHaveTextContent('GC pause')
    fireEvent.click(left().getByRole('checkbox', {name: 'Load-phase boundaries'}))
    expect(figure().querySelector('.annotation-phase')).toBeNull()
    fireEvent.click(left().getByRole('button', {name: 'Remove reference line 1'}))
    expect(figure().querySelector('.annotation-user.annotation-hline')).toBeNull()
  })

  it('lays out 2 × 2 panels with letters, shared X and one legend', () => {
    render(<Studio />)
    fireEvent.click(left().getByRole('radio', {name: '2 × 2'}))
    expect(figure().querySelectorAll('.figure-panel')).toHaveLength(4)
    expect([...figure().querySelectorAll('.panel-letter')].map(item => item.textContent)).toEqual(['(a)', '(b)', '(c)', '(d)'])
    expect(figure().querySelectorAll('.figure-legend')).toHaveLength(1)
    expect(figure().querySelectorAll('.legend-item')).toHaveLength(2)
    // Shared X: tick labels only on the bottom row.
    expect(figure().querySelector('[data-panel="1"] .tick-x text')).toBeNull()
    expect(figure().querySelector('[data-panel="3"] .tick-x text')).not.toBeNull()
    expect((within(studio()).getByLabelText('Caption') as HTMLTextAreaElement).value).toMatch(/^\(a\) P95 latency/)
  })

  it('remembers the spec per experiment and copies / pastes it as JSON', async () => {
    let clipboard = ''
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText: async (value: string) => {clipboard = value}, readText: async () => clipboard}})
    const view = render(<Studio />)
    addRight()
    fireEvent.click(left().getByRole('radio', {name: /^All runs \+ mean/}))
    view.unmount()
    render(<Studio />)
    expect(figure().querySelectorAll('.tick-right').length).toBeGreaterThan(0)
    expect(figure().querySelectorAll('path.series-run').length).toBeGreaterThan(0)
    fireEvent.click(right().getByRole('button', {name: 'Copy spec'}))
    await vi.waitFor(() => expect(clipboard).toContain('"runMode": "spaghetti"'))
    const copied = JSON.parse(clipboard) as FigureSpec
    expect(copied.panels[0].metrics).toEqual([{metric: 'load.p95_response_time', axis: 'left'}, {metric: 'load.achieved_rps', axis: 'right'}])
    expect(copied.style.size).toBe('single')
    cleanup()
    render(<Studio storageKey="experiment:other" />)
    expect(figure().querySelector('.tick-right')).toBeNull()
    fireEvent.click(right().getByRole('button', {name: 'Paste spec'}))
    await vi.waitFor(() => expect(figure().querySelectorAll('.tick-right').length).toBeGreaterThan(0))
    expect(figure().querySelectorAll('path.series-run').length).toBeGreaterThan(0)
    fireEvent.click(right().getByRole('button', {name: 'Paste as text…'}))
    fireEvent.change(right().getByLabelText('Figure spec (JSON)'), {target: {value: '{"oops"'}})
    fireEvent.click(right().getByRole('button', {name: 'Apply spec'}))
    expect(await within(studio()).findByText(/invalid JSON/)).toBeInTheDocument()
  })

  it('saves style presets and applies built-in ones', () => {
    render(<Studio />)
    fireEvent.change(right().getByLabelText('Style preset'), {target: {value: 'Slides'}})
    fireEvent.click(right().getByRole('button', {name: 'Apply'}))
    expect(figure().getAttribute('viewBox')).toMatch(/^0 0 720 /)
    fireEvent.change(right().getByLabelText('Preset name'), {target: {value: 'My talk'}})
    fireEvent.click(right().getByRole('button', {name: 'Save current style as preset'}))
    expect(JSON.parse(window.localStorage.getItem('kubephos.figurePresets.v1')!)).toEqual([expect.objectContaining({name: 'My talk', strokeScale: 1.8})])
  })

  it('exports a matplotlib script and a pgfplots snippet with their CSV', async () => {
    render(<Studio />)
    addRight()
    for (const [label, script, type] of [[/Python/, 'kubephos-p95-latency-client-locust-and-achieved-request-rate.py', 'text/x-python'], [/pgfplots/, 'kubephos-p95-latency-client-locust-and-achieved-request-rate.tex', 'text/x-tex']] as const) {
      vi.mocked(downloadBlob).mockReset()
      fireEvent.click(within(studio()).getByRole('button', {name: 'More export formats'}))
      fireEvent.click(within(studio()).getByRole('menuitem', {name: label}))
      await vi.waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(2))
      const [[name, blob], [csvName, csv]] = vi.mocked(downloadBlob).mock.calls
      expect(name).toBe(script)
      expect((blob as Blob).type).toContain(type)
      expect(csvName).toBe('kubephos-p95-latency-client-locust-and-achieved-request-rate-plot-data.csv')
      expect((await (csv as Blob).text()).split('\n')[0]).toMatch(/^p1s1_x,p1s1_y,p1s1_lo,p1s1_hi/)
      expect(await (blob as Blob).text()).toContain(csvName)
    }
  })
})

describe('code exports', () => {
  const model = (spec: FigureSpec, extra: Partial<ModelData> = {}) => buildFigureModel(spec, {plot, runs, provenance: name => provenanceFor([], name), slo: 180, ...extra}, {fontSize: 8})
  const dualSpec = (): FigureSpec => {
    const spec = defaultSpec()
    spec.panels[0].metrics = [{metric: 'load.p95_response_time', axis: 'left'}, {metric: 'load.achieved_rps', axis: 'right'}]
    spec.panels[0].annotations = [{id: 'r', kind: 'xrange', from: 15, to: 30, label: 'Spike'}]
    spec.series['load.p95_response_time|Beta'] = {marker: 'diamond', color: '#117733'}
    return spec
  }
  const options = {base: 'fig-1', title: 'Latency and rate', caption: 'Caption.', settings: defaultFigureSettings, width: 252, height: 210, grid: 'major' as const}

  it('writes a self-contained matplotlib script with size, fonts, styles, twin axis and annotations', () => {
    const out = matplotlibScript(model(dualSpec()), options)
    expect(out.scriptName).toBe('fig-1.py')
    expect(out.script).toContain('DATA = Path(__file__).with_name("fig-1-plot-data.csv")')
    expect(out.script).toContain('WIDTH_IN, HEIGHT_IN = 3.5, 2.917')
    expect(out.script).toContain('"font.family": "serif"')
    expect(out.script).toContain('ax1r = ax1.twinx()')
    expect(out.script).toMatch(/ax1\.plot\(x, col\("p1s\d_y"\), color="#117733", .*marker="D"/)
    expect(out.script).toMatch(/ax1r\.plot\(x, col\("p1s\d_y"\), color="#d55e00", .*ls=\(0, \(4, 2\.\d+\)\), marker="o", .*mfc="white"/)
    expect(out.script).toContain('ax1.axvspan(0.25, 0.5, color="black", alpha=0.07')
    expect(out.script).toContain('ax1.axhline(180')
    expect(out.script).toContain('"P95 latency (left):"')
    expect(out.csv.split('\n')[0].split(',')).toContain('p1s1_x')
  })

  it('writes a pgfplots snippet with colours, marks, dashes, the right axis and the data file', () => {
    const out = pgfplotsSnippet(model(dualSpec()), options)
    expect(out.texName).toBe('fig-1.tex')
    expect(out.tex).toContain('\\definecolor{kp117733}{HTML}{117733}')
    expect(out.tex).toContain('{fig-1-plot-data.csv}')
    expect(out.tex).toContain('mark=diamond*')
    expect(out.tex).toContain('mark=o')
    expect(out.tex).toContain('dash pattern=on 1.28pt off 0.71pt'.slice(0, 15))
    expect(out.tex).toContain('axis y line*=right')
    expect(out.tex).toContain('\\addlegendentry{P95 latency (left):}')
    expect(out.tex).toMatch(/\\fill\[black, fill opacity=0\.07\]/)
    expect(out.tex).toContain('\\fontsize{8}')
  })

  it('pads the wide CSV with nan and keeps the band outline per segment', () => {
    const {csv, columns} = plotData(model(dualSpec()))
    const [header, ...rows] = csv.trim().split('\n')
    expect(header.split(',')).toEqual(expect.arrayContaining(['p1s1_band1_x', 'p1s1_band1_y']))
    expect(rows.length).toBe(12)
    expect(rows.at(-1)).toContain('nan')
    expect(columns.map(item => item.series.axis)).toEqual(['left', 'left', 'right', 'right'])
  })
})
