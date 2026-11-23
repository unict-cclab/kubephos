import {useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode} from 'react'
import {downloadBlob, exportChart} from '../../charts'
import {matplotlibScript, pgfplotsSnippet} from './figureCode'
import {buildFigureModel, unitSuffix, type FigureModel, type LoadProfile, type ModelSeries} from './figureModel'
import {builtInPresets, defaultSpec, loadPresets, normalizeSpec, parseSpec, savePresets, serializeSpec, type Annotation, type AxisSide, type AxisSpec, type FigureSpec, type PanelGrid, type RunMode, type SpecType, type StylePreset, type XAxisSpec} from './figureSpec'
import {SpecFigure, type FigureGeometry} from './SpecFigure'
import {AnnotationFields, annotationNames, AxisFields, AxisMetrics, NumberInput, newAnnotation, PaletteEditor, PresetsEditor, runNames, SecondY, SeriesProperties, SeriesSwatch, SpecActions, XYAxisPicker} from './SpecEditor'
import {Chips, Group, LabelField, MoreGroup, SelectField, Stepper, Toggle} from './FigureSettingsControl'
import type {AggregateSeries, RunLevelMetric} from '../../resultsData'
import {ActionIcon} from '../ActionIcon'
import {ErrorFeedback, useErrorState} from '../ToastRegion'
import {FigureHostContext, figureMetrics, seriesStyle, type FigureLabelOverrides, type FigureReport} from './chartKit'
import {PercentileLadder, RunDotPlot, ServiceFigure, TimeSeriesFigure, type ServiceRow} from './Figures'
import {customLimits, defaultFigureSettings, figureSize, fontStacks, type FigureSettings, type FigureSize} from './figureSettings'
import {formatMetric, metricTitle} from './format'
import type {FidelitySeries} from './loadFidelity'
import type {OverviewGroup} from './overview'
import type {LadderSeries} from './percentiles'
import type {ResolvedProvenance} from './runs'
import {defaultXYOptions, defaultXYSelection, type XYSource, type XYState} from './xy'
import {KubeStudioMark} from './KubeStudioMark'
import {buildRecipes, customForms, FigureSwitcher, KindIcon, kindLabels, kindOrder, runLevelExcluded, smartTimeSeriesSpec, switcherItems, timeSeriesExcluded, type CustomForm, type SwitcherItem} from './studioRecipes'
import {cellBoxes, cellSettings, composeFigure, defaultLayout, layoutChoices, letters, maxCharts, newChartId, validLayout, type ChartLayout, type ChartState} from './studioCharts'
import {figureChecks, type CheckFix} from './studioChecks'
import {ExportPopover, exportLabels, rememberedFormat, rememberFormat, type ExportAction} from './StudioExport'
import './kubestudio.css'

// KubeStudio: a Figma-like workspace for one publication image made of one to four charts.
// It opens directly on the best chart for this experiment (or the last image edited). Left:
// Layers, the outline of the image (charts, each with title, axes, series, legend,
// annotations; caption), which is also how things are selected; centre: the image on paper
// at its true printed size, clickable and draggable; right: the properties of the selection,
// or of the page. Every change is undoable and autosaved; export lives in the top bar.

export type FigureKind = 'timeseries' | 'xy' | 'runlevel' | 'ladder' | 'fidelity' | 'services' | 'placement'
export interface StudioRequest {kind?: FigureKind; metric?: string; xy?: XYState}

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

// Saves the exported PDF as an experiment figure (POST /experiments/{id}/figures).
export type StudioSave = (figure: {metric: string; format: 'pdf'; bytes: Uint8Array}) => Promise<void>

export const studioName = 'KubeStudio'
const creator = 'KubePhos KubeStudio'

// ---------------------------------------------------------------------------------------
// Remembered studio state (per experiment, localStorage; failures fall back to memory). The
// key keeps its historical "figureStudio" name; single-figure saves from before multi-chart
// images are migrated to one chart when read.

interface StudioMemory {charts?: ChartState[]; layout?: ChartLayout; sharedLegend?: boolean; name?: string; caption?: string; hidden?: string[]; zoom?: number | 'fit'}
interface LegacyMemory {kind?: FigureKind; metric?: string; xy?: XYState; figures?: Record<string, {name?: string; caption?: string; overrides?: FigureLabelOverrides}>; specs?: Partial<Record<SpecType, unknown>>}
const memoryPrefix = 'kubephos.figureStudio.v1:'
const isKind = (value: unknown): value is FigureKind => kindOrder.includes(value as FigureKind)
const validXY = (value: unknown): XYState | undefined => {
  const xy = value as XYState | undefined
  return xy && typeof xy === 'object' && xy.x?.metric && xy.y?.metric ? {...xy, options: {...defaultXYOptions, ...xy.options}} : undefined
}
const chartSpec = (kind: FigureKind, value: unknown) => (kind === 'timeseries' || kind === 'xy') && value ? normalizeSpec(value, kind) : undefined

export function loadStudioMemory(key?: string): StudioMemory {
  if (!key) return {}
  try {
    const value = JSON.parse(window.localStorage.getItem(memoryPrefix + key) ?? '{}') as unknown
    if (!value || typeof value !== 'object') return {}
    const memory = value as StudioMemory & LegacyMemory
    const base = {hidden: Array.isArray(memory.hidden) ? memory.hidden.filter(item => typeof item === 'string') : undefined, zoom: memory.zoom === 'fit' || typeof memory.zoom === 'number' ? memory.zoom : undefined}
    if (Array.isArray(memory.charts)) {
      const charts = memory.charts.flatMap((item): ChartState[] => item && isKind(item.kind) ? [{id: typeof item.id === 'string' ? item.id : newChartId(), kind: item.kind, metric: typeof item.metric === 'string' ? item.metric : '', xy: validXY(item.xy) ?? {...defaultXYSelection([]), options: defaultXYOptions}, spec: chartSpec(item.kind, item.spec), overrides: item.overrides && typeof item.overrides === 'object' ? item.overrides : undefined, origin: item.origin === 'custom' || item.origin === 'recipe' ? item.origin : undefined}] : []).slice(0, maxCharts)
      return {...base, charts: charts.length ? charts : undefined, layout: memory.layout, sharedLegend: memory.sharedLegend === true, name: typeof memory.name === 'string' ? memory.name : undefined, caption: typeof memory.caption === 'string' ? memory.caption : undefined}
    }
    if (!isKind(memory.kind)) return base
    // Before multi-chart images: one figure (kind, metric, xy, specs per type, figures by key).
    const xy = validXY(memory.xy)
    const metric = typeof memory.metric === 'string' ? memory.metric : ''
    const figureKey = memory.kind === 'xy' && xy ? `xy:${xy.x.metric}:${xy.x.service ?? ''}:${xy.y.metric}:${xy.y.service ?? ''}` : `${memory.kind}:${metric}`
    const figure = memory.figures && typeof memory.figures === 'object' ? memory.figures[figureKey] : undefined
    return {...base, charts: [{id: newChartId(), kind: memory.kind, metric, xy: xy ?? {...defaultXYSelection([]), options: defaultXYOptions}, spec: chartSpec(memory.kind, memory.specs?.[memory.kind as SpecType]), overrides: figure?.overrides}], name: figure?.name, caption: figure?.caption}
  } catch {
    return {}
  }
}

function saveStudioMemory(key: string | undefined, memory: StudioMemory): boolean {
  if (!key) return true
  try {window.localStorage.setItem(memoryPrefix + key, JSON.stringify({version: 2, ...memory})); return true} catch {return false}
}

function useNarrow(query = '(max-width: 999px)') {
  const read = () => {try {return typeof window.matchMedia === 'function' && window.matchMedia(query).matches} catch {return false}}
  const [narrow, setNarrow] = useState(read)
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return
    const list = window.matchMedia(query)
    const update = () => setNarrow(list.matches)
    update()
    list.addEventListener?.('change', update)
    return () => list.removeEventListener?.('change', update)
  }, [query])
  return narrow
}

// ---------------------------------------------------------------------------------------
// Undo / redo over JSON snapshots of everything the user edits. Bursts (typing, dragging)
// within 700 ms coalesce into one step.

function useHistory<T>(snapshot: T, apply: (value: T) => void) {
  const json = JSON.stringify(snapshot)
  const past = useRef<string[]>([])
  const future = useRef<string[]>([])
  const last = useRef(json)
  const changedAt = useRef(0)
  const ready = useRef(false)
  const [, bump] = useState(0)
  useEffect(() => {const timer = setTimeout(() => {ready.current = true}, 0); return () => clearTimeout(timer)}, [])
  useEffect(() => {
    if (json === last.current) return
    if (!ready.current) {last.current = json; return}
    const now = Date.now()
    if (now - changedAt.current > 700 || !past.current.length) past.current = [...past.current, last.current].slice(-100)
    changedAt.current = now
    future.current = []
    last.current = json
    bump(value => value + 1)
  }, [json])
  const move = (from: typeof past, to: typeof past) => {
    const target = from.current.at(-1)
    if (target === undefined) return false
    from.current = from.current.slice(0, -1)
    to.current = [...to.current, last.current]
    last.current = target
    changedAt.current = 0
    apply(JSON.parse(target) as T)
    bump(value => value + 1)
    return true
  }
  return {undo: () => move(past, future), redo: () => move(future, past), canUndo: past.current.length > 0, canRedo: future.current.length > 0}
}

const slug = (value: string) => value.normalize('NFKD').replaceAll(/[^a-zA-Z0-9]+/g, '-').replaceAll(/^-+|-+$/g, '').toLowerCase().slice(0, 80) || 'figure'
const inches = (points: number) => String(Number((points / 72).toFixed(points >= 720 ? 1 : 2)))
const oneLine = (value: string) => value.replaceAll(/\s+/g, ' ').trim()

type Report = Omit<FigureReport, 'csv'>
const sameReport = (left: Report | undefined, right: Report) => Boolean(left) && left!.id === right.id && left!.name === right.name && left!.title === right.title && left!.caption === right.caption && left!.methods === right.methods && left!.width === right.width && left!.height === right.height && JSON.stringify(left!.labels) === JSON.stringify(right.labels)

// What is selected inside a chart (undefined = the chart itself). Keys match data-select.
type Target = {type: 'series'; key: string} | {type: 'group'; label: string} | {type: 'axis'; side: 'x' | 'left' | 'right'; panel: number} | {type: 'legend'} | {type: 'title'} | {type: 'annotation'; panel: number; id: string}
interface Selection {chart: string; target?: Target}

function parseTarget(value: string | null | undefined): Target | undefined {
  if (!value) return undefined
  if (value === 'legend' || value === 'title') return {type: value}
  if (value.startsWith('series:')) return {type: 'series', key: value.slice(7)}
  if (value.startsWith('group:')) return {type: 'group', label: value.slice(6)}
  const [type, first, second] = value.split(':')
  if (type === 'axis' && (first === 'x' || first === 'left' || first === 'right')) return {type: 'axis', side: first, panel: Number(second) || 0}
  if (type === 'annotation' && second) return {type: 'annotation', panel: Number(first) || 0, id: second}
  return undefined
}

function targetKey(target: Target | undefined): string {
  if (!target) return ''
  if (target.type === 'series') return `series:${target.key}`
  if (target.type === 'group') return `group:${target.label}`
  if (target.type === 'axis') return `axis:${target.side}:${target.panel}`
  if (target.type === 'annotation') return `annotation:${target.panel}:${target.id}`
  return target.type
}
const selectionKey = (selection: Selection | undefined) => selection ? `${selection.chart}/${targetKey(selection.target)}` : ''
function parseKey(key: string): Selection | undefined {
  if (!key) return undefined
  const slash = key.indexOf('/')
  return {chart: key.slice(0, slash), target: parseTarget(key.slice(slash + 1))}
}

const autoAnnotations: Record<string, {label: string; key: 'slo' | 'phases' | 'warmup'}> = {slo: {label: 'SLO line', key: 'slo'}, phase: {label: 'Load-phase boundaries', key: 'phases'}, warmup: {label: 'Warm-up end', key: 'warmup'}}

// ---------------------------------------------------------------------------------------
// Canvas highlight: hugs the element in chart coordinates (points), on a layer above the
// paper that never reaches the exports.

interface Shape {kind: 'path' | 'rect'; d?: string; x?: number; y?: number; width?: number; height?: number; handles?: Array<{x: number; y: number}>}

