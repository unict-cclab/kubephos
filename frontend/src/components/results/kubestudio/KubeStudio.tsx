import {Fragment, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent, type ReactNode} from 'react'
import {downloadBlob, exportChart} from '../../../charts'
import {ActionIcon} from '../../ActionIcon'
import {ErrorFeedback, useErrorState} from '../../ToastRegion'
import {FigureHostContext, type FigureLabelOverrides} from '../chartKit'
import {customLimits, defaultFigureSettings, fontStacks, legendFontLimits, type FigureSettings, type FigureSize} from '../figureSettings'
import {formatMetric, metricTitle} from '../format'
import type {OverviewGroup} from '../overview'
import {defaultXYOptions, defaultXYSelection, type XYState} from '../xy'
import {figureChecks, type CheckFix} from './checks'
import {Chips, Group, LabelField, MoreGroup, ResetIcon, SelectField, Stepper, Toggle} from './controls'
import {ContextMenu, type MenuAnchor, type MenuItem} from './ContextMenu'
import {ExportPopover, exportLabels, rememberedFormat, rememberFormat, type ExportAction} from './ExportPopover'
import {matplotlibScript, pgfplotsSnippet} from './figureCode'
import {unitSuffix, type ModelSeries} from './figureModel'
import {builtInPresets, defaultSpec, loadPresets, parseSpec, savePresets, serializeSpec, type Annotation, type AxisSide, type AxisSpec, type FigureSpec, type PanelGrid, type RunMode, type StylePreset, type XAxisSpec} from './figureSpec'
import {useHistory} from './history'
import {EyeIcon, layerIcons, svgProps, UndoIcon} from './icons'
import {availableKinds, buildViews, chartContent, defaultXYState, emptyState, imagePage, imageSvg, metricOptions, timeMetrics, useReports, type ChartView, type FigureKind, type StudioData} from './image'
import {KubeStudioMark} from './KubeStudioMark'
import {defaultLayout, layoutChoices, letters, maxCharts, newChartId, validLayout, type ChartLayout, type ChartState} from './layout'
import {loadStudioMemory, saveStudioMemory} from './memory'
import {StudioLanding} from './StudioLanding'
import {buildRecipes, customForms, FigureSwitcher, KindIcon, kindLabels, smartTimeSeriesSpec, switcherItems, type CustomForm, type SwitcherItem} from './recipes'
import type {ChartItem} from './resultsLayout'
import {autoAnnotations, highlightShapes, parseKey, parseTarget, selectionKey, snap, targetKey, type Selection, type Shape, type Target} from './selection'
import {AnnotationFields, annotationExamples, annotationNames, AxisFields, AxisMetrics, NumberInput, newAnnotation, PaletteEditor, PresetsEditor, runNames, SecondY, SeriesProperties, SeriesSwatch, SpecActions, XYAxisPicker} from './SpecEditor'
import type {FigureGeometry} from './SpecFigure'
import './kubestudio.css'

// KubeStudio: a Figma-like workspace for one publication image made of one to four charts.
// A new figure opens on a landing page of charts suggested by the results; otherwise it opens on
// the chart asked for (or the last image edited, else the best chart for the experiment). Left:
// Layers, the outline of the image (charts, each with title, axes, series, legend and
// lines), which is also how things are selected; centre: the image on paper
// at its true printed size, clickable and draggable; right: the properties of the selection,
// or of the page. Every change is undoable and autosaved; export lives in the top bar, with
// "Add to results", which keeps the image on the experiment's results page.

// choose: a new figure from the results page: open on the landing page (StudioLanding) that
// suggests charts from these results, with a custom chart and the last figure beside them.
export interface StudioRequest {kind?: FigureKind; metric?: string; xy?: XYState; figure?: ChartItem; choose?: boolean}

// Saves the exported PDF as an experiment figure (POST /experiments/{id}/figures).
export type StudioSave = (figure: {metric: string; format: 'pdf'; bytes: Uint8Array}) => Promise<void>
// Keeps the image on the results page (a new figure, or the one being edited when id is set).
type StudioKeep = (figure: Omit<ChartItem, 'id'>, id?: string) => ChartItem

const studioName = 'KubeStudio'
const creator = 'KubePhos KubeStudio'

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

const shortcuts: Array<[string, string]> = [['E', 'Export'], ['Ctrl/⌘ Z', 'Undo'], ['Ctrl/⌘ Shift Z', 'Redo'], ['+ / −', 'Zoom in / out'], ['0', 'Fit the image'], ['Esc', 'Deselect, then close'], ['?', 'Show this list']]
const sizeChoices: Array<{value: FigureSize; label: string; hint: string}> = [{value: 'single', label: '1 col', hint: 'Single column · 3.5 in · IEEE/ACM'}, {value: 'onehalf', label: '1.5 col', hint: '1.5 column · 5.5 in'}, {value: 'double', label: '2 col', hint: 'Double column · 7.16 in · IEEE/ACM'}, {value: 'slide', label: 'Slide', hint: 'Slide 16:9 · 10 × 5.63 in'}, {value: 'custom', label: 'Custom', hint: 'Custom width × plot height'}]
// Metrics suggested, in order, for each chart added to an image.
const suggested = ['load.p95_response_time', 'load.achieved_rps', 'cpu.cores', 'load.failure_percentage', 'load.successful_rps', 'memory.working_set', 'application.p95_response_time']

interface LayerRow {key: string; label: string; depth: 1 | 2; icon?: ReactNode; heading?: boolean; selectable?: boolean; eye?: {visible: boolean; label: string; onToggle: () => void}; action?: ReactNode; dim?: boolean; menu?: ReactNode}

