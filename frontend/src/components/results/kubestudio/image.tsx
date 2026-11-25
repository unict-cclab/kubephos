import {useMemo, useRef, useState, type ReactNode} from 'react'
import {exportChart, type ChartExportFormat, type ChartExportOptions} from '../../../charts'
import type {AggregateSeries, RunLevelMetric} from '../../../resultsData'
import {figureMetrics, seriesStyle, type FigureHost, type FigureReport} from '../chartKit'
import {PercentileLadder, RunDotPlot, ServiceFigure, TimeSeriesFigure, type ServiceRow} from '../Figures'
import {figureSize, fontStacks, type FigureSettings} from '../figureSettings'
import type {FidelitySeries} from '../loadFidelity'
import {metricTitle} from '../format'
import type {OverviewGroup} from '../overview'
import type {LadderSeries} from '../percentiles'
import type {ResolvedProvenance} from '../runs'
import {defaultXYOptions, defaultXYSelection, type XYSource, type XYState} from '../xy'
import {buildFigureModel, type FigureModel, type LoadProfile} from './figureModel'
import {defaultSpec, type FigureSpec} from './figureSpec'
import {cellBoxes, cellSettings, composeFigure, letters, validLayout, type ChartLayout, type ChartState} from './layout'
import {kindOrder, runLevelExcluded, timeSeriesExcluded} from './recipes'
import {SpecFigure, type FigureGeometry} from './SpecFigure'

// A KubeStudio image: 1–4 charts laid out on one page. This module turns an image
// (its charts, layout, hidden series) and the experiment's data into drawn figures;
// the editor (KubeStudio) and the figures kept on the results page both use it.

export type FigureKind = 'timeseries' | 'xy' | 'runlevel' | 'ladder' | 'fidelity' | 'services' | 'placement'

// What the results page hands to KubeStudio and to the saved figures.
export interface StudioData {
  metrics: string[]
  plot: (metric: string) => AggregateSeries[]
  runLevel: RunLevelMetric[]
  ladder: LadderSeries[]
  fidelity: FidelitySeries[]
  services: (metric: string) => {rows: ServiceRow[]} | undefined
  serviceMetrics: string[]
  provenance: (metric: string) => ResolvedProvenance
  omitSLO: boolean
  runLabels?: Record<string, string>
  placement?: ReactNode
  xy?: XYSource
  // Load profile (warm-up, phases) for automatic annotations.
  profile?: LoadProfile
}

export interface ImageState {charts: ChartState[]; layout?: ChartLayout; sharedLegend: boolean; hidden: string[]}

// One chart of the image with everything derived from its state.
export interface ChartView {
  chart: ChartState
  index: number
  letter?: string
  kind: FigureKind | undefined
  metricOptions: string[]
  metric: string
  xy: XYState
  spec?: FigureSpec
  model?: FigureModel
  settings: FigureSettings
  box: {x: number; width: number; row: number}
}

type Report = Omit<FigureReport, 'csv'>

export const defaultXYState = (data: StudioData | undefined): XYState => ({...defaultXYSelection(data?.xy?.metrics ?? []), options: defaultXYOptions})

export const timeMetrics = (data: StudioData) => data.metrics.filter(item => !timeSeriesExcluded(item) && data.plot(item).length)

// The chart kinds these results can draw, in switcher order.
export function availableKinds(data: StudioData | undefined): FigureKind[] {
  if (!data) return []
  const kinds = new Set<FigureKind>()
  if (data.fidelity.length) kinds.add('fidelity')
  if (timeMetrics(data).length) kinds.add('timeseries')
  if ((data.xy?.metrics.length ?? 0) > 1) kinds.add('xy')
  if (data.runLevel.some(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length))) kinds.add('runlevel')
  if (data.ladder.length) kinds.add('ladder')
  if (data.serviceMetrics.length) kinds.add('services')
  if (data.placement) kinds.add('placement')
  return kindOrder.filter(item => kinds.has(item))
}

export function metricOptions(data: StudioData | undefined, kind: FigureKind | undefined): string[] {
  if (!data) return []
  if (kind === 'timeseries') return timeMetrics(data)
  if (kind === 'runlevel') return data.runLevel.filter(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length)).map(entry => entry.metric)
  if (kind === 'services') return data.serviceMetrics
  return []
}

