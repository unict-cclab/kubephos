import {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type ReactNode} from 'react'
import {downloadBlob, exportChart} from '../../charts'
import {matplotlibScript, pgfplotsSnippet} from './figureCode'
import {buildFigureModel, type LoadProfile} from './figureModel'
import {builtInPresets, defaultSpec, loadPresets, normalizeSpec, parseSpec, savePresets, serializeSpec, type FigureSpec, type SpecType, type StylePreset} from './figureSpec'
import {SpecFigure} from './SpecFigure'
import {AnnotationsEditor, AxesEditor, DataBindings, PaletteEditor, PresetsEditor, RunModePicker, SecondY, SeriesList, SpecActions} from './SpecEditor'
import {Section} from './FigureSettingsControl'
import {cumulativeCounterMetrics, isControlMetric, type AggregateSeries, type RunLevelMetric} from '../../resultsData'
import {ActionIcon} from '../ActionIcon'
import {ErrorFeedback, useErrorState} from '../ToastRegion'
import {FigureHostContext, seriesStyle, type FigureLabelOverrides, type FigureReport} from './chartKit'
import {FigureStylePanel} from './FigureSettingsControl'
import {PercentileLadder, RunDotPlot, ServiceFigure, TimeSeriesFigure, type ServiceRow} from './Figures'
import {figureMetrics} from './chartKit'
import type {FigureSettings} from './figureSettings'
import {metricTitle} from './format'
import type {FidelitySeries} from './loadFidelity'
import type {OverviewGroup} from './overview'
import type {LadderSeries} from './percentiles'
import type {ResolvedProvenance} from './runs'
import {defaultXYOptions, defaultXYSelection, type XYSource, type XYState} from './xy'
import {XYControls} from './XYChart'

// Figure studio: a full-screen workspace for one publication figure. Left: figure type and
// data; centre: the figure on paper at its true printed size (scaled to fit or zoomed), its
// short caption and the methods & provenance; right: style. Export lives in the top bar.

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

const kindOrder: FigureKind[] = ['timeseries', 'xy', 'runlevel', 'ladder', 'services', 'fidelity', 'placement']
const kindLabels: Record<FigureKind, string> = {timeseries: 'Time series', xy: 'X–Y relationship', runlevel: 'Run-level dots', ladder: 'Percentile ladder', services: 'Services', fidelity: 'Load fidelity', placement: 'Placement'}
const kindHints: Record<FigureKind, string> = {timeseries: 'Metric over the measured window', xy: 'One metric against another', runlevel: 'Every repetition, mean and CI', ladder: 'P50–P99, client vs server', services: 'One value per service', fidelity: 'Offered vs achieved load', placement: 'Replicas per zone over time'}

// Small schematic of each figure type (currentColor strokes, 48×32).
function KindIcon({kind}: {kind: FigureKind}) {
  const shapes: Record<FigureKind, ReactNode> = {
    timeseries: <><path d="M5 22c6-2 8-9 14-8s8 6 14 3 6-8 10-9" strokeWidth="1.6" /><path d="M5 26c6-1 9-5 14-5s9 3 14 2 6-3 10-4" strokeWidth="1.6" opacity=".55" /></>,
    xy: <><circle cx="10" cy="24" r="1.6" /><circle cx="15" cy="21" r="1.6" /><circle cx="19" cy="22" r="1.6" /><circle cx="24" cy="16" r="1.6" /><circle cx="29" cy="15" r="1.6" /><circle cx="34" cy="10" r="1.6" /><circle cx="39" cy="9" r="1.6" /><path d="M7 26 42 7" strokeWidth="1.2" opacity=".6" /></>,
    runlevel: <><circle cx="14" cy="12" r="1.7" /><circle cx="12" cy="16" r="1.7" /><circle cx="16" cy="18" r="1.7" /><path d="M9 15h10" strokeWidth="2" /><circle cx="34" cy="20" r="1.7" /><circle cx="32" cy="24" r="1.7" /><circle cx="36" cy="22" r="1.7" /><path d="M29 22h10" strokeWidth="2" /></>,
    ladder: <><path d="M8 25 18 20 28 14 40 6" strokeWidth="1.6" /><path d="M8 27 18 25 28 22 40 17" strokeWidth="1.6" strokeDasharray="3 2" opacity=".6" /><circle cx="8" cy="25" r="1.7" /><circle cx="18" cy="20" r="1.7" /><circle cx="28" cy="14" r="1.7" /><circle cx="40" cy="6" r="1.7" /></>,
    services: <><path d="M10 10v10M8 10h4M8 20h4M22 18v8M20 18h4M20 26h4M34 6v10M32 6h4M32 16h4" strokeWidth="1.2" /><circle cx="10" cy="15" r="1.9" /><circle cx="22" cy="22" r="1.9" /><circle cx="34" cy="11" r="1.9" /></>,
    fidelity: <><path d="M5 24 18 12h24" strokeWidth="1.6" strokeDasharray="3 2" opacity=".6" /><path d="M5 25 18 16c5-2 10 0 14-1s7-1 10-1" strokeWidth="1.6" /></>,
    placement: <>{[0, 1, 2].flatMap(row => [0, 1, 2, 3, 4].map(column => <rect key={`${row}-${column}`} x={6 + column * 7.4} y={6 + row * 7} width="6.4" height="6" rx="1" opacity={[.25, .5, .85, .4, .65][(row * 2 + column) % 5]} stroke="none" />))}</>
  }
  return <svg className="studio-kind-icon" width="48" height="32" viewBox="0 0 48 32" fill="currentColor" stroke="currentColor" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" focusable="false">
    <path d="M3 3v26h42" fill="none" strokeWidth="1" opacity=".35" />
    <g className={kind === 'placement' ? '' : 'studio-kind-marks'}>{shapes[kind]}</g>
  </svg>
}