export function KubeStudio({open, onClose, request, data, groups, settings, onSettings, storageKey, onSave, onKeep}: {open: boolean; onClose: () => void; request: StudioRequest; data: StudioData | undefined; groups: OverviewGroup[]; settings: FigureSettings; onSettings: (next: FigureSettings) => void; storageKey?: string; onSave?: StudioSave; onKeep?: StudioKeep}) {
  const id = useId()
  const dialog = useRef<HTMLDialogElement>(null)
  const stage = useRef<HTMLDivElement>(null)
  const paper = useRef<HTMLDivElement>(null)
  const geometries = useRef<Record<string, FigureGeometry | undefined>>({})
  const geometryRefs = useRef<Record<string, {current: FigureGeometry | undefined}>>({})
  const geometryOf = (chart: string) => geometryRefs.current[chart] ??= {get current() {return geometries.current[chart]}, set current(value) {geometries.current[chart] = value}}
  // A figure kept on the results page opens as it was saved and is not autosaved as the last image.
  const figure = request.figure
  const memoryKey = figure ? undefined : storageKey
  const remembered = useMemo(() => loadStudioMemory(memoryKey), [memoryKey])
  const requested = Boolean(request.kind || request.metric || request.xy)
  const defaultXY = () => defaultXYState(data)
  // Opening: the kept figure, the request, else the last image, else the best recipe for this experiment.
  const [charts, setCharts] = useState<ChartState[]>(() => {
    if (figure) return figure.charts
    if (requested) {
      const kind = request.kind ?? 'timeseries'
      const previous = remembered.charts?.find(item => item.kind === kind)
      return [{id: newChartId(), kind, metric: request.metric ?? previous?.metric ?? '', xy: request.xy ?? previous?.xy ?? defaultXY(), spec: previous?.spec, overrides: undefined}]
    }
    if (remembered.charts?.length) return remembered.charts
    const recipe = data ? buildRecipes(data, groups)[0] : undefined
    return recipe ? [{id: newChartId(), kind: recipe.kind, metric: recipe.metric ?? '', xy: recipe.xy ?? defaultXY(), spec: recipe.spec}] : []
  })
  const [layout, setLayout] = useState<ChartLayout | undefined>(figure ? figure.layout : remembered.layout)
  const [sharedLegend, setSharedLegend] = useState(figure ? figure.sharedLegend : remembered.sharedLegend ?? false)
  const [doc, setDoc] = useState<{name?: string; caption?: string}>(() => figure ? {name: figure.name, caption: figure.caption} : requested ? {} : {name: remembered.name, caption: remembered.caption})
  const [hidden, setHidden] = useState<string[]>(figure ? figure.hidden : remembered.hidden ?? [])
  const [keptId, setKeptId] = useState(figure?.id)
  const [selection, setSelection] = useState<Selection>()
  // The chart-type picker: changing the selected chart, or choosing the type of a chart to add.
  const [switcher, setSwitcher] = useState<'change' | 'add'>()
  // The axis title being renamed on the figure, with where its field sits on the paper.
  const [axisEdit, setAxisEdit] = useState<{chart: string; side: 'x' | 'left' | 'right'; panel: number; value: string; left: number; top: number; width: number}>()
  const axisCancelled = useRef(false)
  // The landing page of a new figure, until a suggestion, a custom chart or the last figure is chosen.
  const [landing, setLanding] = useState(Boolean(request.choose && !figure))
  // Back to suggestions (Home) from the editor: the figure stays, offered as "Continue last figure".
  const [returned, setReturned] = useState(false)
  const switched = useRef(false)
  // Focus follows between the landing page and the editor.
  useEffect(() => {
    if (switched.current) dialog.current?.querySelector<HTMLElement>(landing ? '.ks-suggestion, .ks-continue' : '.ks-switcher-button')?.focus({preventScroll: true})
    switched.current = true
  }, [landing])
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
  const [captionEditing, setCaptionEditing] = useState(false)
  const [menu, setMenu] = useState<{at: MenuAnchor; chart?: string}>()
  const [dragging, setDragging] = useState<string>()
  const [dropOn, setDropOn] = useState<string>()
  const [dismissed, setDismissed] = useState<string[]>([])
  const [status, setStatus] = useState('')
  const [notice, setNotice] = useState<{text: string; undo?: boolean; at: number}>()
  const [saved, setSaved] = useState<boolean>()
  const [error, setError] = useErrorState()
  const narrow = useNarrow()
  const {reports, csvs, hosts} = useReports(charts)
  const announce = (text: string, undo = false) => {setStatus(text); setNotice({text, undo, at: Date.now()})}
  useEffect(() => {if (!notice) return; const timer = setTimeout(() => setNotice(undefined), notice.undo ? 6000 : 2400); return () => clearTimeout(timer)}, [notice])
  // The kept figure brings its own page style.
  useEffect(() => {if (figure?.settings) onSettings(figure.settings)}, [])

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
    element.querySelector<HTMLElement>('.ks-switcher-button, .ks-suggestion, .ks-custom-form')?.focus({preventScroll: true})
    return () => {
      if (element.open) element.close()
      if (previous?.isConnected) previous.focus({preventScroll: true})
    }
  }, [open])

  const available = useMemo(() => availableKinds(data), [data])
  const recipes = useMemo(() => data ? buildRecipes(data, groups) : [], [data, groups])
  const items = useMemo(() => switcherItems(available, recipes), [available, recipes])
  const forms = customForms.map(form => form.kind).filter(kind => available.includes(kind))
  const seriesMetrics = useMemo(() => data ? timeMetrics(data) : [], [data])
  const metricOptionsFor = (kind: FigureKind | undefined) => metricOptions(data, kind)
  // Data that arrives after opening: start on the best recipe as well.
  useEffect(() => {if (!charts.length && data && recipes.length) setCharts([{id: newChartId(), kind: recipes[0].kind, metric: recipes[0].metric ?? '', xy: recipes[0].xy ?? defaultXY(), spec: recipes[0].spec}])}, [data, recipes])
  useEffect(() => {if (!landing) setSaved(saveStudioMemory(memoryKey, {charts, layout, sharedLegend, name: doc.name, caption: doc.caption, hidden, zoom}))}, [memoryKey, charts, layout, sharedLegend, doc, hidden, zoom, landing])

  const shown = (label: string) => !hidden.includes(label)
  const count = charts.length
  const effectiveLayout = validLayout(count, layout)
  const views: ChartView[] = useMemo(() => buildViews({image: {charts, layout, sharedLegend, hidden}, data, groups, settings, available}), [charts, layout, available, data, settings, sharedLegend, hidden, groups])

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
  const metricPicker = (view: ChartView) => data && view.metricOptions.length > 1 ? <SelectField label="Metric" value={view.metric} onChange={metric => setChartMetric(view, metric)}>{view.metricOptions.map(item => <option key={item} value={item}>{metricTitle(item, data.provenance(item))}</option>)}</SelectField> : undefined
  const rendered = views.map(view => chartContent(view, {data, groups, hidden, geometry: geometryOf(view.chart.id), actions: {
    showAllSeries: () => setHidden([]),
    suggestPair: target => setChartXY(target, current => ({...current, ...defaultXYSelection(data?.xy?.metrics ?? [])})),
    metricPicker
  }}))
  const isPublication = (index: number) => rendered[index]?.[1] && views[index].kind !== 'placement'
  const publication = views.length > 0 && views.every((_, index) => isPublication(index))
  const single = count === 1
  const page = imagePage(views, reports, settings, view => Boolean(isPublication(view.index)))
  const {currentOf, first, cellOf, width: pageWidth, height: pageHeight} = page

  // Name, caption and methods of the whole image. The caption is the user's own, optional
  // text (right-click › Add caption); exports fall back to the charts' own summaries.
  const titles = views.map(view => currentOf(view)?.title ?? (view.kind ? kindLabels[view.kind] : 'Chart'))
  const autoName = single ? titles[0] ?? 'Figure' : titles.join(' · ')
  const name = doc.name ?? autoName
  const summary = single ? first?.caption ?? '' : views.map(view => `${view.letter} ${currentOf(view)?.caption ?? ''}`.trim()).join(' ')
  const caption = doc.caption ?? ''
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
  const description = [caption || summary, methods && `Methods and provenance: ${methods}`].filter(Boolean).join('\n\n')
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
    const composite = imageSvg(views, page, view => {const report = reports[view.chart.id]; return report ? document.getElementById(report.id) as SVGSVGElement | null : null}, settings, `${id.replaceAll(':', '')}-image`)
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
  // Keeps the image on the results page; later clicks update that same figure.
  const keep = () => {
    if (!onKeep) return
    const kept = onKeep({name, caption, charts, layout, sharedLegend, hidden, settings}, keptId)
    setKeptId(kept.id)
    announce(keptId ? 'Updated on the results page.' : 'Added to the results page: right-click it there to edit, download or delete it.')
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

  // ---------------------------------------------------------------------------------------
  // Charts: recipes, custom forms, add / duplicate / move / remove.
  const applyRecipe = (view: ChartView | undefined, item: SwitcherItem) => {
    const recipe = item.recipe
    const next: ChartState = {id: view?.chart.id ?? newChartId(), kind: item.kind, metric: recipe?.metric ?? view?.metric ?? '', xy: recipe?.xy ?? view?.xy ?? defaultXY(), spec: recipe?.spec, overrides: undefined, origin: 'recipe'}
    if (view) updateChart(view.chart.id, () => next); else setCharts([next])
    setSelection(view && count > 1 ? {chart: next.id} : undefined)
    announce(`${item.title}. Click any part of the chart to change it.`, true)
  }
  // A custom chart keeps the current data where it makes sense: Y stays Y. A new chart starts
  // from `prefer`, the next metric the image does not show yet.
  const customChart = (view: ChartView | undefined, form: CustomForm, prefer?: string): ChartState | undefined => {
    if (!data) return
    const current = prefer ?? (view?.kind === 'xy' ? view.xy.y.metric : view?.metric ?? '')
    const xyMetrics = data.xy?.metrics ?? []
    const optionsFor = metricOptionsFor(form)
    const metric = optionsFor.includes(current) ? current : form === 'services' ? optionsFor.find(item => data.services(item)?.rows.length) ?? optionsFor[0] ?? '' : optionsFor.includes('load.p95_response_time') ? 'load.p95_response_time' : optionsFor[0] ?? ''
    const pair = defaultXYSelection(xyMetrics)
    const xy: XYState = form === 'xy' ? {x: view?.kind === 'xy' ? view.xy.x : pair.x, y: xyMetrics.includes(current) && current !== pair.x.metric ? {metric: current} : pair.y, options: {...defaultXYOptions, fit: true}} : view?.xy ?? defaultXY()
    return {id: view?.chart.id ?? newChartId(), kind: form, metric, xy, spec: form === 'timeseries' ? smartTimeSeriesSpec(data.plot(metric)) : undefined, origin: 'custom'}
  }
  const applyForm = (view: ChartView | undefined, form: CustomForm) => {
    const next = customChart(view, form)
    if (!next) return
    if (view) updateChart(view.chart.id, () => next); else setCharts([next])
    setSelection({chart: next.id})
    announce(`Custom ${customForms.find(item => item.kind === form)?.label.toLowerCase()}: choose X and Y on the right.`, true)
  }
  // Leaves the landing page on a new, one-chart image with a history of its own.
  const startWith = (chart: ChartState, text: string, select = false) => {
    history.forget()
    setCharts([chart]); setLayout(undefined); setSharedLegend(false); setHidden([]); setDoc({}); setKeptId(undefined)
    setSelection(select ? {chart: chart.id} : undefined)
    if (select && narrow) setTab('properties')
    setLanding(false)
    announce(text, false)
  }
  // Add chart is guided: the chart-type picker opens first; the chosen chart is added and
  // selected, so its X and Y are on the right.
  const addChart = () => {if (data && count < maxCharts) setSwitcher('add')}
  const nextMetric = () => {
    const used = new Set(views.flatMap(view => view.kind === 'xy' ? [view.xy.y.metric] : view.kind === 'timeseries' || view.kind === 'services' || view.kind === 'runlevel' ? [view.metric] : []))
    return suggested.find(item => seriesMetrics.includes(item) && !used.has(item)) ?? seriesMetrics.find(item => !used.has(item))
  }
  const addCustom = (form: CustomForm) => {const chart = customChart(undefined, form, nextMetric()); if (chart) appendChart(chart, `custom ${customForms.find(item => item.kind === form)?.label.toLowerCase()}`)}
  const addRecipe = (item: SwitcherItem) => appendChart({id: newChartId(), kind: item.kind, metric: item.recipe?.metric ?? '', xy: item.recipe?.xy ?? defaultXY(), spec: item.recipe?.spec, origin: 'recipe'}, item.title)
  const appendChart = (chart: ChartState, what: string) => {
    if (!data || count >= maxCharts) return
    setCharts(list => [...list, chart])
    setLayout(defaultLayout(count + 1))
    if (count === 1 && settings.size === 'single') onSettings({...settings, size: 'double'})
    // Every chart shows the same variants: one legend for the image unless the user chose otherwise.
    if (count === 1 && layout === undefined) setSharedLegend(true)
    setSelection({chart: chart.id})
    if (narrow) setTab('properties')
    announce(`Chart (${letters[count]}) added: ${what}${count === 1 && settings.size === 'single' ? ' — page widened to double column' : ''}. Choose its X and Y on the right.`, true)
  }
  const duplicateChart = (view: ChartView) => {
    if (count >= maxCharts) return
    const copy = {...JSON.parse(JSON.stringify(view.chart)) as ChartState, id: newChartId()}
    setCharts(list => [...list.slice(0, view.index + 1), copy, ...list.slice(view.index + 1)])
    setLayout(defaultLayout(count + 1))
    if (count === 1 && settings.size === 'single') onSettings({...settings, size: 'double'})
    setSelection({chart: copy.id})
    announce(`Chart ${view.letter ?? '(a)'} duplicated.`, true)
  }
  // Puts a chart at another position (drag and drop, or arrow keys on its grip).
  const moveChart = (chartId: string, to: number) => {
    const from = charts.findIndex(item => item.id === chartId)
    const target = Math.max(0, Math.min(count - 1, to))
    if (from < 0 || from === target) return
    setCharts(list => {const next = [...list]; const [item] = next.splice(from, 1); next.splice(target, 0, item); return next})
    announce(`Chart moved to position (${letters[target]}).`, true)
  }
  const removeChart = (view: ChartView) => {
    if (count <= 1) return
    setCharts(list => list.filter(item => item.id !== view.chart.id))
    setLayout(defaultLayout(count - 1))
    setSelection(undefined)
    announce(`Chart ${view.letter} deleted.`, true)
  }
  // Right-click menu of a chart, or of the page when no chart is under the pointer.
  const menuItems = (chartId: string | undefined): MenuItem[] => {
    const view = viewOf(chartId)
    return [
      ...(view ? [
        {label: 'Duplicate chart', icon: 'create' as const, hint: count >= maxCharts ? `An image holds up to ${maxCharts} charts` : 'Adds a copy next to it', disabled: count >= maxCharts, run: () => duplicateChart(view)},
        {label: 'Delete chart', icon: 'delete' as const, danger: true, hint: count <= 1 ? 'An image keeps at least one chart' : undefined, disabled: count <= 1, run: () => removeChart(view)}
      ] : []),
      {label: caption ? 'Edit caption' : 'Add caption', icon: 'edit', hint: 'Optional text that describes the figure', separated: Boolean(view), run: () => setCaptionEditing(true)}
    ]
  }
  const onPaperContextMenu = (event: ReactMouseEvent<HTMLDivElement>) => {
    event.preventDefault()
    const chart = cellAt(event.target)?.getAttribute('data-chart') ?? undefined
    if (chart) select({chart})
    setMenu({at: {x: event.clientX, y: event.clientY}, chart})
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

  // Canvas: click to select, hover to highlight, drag annotations, the SLO line and the legend.
  // legend: where the pointer took hold of the legend, from its top-left corner (points).
  const drag = useRef<{pointer: number; x: number; y: number; moved: boolean; view: ChartView; annotation?: Annotation; slo?: {factor: number}; legend?: {x: number; y: number}; panel: number; grab: {x?: number; y?: number}} | undefined>(undefined)
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
    // The whole legend drags, its entries too (a click on an entry still selects it).
    if (cell && view && (event.target as Element).closest?.('[data-select="legend"]') && event.button === 0) {
      const box = geometries.current[view.chart.id]?.legend
      const at = toPoints(cell, event, view.chart.id)
      if (box && at) drag.current = {pointer: event.pointerId, x: event.clientX, y: event.clientY, moved: false, view, legend: {x: at.x - box.left, y: at.y - box.top}, panel: 0, grab: {}}
      return
    }
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
    if (state.legend) {
      // Above, below or right of the plot the legend goes outside it; anywhere else it stays where it is let go.
      const figure = geometries.current[view.chart.id]
      const plot = figure?.plot
      if (!figure || !plot) return
      const legend = at.y < plot.top ? 'top' : at.y > plot.bottom ? 'bottom' : at.x > plot.right ? 'right' : 'inside'
      const legendAt = legend === 'inside' ? {x: Math.max(0, (at.x - state.legend.x) / figure.width), y: Math.max(0, (at.y - state.legend.y) / figure.height)} : settings.legendAt
      onSettings({...settings, legend, legendAt})
      return
    }
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
      select({chart: state.view.chart.id, target: state.legend ? {type: 'legend'} : {type: 'annotation', panel: state.panel, id: state.annotation?.id ?? 'slo'}})
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
    select({chart: view.chart.id, target: target && (view.spec || target.type === 'title' || target.type === 'axis') ? target : undefined})
    const title = (event.target as Element).closest?.('.axis-label')
    if (target?.type === 'axis' && title && paper.current) {
      const box = title.getBoundingClientRect()
      const frame = paper.current.getBoundingClientRect()
      const width = Math.min(frame.width - 8, Math.max(160, (target.side === 'x' ? box.width : box.height) + 48))
      axisCancelled.current = false
      setAxisEdit({chart: view.chart.id, side: target.side, panel: target.panel, value: title.textContent ?? '', width,
        left: Math.max(4, Math.min(frame.width - width - 4, box.left + box.width / 2 - frame.left - width / 2)), top: box.top + box.height / 2 - frame.top - 15})
    }
  }
  // An axis title edited in place: an empty title restores the automatic one.
  const commitAxis = (value: string) => {
    const edit = axisEdit
    setAxisEdit(undefined)
    const view = edit && viewOf(edit.chart)
    if (!edit || !view || value.trim() === edit.value.trim()) return
    const label = value.trim() || undefined
    if (edit.side !== 'right') labelOverride(view, edit.side === 'x' ? 'x' : 'y', label)
    else if (view.spec) {const axis = view.spec.panels[Math.min(edit.panel, view.spec.panels.length - 1)].right; setAxis(view, edit.panel, 'right', {...axis, label})}
    announce(label ? `Axis renamed to ${label}.` : 'Axis title restored.', true)
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
    const {spec, model} = view
    rows.push({key: key('title'), label: 'Title', depth: 2, icon: layerIcons.title, selectable: true, eye: {visible: settings.showTitle, label: settings.showTitle ? 'Hide the title' : 'Show the title', onToggle: () => onSettings({...settings, showTitle: !settings.showTitle})}, dim: !settings.showTitle})
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
      rows.push({key: key('h-annotations'), label: 'Add a line in the graph', depth: 2, heading: true, action: !model.empty && <button type="button" className="ks-icon-button ks-small" aria-label="Add a line in the graph" aria-expanded={adding} title="Add a threshold (e.g. the SLO), a time range, a vertical line or a label" onClick={() => setAddingNote(adding ? undefined : view.chart.id)}>{layerIcons.plus}</button>,
        menu: adding && <div className="ks-add-menu" role="group" aria-label="Line to add">{(['hline', 'xrange', 'vline', 'text'] as const).map(item => <button key={item} type="button" className="ks-add-option" onClick={() => addNote(view, item)}><strong>{annotationNames[item]}</strong><small>{annotationExamples[item]}</small></button>)}</div>})
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
  // The entries of a chart's legend, by the name the data gives them (their text is an override).
  // name: what the data calls the entry (its text is an override of it). Line and scatter
  // charts colour each series freely; the other forms take the variant's colour.
  const legendEntries = (view: ChartView): Array<{key: string; name: string; color?: string; editable: boolean}> => view.model && !view.model.empty
    ? view.model.legend.map(({series}) => ({key: series.key, name: series.defaultLabel, color: series.style.color, editable: true}))
    : (currentOf(view)?.labels.series ?? []).map(name => ({key: name, name, color: groups.find(group => group.label === name)?.color, editable: false}))
  const setSeriesColor = (view: ChartView, key: string, color: string) => setChartSpec(view)(current => ({...current, series: {...current.series, [key]: {...current.series[key], color}}}))
  // The axes of a chart's first panel, for the links to their settings.
  const axisTargets = (view: ChartView): Array<{side: 'x' | 'left' | 'right'; label: string}> => {
    const panel = view.model && !view.model.empty ? view.model.panels[0] : undefined
    if (view.spec) return panel ? [{side: 'x', label: 'X axis'}, ...(panel.left ? [{side: 'left' as const, label: panel.right ? 'Left Y axis' : 'Y axis'}] : []), ...(panel.right ? [{side: 'right' as const, label: 'Right Y axis'}] : [])] : []
    const labels = currentOf(view)?.labels
    return [...(labels?.x !== undefined ? [{side: 'x' as const, label: 'X axis'}] : []), ...(labels?.y !== undefined || view.metricOptions.length ? [{side: 'left' as const, label: 'Y axis'}] : [])]
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
        <Toggle label="Show the legend" checked={settings.legend !== 'none'} onChange={shown => onSettings({...settings, legend: shown ? 'bottom' : 'none'})} />
        {settings.legend !== 'none' && <>
          <p className="ks-hint">Drag the legend on the figure to move it. Dropped above, below or right of the plot, it sits outside it.</p>
          <SelectField label="Text size (pt)" value={String(settings.legendFontSize ?? settings.fontSize)} onChange={value => onSettings({...settings, legendFontSize: Number(value) === settings.fontSize ? undefined : Number(value)})}>
            {Array.from({length: legendFontLimits.max - legendFontLimits.min + 1}, (_, index) => legendFontLimits.min + index).map(value => <option key={value} value={value}>{value} pt{value === settings.fontSize ? ' (as the text)' : ''}</option>)}
          </SelectField>
        </>}
        {count > 1 && <Toggle label="One shared legend (in chart a)" checked={sharedLegend} onChange={setSharedLegend} hint="Drawn once when every chart shows the same variants" />}
        {legendEntries(view).length > 0 && <Group title="Entries">
          <p className="ks-hint">Each entry is a colour and what it means. Click the colour to change it; type its text beside it.</p>
          <ul className="ks-legend-entries">{legendEntries(view).map(entry => {
            const text = overrides.series?.[entry.name]
            return <li key={entry.key} className="ks-legend-entry">
              {entry.color && (entry.editable
                ? <label className="ks-legend-color" style={{background: entry.color}} title="Change the colour"><input type="color" className="visually-hidden" aria-label={`Colour of ${text ?? entry.name}`} value={entry.color} onChange={event => setSeriesColor(view, entry.key, event.target.value)} /></label>
                : <span className="ks-legend-color" style={{background: entry.color}} title="The colour of this variant" />)}
              <input type="text" aria-label={`Text for ${entry.name}`} value={text ?? entry.name} onChange={event => setSeriesLabel(view, entry.name, event.target.value === entry.name ? undefined : event.target.value)} />
              {text !== undefined && <button type="button" className="ks-icon-button" aria-label={`Restore ${entry.name}`} title={`Restore “${entry.name}”`} onClick={() => setSeriesLabel(view, entry.name, undefined)}><ResetIcon /></button>}
            </li>
          })}</ul>
          {view.model?.legend.length === 1 && overrides.series?.[legendEntries(view)[0].name] === undefined && <p className="ks-hint">With a single line the legend stays hidden until you give its entry a text.</p>}
          {!legendEntries(view)[0].editable && <p className="ks-hint">Colours follow the variants in this chart form.</p>}
        </Group>}
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
            <p className="ks-hint">For a threshold on another metric or the right axis, add a threshold line with + in “Add a line in the graph”.</p>
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
          {axisTargets(view).length > 0 && <div className="ks-field"><span className="ks-label">Axis settings</span>
            <p className="ks-static">{axisTargets(view).map((axis, index) => <Fragment key={axis.side}>{index > 0 && ' · '}<button type="button" className="ks-link" onClick={() => select({chart: view.chart.id, target: {type: 'axis', side: axis.side, panel: 0}})}>{axis.label}</button></Fragment>)}</p>
            <p className="ks-hint">Click an axis title on the figure to rename it.</p>
          </div>}
          <p className="ks-hint">Starting points from the chart switcher stay fully editable: change X, Y and every style.</p>
        </Group>
        {/* Only with repetitions: one run per series has nothing to summarise. */}
        {spec?.type === 'timeseries' && model?.legend.some(item => item.series.n > 1) && <Group title="Uncertainty">
          <SelectField label="Repeated runs" value={spec.runMode} onChange={value => setChartSpec(view)(current => ({...current, runMode: value as RunMode}))} hint={model.runModeFallback ? 'Individual repetitions are not available here; showing the mean with its 95 % CI.' : runNames[spec.runMode].hint}>
            {(Object.keys(runNames) as RunMode[]).map(mode => <option key={mode} value={mode}>{runNames[mode].label}</option>)}
          </SelectField>
        </Group>}
        {spec?.type === 'xy' && <Group title="Scatter">
          <div className="ks-toggles">
            <Toggle label="Fit line and correlation" checked={view.xy.options.fit} onChange={fitLine => setChartXY(view, state => ({...state, options: {...state.options, fit: fitLine}}))} />
            <Toggle label="Connect each run in time order" checked={view.xy.options.path} onChange={path => setChartXY(view, state => ({...state, options: {...state.options, path}}))} />
          </div>
        </Group>}
        {spec && <Group title="Chart settings">
          {spec.type === 'timeseries' && spec.layout.grid !== '1x1' && <Chips legend="Panels inside this chart" name={`${id}-grid`} value={spec.layout.grid} onChange={(grid: PanelGrid) => setChartSpec(view)(current => ({...current, layout: {...current.layout, grid}}))} choices={[{value: '1x1', label: '1'}, {value: '1x2', label: '1 × 2'}, {value: '2x1', label: '2 × 1'}, {value: '2x2', label: '2 × 2'}]} />}
          <Chips legend="Grid lines" name={`${id}-gridlines`} value={spec.grid} onChange={grid => setChartSpec(view)(current => ({...current, grid}))} choices={[{value: 'none', label: 'None'}, {value: 'major', label: 'Major'}, {value: 'minor', label: 'Minor', hint: 'Major and minor gridlines'}]} />
        </Group>}
        {spec && <Group title="Colours">
          <PaletteEditor spec={spec} setSpec={setChartSpec(view)} />
        </Group>}
        {count > 1 && <p className="ks-hint">Drag a chart by its handle to move it; right-click it to duplicate or delete it.</p>}
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
  // Canvas: the image on paper, its right-click menu and the optional caption.
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
              onClick={onPaperClick} onPointerDown={onPaperPointerDown} onPointerMove={onPaperPointerMove} onPointerUp={onPaperPointerUp} onPointerLeave={() => {if (!drag.current) setHoverKey('')}} onContextMenu={onPaperContextMenu}>
              {views.map(view => {
                const cell = cellOf(view)
                return <div key={view.chart.id} className="ks-cell" data-chart={view.chart.id} data-selected={selectedKey === `${view.chart.id}/` || undefined} data-hover={hoveredChart === view.chart.id || undefined}
                  data-dragging={dragging === view.chart.id || undefined} data-drop={dropOn === view.chart.id && dragging !== view.chart.id || undefined}
                  onDragOver={event => {if (!dragging) return; event.preventDefault(); if (dropOn !== view.chart.id) setDropOn(view.chart.id)}}
                  onDrop={event => {event.preventDefault(); if (dragging) moveChart(dragging, view.index); setDragging(undefined); setDropOn(undefined)}}
                  style={single ? undefined : {left: px(cell.x), top: px(cell.y), width: px(cell.width)}}>
                  {view.letter && <span className="ks-letter" aria-hidden="true" style={{fontSize: px(settings.fontSize), fontFamily: fontStacks[settings.font], top: -px(settings.fontSize * 1.35)}}>{view.letter}</span>}
                  {!single && <button type="button" className="ks-grip" draggable aria-label={`Move chart ${view.letter}: drag it onto another chart, or use the arrow keys`} title="Drag to move this chart"
                    onClick={event => event.stopPropagation()} onPointerDown={event => event.stopPropagation()}
                    onDragStart={event => {event.dataTransfer.effectAllowed = 'move'; event.dataTransfer.setData('text/plain', view.chart.id); setDragging(view.chart.id)}}
                    onDragEnd={() => {setDragging(undefined); setDropOn(undefined)}}
                    onKeyDown={event => {const by = event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : 0; if (by) {event.preventDefault(); moveChart(view.chart.id, view.index + by)}}}>
                    <svg width="12" height="16" viewBox="0 0 12 16" aria-hidden="true"><circle cx="4" cy="4" r="1.3" /><circle cx="8" cy="4" r="1.3" /><circle cx="4" cy="8" r="1.3" /><circle cx="8" cy="8" r="1.3" /><circle cx="4" cy="12" r="1.3" /><circle cx="8" cy="12" r="1.3" /></svg>
                  </button>}
                  <FigureHostContext.Provider value={hosts[view.chart.id]}>{rendered[view.index][0]}</FigureHostContext.Provider>
                  {overlayFor(view)}
                </div>
              })}
              {axisEdit && <input className="ks-axis-input" aria-label={`${axisEdit.side === 'x' ? 'X' : axisEdit.side === 'right' ? 'Right Y' : 'Y'} axis title`} defaultValue={axisEdit.value} autoFocus maxLength={120}
                style={{left: axisEdit.left, top: axisEdit.top, width: axisEdit.width}} onFocus={event => event.currentTarget.select()}
                onClick={event => event.stopPropagation()} onPointerDown={event => event.stopPropagation()}
                onBlur={event => {if (axisCancelled.current) axisCancelled.current = false; else commitAxis(event.currentTarget.value)}}
                onKeyDown={event => {event.stopPropagation(); if (event.key === 'Enter') event.currentTarget.blur(); else if (event.key === 'Escape') {event.preventDefault(); axisCancelled.current = true; setAxisEdit(undefined)}}} />}
            </div>}
      {publication && (captionEditing
        ? <div className="ks-caption-editor" style={{width: `${Math.round(px(pageWidth))}px`}}>
          <label className="ks-label" htmlFor={`${id}-caption`}>Caption</label>
          <textarea id={`${id}-caption`} rows={3} autoFocus value={caption} placeholder="Describe what the figure shows, e.g. P95 latency stays under the SLO until the load spike at 8 min." onChange={event => {const value = event.target.value; setDoc(current => ({...current, caption: value.trim() ? value : undefined}))}} onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); setCaptionEditing(false)}}} />
          <div className="ks-actions">
            {caption && <button type="button" className="ks-link" onClick={() => {setDoc(current => ({...current, caption: undefined})); setCaptionEditing(false)}}>Remove caption</button>}
            <button type="button" className="ks-button ks-push" onClick={() => setCaptionEditing(false)}>Done</button>
          </div>
        </div>
        : caption && <button type="button" className="ks-caption" style={{width: `${Math.round(px(pageWidth))}px`}} title="Edit the caption" onClick={() => setCaptionEditing(true)}>{caption}</button>)}
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
    if (landing && event.key !== '?') return
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
        {landing && <p className="ks-landing-title">New figure</p>}
        {!landing && data && <button type="button" className="ks-icon-button ks-home" aria-label="Back to suggestions" title="Back to suggestions — this figure stays available as “Continue last figure”" onClick={() => {setSelection(undefined); setSwitcher(undefined); setExportOpen(false); setReturned(true); setLanding(true)}}>
          <svg {...svgProps}><path d="M4 11 12 4l8 7" /><path d="M6 9.5V20h4.5v-5.5h3V20H18V9.5" /></svg>
        </button>}
        {data && !landing && <FigureSwitcher items={items.filter(item => item.kind !== 'placement' || (count === 1 && switcher !== 'add'))} active={activeView?.kind}
          open={Boolean(switcher)} onOpenChange={open => setSwitcher(open ? 'change' : undefined)} adding={switcher === 'add' ? `chart (${letters[count]})` : undefined} prefix={activeView?.letter ? `${activeView.letter} ` : ''} label={activeView?.chart.origin === 'custom' ? `Custom ${customForms.find(form => form.kind === activeView.kind)?.label.toLowerCase() ?? 'chart'}` : activeView?.kind && items.find(item => item.kind === activeView.kind)?.recipe?.metric && items.find(item => item.kind === activeView.kind)!.recipe!.metric !== activeView.metric ? kindLabels[activeView.kind] : undefined} forms={forms} onPick={item => switcher === 'add' ? addRecipe(item) : applyRecipe(activeView, item)} onCustom={form => switcher === 'add' ? addCustom(form) : applyForm(activeView, form)} />}
        <div className="ks-name">
          {publication && !landing && <><label htmlFor={`${id}-name`} className="visually-hidden">Figure name</label>
            <input id={`${id}-name`} type="text" value={name} placeholder="Figure name" title="Figure name — also the exported file name" onChange={event => {const value = event.target.value; setDoc(current => ({...current, name: value === autoName ? undefined : value}))}} /></>}
        </div>
        <div className="ks-tools">
          {!landing && <><div className="ks-history" role="group" aria-label="History">
            <button type="button" className="ks-icon-button" aria-label="Undo" aria-keyshortcuts="Control+Z Meta+Z" title="Undo (Ctrl/⌘ Z)" disabled={!history.canUndo} onClick={undo}><UndoIcon /></button>
            <button type="button" className="ks-icon-button" aria-label="Redo" aria-keyshortcuts="Control+Shift+Z Meta+Shift+Z" title="Redo (Ctrl/⌘ Shift Z)" disabled={!history.canRedo} onClick={redo}><UndoIcon redo /></button>
          </div>
          {storageKey && <span className={`ks-saved ${saved === false ? 'failed' : ''}`} title={savedText}><svg {...svgProps} width={14} height={14} strokeWidth={2}>{saved === false ? <path d="M12 8v5M12 16.5v.5" /> : <path d="m5 12 5 5 9-10" />}</svg><span>{saved === false ? 'Not saved' : 'Saved'}</span><span className="visually-hidden">: {savedText}</span></span>}</>}
          <button type="button" className="ks-icon-button" aria-label="Keyboard shortcuts" aria-keyshortcuts="?" title="Keyboard shortcuts (?)" aria-expanded={help} onClick={() => setHelp(value => !value)}><span className="ks-help-glyph" aria-hidden="true">?</span></button>
          {onKeep && data && publication && !landing && <button type="button" className="ks-button ks-keep" onClick={keep} title={keptId ? 'Save these changes to the figure on the results page' : 'Keep this image on the experiment’s results page'}>{keptId ? 'Update in results' : 'Add to results'}</button>}
          {data && publication && !landing && <ExportPopover open={exportOpen} onOpenChange={setExportOpen} format={format} onFormat={action => {setFormat(action); rememberFormat(action)}} busy={Boolean(busy)} csv={publication} code={Boolean(codeView)}
            page={{width: pageWidth, height: pageHeight, font: settings.font === 'serif' ? 'Times' : 'Helvetica', fontSize: settings.fontSize}} base={fileName}
            onExport={action => void exportAs(action)} onCopy={action => void copyFigure(action)} onSave={onSave ? () => void save() : undefined} saving={busy === 'save'} />}
          <button type="button" className="ks-icon-button ks-close" aria-label="Close" title="Close (Esc)" onClick={onClose}><ActionIcon name="close" /></button>
        </div>
      </header>
      <span className="visually-hidden" role="status">{status}</span>
      {error && <div className="ks-error"><ErrorFeedback message={error} /></div>}
      {landing && data && <StudioLanding data={data} groups={groups} forms={forms} last={(returned || remembered.charts?.length) && charts.length ? {name, charts: charts.length} : undefined}
        onPick={suggestion => startWith({...suggestion.chart, id: newChartId(), origin: 'recipe'}, `${suggestion.title}. Refine it: click any part of the chart to change it.`)}
        onCustom={form => {const chart = customChart(undefined, form); if (chart) startWith(chart, `Custom ${customForms.find(item => item.kind === form)?.label.toLowerCase()}: choose X and Y on the right.`, true)}}
        onContinue={() => {setLanding(false); announce(returned ? 'Back to your figure, with its undo history.' : 'Your last figure. Every change is saved in this browser.')}} />}
      {!landing && narrow && <div className="ks-tabs" role="tablist" aria-label={`${studioName} panels`} onKeyDown={onTabKey}>
        {tabs.map(item => <button key={item.id} type="button" role="tab" id={`${id}-${item.id}-tab`} aria-selected={tab === item.id} aria-controls={`${id}-${item.id}`} tabIndex={tab === item.id ? 0 : -1} onClick={() => setTab(item.id)}>{item.label}</button>)}
      </div>}
      {!landing && <div className="ks-workspace">
        {panel('layers', 'ks-left', layers, 'Layers')}
        {panel('canvas', 'ks-centre', canvas, 'Figure')}
        {panel('properties', 'ks-right', inspector, 'Properties')}
      </div>}
      {menu && <ContextMenu at={menu.at} label={menu.chart ? `Chart ${viewOf(menu.chart)?.letter ?? ''}`.trim() : 'Figure'} onClose={() => setMenu(undefined)} items={menuItems(menu.chart)} />}
      {help && <div className="ks-popover ks-help" role="dialog" aria-label="Keyboard shortcuts" onKeyDown={event => {if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); setHelp(false)}}}>
        <div className="ks-help-head"><strong>Keyboard shortcuts</strong><button type="button" className="ks-icon-button ks-small" aria-label="Close shortcuts" onClick={() => setHelp(false)}><ActionIcon name="close" /></button></div>
        <dl>{shortcuts.map(([keys, action]) => <div key={keys}><dt><kbd>{keys}</kbd></dt><dd>{action}</dd></div>)}</dl>
        <p className="ks-hint">Select anything in Layers or click it on the figure; its properties appear on the right. Add chart puts up to four charts in one image.</p>
      </div>}
    </div>}
  </dialog>
}