// Every chart with its resolved type, data, spec and model.
export function buildViews({image, data, groups, settings, available}: {image: ImageState; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; available: FigureKind[]}): ChartView[] {
  const {charts, sharedLegend, hidden} = image
  const boxes = cellBoxes(charts.length, validLayout(charts.length, image.layout))
  return charts.map((chart, index) => {
    const kind = chart.kind && available.includes(chart.kind) && !(chart.kind === 'placement' && charts.length > 1) ? chart.kind : available.includes('timeseries') ? 'timeseries' : available[0]
    const options = metricOptions(data, kind)
    const metric = options.includes(chart.metric) ? chart.metric : options.includes('load.p95_response_time') ? 'load.p95_response_time' : options[0] ?? ''
    const xy = data?.xy && chart.xy.x.metric && data.xy.metrics.includes(chart.xy.x.metric) && data.xy.metrics.includes(chart.xy.y.metric) ? chart.xy : defaultXYState(data)
    const box = boxes[index] ?? boxes[0]
    const cell = cellSettings(settings, charts.length, box.width, sharedLegend && index > 0)
    const overrides = chart.overrides ?? {}
    let spec: FigureSpec | undefined
    if (kind === 'timeseries' || kind === 'xy') {
      const stored = chart.spec?.type === kind ? chart.spec : defaultSpec(kind)
      const panels = stored.panels.map((panel, position) => {
        if (position) return panel
        if (stored.type === 'xy') return {...panel, xy: {x: xy.x, y: xy.y, fit: xy.options.fit, path: xy.options.path}, x: {...panel.x, log: xy.options.logX}, left: {...panel.left, log: xy.options.logY}}
        const [first, ...rest] = panel.metrics
        return {...panel, metrics: [{metric, axis: first?.axis ?? 'left' as const}, ...rest]}
      })
      spec = {...stored, panels, style: cell, labels: overrides}
    }
    let model: FigureModel | undefined
    if (spec && data && (spec.type !== 'xy' || data.xy)) {
      const slo = data.omitSLO ? undefined : data.plot('load.p95_response_time').find(item => item.sloMilliseconds)?.sloMilliseconds
      const runs = data.xy?.level === 'run' ? (name: string) => data.xy!.runs({metric: name}) : undefined
      model = buildFigureModel(spec, {plot: data.plot, runs, provenance: data.provenance, slo, profile: data.profile, xy: spec.type === 'xy' && data.xy ? {source: data.xy, x: xy.x, y: xy.y, options: xy.options} : undefined}, {fontSize: figureMetrics(cell).fontSize, hidden: label => groups.length > 1 && hidden.includes(label), labels: overrides})
    }
    return {chart, index, letter: charts.length > 1 ? `(${letters[index]})` : undefined, kind, metricOptions: options, metric, xy, spec, model, settings: cell, box}
  })
}

export const emptyState = (message: string, actions?: ReactNode) => <div className="ks-empty" role="status"><p>{message}</p>{actions && <div className="ks-actions">{actions}</div>}</div>

// Editor-only ways out of an empty chart.
interface ChartActions {showAllSeries?: () => void; suggestPair?: (view: ChartView) => void; metricPicker?: (view: ChartView) => ReactNode}