const timeSeriesExcluded = (metric: string) => isControlMetric(metric)
const runLevelExcluded = (metric: string) => isControlMetric(metric) || ['load.configured_rps', 'load.offered_rps', 'load.users'].includes(metric) || cumulativeCounterMetrics.includes(metric)

// ---------------------------------------------------------------------------------------
// Remembered studio state (per experiment, localStorage; failures fall back to memory).

interface FigureMemory {name?: string; caption?: string; overrides?: FigureLabelOverrides}
interface StudioMemory {kind?: FigureKind; metric?: string; hidden?: string[]; xy?: XYState; zoom?: number | 'fit'; figures?: Record<string, FigureMemory>; specs?: Partial<Record<SpecType, FigureSpec>>}
const memoryPrefix = 'kubephos.figureStudio.v1:'

export function loadStudioMemory(key?: string): StudioMemory {
  if (!key) return {}
  try {
    const value = JSON.parse(window.localStorage.getItem(memoryPrefix + key) ?? '{}') as unknown
    if (!value || typeof value !== 'object') return {}
    const memory = value as StudioMemory
    return {
      kind: kindOrder.includes(memory.kind as FigureKind) ? memory.kind : undefined,
      metric: typeof memory.metric === 'string' ? memory.metric : undefined,
      hidden: Array.isArray(memory.hidden) ? memory.hidden.filter(item => typeof item === 'string') : undefined,
      xy: memory.xy && typeof memory.xy === 'object' && memory.xy.x && memory.xy.y ? {...memory.xy, options: {...defaultXYOptions, ...memory.xy.options}} : undefined,
      zoom: memory.zoom === 'fit' || typeof memory.zoom === 'number' ? memory.zoom : undefined,
      figures: memory.figures && typeof memory.figures === 'object' ? memory.figures : undefined,
      specs: memory.specs && typeof memory.specs === 'object' ? Object.fromEntries((['timeseries', 'xy'] as const).flatMap(type => memory.specs?.[type] ? [[type, normalizeSpec(memory.specs[type], type)]] : [])) : undefined
    }
  } catch {
    return {}
  }
}