function highlightShapes(cell: Element | null, key: string, geometry: FigureGeometry | undefined): Shape[] {
  if (!cell || !key || !geometry) return []
  const svg = cell.querySelector('svg.pub-figure')
  if (!svg) return []
  const elements = [...svg.querySelectorAll('[data-select]')].filter(element => element.getAttribute('data-select') === key)
  if (!elements.length) return []
  const paths = elements.flatMap(element => element.classList.contains('series') ? [...element.querySelectorAll('path.series-line, path.series-fit')] : []).map(path => path.getAttribute('d')).filter((d): d is string => Boolean(d))
  if (paths.length) return paths.map(d => ({kind: 'path', d}))
  const line = elements.length === 1 ? elements[0].querySelector(':scope > line') : null
  if (line && elements[0].classList.contains('annotation')) {
    const [x1, y1, x2, y2] = ['x1', 'y1', 'x2', 'y2'].map(name => Number(line.getAttribute(name)))
    return [{kind: 'path', d: `M${x1},${y1}L${x2},${y2}`, handles: [{x: (x1 + x2) / 2, y: (y1 + y2) / 2}]}]
  }
  const frame = svg.getBoundingClientRect()
  if (!frame.width) return []
  const k = geometry.width / frame.width
  let left = Infinity, top = Infinity, right = -Infinity, bottom = -Infinity
  for (const element of elements) {
    const rect = element.getBoundingClientRect()
    if (!rect.width && !rect.height) continue
    left = Math.min(left, rect.left); top = Math.min(top, rect.top); right = Math.max(right, rect.right); bottom = Math.max(bottom, rect.bottom)
  }
  if (!Number.isFinite(left)) return []
  const pad = 1.5
  const box = {x: (left - frame.left) * k - pad, y: (top - frame.top) * k - pad, width: (right - left) * k + pad * 2, height: (bottom - top) * k + pad * 2}
  return [{kind: 'rect', ...box, handles: key.startsWith('annotation:') ? [{x: box.x + box.width / 2, y: box.y + box.height / 2}] : undefined}]
}

// Snap a dragged value to a readable step (1/20 of the axis span, rounded to 1-2-5).
const snap = (value: number, span: number) => {
  if (!Number.isFinite(value)) return value
  const raw = Math.max(Math.abs(span), 1e-9) / 20
  const power = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map(factor => factor * power).find(candidate => candidate >= raw) ?? raw
  return Number((Math.round(value / step) * step).toPrecision(10))
}

// ---------------------------------------------------------------------------------------
// Icons.

const svgProps = {width: 16, height: 16, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.75, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, 'aria-hidden': true, focusable: 'false' as const}
const UndoIcon = ({redo = false}: {redo?: boolean}) => <svg {...svgProps} style={redo ? {transform: 'scaleX(-1)'} : undefined}><path d="M9 14 4 9l5-5" /><path d="M4 9h10.5a5.5 5.5 0 0 1 0 11H11" /></svg>
const EyeIcon = ({off = false}: {off?: boolean}) => <svg {...svgProps}>{off ? <><path d="M3 3l18 18" /><path d="M10.6 6.1A9.8 9.8 0 0 1 12 6c5 0 9 6 9 6a16 16 0 0 1-2.6 3.1M6.5 7.5C4.3 9.1 3 12 3 12s4 6 9 6a8.6 8.6 0 0 0 4-1" /></> : <><path d="M3 12s4-6 9-6 9 6 9 6-4 6-9 6-9-6-9-6Z" /><circle cx="12" cy="12" r="2.5" /></>}</svg>
const layerIcons: Record<string, ReactNode> = {
  figure: <svg {...svgProps}><rect x="4" y="4" width="16" height="16" rx="2" /><path d="M4 12h16M12 4v16" /></svg>,
  title: <svg {...svgProps}><path d="M6 6h12M12 6v12" /></svg>,
  axis: <svg {...svgProps}><path d="M5 4v15h15" /><path d="M5 9h2M5 14h2M10 19v-2M15 19v-2" /></svg>,
  legend: <svg {...svgProps}><rect x="4" y="5" width="16" height="14" rx="2" /><path d="M7 10h3M13 10h4M7 14h3M13 14h4" /></svg>,
  annotation: <svg {...svgProps}><path d="M4 12h16" strokeDasharray="3 2.5" /><path d="M8 7h8" /></svg>,
  caption: <svg {...svgProps}><path d="M5 7h14M5 12h14M5 17h9" /></svg>,
  plus: <svg {...svgProps}><path d="M12 5v14M5 12h14" /></svg>,
  chevron: <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M4 2.5 7.5 6 4 9.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>
}

const shortcuts: Array<[string, string]> = [['E', 'Export'], ['Ctrl/⌘ Z', 'Undo'], ['Ctrl/⌘ Shift Z', 'Redo'], ['+ / −', 'Zoom in / out'], ['0', 'Fit the image'], ['Esc', 'Deselect, then close'], ['?', 'Show this list']]
const sizeChoices: Array<{value: FigureSize; label: string; hint: string}> = [{value: 'single', label: '1 col', hint: 'Single column · 3.5 in · IEEE/ACM'}, {value: 'onehalf', label: '1.5 col', hint: '1.5 column · 5.5 in'}, {value: 'double', label: '2 col', hint: 'Double column · 7.16 in · IEEE/ACM'}, {value: 'slide', label: 'Slide', hint: 'Slide 16:9 · 10 × 5.63 in'}, {value: 'custom', label: 'Custom', hint: 'Custom width × plot height'}]
// Metrics suggested, in order, for each chart added to an image.
const suggested = ['load.p95_response_time', 'load.achieved_rps', 'cpu.cores', 'load.failure_percentage', 'load.successful_rps', 'memory.working_set', 'application.p95_response_time']

interface LayerRow {key: string; label: string; depth: 1 | 2; icon?: ReactNode; heading?: boolean; selectable?: boolean; eye?: {visible: boolean; label: string; onToggle: () => void}; action?: ReactNode; dim?: boolean; menu?: ReactNode}