// The drawn chart and whether it is a publication figure (selectable, exportable).
export function chartContent(view: ChartView, {data, groups, hidden, idPrefix = '', geometry, actions = {}}: {data: StudioData | undefined; groups: OverviewGroup[]; hidden: string[]; idPrefix?: string; geometry?: {current: FigureGeometry | undefined}; actions?: ChartActions}): [ReactNode, boolean] {
  const {kind, spec, model, metric} = view
  const cell = view.settings
  const shown = (label: string) => !hidden.includes(label)
  const keepGroups = groups.filter(group => shown(group.label))
  const showAll = hidden.length > 0 && actions.showAllSeries && <button type="button" className="ks-button" onClick={actions.showAllSeries}>Show all series</button>
  if (!data || !kind) return [emptyState('No figure can be drawn from these results yet.'), false]
  if (kind === 'placement') return [data.placement, false]
  if (groups.length > 1 && !keepGroups.length) return [emptyState('Every series is hidden. Show one in Layers, or:', showAll), false]
  const provenance = (item: string) => ({metric: item, provenance: data.provenance(item)})
  if (spec && model) {
    if (model.empty) return [emptyState(model.empty, <>{showAll}{spec.type === 'xy' && data.xy && actions.suggestPair && <button type="button" className="ks-button" onClick={() => actions.suggestPair!(view)}>Use the suggested pair</button>}</>), false]
    const metrics = [...new Set(model.panels.flatMap(panel => panel.metrics))]
    const figureName = spec.type === 'xy' ? `xy-${view.xy.x.metric}${view.xy.x.service ? `-${view.xy.x.service}` : ''}-vs-${view.xy.y.metric}${view.xy.y.service ? `-${view.xy.y.service}` : ''}` : metric
    return [<SpecFigure id={`figure-${idPrefix}${view.index ? `${view.index}-` : ''}${figureName.replaceAll(/[^a-zA-Z0-9]/g, '-')}`} name={figureName} model={model} settings={cell} grid={spec.grid} strokeScale={spec.strokeScale} geometry={geometry} provenance={(spec.type === 'xy' ? [view.xy.x.metric, ...metrics] : metrics).filter((item, index, list) => list.indexOf(item) === index).map(provenance)} ariaLabel={spec.type === 'xy' ? `Scatter of ${model.title}` : `${model.title} comparison chart`} />, true]
  }
  if (kind === 'runlevel' && metric) {
    const entry = data.runLevel.find(item => item.metric === metric)
    if (!entry) return [emptyState('No run-level values for this metric. Pick another one:', actions.metricPicker?.(view)), false]
    return [<RunDotPlot data={{...entry, groups: entry.groups.filter(group => shown(group.label))}} settings={cell} provenance={provenance(metric)} runLabels={data.runLabels} />, true]
  }
  if (kind === 'ladder') {
    const metrics = ['load.p95_response_time', 'application.p95_response_time'].filter(item => data.metrics.includes(item))
    return [<PercentileLadder series={data.ladder.filter(item => shown(item.group))} settings={cell} provenance={metrics.map(provenance)} />, true]
  }
  if (kind === 'fidelity') {
    const series = data.fidelity.filter(item => keepGroups.some(group => item.label.startsWith(`${group.label} · `)) || groups.length <= 1)
    const order = [...new Set(series.filter(item => item.role === 'achieved').map(item => item.artifactID))]
    const offeredMetric = data.metrics.includes('load.offered_rps') ? 'load.offered_rps' : 'load.configured_rps'
    return [<TimeSeriesFigure name="load-fidelity" metric="load.achieved_rps" title={offeredMetric === 'load.offered_rps' ? 'Offered vs achieved request rate' : 'Configured vs achieved request rate'} axisLabel="Request rate (req/s)" series={series} styles={series.map(item => {const base = seriesStyle(order.indexOf(item.artifactID), item.color, cell); return item.role === 'offered' ? {...base, dash: '4 2', marker: 5} : base})} settings={cell} provenance={[offeredMetric, 'load.achieved_rps'].map(provenance)} captionLead={`${offeredMetric === 'load.offered_rps' ? 'Offered (dashed, generator target)' : 'Configured (dashed)'} and achieved (solid) request rate`} ariaLabel="Offered versus achieved request rate chart" />, true]
  }
  if (kind === 'services' && metric) {
    const rows = (data.services(metric)?.rows ?? []).filter(row => keepGroups.some(group => group.id === row.groupID || group.label === row.group) || groups.length <= 1)
    const title = metric.endsWith('p95_response_time') ? metric.startsWith('redis.') ? 'Mean window P95 per Redis operation' : 'Mean window P95 per service' : metric.startsWith('redis.') ? metricTitle(metric) : `${metricTitle(metric)} by service`
    return rows.length ? [<ServiceFigure metric={metric} rows={rows} settings={cell} provenance={provenance(metric)} title={title} />, true] : [emptyState('No service values for this metric. Pick another one:', actions.metricPicker?.(view)), false]
  }
  return [null, false]
}

