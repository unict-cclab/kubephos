import {cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import {useState} from 'react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {downloadBlob, exportChart, svgToPDF} from '../../../charts'
import type {AggregateSeries} from '../../../resultsData'
import {matplotlibScript, pgfplotsSnippet, plotData} from './figureCode'
import {axisScale, buildFigureModel, runStatistics, type ModelData} from './figureModel'
import {defaultFigureSettings, type FigureSettings} from '../figureSettings'
import {blendWhite, dashArray, defaultAxis, defaultSpec, formatTick, normalizeSpec, paperContrast, parseSpec, relabelUnit, resolveStyle, serializeSpec, type FigureSpec} from './figureSpec'
import {KubeStudio} from './KubeStudio'
import type {StudioData} from './image'
import {provenanceFor} from '../runs'
import type {XYRunInput} from '../xy'

vi.mock('../../../charts', async original => ({...await original<typeof import('../../../charts')>(), downloadBlob: vi.fn(), exportChart: vi.fn(async () => {})}))

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
  return <KubeStudio open onClose={() => {}} request={{kind}} data={data} groups={groups} settings={settings} onSettings={setSettings} storageKey={storageKey} />
}
const studio = () => screen.getByRole('dialog', {name: 'KubeStudio'})
const figure = () => studio().querySelector('svg.pub-figure') as SVGSVGElement
const layers = () => within(within(studio()).getByRole('navigation', {name: 'Layers'}))
const right = () => within(studio().querySelector('.ks-right') as HTMLElement)
const layer = (name: string | RegExp) => fireEvent.click(layers().getByRole('button', {name}))
const chartProps = () => fireEvent.click(layers().getAllByRole('button', {name: /^Chart/})[0])
const expand = (name: string) => {const toggle = right().getByRole('button', {name}); if (toggle.getAttribute('aria-expanded') !== 'true') fireEvent.click(toggle)}
const seriesGroup = (key: string) => figure().querySelector(`g.series[data-series-key="${key}"]`) as SVGGElement
// The caption exports carry when the user wrote none: the charts' own summaries.
const exportedCaption = async () => {
  vi.mocked(exportChart).mockClear()
  fireEvent.click(within(studio()).getByRole('button', {name: 'Export'}))
  fireEvent.click(within(within(studio()).getByRole('dialog', {name: 'Export figure'})).getByRole('button', {name: 'Download PDF'}))
  await vi.waitFor(() => expect(exportChart).toHaveBeenCalled())
  return vi.mocked(exportChart).mock.calls[0][4]?.caption ?? ''
}
// Axes are selected on the figure: a click on their ticks.
const axis = (side: 'x' | 'left' | 'right') => fireEvent.click(figure().querySelector(`.tick-${side} text`)!)
const addRight = (metric = 'load.achieved_rps') => {
  axis('left')
  fireEvent.change(right().getByLabelText('Add a metric on a right Y axis'), {target: {value: metric}})
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

describe('Spec-driven KubeStudio figures', () => {
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
    // Lines carry no decorative markers: the dash tells the axes apart.
    expect(seriesGroup('load.achieved_rps|Alpha').querySelector('circle')).toBeNull()
    expect([...figure().querySelectorAll('.legend-header')].map(item => item.textContent)).toEqual(['P95 latency (left):', 'Achieved request rate (right):'])
    const pdf = new TextDecoder().decode(svgToPDF(figure()))
    expect(pdf).toMatch(/\[[\d. ]+\] 0 d/)
    // One series per axis: axis titles and ticks wear the series colour.
    fireEvent.click(layers().getAllByRole('button', {name: 'Hide Beta'})[0])
    expect(figure().querySelector('.axis-title-right text')).toHaveAttribute('fill', '#d55e00')
    expect(figure().querySelector('.axis-title-left text')).toHaveAttribute('fill', '#0072b2')
  })

  it('edits one series from its legend entry: colour, line, width, opacity, label and order', () => {
    render(<Studio />)
    fireEvent.click(figure().querySelector('.legend-item[data-series-key="load.p95_response_time|Beta"] text')!)
    // Clicking the legend entry selects the series: its style opens in the inspector.
    expect(layers().getByRole('button', {name: 'Beta'})).toHaveAttribute('aria-current', 'true')
    const editor = within(right().getByRole('group', {name: 'Style of Beta'}))
    // Points are a scatter setting: a line series has none to edit.
    expect(editor.queryByRole('radio', {name: 'Diamond'})).not.toBeInTheDocument()
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
    fireEvent.click(editor.getByRole('button', {name: 'Move up'}))
    expect([...figure().querySelectorAll('.legend-label')].map(item => item.textContent)).toEqual(['Raised', 'Alpha'])
    expect(figure().querySelector('g.series')).toHaveAttribute('data-series-key', 'load.p95_response_time|Beta')
    fireEvent.click(editor.getByRole('button', {name: 'Reset'}))
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')).toHaveAttribute('stroke', '#d55e00')
  })

  it('switches palettes and draws every repeated-run mode', async () => {
    render(<Studio />)
    chartProps()
    fireEvent.change(right().getByLabelText('Palette'), {target: {value: 'grayscale'}})
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).toHaveAttribute('stroke', '#000000')
    expect(seriesGroup('load.p95_response_time|Beta').querySelector('path.series-line')!.getAttribute('stroke-dasharray')).toBeTruthy()
    const mode = (value: string) => fireEvent.change(right().getByLabelText('Repeated runs'), {target: {value}})
    expect(figure().querySelectorAll('.deviation-band path')).toHaveLength(2)
    mode('spaghetti')
    expect(seriesGroup('load.p95_response_time|Alpha').querySelectorAll('path.series-run')).toHaveLength(3)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).not.toBeNull()
    mode('runs')
    expect(seriesGroup('load.p95_response_time|Alpha').querySelectorAll('path.series-run')).toHaveLength(3)
    expect(seriesGroup('load.p95_response_time|Alpha').querySelector('path.series-line')).toBeNull()
    mode('iqr')
    expect(figure().querySelectorAll('.deviation-band path')).toHaveLength(2)
    expect(await exportedCaption()).toMatch(/interquartile range/)
    mode('mean')
    expect(figure().querySelector('.deviation-band')).toBeNull()
  })

  it('applies axis ranges, ticks, units, number format, time format and grid', () => {
    render(<Studio />)
    axis('left')
    let axes = right()
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
    axis('x')
    axes = right()
    fireEvent.change(axes.getByLabelText('Time as'), {target: {value: 'mmss'}})
    expect([...figure().querySelectorAll('.tick-x text')].map(item => item.textContent)).toContain('0:30')
    expect(figure()).toHaveTextContent('Time (mm:ss)')
    chartProps()
    axes = within(right().getByRole('group', {name: 'Grid lines'}))
    fireEvent.click(axes.getByRole('radio', {name: 'Minor'}))
    expect(figure().querySelectorAll('.grid-minor').length).toBeGreaterThan(0)
    fireEvent.click(axes.getByRole('radio', {name: 'None'}))
    expect(figure().querySelector('.grid-major')).toBeNull()
  })

  it('draws automatic and user annotations', () => {
    render(<Studio />)
    expect(figure().querySelector('.annotation-phase')).toHaveTextContent('20 req/s')
    expect(figure().querySelector('.annotation-slo')).toHaveTextContent('SLO 180 ms')
    fireEvent.click(layers().getByRole('button', {name: 'Show warm-up end'}))
    expect(figure().querySelector('.annotation-warmup')).toHaveTextContent('after 60 s warm-up')
    const add = (name: string) => {fireEvent.click(layers().getByRole('button', {name: 'Add a line in the graph'})); fireEvent.click(within(layers().getByRole('group', {name: 'Line to add'})).getByRole('button', {name: new RegExp(`^${name}`)}))}
    fireEvent.click(layers().getByRole('button', {name: 'Add a line in the graph'}))
    // Each kind says what it is for, with an example.
    expect(within(layers().getByRole('group', {name: 'Line to add'})).getByRole('button', {name: /^Threshold line.*SLO/})).toBeInTheDocument()
    fireEvent.click(layers().getByRole('button', {name: 'Add a line in the graph'}))
    add('Threshold line')
    fireEvent.change(right().getByLabelText('Value (ms)'), {target: {value: '120'}})
    expect(figure().querySelector('.annotation-user.annotation-hline')).toHaveTextContent('Threshold 120 ms')
    add('Shaded range')
    expect(figure().querySelector('.annotation-xrange rect')).toHaveAttribute('fill-opacity', '0.07')
    add('Text label')
    fireEvent.change(right().getByLabelText('Text'), {target: {value: 'GC pause'}})
    expect(figure().querySelector('.annotation-text')).toHaveTextContent('GC pause')
    fireEvent.click(layers().getByRole('button', {name: 'Hide load phases'}))
    expect(figure().querySelector('.annotation-phase')).toBeNull()
    layer('Threshold line')
    fireEvent.click(right().getByRole('button', {name: 'Delete threshold line'}))
    expect(figure().querySelector('.annotation-user.annotation-hline')).toBeNull()
  })

  it('still draws saved charts with 2 × 2 panels inside: letters, shared X and one legend', async () => {
    window.localStorage.setItem('kubephos.figureStudio.v1:experiment:spec', JSON.stringify({charts: [{id: 'c1', kind: 'timeseries', metric: 'load.p95_response_time', spec: {version: 1, type: 'timeseries', layout: {grid: '2x2'}, panels: [{metrics: [{metric: 'load.p95_response_time', axis: 'left'}]}, {metrics: [{metric: 'load.achieved_rps', axis: 'left'}]}]}}]}))
    render(<Studio />)
    expect(figure().querySelectorAll('.figure-panel')).toHaveLength(4)
    expect([...figure().querySelectorAll('.panel-letter')].map(item => item.textContent)).toEqual(['(a)', '(b)', '(c)', '(d)'])
    expect(figure().querySelectorAll('.figure-legend')).toHaveLength(1)
    expect(figure().querySelectorAll('.legend-item')).toHaveLength(2)
    // Shared X: tick labels only on the bottom row.
    expect(figure().querySelector('[data-panel="1"] .tick-x text')).toBeNull()
    expect(figure().querySelector('[data-panel="3"] .tick-x text')).not.toBeNull()
    expect(await exportedCaption()).toMatch(/^\(a\) P95 latency/)
  })

  it('remembers the spec per experiment and copies / pastes it as JSON', async () => {
    let clipboard = ''
    vi.stubGlobal('navigator', {...navigator, clipboard: {writeText: async (value: string) => {clipboard = value}, readText: async () => clipboard}})
    const view = render(<Studio />)
    addRight()
    chartProps()
    fireEvent.change(right().getByLabelText('Repeated runs'), {target: {value: 'spaghetti'}})
    view.unmount()
    render(<Studio />)
    expect(figure().querySelectorAll('.tick-right').length).toBeGreaterThan(0)
    expect(figure().querySelectorAll('path.series-run').length).toBeGreaterThan(0)
    chartProps()
    expand('Presets and spec')
    fireEvent.click(right().getByRole('button', {name: 'Copy spec'}))
    await vi.waitFor(() => expect(clipboard).toContain('"runMode": "spaghetti"'))
    const copied = JSON.parse(clipboard) as FigureSpec
    expect(copied.panels[0].metrics).toEqual([{metric: 'load.p95_response_time', axis: 'left'}, {metric: 'load.achieved_rps', axis: 'right'}])
    expect(copied.style.size).toBe('single')
    cleanup()
    render(<Studio storageKey="experiment:other" />)
    expect(figure().querySelector('.tick-right')).toBeNull()
    chartProps()
    expand('Presets and spec')
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
    chartProps()
    expand('Presets and spec')
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
      fireEvent.click(within(studio()).getByRole('button', {name: 'Export'}))
      const popover = within(within(studio()).getByRole('dialog', {name: 'Export figure'}))
      fireEvent.click(popover.getByRole('radio', {name: label}))
      expect(popover.getByText(/\.(py|tex) \+ .*-plot-data\.csv$/)).toBeInTheDocument()
      fireEvent.click(popover.getByRole('button', {name: /^Download/}))
      await vi.waitFor(() => expect(downloadBlob).toHaveBeenCalledTimes(2))
      const [[name, blob], [csvName, csv]] = vi.mocked(downloadBlob).mock.calls
      expect(name).toBe(script)
      expect((blob as Blob).type).toContain(type)
      expect(csvName).toBe('kubephos-p95-latency-client-locust-and-achieved-request-rate-plot-data.csv')
      expect((await (csv as Blob).text()).split('\n')[0]).toMatch(/^p1s1_x,p1s1_y,p1s1_lo,p1s1_hi/)
      expect(await (blob as Blob).text()).toContain(csvName)
    }
  })

  it('changes the SLO threshold from its layer; the label follows', async () => {
    render(<Studio />)
    expect(figure().querySelector('.annotation-slo')).toHaveTextContent('SLO 180 ms')
    layer('SLO line')
    fireEvent.change(right().getByLabelText('Threshold (ms)'), {target: {value: '200'}})
    expect(figure().querySelector('.annotation-slo')).toHaveTextContent('SLO 200 ms')
    expect(await exportedCaption()).toMatch(/SLO of 200\.0 ms/)
    fireEvent.click(right().getByRole('button', {name: 'Use the experiment SLO'}))
    expect(figure().querySelector('.annotation-slo')).toHaveTextContent('SLO 180 ms')
  })

  it('adds charts to one image with letters and a layout picker, and exports them as one SVG', async () => {
    render(<Studio />)
    // Add chart is guided: its type first, then X and Y on the right.
    const add = () => {
      fireEvent.click(layers().getByRole('button', {name: 'Add chart'}))
      fireEvent.click(within(studio()).getByRole('dialog', {name: /^Add chart \([b-d]\): choose its type$/}).querySelector('[data-form="timeseries"]')!)
    }
    add()
    // Chart (b) gets the next suggested metric; the page widens to double column.
    expect(layers().getByRole('button', {name: 'Chart (b): Achieved request rate'})).toHaveAttribute('aria-current', 'true')
    expect(studio().querySelectorAll('svg.pub-figure')).toHaveLength(2)
    expect([...studio().querySelectorAll('.ks-letter')].map(item => item.textContent)).toEqual(['(a)', '(b)'])
    layer('Page')
    expect(within(right().getByRole('group', {name: 'Layout'})).getAllByRole('radio').map(radio => radio.closest('label')?.textContent)).toEqual(['1 × 2', '2 × 1'])
    add()
    add()
    expect(studio().querySelectorAll('svg.pub-figure')).toHaveLength(4)
    expect(layers().queryByRole('button', {name: 'Add chart'})).not.toBeInTheDocument()
    layer('Page')
    expect(within(right().getByRole('group', {name: 'Layout'})).getByRole('radio', {name: '2 × 2'})).toBeChecked()
    // Each chart has its own form: chart (d) becomes a scatter.
    fireEvent.click(layers().getByRole('button', {name: /^Chart \(d\)/}))
    fireEvent.click(within(right().getByRole('group', {name: 'Chart form'})).getByRole('radio', {name: 'Scatter X–Y'}))
    expect(within(studio()).getByRole('img', {name: /^Scatter of/})).toBeInTheDocument()
    fireEvent.change(right().getByLabelText('Y'), {target: {value: 'load.p95_response_time'}})
    // Export: one composed SVG with each chart translated into its cell and the letters.
    let composed = ''
    vi.mocked(exportChart).mockImplementation(async id => {const svg = document.getElementById(id)!; composed = svg.outerHTML})
    fireEvent.click(within(studio()).getByRole('button', {name: 'Export'}))
    fireEvent.click(within(within(studio()).getByRole('dialog', {name: 'Export figure'})).getByRole('button', {name: 'Download PDF'}))
    await vi.waitFor(() => expect(composed).not.toBe(''))
    expect(composed.match(/<g transform="translate\(/g)?.length).toBeGreaterThanOrEqual(4)
    expect(composed).toContain('>(d)</text>')
    expect(composed).toMatch(/viewBox="0 0 515.52 /)
    // Right-click a chart to delete it.
    fireEvent.contextMenu(studio().querySelectorAll('.ks-cell')[3])
    fireEvent.click(within(studio()).getByRole('menuitem', {name: /^Delete chart/}))
    expect(studio().querySelectorAll('svg.pub-figure')).toHaveLength(3)
  })

  it('makes a custom scatter from the chart switcher and keeps recipes to one chart', () => {
    window.localStorage.setItem('kubephos.figureStudio.v1:experiment:spec', JSON.stringify({charts: [{id: 'c1', kind: 'timeseries', metric: '', spec: {version: 1, type: 'timeseries', layout: {grid: '2x2'}, panels: [{metrics: []}]}}]}))
    render(<Studio />)
    expect(figure().querySelectorAll('.figure-panel')).toHaveLength(4)
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    fireEvent.click(within(studio()).getByRole('dialog', {name: 'Choose a figure'}).querySelector('[data-kind="timeseries"]')!)
    expect(figure().querySelectorAll('.figure-panel')).toHaveLength(1)
    fireEvent.click(within(studio()).getByRole('button', {name: /^Figure:/}))
    fireEvent.click(within(studio()).getByRole('dialog', {name: 'Choose a figure'}).querySelector('[data-form="xy"]')!)
    expect(within(studio()).getByRole('img', {name: /^Scatter of/})).toBeInTheDocument()
    fireEvent.change(right().getByLabelText('X'), {target: {value: 'load.achieved_rps'}})
    fireEvent.change(right().getByLabelText('Y'), {target: {value: 'load.p95_response_time'}})
    expect(within(studio()).getByRole('img', {name: 'Scatter of P95 latency — client (Locust) vs Achieved request rate'})).toBeInTheDocument()
  })
})

describe('PDF writer', () => {
  it('honours group translations (legends drawn above the plot by other figure types)', () => {
    const host = document.createElement('div')
    host.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50" data-export-size="physical"><g transform="translate(0 12)"><line x1="0" y1="0" x2="10" y2="0" stroke="#000" /></g><line x1="0" y1="0" x2="10" y2="0" stroke="#000" /></svg>'
    const pdf = new TextDecoder().decode(svgToPDF(host.querySelector('svg')!))
    expect(pdf).toContain('q 1 0 0 1 0.000 -12.000 cm')
    expect(pdf.match(/ cm\n/g)).toHaveLength(1)
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
    // Line series draw no markers, as on screen.
    expect(out.script).toMatch(/ax1\.plot\(x, col\("p1s\d_y"\), color="#117733", .*marker="None"/)
    expect(out.script).toMatch(/ax1r\.plot\(x, col\("p1s\d_y"\), color="#d55e00", .*ls=\(0, \(4, 2\.\d+\)\), marker="None"/)
    expect(out.script).toContain('ax1.axvspan(0.25, 0.5, color="black", alpha=0.07')
    expect(out.script).toContain('ax1.axhline(180')
    expect(out.script).toContain('"P95 latency (left):"')
    expect(out.csv.split('\n')[0].split(',')).toContain('p1s1_x')
  })

  it('writes a pgfplots snippet with colours, dashes, the right axis and the data file', () => {
    const out = pgfplotsSnippet(model(dualSpec()), options)
    expect(out.texName).toBe('fig-1.tex')
    expect(out.tex).toContain('\\definecolor{kp117733}{HTML}{117733}')
    expect(out.tex).toContain('{fig-1-plot-data.csv}')
    expect(out.tex).not.toContain('mark=')
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