// One chart of the image with everything derived from its state.
interface ChartView {
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

export function FigureStudio({open, onClose, request, data, groups, settings, onSettings, storageKey, onSave}: {open: boolean; onClose: () => void; request: StudioRequest; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; onSettings: (next: FigureSettings) => void; storageKey?: string; onSave?: StudioSave}) {
  const id = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const paper = useRef<HTMLDivElement>(null)
  const captionRef = useRef<HTMLTextAreaElement>(null)
  const geometries = useRef<Record<string, FigureGeometry | undefined>>({})
  const geometryRefs = useRef<Record<string, {current: FigureGeometry | undefined}>>({})
  const geometryOf = (chart: string) => geometryRefs.current[chart] ??= {get current() {return geometries.current[chart]}, set current(value) {geometries.current[chart] = value}}
  const remembered = useMemo(() => loadStudioMemory(storageKey), [storageKey])
  const requested = Boolean(request.kind || request.metric || request.xy)
  const defaultXY = (): XYState => ({...defaultXYSelection(data?.xy?.metrics ?? []), options: defaultXYOptions})
  // Opening: the request, else the last image, else the best recipe for this experiment.
  const [charts, setCharts] = useState<ChartState[]>(() => {
    if (requested) {
      const kind = request.kind ?? 'timeseries'
      const previous = remembered.charts?.find(item => item.kind === kind)
      return [{id: newChartId(), kind, metric: request.metric ?? previous?.metric ?? '', xy: request.xy ?? previous?.xy ?? defaultXY(), spec: previous?.spec, overrides: undefined}]
    }
    if (remembered.charts?.length) return remembered.charts
    const recipe = data ? buildRecipes(data, groups)[0] : undefined
    return recipe ? [{id: newChartId(), kind: recipe.kind, metric: recipe.metric ?? '', xy: recipe.xy ?? defaultXY(), spec: recipe.spec}] : []
  })
  const [layout, setLayout] = useState<ChartLayout | undefined>(remembered.layout)
  const [sharedLegend, setSharedLegend] = useState(remembered.sharedLegend ?? false)
  const [doc, setDoc] = useState<{name?: string; caption?: string}>(() => requested ? {} : {name: remembered.name, caption: remembered.caption})
  const [hidden, setHidden] = useState<string[]>(remembered.hidden ?? [])
  const [selection, setSelection] = useState<Selection>()
  const [hoverKey, setHoverKey] = useState('')
  const [shapes, setShapes] = useState<{hover: Shape[]; selected: Shape[]; hoverChart?: string; selectedChart?: string}>({hover: [], selected: []})
  const [addingNote, setAddingNote] = useState<string>()
  const [presets, setPresets] = useState<StylePreset[]>(() => loadPresets())
  const [zoom, setZoom] = useState<number | 'fit'>(remembered.zoom ?? 'fit')
  const [stageSize, setStageSize] = useState<{width: number; height: number}>()
  const [tab, setTab] = useState<'layers' | 'canvas' | 'properties'>('canvas')
  const [busy, setBusy] = useState<ExportAction | 'save' | 'copy' | null>(null)
  const [exportOpen, setExportOpen] = useState(false)
  const [format, setFormat] = useState<ExportAction>(() => rememberedFormat())
  const [help, setHelp] = useState(false)
  const [drawer, setDrawer] = useState(false)
  const [dismissed, setDismissed] = useState<string[]>([])
  const [status, setStatus] = useState('')
  const [notice, setNotice] = useState<{text: string; undo?: boolean; at: number}>()
  const [saved, setSaved] = useState<boolean>()
  const [error, setError] = useErrorState()
  const narrow = useNarrow()
  const [reports, setReports] = useState<Record<string, Report>>({})
  const csvs = useRef<Record<string, (() => string) | undefined>>({})
  const reporters = useRef<Record<string, (figure: FigureReport) => void>>({})
  const reporter = (chart: string) => reporters.current[chart] ??= (figure: FigureReport) => {
    csvs.current[chart] = figure.csv
    const {csv: _csv, ...rest} = figure
    void _csv
    setReports(previous => sameReport(previous[chart], rest) ? previous : {...previous, [chart]: rest})
  }
  const announce = (text: string, undo = false) => {setStatus(text); setNotice({text, undo, at: Date.now()})}
  useEffect(() => {if (!notice) return; const timer = setTimeout(() => setNotice(undefined), notice.undo ? 6000 : 2400); return () => clearTimeout(timer)}, [notice])

  // Undo / redo of everything the user edits (style settings live in the page).
  const history = useHistory({settings, charts, layout, sharedLegend, doc, hidden}, value => {
    onSettings(value.settings); setCharts(value.charts); setLayout(value.layout); setSharedLegend(value.sharedLegend); setDoc(value.doc); setHidden(value.hidden)
  })
  const undo = () => {if (history.undo()) announce('Undone.')}
  const redo = () => {if (history.redo()) announce('Redone.')}

  // Native modal dialog: the rest of the page is inert and Esc closes it.
  useEffect(() => {
    const element = dialog.current
    if (!element || !open) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (!element.open) element.showModal()
    element.querySelector<HTMLElement>('.ks-switcher-button')?.focus({preventScroll: true})
    return () => {
      if (element.open) element.close()
      if (previous?.isConnected) previous.focus({preventScroll: true})
    }
  }, [open])

  const available = useMemo(() => {
    if (!data) return [] as FigureKind[]
    const kinds = new Set<FigureKind>()
    if (data.fidelity.length) kinds.add('fidelity')
    if (data.metrics.some(item => !timeSeriesExcluded(item) && data.plot(item).length)) kinds.add('timeseries')
    if ((data.xy?.metrics.length ?? 0) > 1) kinds.add('xy')
    if (data.runLevel.some(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length))) kinds.add('runlevel')
    if (data.ladder.length) kinds.add('ladder')
    if (data.serviceMetrics.length) kinds.add('services')
    if (data.placement) kinds.add('placement')
    return kindOrder.filter(item => kinds.has(item))
  }, [data])
  const recipes = useMemo(() => data ? buildRecipes(data, groups) : [], [data, groups])
  const items = useMemo(() => switcherItems(available, recipes), [available, recipes])
  const forms = customForms.map(form => form.kind).filter(kind => available.includes(kind))
  const timeMetrics = useMemo(() => data ? data.metrics.filter(item => !timeSeriesExcluded(item) && data.plot(item).length) : [], [data])
  const metricOptionsFor = (kind: FigureKind | undefined) => !data ? [] : kind === 'timeseries' ? timeMetrics : kind === 'runlevel' ? data.runLevel.filter(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length)).map(entry => entry.metric) : kind === 'services' ? data.serviceMetrics : []
  // Data that arrives after opening: start on the best recipe as well.
  useEffect(() => {if (!charts.length && data && recipes.length) setCharts([{id: newChartId(), kind: recipes[0].kind, metric: recipes[0].metric ?? '', xy: recipes[0].xy ?? defaultXY(), spec: recipes[0].spec}])}, [data, recipes])
  useEffect(() => {setSaved(saveStudioMemory(storageKey, {charts, layout, sharedLegend, name: doc.name, caption: doc.caption, hidden, zoom}))}, [storageKey, charts, layout, sharedLegend, doc, hidden, zoom])

  const shown = (label: string) => !hidden.includes(label)
  const keepGroups = groups.filter(group => shown(group.label))
  const count = charts.length
  const effectiveLayout = validLayout(count, layout)
  const boxes = cellBoxes(count, effectiveLayout)

  // Every chart with its resolved type, data, spec and model.
  const views: ChartView[] = useMemo(() => charts.map((chart, index) => {
    const kind = chart.kind && available.includes(chart.kind) && !(chart.kind === 'placement' && charts.length > 1) ? chart.kind : available.includes('timeseries') ? 'timeseries' : available[0]
    const options = metricOptionsFor(kind)
    const metric = options.includes(chart.metric) ? chart.metric : options.includes('load.p95_response_time') ? 'load.p95_response_time' : options[0] ?? ''
    const xy = data?.xy && chart.xy.x.metric && data.xy.metrics.includes(chart.xy.x.metric) && data.xy.metrics.includes(chart.xy.y.metric) ? chart.xy : defaultXY()
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
  }), [charts, available, data, settings, sharedLegend, hidden, groups.length, effectiveLayout])

  const updateChart = (chartId: string, update: (chart: ChartState) => ChartState) => setCharts(list => list.map(item => item.id === chartId ? update(item) : item))
  const setChartSpec = (view: ChartView) => (update: (spec: FigureSpec) => FigureSpec) => {
    if (view.kind !== 'timeseries' && view.kind !== 'xy') return
    const type = view.kind
    updateChart(view.chart.id, chart => ({...chart, spec: update(chart.spec?.type === type ? chart.spec : defaultSpec(type))}))
  }
  const setChartXY = (view: ChartView, update: (xy: XYState) => XYState) => updateChart(view.chart.id, chart => ({...chart, xy: update(view.xy)}))
  const setChartMetric = (view: ChartView, metric: string) => updateChart(view.chart.id, chart => ({...chart, metric}))
  const setOverrides = (view: ChartView, next: FigureLabelOverrides) => updateChart(view.chart.id, chart => ({...chart, overrides: Object.values(next).some(item => item !== undefined && (typeof item !== 'object' || Object.keys(item).length)) ? next : undefined}))
  const hasSLO = Boolean(data && !data.omitSLO && data.plot('load.p95_response_time').some(item => item.sloMilliseconds))
  const dataSLO = data && !data.omitSLO ? data.plot('load.p95_response_time').find(item => item.sloMilliseconds)?.sloMilliseconds : undefined

  // Previews.
  const emptyState = (message: string, actions?: ReactNode) => <div className="ks-empty" role="status"><p>{message}</p>{actions && <div className="ks-actions">{actions}</div>}</div>
  const metricPicker = (view: ChartView) => data && view.metricOptions.length > 1 ? <SelectField label="Metric" value={view.metric} onChange={metric => setChartMetric(view, metric)}>{view.metricOptions.map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}</SelectField> : undefined
  const render = (view: ChartView): [ReactNode, boolean] => {
    const {kind, spec, model, metric} = view
    const cell = view.settings
    if (!data || !kind) return [emptyState('No figure can be drawn from these results yet.'), false]
    if (kind === 'placement') return [data.placement, false]
    if (groups.length > 1 && !keepGroups.length) return [emptyState('Every series is hidden. Show one in Layers, or:', <button type="button" className="ks-button" onClick={() => setHidden([])}>Show all series</button>), false]
    const provenance = (item: string) => ({metric: item, provenance: data.provenance(item)})
    if (spec && model) {
      if (model.empty) return [emptyState(model.empty, <>{hidden.length > 0 && <button type="button" className="ks-button" onClick={() => setHidden([])}>Show all series</button>}{spec.type === 'xy' && data.xy && <button type="button" className="ks-button" onClick={() => setChartXY(view, current => ({...current, ...defaultXYSelection(data.xy!.metrics)}))}>Use the suggested pair</button>}</>), false]
      const metrics = [...new Set(model.panels.flatMap(panel => panel.metrics))]
      const figureName = spec.type === 'xy' ? `xy-${view.xy.x.metric}${view.xy.x.service ? `-${view.xy.x.service}` : ''}-vs-${view.xy.y.metric}${view.xy.y.service ? `-${view.xy.y.service}` : ''}` : metric
      return [<SpecFigure id={`figure-${view.index ? `${view.index}-` : ''}${figureName.replaceAll(/[^a-zA-Z0-9]/g, '-')}`} name={figureName} model={model} settings={cell} grid={spec.grid} strokeScale={spec.strokeScale} geometry={geometryOf(view.chart.id)} provenance={(spec.type === 'xy' ? [view.xy.x.metric, ...metrics] : metrics).filter((item, index, list) => list.indexOf(item) === index).map(provenance)} ariaLabel={spec.type === 'xy' ? `Scatter of ${model.title}` : `${model.title} comparison chart`} />, true]
    }
    if (kind === 'runlevel' && metric) {
      const entry = data.runLevel.find(item => item.metric === metric)
      if (!entry) return [emptyState('No run-level values for this metric. Pick another one:', metricPicker(view)), false]
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
      return rows.length ? [<ServiceFigure metric={metric} rows={rows} settings={cell} provenance={provenance(metric)} title={title} />, true] : [emptyState('No service values for this metric. Pick another one:', metricPicker(view)), false]
    }
    return [null, false]
  }
  const rendered = views.map(view => render(view))
  const hosts = useMemo(() => Object.fromEntries(charts.map(chart => [chart.id, {overrides: chart.overrides ?? {}, report: reporter(chart.id)}])), [JSON.stringify(charts.map(chart => [chart.id, chart.overrides]))])
  const isPublication = (index: number) => rendered[index]?.[1] && views[index].kind !== 'placement'
  const publication = views.length > 0 && views.every((_, index) => isPublication(index))
  const single = count === 1
  const currentOf = (view: ChartView) => isPublication(view.index) ? reports[view.chart.id] : undefined
  const first = views[0] ? currentOf(views[0]) : undefined

  // Page geometry (points): cells laid out by row, a letter band above each row.
  const page = figureSize(settings)
  const pageWidth = single ? first?.width ?? page.width : page.width
  const band = single ? 0 : settings.fontSize * 1.6
  const rows = Math.max(0, ...views.map(view => view.box.row + 1))
  const rowHeights = Array.from({length: rows}, (_, row) => Math.max(0, ...views.filter(view => view.box.row === row).map(view => reports[view.chart.id]?.height ?? figureSize(view.settings).height * 1.3)))
  const rowTops = rowHeights.map((_, row) => rowHeights.slice(0, row).reduce((sum, value) => sum + value + band, 0) + band)
  const pageHeight = single ? first?.height ?? page.height * 1.3 : rowHeights.reduce((sum, value) => sum + value + band, 0)
  const cellOf = (view: ChartView) => ({x: view.box.x * pageWidth, y: single ? 0 : rowTops[view.box.row], width: view.box.width * pageWidth})

  // Name, caption and methods of the whole image.
  const titles = views.map(view => currentOf(view)?.title ?? (view.kind ? kindLabels[view.kind] : 'Chart'))
  const autoName = single ? titles[0] ?? 'Figure' : titles.join(' · ')
  const name = doc.name ?? autoName
  const autoCaption = single ? first?.caption ?? '' : views.map(view => `${view.letter} ${currentOf(view)?.caption ?? ''}`.trim()).join(' ')
  const caption = doc.caption ?? autoCaption
  const methods = [...new Set(views.map(view => currentOf(view)?.methods ?? '').filter(Boolean))].join(' ')

  // Selection: resolved against the current charts and their elements.
  const viewOf = (chartId: string | undefined) => views.find(view => view.chart.id === chartId)
  const validTarget = (view: ChartView, target: Target | undefined): Target | undefined => {
    if (!target || !isPublication(view.index)) return undefined
    const current = currentOf(view)
    if (target.type === 'title' || target.type === 'legend') return target
    if (target.type === 'group') return groups.some(group => group.label === target.label) ? target : undefined
    if (target.type === 'axis') return !view.spec ? (target.side === 'x' ? current?.labels.x !== undefined : target.side === 'left' && (current?.labels.y !== undefined || view.metricOptions.length > 0)) ? target : undefined : view.model?.panels[target.panel] && (target.side !== 'right' || view.model.panels[target.panel].right) ? target : undefined
    if (!view.spec || !view.model || view.model.empty) return undefined
    if (target.type === 'series') return view.model.legend.some(item => item.series.key === target.key) ? target : undefined
    return view.spec.panels[target.panel]?.annotations.some(item => item.id === target.id) || (target.id in autoAnnotations && view.model.panels[target.panel]?.annotations.some(item => item.source === target.id)) ? target : undefined
  }
  const selectedView = viewOf(selection?.chart)
  const selected: Selection | undefined = selectedView ? {chart: selectedView.chart.id, target: validTarget(selectedView, selection!.target)} : undefined
  const selectedKey = selectionKey(selected)
  const activeView = selectedView ?? views[0]
  const select = (next: Selection | undefined) => {
    setSelection(next)
    setAddingNote(undefined)
    if (next && narrow) setTab('properties')
  }

  // Fit-to-stage zoom from the stage size (ResizeObserver when available).
  useLayoutEffect(() => {
    const element = stage.current
    if (!element) return
    const measure = () => setStageSize(previous => {const next = {width: element.clientWidth, height: element.clientHeight}; return previous && previous.width === next.width && previous.height === next.height ? previous : next})
    measure()
    if (typeof ResizeObserver !== 'function') return
    const observer = new ResizeObserver(measure)
    observer.observe(element)
    return () => observer.disconnect()
  }, [open, narrow, tab, Boolean(data)])
  const paperWidth = pageWidth * 96 / 72
  const paperHeight = pageHeight * 96 / 72
  const fit = stageSize && stageSize.width > 0 && stageSize.height > 0 ? Math.min(3, Math.max(.25, Math.min((stageSize.width - (narrow ? 32 : 64)) / paperWidth, (stageSize.height - (narrow ? 80 : 64)) / paperHeight))) : 1
  const scale = zoom === 'fit' ? fit : zoom
  const step = (direction: 1 | -1) => setZoom(Math.min(4, Math.max(.25, Math.round((scale * (direction > 0 ? 1.25 : .8)) * 100) / 100)))
  const px = (points: number) => points * 96 / 72 * scale

  // Highlights measured from the drawn SVG.
  const hover = parseKey(hoverKey)
  useLayoutEffect(() => {
    const cell = (chart: string | undefined) => chart ? paper.current?.querySelector(`[data-chart="${chart}"]`) ?? null : null
    const hoverTarget = hover && hoverKey !== selectedKey ? hover : undefined
    const next = {
      hover: hoverTarget ? highlightShapes(cell(hoverTarget.chart), targetKey(hoverTarget.target), geometries.current[hoverTarget.chart]) : [], hoverChart: hoverTarget?.chart,
      selected: selected ? highlightShapes(cell(selected.chart), targetKey(selected.target), geometries.current[selected.chart]) : [], selectedChart: selected?.chart
    }
    setShapes(previous => JSON.stringify(previous) === JSON.stringify(next) ? previous : next)
  })

  // ---------------------------------------------------------------------------------------
  // Export.
  const description = [caption, methods && `Methods and provenance: ${methods}`].filter(Boolean).join('\n\n')
  const fileName = `kubephos-${slug(name)}`
  const savedMetric = (() => {const view = views[0]; return !view ? 'figure' : view.kind === 'timeseries' || view.kind === 'runlevel' || view.kind === 'services' ? view.metric : view.kind === 'xy' ? view.xy.y.metric : view.kind === 'ladder' ? 'load.p95_response_time' : view.kind === 'fidelity' ? 'load.achieved_rps' : 'placement'})()
  const exportOptions = {title: name, caption: description, fileName, creator}
  const codeView = single && views[0]?.spec && views[0].model && first ? views[0] : undefined
  // Runs an export on the image's SVG: the chart itself, or a composed page for several charts.
  const withImage = async (run: (svgId: string) => Promise<void>) => {
    if (single) {
      const svgId = publication ? first?.id : paper.current?.querySelector('svg[id]')?.id
      if (!svgId) throw new Error('The figure is not ready yet. Try again.')
      await run(svgId)
      return
    }
    const parts = views.map(view => {const report = reports[view.chart.id]; const svg = report ? document.getElementById(report.id) as SVGSVGElement | null : null; const cell = cellOf(view); return {svg, x: cell.x, y: cell.y, letter: view.letter}})
    if (parts.some(part => !part.svg)) throw new Error('The image is not ready yet. Try again.')
    const composite = composeFigure(`${id.replaceAll(':', '')}-image`, parts as Array<{svg: SVGSVGElement; x: number; y: number; letter?: string}>, pageWidth, pageHeight, {family: fontStacks[settings.font], size: settings.fontSize})
    composite.setAttribute('style', 'position:absolute;left:-10000px;top:0;width:1px;height:1px')
    document.body.appendChild(composite)
    try {await run(composite.id)} finally {composite.remove()}
  }
  const imageCSV = () => {
    if (single) return csvs.current[views[0]?.chart.id ?? '']?.()
    const parts = views.map(view => {const csv = csvs.current[view.chart.id]?.(); return csv ? `# ${view.letter} ${oneLine(currentOf(view)?.title ?? '')}\n${csv}` : ''}).filter(Boolean)
    return parts.length ? parts.join('\n') : undefined
  }
  const exportAs = async (action: ExportAction) => {
    if (busy) return
    setBusy(action)
    setError('')
    setStatus('')
    rememberFormat(action)
    setFormat(action)
    try {
      if (action === 'python' || action === 'pgfplots') {
        if (!codeView?.model || !codeView.spec || !first) throw new Error('Code export covers images with one time-series or X–Y chart.')
        const options = {base: fileName, title: name, caption, settings, width: first.width, height: first.height, grid: codeView.spec.grid}
        if (action === 'python') {
          const out = matplotlibScript(codeView.model, options)
          downloadBlob(out.scriptName, new Blob([out.script], {type: 'text/x-python;charset=utf-8'}))
          downloadBlob(out.csvName, new Blob([out.csv], {type: 'text/csv;charset=utf-8'}))
        } else {
          const out = pgfplotsSnippet(codeView.model, options)
          downloadBlob(out.texName, new Blob([out.tex], {type: 'text/x-tex;charset=utf-8'}))
          downloadBlob(out.csvName, new Blob([out.csv], {type: 'text/csv;charset=utf-8'}))
        }
      } else if (action === 'csv') {
        const csv = publication ? imageCSV() : undefined
        if (!csv) throw new Error('This figure has no exportable data.')
        const header = [`# ${oneLine(name)}`, caption && `# Caption: ${oneLine(caption)}`, methods && `# Methods and provenance: ${oneLine(methods)}`].filter(Boolean).join('\n')
        downloadBlob(`${fileName}-data.csv`, new Blob([`${header}\n${csv}`], {type: 'text/csv;charset=utf-8'}))
      } else {
        await withImage(async svgId => {
          if (action === 'png300' || action === 'png600') await exportChart(svgId, savedMetric, 'png', undefined, {...exportOptions, dpi: action === 'png300' ? 300 : 600})
          else await exportChart(svgId, savedMetric, action, undefined, exportOptions)
        })
      }
      setExportOpen(false)
      announce(`${exportLabels[action]} exported.`)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not export the figure. Try again.')}
    finally {setBusy(null)}
  }
  const copyFigure = async (action: 'png300' | 'svg') => {
    if (busy) return
    setBusy('copy')
    setError('')
    try {
      await withImage(async svgId => {
        if (action === 'svg') {
          if (!navigator.clipboard?.writeText) throw new Error('clipboard')
          await exportChart(svgId, savedMetric, 'svg', async bytes => {await navigator.clipboard.writeText(new TextDecoder().decode(bytes))}, exportOptions)
        } else {
          if (typeof ClipboardItem === 'undefined' || !navigator.clipboard?.write) throw new Error('clipboard')
          await exportChart(svgId, savedMetric, 'png', async bytes => {await navigator.clipboard.write([new ClipboardItem({'image/png': new Blob([bytes.slice().buffer as ArrayBuffer], {type: 'image/png'})})])}, {...exportOptions, dpi: 300})
        }
      })
      setExportOpen(false)
      announce(action === 'svg' ? 'SVG copied to the clipboard.' : 'Image copied to the clipboard (300 dpi).')
    } catch {setError('Could not copy: this browser does not allow it here. Download the file instead.')}
    finally {setBusy(null)}
  }
  const save = async () => {
    if (!onSave || busy) return
    setBusy('save')
    setError('')
    setStatus('')
    try {
      await withImage(svgId => exportChart(svgId, savedMetric, 'pdf', bytes => onSave({metric: savedMetric.replaceAll(/[^a-zA-Z0-9._-]/g, '-').slice(0, 100), format: 'pdf', bytes}), exportOptions))
      setExportOpen(false)
      announce('Saved to the experiment (PDF). Find it under Export › Saved figures.')
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save the figure. Try again.')}
    finally {setBusy(null)}
  }
  const copySpec = async (view: ChartView) => {
    if (!view.spec) return
    setError('')
    try {
      await navigator.clipboard.writeText(serializeSpec(view.spec))
      announce('Chart spec copied.')
    } catch {setError('Could not copy the spec: the clipboard is not available.')}
  }
  const pasteSpec = async (view: ChartView, json?: string) => {
    setError('')
    let text = json
    if (text === undefined) {
      try {text = await navigator.clipboard.readText()} catch {setError('Could not read the clipboard. Use “Paste as text…” instead.'); return}
    }
    try {
      const parsed = parseSpec(text ?? '')
      const firstPanel = parsed.panels[0]
      const metric = parsed.type === 'timeseries' && firstPanel.metrics[0]?.metric && data?.metrics.includes(firstPanel.metrics[0].metric) ? firstPanel.metrics[0].metric : view.metric
      const xy = parsed.type === 'xy' && firstPanel.xy ? {x: firstPanel.xy.x, y: firstPanel.xy.y, options: {...view.xy.options, fit: firstPanel.xy.fit, path: firstPanel.xy.path, logX: firstPanel.x.log, logY: firstPanel.left.log}} : view.xy
      updateChart(view.chart.id, chart => ({...chart, kind: parsed.type, spec: parsed, metric, xy, overrides: Object.keys(parsed.labels).length ? parsed.labels : undefined}))
      onSettings(parsed.style)
      announce('Chart spec applied.', true)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not apply the spec.')}
  }
  const applyPreset = (view: ChartView, preset: StylePreset) => {
    onSettings({...settings, ...preset.style, uncertainty: settings.uncertainty, zeroBased: settings.zeroBased, logY: settings.logY, grayscale: settings.grayscale, showTitle: settings.showTitle})
    setChartSpec(view)(current => ({...current, strokeScale: preset.strokeScale, grid: preset.grid}))
    announce(`Preset “${preset.name}” applied.`, true)
  }
  const storePresets = (next: StylePreset[]) => {
    setPresets(next)
    if (!savePresets(next)) setError('The preset is kept for this session only: browser storage is not available.')
  }
  const copyCaption = async () => {
    setError('')
    try {
      await navigator.clipboard.writeText(caption)
      announce('Caption copied.')
    } catch {setError('Could not copy the caption: the clipboard is not available. Select the caption text instead.')}
  }

  // ---------------------------------------------------------------------------------------
  // Charts: recipes, custom forms, add / duplicate / move / remove.
  const applyRecipe = (view: ChartView | undefined, item: SwitcherItem) => {
    const recipe = item.recipe
    const next: ChartState = {id: view?.chart.id ?? newChartId(), kind: item.kind, metric: recipe?.metric ?? view?.metric ?? '', xy: recipe?.xy ?? view?.xy ?? defaultXY(), spec: recipe?.spec, overrides: undefined, origin: 'recipe'}
    if (view) updateChart(view.chart.id, () => next); else setCharts([next])
    setSelection(view && count > 1 ? {chart: next.id} : undefined)
    announce(`${item.title}. Click any part of the chart to change it.`, true)
  }
  // A custom chart keeps the current data where it makes sense: Y stays Y.
  const applyForm = (view: ChartView | undefined, form: CustomForm) => {
    if (!data) return
    const current = view?.kind === 'xy' ? view.xy.y.metric : view?.metric ?? ''
    const xyMetrics = data.xy?.metrics ?? []
    const optionsFor = metricOptionsFor(form)
    const metric = optionsFor.includes(current) ? current : form === 'services' ? optionsFor.find(item => data.services(item)?.rows.length) ?? optionsFor[0] ?? '' : optionsFor.includes('load.p95_response_time') ? 'load.p95_response_time' : optionsFor[0] ?? ''
    const pair = defaultXYSelection(xyMetrics)
    const xy: XYState = form === 'xy' ? {x: view?.kind === 'xy' ? view.xy.x : pair.x, y: xyMetrics.includes(current) && current !== pair.x.metric ? {metric: current} : pair.y, options: {...defaultXYOptions, fit: true}} : view?.xy ?? defaultXY()
    const next: ChartState = {id: view?.chart.id ?? newChartId(), kind: form, metric, xy, spec: form === 'timeseries' ? smartTimeSeriesSpec(data.plot(metric)) : undefined, origin: 'custom'}
    if (view) updateChart(view.chart.id, () => next); else setCharts([next])
    setSelection({chart: next.id})
    announce(`Custom ${customForms.find(item => item.kind === form)?.label.toLowerCase()}: choose X and Y on the right.`, true)
  }
  const addChart = () => {
    if (!data || count >= maxCharts) return
    const used = new Set(views.flatMap(view => view.kind === 'timeseries' ? [view.metric] : view.kind === 'xy' ? [view.xy.y.metric] : view.kind === 'services' || view.kind === 'runlevel' ? [view.metric] : []))
    const metric = suggested.find(item => timeMetrics.includes(item) && !used.has(item)) ?? timeMetrics.find(item => !used.has(item)) ?? timeMetrics[0]
    const chart: ChartState = metric ? {id: newChartId(), kind: 'timeseries', metric, xy: defaultXY(), spec: smartTimeSeriesSpec(data.plot(metric)), origin: 'custom'} : {id: newChartId(), kind: available.find(kind => kind !== 'placement') ?? 'timeseries', metric: '', xy: defaultXY()}
    setCharts(list => [...list, chart])
    setLayout(defaultLayout(count + 1))
    if (count === 1 && settings.size === 'single') onSettings({...settings, size: 'double'})
    // Every chart shows the same variants: one legend for the image unless the user chose otherwise.
    if (count === 1 && layout === undefined) setSharedLegend(true)
    setSelection({chart: chart.id})
    if (narrow) setTab('properties')
    announce(`Chart (${letters[count]}) added${metric ? `: ${metricTitle(metric, data.provenance(metric))}` : ''}${count === 1 && settings.size === 'single' ? ' — page widened to double column' : ''}. Change its form or data on the right.`, true)
  }
  const duplicateChart = (view: ChartView) => {
    if (count >= maxCharts) return
    const copy = {...JSON.parse(JSON.stringify(view.chart)) as ChartState, id: newChartId()}
    setCharts(list => [...list.slice(0, view.index + 1), copy, ...list.slice(view.index + 1)])
    setLayout(defaultLayout(count + 1))
    setSelection({chart: copy.id})
  }
  const moveChart = (view: ChartView, by: -1 | 1) => setCharts(list => {
    const next = [...list]
    const [item] = next.splice(view.index, 1)
    next.splice(Math.max(0, Math.min(list.length - 1, view.index + by)), 0, item)
    return next
  })
  const removeChart = (view: ChartView) => {
    if (count <= 1) return
    setCharts(list => list.filter(item => item.id !== view.chart.id))
    setLayout(defaultLayout(count - 1))
    setSelection(undefined)
    announce(`Chart ${view.letter} removed.`, true)
  }

  // Check fixes (undoable like any edit).
  const applyFix = (view: ChartView, fix: CheckFix) => {
    const setSpec = setChartSpec(view)
    const firstPanel = (update: (panel: FigureSpec['panels'][number]) => FigureSpec['panels'][number]) => setSpec(current => ({...current, panels: current.panels.map((panel, index) => index ? panel : update(panel))}))
    if (fix === 'palette') setSpec(current => ({...current, palette: 'okabe-ito', series: Object.fromEntries(Object.entries(current.series).flatMap(([key, style]) => {const {color: _, ...rest} = style; void _; return Object.keys(rest).length ? [[key, rest]] : []}))}))
    else if (fix === 'one-axis') setSpec(current => ({...current, panels: current.panels.map(panel => ({...panel, metrics: panel.metrics.map(binding => ({...binding, axis: 'left' as const}))}))}))
    else if (fix === 'log-left') {if (view.kind === 'xy') setChartXY(view, state => ({...state, options: {...state.options, logY: true}})); else firstPanel(panel => ({...panel, left: {...panel.left, log: true}}))}
    else if (fix === 'seconds') firstPanel(panel => ({...panel, left: {...panel.left, unit: 's'}}))
    else if (fix === 'zero-left') firstPanel(panel => ({...panel, left: {...panel.left, range: 'zero'}}))
    else if (fix === 'legend') onSettings({...settings, legend: 'bottom'})
    else if (fix === 'slide-font') onSettings({...settings, fontSize: 12})
    else if (fix === 'hide-extra') setHidden(groups.slice(6).map(group => group.label))
    announce('Fixed.', true)
  }
  const checks = useMemo(() => views.flatMap(view => view.spec && view.model ? figureChecks(view.model, view.spec, view.settings).filter(check => !(check.id === 'legend' && sharedLegend && view.index > 0)).map(check => ({...check, view, key: `${view.chart.id}:${check.id}`, message: view.letter ? `${view.letter} ${check.message}` : check.message})) : []).filter(item => !dismissed.includes(item.key)), [views, dismissed])

  // Canvas: click to select, hover to highlight, drag annotations and the SLO line.
  const drag = useRef<{pointer: number; x: number; y: number; moved: boolean; view: ChartView; annotation?: Annotation; slo?: {factor: number}; panel: number; grab: {x?: number; y?: number}} | undefined>(undefined)
  const cellAt = (target: EventTarget | null) => (target as Element | null)?.closest?.('[data-chart]') ?? null
  const toPoints = (cell: Element, event: {clientX: number; clientY: number}, chart: string) => {
    const svg = cell.querySelector('svg.pub-figure')
    const rect = svg?.getBoundingClientRect()
    const geometry = geometries.current[chart]
    if (!rect || !rect.width || !geometry) return undefined
    const k = geometry.width / rect.width
    return {x: (event.clientX - rect.left) * k, y: (event.clientY - rect.top) * k}
  }
  const xToStored = (view: ChartView, display: number, panel: number) => view.spec?.type === 'timeseries' && view.spec.panels[panel]?.x.time === 'min' ? display * 60 : display
  const onPaperPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    const cell = cellAt(event.target)
    const view = viewOf(cell?.getAttribute('data-chart') ?? undefined)
    const target = parseTarget((event.target as Element).closest?.('[data-select]')?.getAttribute('data-select'))
    if (!cell || !view?.spec || target?.type !== 'annotation' || event.button !== 0) return
    const geo = geometries.current[view.chart.id]?.panels[target.panel]
    const at = toPoints(cell, event, view.chart.id)
    if (!geo || !at) return
    if (target.id === 'slo') {
      const line = view.model?.panels[target.panel]?.annotations.find(item => item.source === 'slo')
      const current = view.spec.auto.sloValue ?? dataSLO
      const y = line ? geo.y(line.axis, at.y) : undefined
      if (!line || !current || y === undefined) return
      drag.current = {pointer: event.pointerId, x: event.clientX, y: event.clientY, moved: false, view, slo: {factor: line.y! / current}, panel: target.panel, grab: {y: line.y! - y}}
      return
    }
    const annotation = view.spec.panels[target.panel]?.annotations.find(item => item.id === target.id)
    if (!annotation) return
    const x = geo.x(at.x)
    const y = geo.y(annotation.kind === 'hline' || annotation.kind === 'text' ? annotation.axis : 'left', at.y)
    const storedX = x === undefined ? undefined : xToStored(view, x, target.panel)
    drag.current = {pointer: event.pointerId, x: event.clientX, y: event.clientY, moved: false, view, annotation, panel: target.panel, grab: {
      x: storedX === undefined ? undefined : annotation.kind === 'xrange' ? annotation.from - storedX : annotation.kind === 'vline' || annotation.kind === 'text' ? annotation.at - storedX : undefined,
      y: y === undefined ? undefined : annotation.kind === 'hline' || annotation.kind === 'text' ? annotation.value - y : undefined
    }}
  }
  const onPaperPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const state = drag.current
    if (!state || state.pointer !== event.pointerId) {
      const cell = cellAt(event.target)
      const chart = cell?.getAttribute('data-chart')
      const key = chart ? `${chart}/${(event.target as Element).closest?.('[data-select]')?.getAttribute('data-select') ?? ''}` : ''
      if (key !== hoverKey) setHoverKey(key)
      return
    }
    if (!state.moved && Math.hypot(event.clientX - state.x, event.clientY - state.y) < 3) return
    if (!state.moved) {state.moved = true; try {event.currentTarget.setPointerCapture(event.pointerId)} catch {/* not capturable */}}
    const {view} = state
    const cell = paper.current?.querySelector(`[data-chart="${view.chart.id}"]`)
    const geo = geometries.current[view.chart.id]?.panels[state.panel]
    const at = cell ? toPoints(cell, event, view.chart.id) : undefined
    if (!geo || !at) return
    const panelModel = view.model?.panels[state.panel]
    const span = (side: 'x' | 'left' | 'right') => {const axis = side === 'x' ? panelModel?.x : side === 'left' ? panelModel?.left : panelModel?.right; return axis ? axis.scale.upper - axis.scale.lower : 1}
    const setSpec = setChartSpec(view)
    if (state.slo) {
      const line = panelModel?.annotations.find(item => item.source === 'slo')
      const y = line ? geo.y(line.axis, at.y) : undefined
      if (y === undefined || state.grab.y === undefined || !line) return
      const display = snap(y + state.grab.y, span(line.axis))
      if (display > 0) setSpec(current => ({...current, auto: {...current.auto, sloValue: Number((display / state.slo!.factor).toPrecision(6))}}))
      return
    }
    const item = state.annotation!
    const xDisplay = geo.x(at.x)
    const xStored = xDisplay === undefined || state.grab.x === undefined ? undefined : snap(xToStored(view, xDisplay, state.panel) + state.grab.x, xToStored(view, span('x'), state.panel))
    const side = item.kind === 'hline' || item.kind === 'text' ? item.axis : 'left'
    const yDisplay = geo.y(side, at.y)
    const yValue = yDisplay === undefined || state.grab.y === undefined ? undefined : snap(yDisplay + state.grab.y, span(side))
    const next: Annotation = item.kind === 'hline' ? {...item, value: yValue ?? item.value}
      : item.kind === 'vline' ? {...item, at: xStored ?? item.at}
        : item.kind === 'xrange' ? {...item, from: xStored ?? item.from, to: xStored === undefined ? item.to : xStored + (item.to - item.from)}
          : {...item, at: xStored ?? item.at, value: yValue ?? item.value}
    setSpec(currentSpec => ({...currentSpec, panels: currentSpec.panels.map((panel, index) => index === state.panel ? {...panel, annotations: panel.annotations.map(entry => entry.id === item.id ? next : entry)} : panel)}))
  }
  const onPaperPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const state = drag.current
    drag.current = undefined
    if (state?.moved) {
      try {event.currentTarget.releasePointerCapture(event.pointerId)} catch {/* released */}
      select({chart: state.view.chart.id, target: {type: 'annotation', panel: state.panel, id: state.annotation?.id ?? 'slo'}})
      suppressClick.current = true
    }
  }
  const suppressClick = useRef(false)
  const onPaperClick = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (suppressClick.current) {suppressClick.current = false; return}
    const cell = cellAt(event.target)
    const view = viewOf(cell?.getAttribute('data-chart') ?? undefined)
    if (!view) {select(undefined); return}
    const target = parseTarget((event.target as Element).closest?.('[data-select]')?.getAttribute('data-select'))
    select({chart: view.chart.id, target: target && (view.spec || target.type === 'title') ? target : undefined})
  }

  // ---------------------------------------------------------------------------------------
  // Edits shared by Layers and Properties.
  const labelOverride = (view: ChartView, key: 'title' | 'x' | 'y', value: string | undefined) => setOverrides(view, {...view.chart.overrides, [key]: value})
  const setSeriesLabel = (view: ChartView, label: string, value: string | undefined) => {const next = {...view.chart.overrides?.series}; if (value === undefined) delete next[label]; else next[label] = value; setOverrides(view, {...view.chart.overrides, series: next})}
  const toggleGroup = (label: string) => setHidden(list => list.includes(label) ? list.filter(item => item !== label) : [...list, label])
  const setAxis = (view: ChartView, panel: number, side: 'x' | 'left' | 'right', next: AxisSpec | XAxisSpec) => {
    const before = view.spec?.panels[panel]?.[side]
    if (view.kind === 'xy' && side === 'x' && before && next.log !== before.log) setChartXY(view, state => ({...state, options: {...state.options, logX: next.log}}))
    if (view.kind === 'xy' && side === 'left' && before && next.log !== before.log) setChartXY(view, state => ({...state, options: {...state.options, logY: next.log}}))
    // The time format is shared by every panel of the chart (one X axis family).
    setChartSpec(view)(current => ({...current, panels: current.panels.map((item, index) => index === panel ? {...item, [side]: next} : side === 'x' && view.kind === 'timeseries' ? {...item, x: {...item.x, time: (next as XAxisSpec).time}} : item)}))
  }
  const seriesAxis = (view: ChartView, series: ModelSeries, side: AxisSide) => setChartSpec(view)(current => ({...current, panels: current.panels.map((panel, index) => {
    const list = index === 0 && !panel.metrics.length ? [{metric: view.metric, axis: 'left' as const}] : panel.metrics
    return list.some(binding => binding.metric === series.metric) ? {...panel, metrics: list.map(binding => binding.metric === series.metric ? {...binding, axis: side} : binding)} : panel
  })}))
  const moveSeries = (view: ChartView, key: string, by: -1 | 1) => {
    const keys = view.model?.legend.map(item => item.series.key) ?? []
    const index = keys.indexOf(key)
    const next = [...keys]
    next.splice(index, 1)
    next.splice(Math.max(0, Math.min(keys.length - 1, index + by)), 0, key)
    setChartSpec(view)(current => ({...current, order: next}))
  }
  const addNote = (view: ChartView, kindOfNote: Annotation['kind']) => {
    if (!view.spec || !view.model) return
    const panel = view.model.panels[0]
    const axis = panel.left ?? panel.right
    const at = {x: xToStored(view, panel.x.scale.lower + (panel.x.scale.upper - panel.x.scale.lower) * .5, 0), x2: xToStored(view, panel.x.scale.lower + (panel.x.scale.upper - panel.x.scale.lower) * .65, 0), y: axis ? axis.scale.lower + (axis.scale.upper - axis.scale.lower) * .75 : 0, axis: panel.left ? 'left' as const : 'right' as const}
    const item = newAnnotation(kindOfNote, at)
    setChartSpec(view)(current => ({...current, panels: current.panels.map((entry, index) => index === 0 ? {...entry, annotations: [...entry.annotations, item]} : entry)}))
    setAddingNote(undefined)
    select({chart: view.chart.id, target: {type: 'annotation', panel: 0, id: item.id}})
  }
  const removeNote = (view: ChartView, panel: number, noteId: string) => {
    setChartSpec(view)(current => ({...current, panels: current.panels.map((entry, index) => index === panel ? {...entry, annotations: entry.annotations.filter(item => item.id !== noteId)} : entry)}))
    select({chart: view.chart.id})
    announce('Annotation deleted.', true)
  }
  const ySuffix = (view: ChartView, panel: number) => (side: AxisSide) => {const axis = side === 'left' ? view.model?.panels[panel]?.left : view.model?.panels[panel]?.right; return axis ? unitSuffix(axis.unit, axis.spec.unit) : ''}

  // ---------------------------------------------------------------------------------------
  // Layers: the outline of the image.
  const chartRows = (view: ChartView): LayerRow[] => {
    const rows: LayerRow[] = []
    const key = (target: string) => `${view.chart.id}/${target}`
    if (!isPublication(view.index)) return rows
    const current = currentOf(view)
    const {spec, model} = view
    rows.push({key: key('title'), label: 'Title', depth: 2, icon: layerIcons.title, selectable: true, eye: {visible: settings.showTitle, label: settings.showTitle ? 'Hide the title' : 'Show the title', onToggle: () => onSettings({...settings, showTitle: !settings.showTitle})}, dim: !settings.showTitle})
    rows.push({key: key('h-axes'), label: 'Axes', depth: 2, heading: true})
    if (spec && model && !model.empty) model.panels.forEach(panel => {
      const suffix = model.panels.length > 1 ? ` ${panel.letter ?? `(${panel.index + 1})`}` : ''
      rows.push({key: key(`axis:x:${panel.index}`), label: `X axis${suffix}`, depth: 2, icon: layerIcons.axis, selectable: true})
      if (panel.left) rows.push({key: key(`axis:left:${panel.index}`), label: `${panel.right ? 'Left Y axis' : 'Y axis'}${suffix}`, depth: 2, icon: layerIcons.axis, selectable: true})
      if (panel.right) rows.push({key: key(`axis:right:${panel.index}`), label: `Right Y axis${suffix}`, depth: 2, icon: layerIcons.axis, selectable: true})
    })
    else {
      if (current?.labels.x !== undefined) rows.push({key: key('axis:x:0'), label: 'X axis', depth: 2, icon: layerIcons.axis, selectable: true})
      if (current?.labels.y !== undefined || view.metricOptions.length) rows.push({key: key('axis:left:0'), label: 'Y axis', depth: 2, icon: layerIcons.axis, selectable: true})
    }
    rows.push({key: key('h-series'), label: 'Series', depth: 2, heading: true})
    if (spec && model) {
      const seen = new Set<string>()
      model.legend.forEach(({series}) => {seen.add(series.group); rows.push({key: key(`series:${series.key}`), label: series.label, depth: 2, icon: <SeriesSwatch series={series} />, selectable: true, eye: groups.length > 1 ? {visible: true, label: `Hide ${series.group}`, onToggle: () => toggleGroup(series.group)} : undefined})})
      groups.filter(group => !seen.has(group.label) && hidden.includes(group.label)).forEach(group => rows.push({key: key(`hidden:${group.label}`), label: group.label, depth: 2, icon: <span className="ks-dot" style={{background: group.color}} />, dim: true, eye: {visible: false, label: `Show ${group.label}`, onToggle: () => toggleGroup(group.label)}}))
    } else groups.forEach(group => rows.push({key: key(`group:${group.label}`), label: view.chart.overrides?.series?.[group.label] ?? group.label, depth: 2, icon: <span className="ks-dot" style={{background: group.color}} />, selectable: true, dim: !shown(group.label), eye: groups.length > 1 ? {visible: shown(group.label), label: `${shown(group.label) ? 'Hide' : 'Show'} ${group.label}`, onToggle: () => toggleGroup(group.label)} : undefined}))
    const legendOff = view.settings.legend === 'none'
    rows.push({key: key('legend'), label: 'Legend', depth: 2, icon: layerIcons.legend, selectable: true, dim: legendOff, eye: {visible: !legendOff, label: legendOff ? 'Show the legend' : 'Hide the legend', onToggle: () => {if (sharedLegend && view.index > 0) setSharedLegend(false); else onSettings({...settings, legend: settings.legend === 'none' ? 'bottom' : 'none'})}}})
    if (spec && model) {
      const adding = addingNote === view.chart.id
      rows.push({key: key('h-annotations'), label: 'Lines and labels', depth: 2, heading: true, action: !model.empty && <button type="button" className="ks-icon-button ks-small" aria-label="Add annotation" aria-expanded={adding} title="Add a threshold, range, line or label" onClick={() => setAddingNote(adding ? undefined : view.chart.id)}>{layerIcons.plus}</button>,
        menu: adding && <div className="ks-add-menu" role="group" aria-label="Annotation to add">{(['hline', 'xrange', 'vline', 'text'] as const).map(item => <button key={item} type="button" className="ks-button" onClick={() => addNote(view, item)}>{annotationNames[item]}</button>)}</div>})
      if (spec.type === 'timeseries') {
        const sloShown = model.panels.some(panel => panel.annotations.some(item => item.source === 'slo'))
        if (hasSLO && model.panels.some(panel => panel.metrics.some(item => /^load\.(p\d+|mean)_response_time$/.test(item)))) rows.push({key: key('annotation:0:slo'), label: 'SLO line', depth: 2, icon: layerIcons.annotation, selectable: sloShown, dim: !spec.auto.slo, eye: {visible: spec.auto.slo, label: spec.auto.slo ? 'Hide the SLO line' : 'Show the SLO line', onToggle: () => setChartSpec(view)(current => ({...current, auto: {...current.auto, slo: !current.auto.slo}}))}})
        if (data?.profile) {
          rows.push({key: key('annotation:0:phase'), label: 'Load phases', depth: 2, icon: layerIcons.annotation, selectable: spec.auto.phases, dim: !spec.auto.phases, eye: {visible: spec.auto.phases, label: spec.auto.phases ? 'Hide load phases' : 'Show load phases', onToggle: () => setChartSpec(view)(current => ({...current, auto: {...current.auto, phases: !current.auto.phases}}))}})
          rows.push({key: key('annotation:0:warmup'), label: 'Warm-up end', depth: 2, icon: layerIcons.annotation, selectable: spec.auto.warmup, dim: !spec.auto.warmup, eye: {visible: spec.auto.warmup, label: spec.auto.warmup ? 'Hide warm-up end' : 'Show warm-up end', onToggle: () => setChartSpec(view)(current => ({...current, auto: {...current.auto, warmup: !current.auto.warmup}}))}})
        }
      }
      spec.panels.forEach((panel, index) => panel.annotations.forEach(item => rows.push({key: key(`annotation:${index}:${item.id}`), label: item.kind === 'text' ? item.text || 'Text label' : item.label && item.label !== 'Threshold {value}' ? `${annotationNames[item.kind]} · ${item.label.replaceAll('{value}', '').trim()}` : annotationNames[item.kind], depth: 2, icon: layerIcons.annotation, selectable: true})))
    }
    return rows
  }
  const expandedChart = selected?.chart ?? views[0]?.chart.id
  const chartLabel = (view: ChartView) => `${view.letter ? `Chart ${view.letter}` : 'Chart'}: ${titles[view.index]}`
  const layerRow = (row: LayerRow) => <li key={row.key} className="ks-layer-row" data-depth={row.depth} data-heading={row.heading || undefined} data-dim={row.dim || undefined}
    onPointerEnter={() => {if (row.selectable) setHoverKey(row.key)}} onPointerLeave={() => setHoverKey('')}>
    {row.heading ? <span className="ks-layer-heading">{row.label}</span>
      : <button type="button" className="ks-layer" aria-current={selectedKey === row.key || undefined} disabled={!row.selectable} onClick={() => select(parseKey(row.key))}>{row.icon}<span>{row.label}</span></button>}
    {row.eye && <button type="button" className="ks-icon-button ks-small ks-eye" aria-label={row.eye.label} title={row.eye.label} aria-pressed={!row.eye.visible} onClick={row.eye.onToggle}><EyeIcon off={!row.eye.visible} /></button>}
    {row.action}
    {row.menu}
  </li>
  const layers = <nav className="ks-layers" aria-label="Layers">
    <div className="ks-panel-head"><h3>Layers</h3></div>
    <ul>
      <li className="ks-layer-row" data-depth={0}>
        <button type="button" className="ks-layer" aria-current={!selected || undefined} onClick={() => select(undefined)}>{layerIcons.figure}<span>{single ? 'Figure' : 'Page'}</span></button>
      </li>
      {views.map(view => {
        const open = view.chart.id === expandedChart
        const rows = open ? chartRows(view) : []
        return <li key={view.chart.id} className="ks-chart-group" data-open={open || undefined}>
          <div className="ks-layer-row" data-depth={1} onPointerEnter={() => setHoverKey(`${view.chart.id}/`)} onPointerLeave={() => setHoverKey('')}>
            <button type="button" className="ks-layer ks-chart-layer" aria-current={selectedKey === `${view.chart.id}/` || undefined} aria-label={chartLabel(view)} onClick={() => select({chart: view.chart.id})}>
              {view.kind && <KindIcon kind={view.kind} />}<span><strong>{view.letter ? `Chart ${view.letter}` : 'Chart'}</strong><small>{titles[view.index]}</small></span>
            </button>
          </div>
          {rows.length > 0 && <ul aria-label={view.letter ? `Chart ${view.letter}` : 'Chart'}>{rows.map(layerRow)}</ul>}
        </li>
      })}
      {data && publication && count < maxCharts && <li className="ks-layer-row" data-depth={1}>
        <button type="button" className="ks-add-chart" onClick={addChart} title="Add another chart to this image">{layerIcons.plus}<span>Add chart</span></button>
      </li>}
      {publication && <li className="ks-layer-row" data-depth={0}>
        <button type="button" className="ks-layer" aria-expanded={drawer} aria-controls={`${id}-caption-body`} onClick={() => {const next = !drawer; setDrawer(next); if (next) setTimeout(() => captionRef.current?.focus(), 0)}}>{layerIcons.caption}<span>Caption</span></button>
      </li>}
    </ul>
    {!publication && data && single && <p className="ks-hint ks-pad">This figure keeps its on-screen layout; it has no editable layers.</p>}
  </nav>

  // ---------------------------------------------------------------------------------------
  // Properties of the selection (a chart, an element of a chart) or of the page.
  const formChips = (view: ChartView) => {
    const value = (customForms.some(form => form.kind === view.kind) ? view.kind : '') as CustomForm
    return <div className="ks-field">
      <Chips legend="Chart form" className="ks-form-chips" name={`${id}-${view.chart.id}-form`} value={value} onChange={form => applyForm(view, form)} choices={customForms.filter(form => forms.includes(form.kind)).map(form => ({value: form.kind, label: form.label, hint: form.hint}))} />
      {!value && view.kind && <p className="ks-hint">{kindLabels[view.kind]} is a fixed form. Pick one above to choose X and Y freely.</p>}
    </div>
  }
  const dataFields = (view: ChartView) => {
    if (!data) return null
    if (view.kind === 'timeseries') return <>
      <div className="ks-field"><span className="ks-label">X</span><p className="ks-static">Time (measured window)</p></div>
      <AxisMetrics spec={view.spec!} setSpec={setChartSpec(view)} panel={0} side="left" metrics={view.metricOptions} provenance={data.provenance} primary={view.metric} onPrimary={metric => setChartMetric(view, metric)} label="Y" />
    </>
    if (view.kind === 'xy' && data.xy) return <>
      <XYAxisPicker label="X" source={data.xy} value={view.xy.x} onChange={next => next && setChartXY(view, state => ({...state, x: next}))} />
      <XYAxisPicker label="Y" source={data.xy} value={view.xy.y} onChange={next => next && setChartXY(view, state => ({...state, y: next}))} />
    </>
    if (view.kind === 'services' || view.kind === 'runlevel') return <>
      <div className="ks-field"><span className="ks-label">X</span><p className="ks-static">{view.kind === 'services' ? 'Services' : 'Variants (every repetition)'}</p></div>
      <SelectField label="Y" value={view.metric} onChange={metric => setChartMetric(view, metric)}>{view.metricOptions.map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}</SelectField>
    </>
    return null
  }
  const propertiesFor = (): {title: string; kind: string; body: ReactNode} => {
    const view = selectedView
    const target = selected?.target
    if (view && target) {
      const current = currentOf(view)
      const overrides = view.chart.overrides ?? {}
      const {spec, model} = view
      if (target.type === 'title') return {kind: `Text${view.letter ? ` · chart ${view.letter}` : ''}`, title: 'Title', body: <>
        <Toggle label="Show titles in the figure" checked={settings.showTitle} onChange={showTitle => onSettings({...settings, showTitle})} />
        {current && <LabelField label="Title" value={overrides.title} fallback={current.title} onChange={value => labelOverride(view, 'title', value)} />}
        <p className="ks-hint">Papers usually leave the title to the caption. The file name comes from the figure name in the top bar.</p>
      </>}
      if (target.type === 'legend') return {kind: 'Legend', title: 'Legend', body: <>
        <Chips legend="Position" name={`${id}-legend`} value={settings.legend} onChange={legend => onSettings({...settings, legend})} choices={[{value: 'top', label: 'Top'}, {value: 'right', label: 'Right'}, {value: 'bottom', label: 'Bottom'}, {value: 'inside', label: 'Inside'}, {value: 'none', label: 'None'}]} />
        {count > 1 && <Toggle label="One shared legend (in chart a)" checked={sharedLegend} onChange={setSharedLegend} hint="Drawn once when every chart shows the same variants" />}
        <p className="ks-hint">Rename or restyle an entry by selecting its series.</p>
      </>}
      if (target.type === 'group') {
        const label = target.label
        return {kind: 'Series', title: overrides.series?.[label] ?? label, body: <>
          {current?.labels.series.includes(label) && <LabelField label="Label" value={overrides.series?.[label]} fallback={label} onChange={value => setSeriesLabel(view, label, value)} />}
          {groups.length > 1 && <Toggle label="Shown in the figure" checked={shown(label)} onChange={() => toggleGroup(label)} />}
          <p className="ks-hint">Colours follow the variant. Line and scatter charts allow per-series styling.</p>
        </>}
      }
      if (target.type === 'axis' && !spec) return {kind: 'Axis', title: target.side === 'x' ? 'X axis' : 'Y axis', body: <>
        {target.side === 'left' && view.metricOptions.length > 0 && data && <SelectField label="Metric" value={view.metric} onChange={metric => setChartMetric(view, metric)}>{view.metricOptions.map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}</SelectField>}
        {target.side === 'x' && <p className="ks-hint">{view.kind === 'services' ? 'One position per service.' : view.kind === 'runlevel' ? 'One position per variant.' : 'Fixed by this chart form.'} Switch the chart to a custom form to choose X freely.</p>}
        {target.side === 'x' && current?.labels.x !== undefined && <LabelField label="X axis label" value={overrides.x} fallback={current.labels.x} onChange={value => labelOverride(view, 'x', value)} />}
        {target.side === 'left' && current?.labels.y !== undefined && <LabelField label="Y axis label" value={overrides.y} fallback={current.labels.y} onChange={value => labelOverride(view, 'y', value)} />}
      </>}
      if (target.type === 'axis' && spec && model && data) {
        const panel = model.panels[Math.min(target.panel, model.panels.length - 1)]
        const panelSpec = spec.panels[Math.min(target.panel, spec.panels.length - 1)]
        const side = target.side
        const title = side === 'x' ? 'X axis' : side === 'right' ? 'Right Y axis' : panel.right ? 'Left Y axis' : 'Y axis'
        const labelField = side === 'x' ? <LabelField label="Label" value={overrides.x} fallback={current?.labels.x ?? panel.x.defaultLabel} onChange={value => labelOverride(view, 'x', value)} />
          : side === 'left' ? <LabelField label="Label" value={overrides.y} fallback={current?.labels.y ?? panel.left?.defaultLabel ?? ''} onChange={value => labelOverride(view, 'y', value)} />
            : <LabelField label="Label" value={panelSpec.right.label} fallback={panel.right?.defaultLabel ?? ''} onChange={value => setAxis(view, target.panel, 'right', {...panelSpec.right, label: value})} />
        const unit = side === 'x' ? (spec.type === 'xy' ? panel.x.unit : undefined) : side === 'left' ? panel.left?.unit : panel.right?.unit
        return {kind: `Axis${view.letter ? ` · chart ${view.letter}` : ''}`, title: model.panels.length > 1 ? `${title} ${panel.letter ?? ''}`.trim() : title, body: <>
          {spec.type === 'timeseries' && side === 'x' && <div className="ks-field"><span className="ks-label">Data</span><p className="ks-static">Time (measured window). <button type="button" className="ks-link" onClick={() => applyForm(view, 'xy')}>Plot against a metric instead</button></p></div>}
          {spec.type === 'timeseries' && side !== 'x' && <AxisMetrics spec={spec} setSpec={setChartSpec(view)} panel={target.panel} side={side} metrics={view.metricOptions} provenance={data.provenance} primary={view.metric} onPrimary={metric => setChartMetric(view, metric)} />}
          {spec.type === 'timeseries' && side === 'left' && !panel.right && view.metricOptions.length > 1 && <select aria-label="Add a metric on a right Y axis" className="ks-add-select" value="" onChange={event => {const value = event.target.value; if (value) {setChartSpec(view)(current => ({...current, panels: current.panels.map((entry, index) => index === target.panel ? {...entry, metrics: [...(index === 0 && !entry.metrics.length ? [{metric: view.metric, axis: 'left' as const}] : entry.metrics), {metric: value, axis: 'right' as const}]} : entry)})); select({chart: view.chart.id, target: {type: 'axis', side: 'right', panel: target.panel}})}}}>
            <option value="">+ Add a metric on a right Y axis…</option>
            {view.metricOptions.filter(item => item !== view.metric).map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}
          </select>}
          {spec.type === 'xy' && data.xy && side === 'x' && <XYAxisPicker label="Metric" source={data.xy} value={view.xy.x} onChange={next => next && setChartXY(view, state => ({...state, x: next}))} />}
          {spec.type === 'xy' && data.xy && side === 'left' && <XYAxisPicker label="Metric" source={data.xy} value={view.xy.y} onChange={next => next && setChartXY(view, state => ({...state, y: next}))} />}
          {spec.type === 'xy' && data.xy && side !== 'x' && <SecondY spec={spec} setSpec={setChartSpec(view)} source={data.xy} />}
          {labelField}
          <AxisFields key={`${target.panel}-${side}`} axis={panelSpec[side]} onChange={next => setAxis(view, target.panel, side, next)} unit={unit} time={side === 'x' && spec.type === 'timeseries'} />
        </>}
      }
      if (target.type === 'series' && spec && model) {
        const legend = model.legend.map(item => item.series)
        const item = legend.find(entry => entry.key === target.key)!
        const index = legend.indexOf(item)
        return {kind: `Series${view.letter ? ` · chart ${view.letter}` : ''}`, title: item.label, body: <SeriesProperties key={item.key} series={item} spec={spec} setSpec={setChartSpec(view)} label={overrides.series?.[item.defaultLabel] ?? item.defaultLabel} onLabel={value => setSeriesLabel(view, item.defaultLabel, value)}
          axis={spec.type === 'timeseries' ? item.axis : undefined} onAxis={side => seriesAxis(view, item, side)} onMove={by => moveSeries(view, item.key, by)} first={index === 0} last={index === legend.length - 1} />}
      }
      if (target.type === 'annotation' && spec && model) {
        const auto = autoAnnotations[target.id]
        if (auto?.key === 'slo') {
          const line = model.panels[target.panel]?.annotations.find(entry => entry.source === 'slo')
          const axis = line?.axis === 'right' ? model.panels[target.panel].right : model.panels[target.panel].left
          const suffix = axis ? unitSuffix(axis.unit, axis.spec.unit) : 'ms'
          const factor = line && (spec.auto.sloValue ?? dataSLO) ? line.y! / (spec.auto.sloValue ?? dataSLO)! : 1
          const value = line?.y
          return {kind: 'Threshold', title: 'SLO line', body: <>
            <Toggle label="Show the SLO line" checked={spec.auto.slo} onChange={value => setChartSpec(view)(current => ({...current, auto: {...current.auto, slo: value}}))} />
            <div className="ks-field">
              <label className="ks-label" htmlFor={`${id}-slo`}>Threshold ({suffix})</label>
              <NumberInput id={`${id}-slo`} min={0} value={value === undefined ? undefined : Number(value.toPrecision(6))} onChange={next => {if (next !== undefined && next > 0) setChartSpec(view)(current => ({...current, auto: {...current.auto, sloValue: Number((next / factor).toPrecision(6))}}))}} />
              <p className="ks-hint">{spec.auto.sloValue !== undefined ? <>Experiment SLO: {formatMetric(dataSLO, 'milliseconds')}. <button type="button" className="ks-link" onClick={() => setChartSpec(view)(current => {const {sloValue: _, ...rest} = current.auto; void _; return {...current, auto: rest}})}>Use the experiment SLO</button></> : 'From the experiment. Type a value or drag the line on the figure.'}</p>
            </div>
            <p className="ks-hint">For a threshold on another metric or the right axis, add a threshold line with + in Lines and labels.</p>
          </>}
        }
        if (auto) return {kind: 'Annotation', title: auto.label, body: <>
          <Toggle label={`Show ${auto.label.toLowerCase()}`} checked={spec.auto[auto.key]} onChange={value => setChartSpec(view)(current => ({...current, auto: {...current.auto, [auto.key]: value}}))} />
          <p className="ks-hint">Drawn automatically from the experiment’s load profile.</p>
        </>}
        const item = spec.panels[target.panel].annotations.find(entry => entry.id === target.id)!
        return {kind: item.kind === 'hline' ? 'Threshold' : 'Annotation', title: annotationNames[item.kind], body: <AnnotationFields key={item.id} item={item} xUnit={spec.type === 'xy' ? model.panels[0]?.x.label ?? 'X' : 's'} yUnit={ySuffix(view, target.panel)} onRemove={() => removeNote(view, target.panel, item.id)}
          onChange={patch => setChartSpec(view)(current => ({...current, panels: current.panels.map((panel, index) => index === target.panel ? {...panel, annotations: panel.annotations.map(entry => entry.id === item.id ? {...entry, ...patch} as Annotation : entry)} : panel)}))} />}
      }
    }
    if (view) {
      // A chart: its form, data and chart-level style.
      const {spec, model} = view
      return {kind: view.letter ? `Chart ${view.letter}` : 'Chart', title: titles[view.index], body: <>
        <Group title="Form and data">
          {formChips(view)}
          {dataFields(view)}
          <p className="ks-hint">Starting points from the chart switcher stay fully editable: change X, Y and every style.</p>
        </Group>
        {spec?.type === 'timeseries' && model && <Group title="Uncertainty">
          <SelectField label="Repeated runs" value={spec.runMode} onChange={value => setChartSpec(view)(current => ({...current, runMode: value as RunMode}))} hint={model.runModeFallback ? 'Individual repetitions are not available here; showing the mean with its 95 % CI.' : !model.legend.some(item => item.series.n > 1) ? 'One run per series: nothing to summarise.' : runNames[spec.runMode].hint}>
            {(Object.keys(runNames) as RunMode[]).map(mode => <option key={mode} value={mode}>{runNames[mode].label}</option>)}
          </SelectField>
          {spec.layout.grid !== '1x1' && <Chips legend="Panels inside this chart" name={`${id}-grid`} value={spec.layout.grid} onChange={(grid: PanelGrid) => setChartSpec(view)(current => ({...current, layout: {...current.layout, grid}}))} choices={[{value: '1x1', label: '1'}, {value: '1x2', label: '1 × 2'}, {value: '2x1', label: '2 × 1'}, {value: '2x2', label: '2 × 2'}]} />}
        </Group>}
        {spec?.type === 'xy' && <Group title="Scatter">
          <div className="ks-toggles">
            <Toggle label="Fit line and correlation" checked={view.xy.options.fit} onChange={fitLine => setChartXY(view, state => ({...state, options: {...state.options, fit: fitLine}}))} />
            <Toggle label="Connect each run in time order" checked={view.xy.options.path} onChange={path => setChartXY(view, state => ({...state, options: {...state.options, path}}))} />
          </div>
        </Group>}
        {spec && <Group title="Colour">
          <PaletteEditor spec={spec} setSpec={setChartSpec(view)} />
          <Chips legend="Grid" name={`${id}-gridlines`} value={spec.grid} onChange={grid => setChartSpec(view)(current => ({...current, grid}))} choices={[{value: 'none', label: 'None'}, {value: 'major', label: 'Major'}, {value: 'minor', label: 'Minor', hint: 'Major and minor gridlines'}]} />
        </Group>}
        {count > 1 && <Group title="Arrange">
          <div className="ks-actions">
            <button type="button" className="ks-button" disabled={view.index === 0} onClick={() => moveChart(view, -1)}>Move earlier</button>
            <button type="button" className="ks-button" disabled={view.index === count - 1} onClick={() => moveChart(view, 1)}>Move later</button>
          </div>
        </Group>}
        <div className="ks-actions">
          {count < maxCharts && <button type="button" className="ks-button" onClick={() => duplicateChart(view)}>Duplicate chart</button>}
          {count > 1 && <button type="button" className="ks-button ks-danger ks-push" onClick={() => removeChart(view)}>Remove chart</button>}
        </div>
        {spec && <MoreGroup title="Presets and spec">
          <PresetsEditor presets={presets} onApply={preset => applyPreset(view, preset)} onSave={presetName => storePresets([...presets.filter(item => item.name !== presetName && !builtInPresets.some(builtIn => builtIn.name === presetName)), {name: presetName, style: settings, strokeScale: spec.strokeScale, grid: spec.grid}])} onDelete={presetName => storePresets(presets.filter(item => item.name !== presetName))} />
          <SpecActions onCopy={() => void copySpec(view)} onPaste={json => void pasteSpec(view, json)} onReset={() => {updateChart(view.chart.id, chart => ({...chart, spec: undefined, overrides: undefined})); announce('Chart reset.', true)}} />
        </MoreGroup>}
      </>}
    }
    // The page.
    return {kind: single ? 'Figure' : 'Page', title: single ? titles[0] ?? 'Figure' : `${count} charts`, body: !publication && single ? <p className="ks-hint">This figure keeps its on-screen layout; size and type apply to publication figures.</p> : <>
      {checks.length > 0 && <section className="ks-check" aria-label="Figure check">
        <h3>Figure check</h3>
        <ul>
          {checks.map(check => <li key={check.key} data-level={check.level}>
            <p><span className="visually-hidden">{check.level === 'warning' ? 'Warning: ' : 'Suggestion: '}</span>{check.message}</p>
            <div className="ks-actions">
              {check.fix && <button type="button" className="ks-button" onClick={() => applyFix(check.view, check.fix!.id)}>{check.fix.label}</button>}
              <button type="button" className="ks-link" onClick={() => setDismissed(list => [...list, check.key])}>Dismiss</button>
            </div>
          </li>)}
        </ul>
      </section>}
      <Group title="Size">
        <Chips legend="Page size" hideLegend name={`${id}-size`} value={settings.size} onChange={value => onSettings({...settings, size: value})} choices={sizeChoices} />
        <p className="ks-hint">{sizeChoices.find(choice => choice.value === settings.size)?.hint}{publication ? ` · image ${inches(pageWidth)} × ${inches(pageHeight)} in` : ''}</p>
        {settings.size === 'custom' && <div className="ks-two">
          <Stepper label="Width" unit="in" value={settings.customWidth} min={customLimits.min} max={customLimits.max} step={.25} onChange={customWidth => onSettings({...settings, customWidth})} />
          <Stepper label="Plot height" unit="in" value={settings.customHeight} min={customLimits.min} max={customLimits.max} step={.25} onChange={customHeight => onSettings({...settings, customHeight})} />
        </div>}
      </Group>
      <Group title="Text">
        <div className="ks-two">
          <Chips legend="Font" name={`${id}-font`} value={settings.font} onChange={font => onSettings({...settings, font})} choices={[{value: 'serif', label: 'Serif', hint: 'Times — most journals'}, {value: 'sans', label: 'Sans', hint: 'Helvetica — slides, posters'}]} />
          <SelectField label="Size (pt)" value={String(settings.fontSize)} onChange={value => onSettings({...settings, fontSize: Number(value)})}>{[7, 8, 9, 10, 11, 12].map(value => <option key={value} value={value}>{value} pt</option>)}</SelectField>
        </div>
      </Group>
      <Group title="Charts">
        {count > 1 ? <>
          <Chips legend="Layout" name={`${id}-layout`} value={effectiveLayout} onChange={setLayout} choices={layoutChoices(count)} />
          <Toggle label="One shared legend (in chart a)" checked={sharedLegend} onChange={setSharedLegend} hint="Drawn once when every chart shows the same variants" />
        </> : <p className="ks-hint">One chart. Add more to compare metrics side by side in the same image, with panel letters.</p>}
        {count < maxCharts && <div className="ks-actions"><button type="button" className="ks-button" onClick={addChart}>{layerIcons.plus}Add chart</button></div>}
      </Group>
      {views.some(view => !view.spec) && <Group title="Dot and band charts">
        <Chips legend="Uncertainty" name={`${id}-uncertainty`} value={settings.uncertainty} onChange={uncertainty => onSettings({...settings, uncertainty})} choices={[{value: 'none', label: 'None'}, {value: 'sd', label: '±1 SD'}, {value: 'ci', label: '95 % CI'}]} />
        <div className="ks-toggles">
          <Toggle label="Y axis from zero" checked={settings.zeroBased} onChange={zeroBased => onSettings({...settings, zeroBased})} />
          <Toggle label="Log Y axis" checked={settings.logY} onChange={logY => onSettings({...settings, logY})} />
          <Toggle label="Grayscale-safe (Okabe–Ito, dashes, markers)" checked={settings.grayscale} onChange={grayscale => onSettings({...settings, grayscale})} />
        </div>
      </Group>}
      <MoreGroup title="Reset">
        <div className="ks-actions"><button type="button" className="ks-button" onClick={() => {onSettings({...defaultFigureSettings, customWidth: settings.customWidth, customHeight: settings.customHeight}); announce('Style reset to the IEEE defaults.', true)}}>Reset page style to defaults</button></div>
      </MoreGroup>
    </>}
  }
  const properties = propertiesFor()
  const inspector = <div className="ks-properties">
    <div className="ks-panel-head"><span className="ks-kicker">{properties.kind}</span><h3 id={`${id}-properties`}>{properties.title}</h3></div>
    <section className="ks-properties-body" aria-labelledby={`${id}-properties`}>{properties.body}</section>
  </div>

  // ---------------------------------------------------------------------------------------
  // Canvas and caption drawer.
  const overlayFor = (view: ChartView) => {
    const geometry = geometries.current[view.chart.id]
    const hoverShapes = shapes.hoverChart === view.chart.id ? shapes.hover : []
    const selectedShapes = shapes.selectedChart === view.chart.id ? shapes.selected : []
    if (!geometry || (!hoverShapes.length && !selectedShapes.length)) return null
    return <svg className="ks-overlay" viewBox={`0 0 ${geometry.width} ${geometry.height}`} aria-hidden="true" focusable="false">
      {([['hover', hoverShapes], ['selected', selectedShapes]] as const).flatMap(([which, list]) => list.map((shape, index) => shape.kind === 'path'
        ? <path key={`${which}-${index}`} className={`ks-halo ${which}`} d={shape.d} />
        : <rect key={`${which}-${index}`} className={`ks-box ${which}`} x={shape.x} y={shape.y} width={shape.width} height={shape.height} rx="1.5" />))}
      {selectedShapes.flatMap((shape, index) => (shape.handles ?? []).map((handle, position) => <rect key={`h-${index}-${position}`} className="ks-handle" x={handle.x - 2.25} y={handle.y - 2.25} width="4.5" height="4.5" rx=".75" />))}
    </svg>
  }
  const hoveredChart = hover && !hover.target ? hover.chart : undefined
  const canvas = <div className="ks-canvas">
    <div className="ks-stage" ref={stage}>
      {!data ? <div className="results-placeholder" role="status"><span className="results-spinner" /><strong>Preparing figure data…</strong></div>
        : !views.length ? emptyState('No figure can be drawn from these results yet.')
          : single && !publication ? (views[0].kind === 'placement' && data.placement ? <div className="ks-paper ks-paper-free" ref={paper}>{rendered[0][0]}</div> : <div className="ks-paper-empty">{rendered[0][0]}</div>)
            : <div className="ks-paper" ref={paper} data-multi={single ? undefined : true} style={{width: `${Math.round(px(pageWidth))}px`, height: single ? undefined : `${Math.round(px(pageHeight))}px`}}
              onClick={onPaperClick} onPointerDown={onPaperPointerDown} onPointerMove={onPaperPointerMove} onPointerUp={onPaperPointerUp} onPointerLeave={() => {if (!drag.current) setHoverKey('')}}>
              {views.map(view => {
                const cell = cellOf(view)
                return <div key={view.chart.id} className="ks-cell" data-chart={view.chart.id} data-selected={selectedKey === `${view.chart.id}/` || undefined} data-hover={hoveredChart === view.chart.id || undefined}
                  style={single ? undefined : {left: px(cell.x), top: px(cell.y), width: px(cell.width)}}>
                  {view.letter && <span className="ks-letter" aria-hidden="true" style={{fontSize: px(settings.fontSize), fontFamily: fontStacks[settings.font], top: -px(settings.fontSize * 1.35)}}>{view.letter}</span>}
                  <FigureHostContext.Provider value={hosts[view.chart.id]}>{rendered[view.index][0]}</FigureHostContext.Provider>
                  {overlayFor(view)}
                </div>
              })}
            </div>}
    </div>
    {checks.length > 0 && publication && <button type="button" className="ks-check-pill" data-warning={checks.some(check => check.level === 'warning') || undefined} onClick={() => select(undefined)}>{checks.length} {checks.some(check => check.level === 'warning') ? `issue${checks.length > 1 ? 's' : ''}` : `suggestion${checks.length > 1 ? 's' : ''}`}</button>}
    {publication && <div className="ks-zoom" role="group" aria-label="Zoom">
      <span className="ks-dimensions" title="Printed size and type">{inches(pageWidth)} × {inches(pageHeight)} in · {settings.font === 'serif' ? 'Times' : 'Helvetica'} {settings.fontSize} pt</span>
      <button type="button" className="ks-icon-button ks-small" aria-label="Zoom out" aria-keyshortcuts="-" title="Zoom out (−)" onClick={() => step(-1)} disabled={scale <= .25}><svg {...svgProps}><path d="M6 12h12" /></svg></button>
      <button type="button" className="ks-zoom-value" aria-label={`Zoom ${Math.round(scale * 100)} %, show at 100 %`} title="Actual size (100 %)" onClick={() => setZoom(1)}>{Math.round(scale * 100)} %</button>
      <button type="button" className="ks-icon-button ks-small" aria-label="Zoom in" aria-keyshortcuts="+" title="Zoom in (+)" onClick={() => step(1)} disabled={scale >= 4}><svg {...svgProps}><path d="M12 6v12M6 12h12" /></svg></button>
      <button type="button" className="ks-button ks-small" aria-pressed={zoom === 'fit'} aria-keyshortcuts="0" title="Fit the image (0)" onClick={() => setZoom('fit')}>Fit</button>
    </div>}
    <div className="ks-toasts" aria-hidden={notice ? undefined : true}>
      {notice && <div className="ks-toast" data-action={notice.undo || undefined}><span aria-hidden="true">{notice.text}</span>{notice.undo && history.canUndo && <button type="button" className="ks-link" onClick={() => {setNotice(undefined); undo()}}>Undo</button>}</div>}
    </div>
  </div>
  const captionDrawer = publication && <section className="ks-drawer" data-open={drawer || undefined} aria-label="Caption">
    <div className="ks-drawer-head">
      <button type="button" className="ks-drawer-toggle" aria-expanded={drawer} aria-controls={`${id}-caption-body`} onClick={() => setDrawer(value => !value)}>
        {layerIcons.chevron}<strong>Caption</strong>{!drawer && <span className="ks-drawer-preview">{caption}</span>}
      </button>
      <button type="button" className="ks-link" onClick={() => void copyCaption()} disabled={!caption}>Copy caption</button>
    </div>
    <div id={`${id}-caption-body`} className="ks-drawer-body" hidden={!drawer}>
      <div className="ks-field">
        <div className="ks-label-row"><label className="ks-label" htmlFor={`${id}-caption`}>Caption</label>{doc.caption !== undefined && <button type="button" className="ks-link" onClick={() => setDoc(current => ({...current, caption: undefined}))}>Reset to automatic</button>}</div>
        <textarea ref={captionRef} id={`${id}-caption`} rows={3} value={caption} onChange={event => {const value = event.target.value; setDoc(current => ({...current, caption: value === autoCaption ? undefined : value}))}} />
      </div>
      {methods && <details className="ks-methods">
        <summary>Methods & provenance</summary>
        <p>{methods}</p>
        <div className="ks-actions">
          <span className="ks-hint">Embedded in the SVG description, the PDF metadata and the CSV header.</span>
          <button type="button" className="ks-button ks-push" onClick={() => setDoc(current => ({...current, caption: `${caption.trim()} ${methods}`.trim()}))}>Insert into caption</button>
        </div>
      </details>}
    </div>
  </section>

  const tabs = [{id: 'layers', label: 'Layers'}, {id: 'canvas', label: 'Figure'}, {id: 'properties', label: 'Properties'}] as const
  const onTabKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const index = tabs.findIndex(item => item.id === tab)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
    setTab(tabs[next].id)
    event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus()
  }
  const panel = (which: 'layers' | 'canvas' | 'properties', className: string, content: ReactNode, label: string) => narrow
    ? <div className={`ks-panel ${className}`} role="tabpanel" id={`${id}-${which}`} aria-labelledby={`${id}-${which}-tab`} hidden={tab !== which}>{content}</div>
    : <div className={`ks-panel ${className}`} role="region" aria-label={label}>{content}</div>

  // Shortcuts listen on the document while open (focus may rest on <body> inside the modal).
  const onKeyDown = (event: globalThis.KeyboardEvent) => {
    const target = (event.target instanceof Element ? event.target : document.body) as HTMLElement
    const typing = Boolean(target.closest?.('textarea, select, [contenteditable="true"], input:not([type="checkbox"]):not([type="radio"]):not([type="range"]):not([type="color"])'))
    const mod = event.metaKey || event.ctrlKey
    const key = event.key.toLowerCase()
    if (mod && !event.altKey && (key === 'z' || key === 'y')) {
      if (typing) return
      event.preventDefault()
      if (key === 'y' || event.shiftKey) redo(); else undo()
      return
    }
    if (mod || event.altKey || typing) return
    if (key === 'e' && data && publication) {event.preventDefault(); setExportOpen(true)}
    else if (event.key === '?') {event.preventDefault(); setHelp(value => !value)}
    else if (publication && (event.key === '+' || event.key === '=')) {event.preventDefault(); step(1)}
    else if (publication && event.key === '-') {event.preventDefault(); step(-1)}
    else if (publication && event.key === '0') {event.preventDefault(); setZoom('fit')}
  }
  const keyHandler = useRef(onKeyDown)
  keyHandler.current = onKeyDown
  useEffect(() => {
    if (!open) return
    const listener = (event: globalThis.KeyboardEvent) => {if (!event.defaultPrevented) keyHandler.current(event)}
    document.addEventListener('keydown', listener)
    return () => document.removeEventListener('keydown', listener)
  }, [open])

  const savedText = saved === false ? 'Not saved: browser storage is unavailable' : 'Every change is saved in this browser'
  return <dialog ref={dialog} className="figure-studio" aria-labelledby={`${id}-title`} onCancel={event => {event.preventDefault(); if (help) setHelp(false); else if (selected?.target) select({chart: selected.chart}); else if (selected) select(undefined); else onClose()}}>
    {open && <div className="ks-shell" data-narrow={narrow || undefined}>
      <header className="ks-topbar">
        <div className="ks-brand"><KubeStudioMark size={24} /><h2 id={`${id}-title`}>{studioName}</h2></div>
        {data && <FigureSwitcher items={items.filter(item => item.kind !== 'placement' || count === 1)} active={activeView?.kind} prefix={activeView?.letter ? `${activeView.letter} ` : ''} label={activeView?.chart.origin === 'custom' ? `Custom ${customForms.find(form => form.kind === activeView.kind)?.label.toLowerCase() ?? 'chart'}` : undefined} forms={forms} onPick={item => applyRecipe(activeView, item)} onCustom={form => applyForm(activeView, form)} />}
        <div className="ks-name">
          {publication && <><label htmlFor={`${id}-name`} className="visually-hidden">Figure name</label>
            <input id={`${id}-name`} type="text" value={name} placeholder="Figure name" title="Figure name — also the exported file name" onChange={event => {const value = event.target.value; setDoc(current => ({...current, name: value === autoName ? undefined : value}))}} /></>}
        </div>
        <div className="ks-tools">
          <div className="ks-history" role="group" aria-label="History">
            <button type="button" className="ks-icon-button" aria-label="Undo" aria-keyshortcuts="Control+Z Meta+Z" title="Undo (Ctrl/⌘ Z)" disabled={!history.canUndo} onClick={undo}><UndoIcon /></button>
            <button type="button" className="ks-icon-button" aria-label="Redo" aria-keyshortcuts="Control+Shift+Z Meta+Shift+Z" title="Redo (Ctrl/⌘ Shift Z)" disabled={!history.canRedo} onClick={redo}><UndoIcon redo /></button>
          </div>
          {storageKey && <span className={`ks-saved ${saved === false ? 'failed' : ''}`} title={savedText}><svg {...svgProps} width={14} height={14} strokeWidth={2}>{saved === false ? <path d="M12 8v5M12 16.5v.5" /> : <path d="m5 12 5 5 9-10" />}</svg><span>{saved === false ? 'Not saved' : 'Saved'}</span><span className="visually-hidden">: {savedText}</span></span>}
          <button type="button" className="ks-icon-button" aria-label="Keyboard shortcuts" aria-keyshortcuts="?" title="Keyboard shortcuts (?)" aria-expanded={help} onClick={() => setHelp(value => !value)}><span className="ks-help-glyph" aria-hidden="true">?</span></button>
          {data && publication && <ExportPopover open={exportOpen} onOpenChange={setExportOpen} format={format} onFormat={action => {setFormat(action); rememberFormat(action)}} busy={Boolean(busy)} csv={publication} code={Boolean(codeView)}
            page={{width: pageWidth, height: pageHeight, font: settings.font === 'serif' ? 'Times' : 'Helvetica', fontSize: settings.fontSize}} base={fileName}
            onExport={action => void exportAs(action)} onCopy={action => void copyFigure(action)} onSave={onSave ? () => void save() : undefined} saving={busy === 'save'} />}
          <button type="button" className="ks-icon-button ks-close" aria-label="Close" title="Close (Esc)" onClick={onClose}><ActionIcon name="close" /></button>
        </div>
      </header>
      <span className="visually-hidden" role="status">{status}</span>
      {error && <div className="ks-error"><ErrorFeedback message={error} /></div>}
      {narrow && <div className="ks-tabs" role="tablist" aria-label={`${studioName} panels`} onKeyDown={onTabKey}>
        {tabs.map(item => <button key={item.id} type="button" role="tab" id={`${id}-${item.id}-tab`} aria-selected={tab === item.id} aria-controls={`${id}-${item.id}`} tabIndex={tab === item.id ? 0 : -1} onClick={() => setTab(item.id)}>{item.label}</button>)}
      </div>}
      <div className="ks-workspace">
        {panel('layers', 'ks-left', layers, 'Layers')}
        {panel('canvas', 'ks-centre', <>{canvas}{captionDrawer}</>, 'Figure')}
        {panel('properties', 'ks-right', inspector, 'Properties')}
      </div>
      {help && <div className="ks-popover ks-help" role="dialog" aria-label="Keyboard shortcuts" onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); setHelp(false)}}}>
        <div className="ks-help-head"><strong>Keyboard shortcuts</strong><button type="button" className="ks-icon-button ks-small" aria-label="Close shortcuts" onClick={() => setHelp(false)}><ActionIcon name="close" /></button></div>
        <dl>{shortcuts.map(([keys, action]) => <div key={keys}><dt><kbd>{keys}</kbd></dt><dd>{action}</dd></div>)}</dl>
        <p className="ks-hint">Select anything in Layers or click it on the figure; its properties appear on the right. Add chart puts up to four charts in one image.</p>
      </div>}
    </div>}
  </dialog>
}

export const figureKindLabel = (kind: FigureKind) => kindLabels[kind]
export type {ChartState}