function saveStudioMemory(key: string | undefined, memory: StudioMemory) {
  if (!key) return
  // Keep the 20 most recently edited figures.
  const figures = Object.fromEntries(Object.entries(memory.figures ?? {}).slice(-20))
  try {window.localStorage.setItem(memoryPrefix + key, JSON.stringify({...memory, figures}))} catch {/* storage unavailable: keep the in-memory state */}
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

const slug = (value: string) => value.normalize('NFKD').replaceAll(/[^a-zA-Z0-9]+/g, '-').replaceAll(/^-+|-+$/g, '').toLowerCase().slice(0, 80) || 'figure'
const inches = (points: number) => String(Number((points / 72).toFixed(points >= 720 ? 1 : 2)))
const oneLine = (value: string) => value.replaceAll(/\s+/g, ' ').trim()

type ExportAction = 'pdf' | 'svg' | 'png300' | 'png600' | 'csv' | 'python' | 'pgfplots'
const exportLabels: Record<ExportAction, string> = {pdf: 'PDF', svg: 'SVG', png300: 'PNG 300 dpi', png600: 'PNG 600 dpi', csv: 'Data CSV', python: 'Python (matplotlib)', pgfplots: 'pgfplots (LaTeX)'}
const exportHints: Record<ExportAction, string> = {pdf: 'Vector, real text', svg: 'Vector, editable', png300: 'Raster for slides', png600: 'Raster for print', csv: 'Plotted values + methods', python: 'Script + CSV that redraw it', pgfplots: '.tex snippet + CSV'}

// Split button: the main part exports PDF; the menu offers every format.
function ExportMenu({onExport, busy, csv, code}: {onExport: (action: ExportAction) => void; busy: ExportAction | null; csv: boolean; code: boolean}) {
  const [open, setOpen] = useState(false)
  const menuId = useId()
  const toggle = useRef<HTMLButtonElement>(null)
  const menu = useRef<HTMLDivElement>(null)
  const actions: ExportAction[] = ['pdf', 'svg', 'png300', 'png600', ...(csv ? ['csv' as const] : []), ...(code ? ['python' as const, 'pgfplots' as const] : [])]
  useEffect(() => {
    if (!open) return
    menu.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
    const outside = (event: PointerEvent) => {if (!menu.current?.contains(event.target as Node) && !toggle.current?.contains(event.target as Node)) setOpen(false)}
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  const close = () => {setOpen(false); toggle.current?.focus()}
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = [...(menu.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') ?? [])]
    const index = items.indexOf(document.activeElement as HTMLElement)
    if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); close()}
    else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {event.preventDefault(); items[(index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length]?.focus()}
    else if (event.key === 'Home' || event.key === 'End') {event.preventDefault(); items[event.key === 'Home' ? 0 : items.length - 1]?.focus()}
    else if (event.key === 'Tab') setOpen(false)
  }
  return <div className="studio-split">
    <button type="button" className="button primary compact studio-split-main" disabled={Boolean(busy)} onClick={() => onExport('pdf')}>
      {busy ? <span className="loading-indicator" aria-hidden="true" /> : <ActionIcon name="download" />}{busy ? `Exporting ${exportLabels[busy]}…` : 'Export PDF'}
    </button>
    <button ref={toggle} type="button" className="button primary compact studio-split-toggle" aria-label="More export formats" aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuId : undefined} disabled={Boolean(busy)} onClick={() => setOpen(value => !value)} onKeyDown={event => {if (event.key === 'ArrowDown') {event.preventDefault(); setOpen(true)}}}><ActionIcon name="down" /></button>
    {open && <div ref={menu} id={menuId} className="studio-menu" role="menu" aria-label="Export formats" onKeyDown={onKey}>
      {actions.map(action => <button key={action} type="button" role="menuitem" tabIndex={-1} className="studio-menu-item" onClick={() => {setOpen(false); toggle.current?.focus(); onExport(action)}}>
        <strong>{exportLabels[action]}</strong><small>{exportHints[action]}</small>
      </button>)}
    </div>}
  </div>
}

type Report = Omit<FigureReport, 'csv'>
const sameReport = (left: Report | undefined, right: Report) => Boolean(left) && left!.id === right.id && left!.name === right.name && left!.title === right.title && left!.caption === right.caption && left!.methods === right.methods && left!.width === right.width && left!.height === right.height && JSON.stringify(left!.labels) === JSON.stringify(right.labels)

export function FigureStudio({open, onClose, request, data, groups, settings, onSettings, storageKey, onSave}: {open: boolean; onClose: () => void; request: StudioRequest; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; onSettings: (next: FigureSettings) => void; storageKey?: string; onSave?: StudioSave}) {
  const id = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const paper = useRef<HTMLDivElement>(null)
  const remembered = useMemo(() => loadStudioMemory(storageKey), [storageKey])
  const [kind, setKind] = useState<FigureKind | undefined>(request.kind ?? (request.metric ? 'timeseries' : remembered.kind))
  const [metric, setMetric] = useState(request.metric ?? remembered.metric ?? '')
  const [hidden, setHidden] = useState<string[]>(remembered.hidden ?? [])
  const [xy, setXY] = useState<XYState>(() => request.xy ?? remembered.xy ?? {...defaultXYSelection(data?.xy?.metrics ?? []), options: defaultXYOptions})
  const [figures, setFigures] = useState<Record<string, FigureMemory>>(remembered.figures ?? {})
  const [specs, setSpecs] = useState<Partial<Record<SpecType, FigureSpec>>>(remembered.specs ?? {})
  const [editing, setEditing] = useState<string>()
  const [panelIndex, setPanelIndex] = useState(0)
  const [presets, setPresets] = useState<StylePreset[]>(() => loadPresets())
  const [zoom, setZoom] = useState<number | 'fit'>(remembered.zoom ?? 'fit')
  const [stageSize, setStageSize] = useState<{width: number; height: number}>()
  const [tab, setTab] = useState<'figure' | 'style' | 'preview'>('preview')
  const [busy, setBusy] = useState<ExportAction | 'save' | null>(null)
  const [status, setStatus] = useState('')
  const [error, setError] = useErrorState()
  const narrow = useNarrow()
  const [report, setReport] = useState<Report>()
  const csvRef = useRef<(() => string) | undefined>(undefined)
  const onReport = useCallback((figure: FigureReport) => {
    csvRef.current = figure.csv
    const {csv: _csv, ...rest} = figure
    void _csv
    setReport(previous => sameReport(previous, rest) ? previous : rest)
  }, [])

  // Native modal dialog: the rest of the page is inert and Esc closes it.
  useEffect(() => {
    const element = dialog.current
    if (!element || !open) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (!element.open) element.showModal()
    element.querySelector<HTMLElement>('.studio-kind input:checked, .studio-kind input')?.focus({preventScroll: true})
    return () => {
      if (element.open) element.close()
      if (previous?.isConnected) previous.focus({preventScroll: true})
    }
  }, [open])
  useEffect(() => {if (open && request.kind) setKind(request.kind); else if (open && request.metric) setKind('timeseries'); if (request.metric) setMetric(request.metric); if (request.xy) setXY(request.xy)}, [open, request.kind, request.metric, request.xy])
  useEffect(() => {if (data?.xy && !data.xy.metrics.includes(xy.x.metric)) setXY(current => ({...current, ...defaultXYSelection(data.xy!.metrics)}))}, [data?.xy])

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
  const activeKind = kind && available.includes(kind) ? kind : available.includes('fidelity') ? 'fidelity' : available[0]
  const metricOptions = !data ? [] : activeKind === 'timeseries' ? data.metrics.filter(item => !timeSeriesExcluded(item) && data.plot(item).length) : activeKind === 'runlevel' ? data.runLevel.filter(entry => !runLevelExcluded(entry.metric) && entry.groups.some(group => group.values.length)).map(entry => entry.metric) : activeKind === 'services' ? data.serviceMetrics : []
  const activeMetric = metricOptions.includes(metric) ? metric : metricOptions.includes('load.p95_response_time') ? 'load.p95_response_time' : metricOptions[0] ?? ''
  const figureKey = activeKind === 'xy' ? `xy:${xy.x.metric}:${xy.x.service ?? ''}:${xy.y.metric}:${xy.y.service ?? ''}` : `${activeKind}:${activeMetric}`
  const memory = figures[figureKey] ?? {}
  const overrides = memory.overrides ?? {}
  const updateFigure = (patch: Partial<FigureMemory>) => setFigures(current => {
    const {[figureKey]: existing, ...rest} = current
    const next = {...existing, ...patch}
    for (const key of Object.keys(next) as Array<keyof FigureMemory>) if (next[key] === undefined) delete next[key]
    return Object.keys(next).length ? {...rest, [figureKey]: next} : rest
  })
  useEffect(() => {saveStudioMemory(storageKey, {kind: activeKind, metric: activeMetric || undefined, hidden, xy, zoom, figures, specs})}, [storageKey, activeKind, activeMetric, hidden, xy, zoom, figures, specs])

  const shown = (label: string) => !hidden.includes(label)
  const keepGroups = groups.filter(group => shown(group.label))
  const host = useMemo(() => ({overrides, report: onReport}), [JSON.stringify(overrides), onReport])

  // Spec-driven figures (time series, X–Y): the FigureSpec drives preview and exports.
  const specType: SpecType | undefined = activeKind === 'timeseries' || activeKind === 'xy' ? activeKind : undefined
  const storedSpec = specType ? specs[specType] ?? defaultSpec(specType) : undefined
  const setSpec = useCallback((update: (spec: FigureSpec) => FigureSpec) => {if (specType) setSpecs(current => ({...current, [specType]: update(current[specType] ?? defaultSpec(specType))}))}, [specType])
  const spec = useMemo((): FigureSpec | undefined => {
    if (!storedSpec) return undefined
    const panels = storedSpec.panels.map((panel, index) => {
      if (index) return panel
      if (storedSpec.type === 'xy') return {...panel, xy: {x: xy.x, y: xy.y, fit: xy.options.fit, path: xy.options.path}, x: {...panel.x, log: xy.options.logX}, left: {...panel.left, log: xy.options.logY}}
      const [first, ...rest] = panel.metrics
      return {...panel, metrics: [{metric: activeMetric, axis: first?.axis ?? 'left' as const}, ...rest]}
    })
    return {...storedSpec, panels, style: settings, labels: overrides}
  }, [storedSpec, activeMetric, xy, settings, JSON.stringify(overrides)])
  const model = useMemo(() => {
    if (!spec || !data || (spec.type === 'xy' && !data.xy)) return undefined
    const slo = data.omitSLO ? undefined : data.plot('load.p95_response_time').find(item => item.sloMilliseconds)?.sloMilliseconds
    const runs = data.xy?.level === 'run' ? (metric: string) => data.xy!.runs({metric}) : undefined
    return buildFigureModel(spec, {plot: data.plot, runs, provenance: data.provenance, slo, profile: data.profile, xy: spec.type === 'xy' && data.xy ? {source: data.xy, x: xy.x, y: xy.y, options: xy.options} : undefined}, {fontSize: figureMetrics(settings).fontSize, hidden: label => groups.length > 1 && hidden.includes(label), labels: overrides})
  }, [spec, data, hidden, groups.length])
  const legendSeries = model?.legend.map(item => item.series) ?? []
  useEffect(() => {if (editing && !legendSeries.some(item => item.key === editing)) setEditing(undefined)}, [editing, legendSeries.map(item => item.key).join('|')])
  useEffect(() => {if (editing) document.getElementById(`${id}-editor-${editing}`)?.scrollIntoView?.({block: 'nearest'})}, [editing, id])

  // [content, is a publication figure]
  const [preview, figureShown] = ((): [ReactNode, boolean] => {
    if (!data || !activeKind) return [<p className="studio-empty">No figure can be drawn from these results.</p>, false]
    if (activeKind === 'placement') return [data.placement, false]
    if (groups.length > 1 && !keepGroups.length) return [<p className="studio-empty">Select at least one series.</p>, false]
    const provenance = (item: string) => ({metric: item, provenance: data.provenance(item)})
    if (specType && spec && model) {
      if (model.empty) return [<p className="studio-empty">{model.empty}</p>, false]
      const metrics = [...new Set(model.panels.flatMap(panel => panel.metrics))]
      const figureName = specType === 'xy' ? `xy-${xy.x.metric}${xy.x.service ? `-${xy.x.service}` : ''}-vs-${xy.y.metric}${xy.y.service ? `-${xy.y.service}` : ''}` : activeMetric
      return [<SpecFigure id={`figure-${figureName.replaceAll(/[^a-zA-Z0-9]/g, '-')}`} name={figureName} model={model} settings={settings} grid={spec.grid} strokeScale={spec.strokeScale} provenance={(specType === 'xy' ? [xy.x.metric, ...metrics] : metrics).filter((item, index, list) => list.indexOf(item) === index).map(provenance)} ariaLabel={specType === 'xy' ? `Scatter of ${model.title}` : `${model.title} comparison chart`} />, true]
    }
    if (activeKind === 'runlevel' && activeMetric) {
      const entry = data.runLevel.find(item => item.metric === activeMetric)
      if (!entry) return [null, false]
      return [<RunDotPlot data={{...entry, groups: entry.groups.filter(group => shown(group.label))}} settings={settings} provenance={provenance(activeMetric)} runLabels={data.runLabels} />, true]
    }
    if (activeKind === 'ladder') {
      const metrics = ['load.p95_response_time', 'application.p95_response_time'].filter(item => data.metrics.includes(item))
      return [<PercentileLadder series={data.ladder.filter(item => shown(item.group))} settings={settings} provenance={metrics.map(provenance)} />, true]
    }
    if (activeKind === 'fidelity') {
      const series = data.fidelity.filter(item => keepGroups.some(group => item.label.startsWith(`${group.label} · `)) || groups.length <= 1)
      const order = [...new Set(series.filter(item => item.role === 'achieved').map(item => item.artifactID))]
      const offeredMetric = data.metrics.includes('load.offered_rps') ? 'load.offered_rps' : 'load.configured_rps'
      return [<TimeSeriesFigure name="load-fidelity" metric="load.achieved_rps" title={offeredMetric === 'load.offered_rps' ? 'Offered vs achieved request rate' : 'Configured vs achieved request rate'} axisLabel="Request rate (req/s)" series={series} styles={series.map(item => {const base = seriesStyle(order.indexOf(item.artifactID), item.color, settings); return item.role === 'offered' ? {...base, dash: '4 2', marker: 5} : base})} settings={settings} provenance={[offeredMetric, 'load.achieved_rps'].map(provenance)} captionLead={`${offeredMetric === 'load.offered_rps' ? 'Offered (dashed, generator target)' : 'Configured (dashed)'} and achieved (solid) request rate`} ariaLabel="Offered versus achieved request rate chart" />, true]
    }
    if (activeKind === 'services' && activeMetric) {
      const rows = (data.services(activeMetric)?.rows ?? []).filter(row => keepGroups.some(group => group.id === row.groupID || group.label === row.group) || groups.length <= 1)
      const title = activeMetric.endsWith('p95_response_time') ? activeMetric.startsWith('redis.') ? 'Mean window P95 per Redis operation' : 'Mean window P95 per service' : activeMetric.startsWith('redis.') ? metricTitle(activeMetric) : `${metricTitle(activeMetric)} by service`
      return rows.length ? [<ServiceFigure metric={activeMetric} rows={rows} settings={settings} provenance={provenance(activeMetric)} title={title} />, true] : [<p className="studio-empty">No service values for this metric.</p>, false]
    }
    return [null, false]
  })()
  const publication = figureShown && activeKind !== 'placement'
  const current = publication ? report : undefined
  const caption = memory.caption ?? current?.caption ?? ''
  const methods = current?.methods ?? ''
  const name = memory.name ?? current?.title ?? (activeKind ? kindLabels[activeKind] : 'Figure')

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
  const paperWidth = (current?.width ?? 252) * 96 / 72
  const paperHeight = (current?.height ?? 190) * 96 / 72
  const fit = stageSize && stageSize.width > 0 && stageSize.height > 0 ? Math.min(4, Math.max(.25, Math.min((stageSize.width - 48) / paperWidth, (stageSize.height - 48) / paperHeight))) : 1
  const scale = zoom === 'fit' ? fit : zoom
  const step = (direction: 1 | -1) => setZoom(Math.min(4, Math.max(.25, Math.round((scale * (direction > 0 ? 1.25 : .8)) * 100) / 100)))

  const description = [caption, methods && `Methods and provenance: ${methods}`].filter(Boolean).join('\n\n')
  const sourceSVG = () => publication ? current?.id : paper.current?.querySelector('svg[id]')?.id
  const fileName = `kubephos-${slug(name)}`
  const savedMetric = (activeKind === 'timeseries' || activeKind === 'runlevel' || activeKind === 'services') ? activeMetric : activeKind === 'xy' ? xy.y.metric : activeKind === 'ladder' ? 'load.p95_response_time' : activeKind === 'fidelity' ? 'load.achieved_rps' : 'placement'
  const exportAs = async (action: ExportAction) => {
    if (busy) return
    setBusy(action)
    setError('')
    setStatus('')
    try {
      if (action === 'python' || action === 'pgfplots') {
        if (!model || !current || !spec) throw new Error('This figure cannot be exported as code.')
        const options = {base: fileName, title: name, caption, settings, width: current.width, height: current.height, grid: spec.grid}
        if (action === 'python') {
          const out = matplotlibScript(model, options)
          downloadBlob(out.scriptName, new Blob([out.script], {type: 'text/x-python;charset=utf-8'}))
          downloadBlob(out.csvName, new Blob([out.csv], {type: 'text/csv;charset=utf-8'}))
        } else {
          const out = pgfplotsSnippet(model, options)
          downloadBlob(out.texName, new Blob([out.tex], {type: 'text/x-tex;charset=utf-8'}))
          downloadBlob(out.csvName, new Blob([out.csv], {type: 'text/csv;charset=utf-8'}))
        }
      } else if (action === 'csv') {
        const csv = csvRef.current
        if (!publication || !csv) throw new Error('This figure has no exportable data.')
        const header = [`# ${oneLine(name)}`, caption && `# Caption: ${oneLine(caption)}`, methods && `# Methods and provenance: ${oneLine(methods)}`].filter(Boolean).join('\n')
        downloadBlob(`${fileName}-data.csv`, new Blob([`${header}\n${csv()}`], {type: 'text/csv;charset=utf-8'}))
      } else {
        const svg = sourceSVG()
        if (!svg) throw new Error('The figure is not ready yet. Try again.')
        const options = {title: name, caption: description, fileName}
        if (action === 'png300' || action === 'png600') await exportChart(svg, savedMetric, 'png', undefined, {...options, dpi: action === 'png300' ? 300 : 600})
        else await exportChart(svg, savedMetric, action, undefined, options)
      }
      setStatus(`${exportLabels[action]} exported.`)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not export the figure. Try again.')}
    finally {setBusy(null)}
  }
  const save = async () => {
    const svg = sourceSVG()
    if (!onSave || !svg || busy) return
    setBusy('save')
    setError('')
    setStatus('')
    try {
      await exportChart(svg, savedMetric, 'pdf', bytes => onSave({metric: savedMetric.replaceAll(/[^a-zA-Z0-9._-]/g, '-').slice(0, 100), format: 'pdf', bytes}), {title: name, caption: description, fileName})
      setStatus('Saved to the experiment (PDF). Find it under Export › Saved figures.')
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save the figure. Try again.')}
    finally {setBusy(null)}
  }
  const copySpec = async () => {
    if (!spec) return
    setError('')
    try {
      await navigator.clipboard.writeText(serializeSpec(spec))
      setStatus('Figure spec copied.')
    } catch {setError('Could not copy the spec: the clipboard is not available.')}
  }
  const pasteSpec = async (json?: string) => {
    setError('')
    let text = json
    if (text === undefined) {
      try {text = await navigator.clipboard.readText()} catch {setError('Could not read the clipboard. Use “Paste as text…” instead.'); return}
    }
    try {
      const parsed = parseSpec(text ?? '')
      const first = parsed.panels[0]
      const nextMetric = parsed.type === 'timeseries' && first.metrics[0]?.metric && data?.metrics.includes(first.metrics[0].metric) ? first.metrics[0].metric : activeMetric
      const nextXY = parsed.type === 'xy' && first.xy ? {x: first.xy.x, y: first.xy.y, options: {...xy.options, fit: first.xy.fit, path: first.xy.path, logX: first.x.log, logY: first.left.log}} : xy
      const key = parsed.type === 'xy' ? `xy:${nextXY.x.metric}:${nextXY.x.service ?? ''}:${nextXY.y.metric}:${nextXY.y.service ?? ''}` : `timeseries:${nextMetric}`
      setKind(parsed.type)
      setSpecs(current => ({...current, [parsed.type]: parsed}))
      if (parsed.type === 'timeseries') setMetric(nextMetric)
      else setXY(nextXY)
      setFigures(current => ({...current, [key]: {...current[key], overrides: Object.keys(parsed.labels).length ? parsed.labels : undefined}}))
      onSettings(parsed.style)
      setStatus('Figure spec applied.')
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not apply the spec.')}
  }
  const applyPreset = (preset: StylePreset) => {
    onSettings({...settings, ...preset.style, uncertainty: settings.uncertainty, zeroBased: settings.zeroBased, logY: settings.logY, grayscale: settings.grayscale, showTitle: settings.showTitle})
    setSpec(current => ({...current, strokeScale: preset.strokeScale, grid: preset.grid}))
    setStatus(`Preset “${preset.name}” applied.`)
  }
  const storePresets = (next: StylePreset[]) => {
    setPresets(next)
    if (!savePresets(next)) setError('The preset is kept for this session only: browser storage is not available.')
  }
  const copyCaption = async () => {
    setError('')
    try {
      await navigator.clipboard.writeText(caption)
      setStatus('Caption copied.')
    } catch {setError('Could not copy the caption: the clipboard is not available. Select the caption text instead.')}
  }

  const figurePanel = data && <div className="studio-panel-body">
    <fieldset className="studio-gallery">
      <legend className="studio-panel-title">Figure</legend>
      <div className="studio-gallery-grid">
        {available.map(item => <label key={item} className="studio-kind" data-checked={item === activeKind}>
          <input type="radio" className="visually-hidden" name={`${id}-kind`} value={item} checked={item === activeKind} onChange={() => setKind(item)} />
          <KindIcon kind={item} />
          <span className="studio-kind-text"><span>{kindLabels[item]}</span><small>{kindHints[item]}</small></span>
        </label>)}
      </div>
    </fieldset>
    {activeKind && activeKind !== 'placement' && <section className="studio-section" aria-label="Data">
      <div className="studio-section-head"><h3>Data</h3></div>
      {activeKind === 'timeseries' && spec && <DataBindings spec={spec} setSpec={setSpec} metrics={metricOptions} provenance={data.provenance} primary={activeMetric} onPrimary={setMetric} panel={panelIndex} onPanel={setPanelIndex} />}
      {activeKind !== 'timeseries' && metricOptions.length > 0 && <label className="studio-field" htmlFor={`${id}-metric`}>Metric<select id={`${id}-metric`} value={activeMetric} onChange={event => setMetric(event.target.value)}>{metricOptions.map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}</select></label>}
      {activeKind === 'xy' && data.xy && <XYControls source={data.xy} value={xy} onChange={setXY} options={['fit', 'path']} />}
      {activeKind === 'xy' && data.xy && spec && <SecondY spec={spec} setSpec={setSpec} source={data.xy} />}
      {groups.length > 1 && <fieldset className="studio-series">
        <legend className="studio-field-label">Series</legend>
        {groups.map(group => <label key={group.id} className="studio-series-row" title={group.colorNote}>
          <input type="checkbox" checked={shown(group.label)} onChange={event => setHidden(current => event.target.checked ? current.filter(item => item !== group.label) : [...current, group.label])} />
          <span className="studio-swatch" style={{background: legendSeries.find(item => item.group === group.label)?.style.color ?? group.color}} aria-hidden="true" />
          <span className="studio-series-name">{group.label}</span>
          {group.colorNote && <span className="visually-hidden"> ({group.colorNote})</span>}
        </label>)}
      </fieldset>}
    </section>}
    {spec && model && !model.empty && <section className="studio-section" aria-label="Series style" id={`${id}-series`}>
      <div className="studio-section-head"><h3>Series style</h3></div>
      <p className="studio-hint">Click a series (or its legend entry in the figure) to edit colour, marker and line. Order = legend and drawing order.</p>
      <SeriesList series={legendSeries} spec={spec} setSpec={setSpec} editing={editing} onEdit={setEditing} labels={overrides.series ?? {}} onLabel={(label, value) => {const next = {...overrides.series}; if (value === undefined) delete next[label]; else next[label] = value; updateFigure({overrides: {...overrides, series: next}})}} idPrefix={`${id}-editor-`} />
    </section>}
    {spec?.type === 'timeseries' && model && !model.empty && <Section title="Repeated runs">
      <RunModePicker value={spec.runMode} onChange={runMode => setSpec(current => ({...current, runMode}))} fallback={model.runModeFallback} disabled={!legendSeries.some(item => item.n > 1)} />
    </Section>}
    {spec && model && <Section title="Annotations">
      <AnnotationsEditor spec={spec} setSpec={setSpec} panel={spec.type === 'xy' ? 0 : Math.min(panelIndex, spec.panels.length - 1)} xUnit={spec.type === 'xy' ? model.panels[0]?.x.label ?? 'X' : 's'} hasProfile={Boolean(data.profile)} hasSLO={!data.omitSLO && data.plot('load.p95_response_time').some(item => item.sloMilliseconds)} timeSeries={spec.type === 'timeseries'} />
    </Section>}
  </div>

  const modelPanel = model?.panels[Math.min(panelIndex, model.panels.length - 1)]
  const stylePanel = <div className="studio-panel-body">
    <h2 className="studio-panel-title">Style</h2>
    {spec && <Section title="Preset">
      <PresetsEditor presets={presets} onApply={applyPreset} onSave={presetName => storePresets([...presets.filter(item => item.name !== presetName && !builtInPresets.some(builtIn => builtIn.name === presetName)), {name: presetName, style: settings, strokeScale: spec.strokeScale, grid: spec.grid}])} onDelete={presetName => storePresets(presets.filter(item => item.name !== presetName))} />
    </Section>}
    <FigureStylePanel settings={settings} onChange={onSettings} publication={activeKind !== 'placement'}
      labels={current ? {title: current.title, x: current.labels.x, y: current.labels.y, series: current.labels.series} : undefined}
      overrides={overrides} onOverrides={next => updateFigure({overrides: Object.values(next).some(value => value !== undefined && (typeof value !== 'object' || Object.keys(value).length)) ? next : undefined})}
      uncertainty={activeKind !== 'xy'}
      logX={activeKind === 'xy' ? {x: xy.options.logX, y: xy.options.logY} : undefined}
      onLogX={next => setXY(state => ({...state, options: {...state.options, logX: next.x, logY: next.y}}))} spec={Boolean(spec)} />
    {spec && <Section title="Colour">
      <PaletteEditor spec={spec} setSpec={setSpec} />
    </Section>}
    {spec && model && <Section title={model.panels.length > 1 ? `Axes · panel (${'abcd'[Math.min(panelIndex, model.panels.length - 1)]})` : 'Axes'}>
      <AxesEditor spec={spec} setSpec={setSpec} panel={Math.min(panelIndex, spec.panels.length - 1)} timeSeries={spec.type === 'timeseries'} hasRight={Boolean(modelPanel?.right)} units={{x: spec.type === 'xy' ? modelPanel?.x.unit : undefined, left: modelPanel?.left?.unit, right: modelPanel?.right?.unit}}
        onXLog={spec.type === 'xy' ? logX => setXY(state => ({...state, options: {...state.options, logX}})) : undefined} onLeftLog={spec.type === 'xy' ? logY => setXY(state => ({...state, options: {...state.options, logY}})) : undefined} />
    </Section>}
    {spec && <Section title="Figure spec">
      <p className="studio-hint">The whole figure as JSON: data bindings, series styles, axes, annotations, layout and style. Paste it into another experiment to reuse it.</p>
      <SpecActions onCopy={() => void copySpec()} onPaste={json => void pasteSpec(json)} onReset={() => {setSpecs(current => {const {[spec.type]: _, ...rest} = current; void _; return rest}); updateFigure({overrides: undefined}); setEditing(undefined)}} />
    </Section>}
  </div>

  const canvas = <div className="studio-canvas">
    <div className="studio-canvas-bar">
      {publication && <div className="studio-zoom" role="group" aria-label="Zoom">
        <button type="button" className="icon-button" aria-label="Zoom out" onClick={() => step(-1)} disabled={scale <= .25}><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" aria-hidden="true"><path d="M5 12h14" /></svg></button>
        <span className="studio-zoom-value" aria-live="polite">{Math.round(scale * 100)} %</span>
        <button type="button" className="icon-button" aria-label="Zoom in" onClick={() => step(1)} disabled={scale >= 4}><ActionIcon name="create" /></button>
        <button type="button" className="button ghost compact" aria-pressed={zoom === 'fit'} onClick={() => setZoom('fit')}>Fit</button>
        <button type="button" className="button ghost compact" aria-pressed={zoom === 1} onClick={() => setZoom(1)}>100 %</button>
      </div>}
      {current && <span className="studio-dimensions">{inches(current.width)} × {inches(current.height)} in · {settings.font === 'serif' ? 'Times' : 'Helvetica'} {settings.fontSize} pt</span>}
    </div>
    <div className="studio-stage" ref={stage}>
      {!data ? <div className="results-placeholder" role="status"><span className="results-spinner" /><strong>Preparing figure data…</strong></div>
        : publication ? <div className="studio-paper" ref={paper} style={{width: `${Math.round(paperWidth * scale)}px`}} data-editable={spec ? true : undefined} onClick={event => {
          const key = (event.target as Element).closest?.('[data-series-key]')?.getAttribute('data-series-key')
          if (key && spec) {setEditing(key); if (narrow) setTab('figure')}
        }}><FigureHostContext.Provider value={host}>{preview}</FigureHostContext.Provider></div>
          : activeKind === 'placement' && data.placement ? <div className="studio-paper studio-paper-free" ref={paper}>{preview}</div>
            : <div className="studio-paper-empty">{preview}</div>}
    </div>
    {publication && <div className="studio-caption">
      <div className="studio-caption-head">
        <label htmlFor={`${id}-caption`}>Caption</label>
        {memory.caption !== undefined && <button type="button" className="link-button" onClick={() => updateFigure({caption: undefined})}>Reset to automatic</button>}
      </div>
      <textarea id={`${id}-caption`} rows={3} value={caption} onChange={event => updateFigure({caption: event.target.value === current?.caption ? undefined : event.target.value})} />
      {methods && <details className="studio-methods">
        <summary>Methods & provenance</summary>
        <p>{methods}</p>
        <div className="studio-methods-foot">
          <small>Embedded in the SVG description, the PDF metadata and the CSV header.</small>
          <button type="button" className="button secondary compact" onClick={() => updateFigure({caption: `${caption.trim()} ${methods}`.trim()})}>Insert into caption</button>
        </div>
      </details>}
    </div>}
  </div>

  const tabs = [{id: 'figure', label: 'Figure'}, {id: 'style', label: 'Style'}, {id: 'preview', label: 'Preview'}] as const
  const onTabKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return
    event.preventDefault()
    const index = tabs.findIndex(item => item.id === tab)
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1 : (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length
    setTab(tabs[next].id)
    event.currentTarget.querySelectorAll<HTMLElement>('[role="tab"]')[next]?.focus()
  }
  const panel = (which: 'figure' | 'style' | 'preview', className: string, content: ReactNode, label: string) => narrow
    ? <div className={`studio-panel ${className}`} role="tabpanel" id={`${id}-${which}`} aria-labelledby={`${id}-${which}-tab`} hidden={tab !== which}>{content}</div>
    : <div className={`studio-panel ${className}`} role="region" aria-label={label}>{content}</div>

  return <dialog ref={dialog} className="figure-studio" aria-labelledby={`${id}-title`} onCancel={event => {event.preventDefault(); onClose()}}>
    {open && <div className="studio-shell" data-narrow={narrow || undefined}>
      <header className="studio-topbar">
        <div className="studio-brand"><span className="studio-brand-mark" aria-hidden="true"><ActionIcon name="chart" /></span><h2 id={`${id}-title`}>Figure studio</h2></div>
        <div className="studio-name">
          <label htmlFor={`${id}-name`} className="visually-hidden">Figure name</label>
          <input id={`${id}-name`} type="text" value={name} placeholder="Figure name" onChange={event => updateFigure({name: event.target.value === current?.title ? undefined : event.target.value})} disabled={!data} />
        </div>
        <div className="studio-actions">
          {publication && <button type="button" className="button ghost compact studio-copy" onClick={() => void copyCaption()} disabled={!caption}><ActionIcon name="file" /><span>Copy caption</span></button>}
          {onSave && data && <button type="button" className="button secondary compact" disabled={Boolean(busy)} onClick={() => void save()}>{busy === 'save' ? 'Saving…' : 'Save to experiment'}</button>}
          {data && <ExportMenu busy={busy === 'save' ? null : busy} onExport={action => void exportAs(action)} csv={publication} code={publication && Boolean(spec && model)} />}
        </div>
        <button type="button" className="icon-button studio-close" aria-label="Close" onClick={onClose}><ActionIcon name="close" /></button>
      </header>
      <span className="visually-hidden" role="status">{status}</span>
      {error && <div className="studio-error"><ErrorFeedback message={error} /></div>}
      {status && <div className="studio-toast" aria-hidden="true">{status}</div>}
      {narrow && <div className="studio-tabs" role="tablist" aria-label="Figure studio panels" onKeyDown={onTabKey}>
        {tabs.map(item => <button key={item.id} type="button" role="tab" id={`${id}-${item.id}-tab`} aria-selected={tab === item.id} aria-controls={`${id}-${item.id}`} tabIndex={tab === item.id ? 0 : -1} onClick={() => setTab(item.id)}>{item.label}</button>)}
      </div>}
      <div className="studio-workspace">
        {panel('figure', 'studio-left', figurePanel, 'Figure and data')}
        {panel('preview', 'studio-centre', canvas, 'Preview')}
        {panel('style', 'studio-right', stylePanel, 'Style')}
      </div>
    </div>}
  </dialog>
}

export const figureKindLabel = (kind: FigureKind) => kindLabels[kind]