const sameReport = (left: Report | undefined, right: Report) => Boolean(left) && left!.id === right.id && left!.name === right.name && left!.title === right.title && left!.caption === right.caption && left!.methods === right.methods && left!.width === right.width && left!.height === right.height && JSON.stringify(left!.labels) === JSON.stringify(right.labels)

// What each drawn chart reports about itself (title, caption, size, CSV), through FigureHostContext.
export function useReports(charts: ChartState[]) {
  const [reports, setReports] = useState<Record<string, Report>>({})
  const csvs = useRef<Record<string, (() => string) | undefined>>({})
  const reporters = useRef<Record<string, (figure: FigureReport) => void>>({})
  const reporter = (chart: string) => reporters.current[chart] ??= (figure: FigureReport) => {
    csvs.current[chart] = figure.csv
    const {csv: _csv, ...rest} = figure
    void _csv
    setReports(previous => sameReport(previous[chart], rest) ? previous : {...previous, [chart]: rest})
  }
  const hosts: Record<string, FigureHost> = useMemo(() => Object.fromEntries(charts.map(chart => [chart.id, {overrides: chart.overrides ?? {}, report: reporter(chart.id)}])), [JSON.stringify(charts.map(chart => [chart.id, chart.overrides]))])
  return {reports, csvs, hosts}
}

// Page geometry (points): cells laid out by row, a letter band above each row.
export function imagePage(views: ChartView[], reports: Record<string, Report>, settings: FigureSettings, isPublication: (view: ChartView) => boolean) {
  const single = views.length === 1
  const currentOf = (view: ChartView) => isPublication(view) ? reports[view.chart.id] : undefined
  const first = views[0] ? currentOf(views[0]) : undefined
  const page = figureSize(settings)
  const width = single ? first?.width ?? page.width : page.width
  const band = single ? 0 : settings.fontSize * 1.6
  const rows = Math.max(0, ...views.map(view => view.box.row + 1))
  const rowHeights = Array.from({length: rows}, (_, row) => Math.max(0, ...views.filter(view => view.box.row === row).map(view => reports[view.chart.id]?.height ?? figureSize(view.settings).height * 1.3)))
  const rowTops = rowHeights.map((_, row) => rowHeights.slice(0, row).reduce((sum, value) => sum + value + band, 0) + band)
  const height = single ? first?.height ?? page.height * 1.3 : rowHeights.reduce((sum, value) => sum + value + band, 0)
  const cellOf = (view: ChartView) => ({x: view.box.x * width, y: single ? 0 : rowTops[view.box.row], width: view.box.width * width})
  return {single, width, height, cellOf, currentOf, first}
}
type ImagePage = ReturnType<typeof imagePage>

// The image as one SVG: the chart itself, or a page composed of every chart with letters.
export function imageSvg(views: ChartView[], page: ImagePage, svgOf: (view: ChartView) => SVGSVGElement | null, settings: FigureSettings, id: string): SVGSVGElement {
  if (page.single) {
    const svg = views[0] ? svgOf(views[0]) : null
    if (!svg) throw new Error('The figure is not ready yet. Try again.')
    const copy = svg.cloneNode(true) as SVGSVGElement
    copy.setAttribute('id', id)
    return copy
  }
  const parts = views.map(view => ({svg: svgOf(view), ...page.cellOf(view), letter: view.letter}))
  if (parts.some(part => !part.svg)) throw new Error('The image is not ready yet. Try again.')
  return composeFigure(id, parts as Array<{svg: SVGSVGElement; x: number; y: number; letter?: string}>, page.width, page.height, {family: fontStacks[settings.font], size: settings.fontSize})
}

// Exports an SVG built off-screen (exportChart reads it by id from the document).
export async function exportSvg(svg: SVGSVGElement, format: ChartExportFormat, metric: string, options: ChartExportOptions, onGenerated?: (value: Uint8Array) => Promise<void>) {
  svg.setAttribute('style', 'position:absolute;left:-10000px;top:0;width:1px;height:1px')
  document.body.appendChild(svg)
  try {await exportChart(svg.id, metric, format, onGenerated, options)} finally {svg.remove()}
}
