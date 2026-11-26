import type {FigureLabelOverrides} from '../chartKit'
import {contrast} from '../chartColor'
import {defaultFigureSettings, normalizeFigureSettings, type FigureSettings} from '../figureSettings'
import type {XYAxisSelection} from '../xy'

// FigureSpec: the serializable description of one studio figure (type, data bindings,
// per-series styles, axes, annotations, layout and style). The studio renders the preview
// from it, every export (SVG/PDF/PNG/CSV, matplotlib, pgfplots) derives from the same
// resolved model, and it is persisted per experiment and copied/pasted as JSON.

type SpecType = 'timeseries' | 'xy'
export type MarkerShape = 'circle' | 'square' | 'triangle' | 'diamond' | 'cross' | 'x' | 'none'
export type LineStyle = 'solid' | 'dashed' | 'dotted' | 'dashdot' | 'none'
export type RunMode = 'ci' | 'sd' | 'iqr' | 'spaghetti' | 'runs' | 'mean'
export type AxisSide = 'left' | 'right'
type RangeMode = 'auto' | 'zero' | 'custom'
type TickMode = 'auto' | 'count' | 'step'
export type GridMode = 'none' | 'major' | 'minor'
export type TimeFormat = 'min' | 's' | 'mmss'
export type PanelGrid = '1x1' | '1x2' | '2x1' | '2x2'
export type PaletteId = 'okabe-ito' | 'tableau10' | 'tol-bright' | 'tol-muted' | 'viridis' | 'grayscale' | 'page'

export const markerShapes: MarkerShape[] = ['circle', 'square', 'triangle', 'diamond', 'cross', 'x', 'none']
export const lineStyles: LineStyle[] = ['solid', 'dashed', 'dotted', 'dashdot', 'none']
const runModes: RunMode[] = ['ci', 'sd', 'iqr', 'spaghetti', 'runs', 'mean']

export interface SeriesStyleSpec {
  color?: string
  marker?: MarkerShape
  // Marker radius in points.
  markerSize?: number
  hollow?: boolean
  line?: LineStyle
  // Line width in points.
  lineWidth?: number
  // 0.1–1; drawn as the colour blended with the white paper (exact in PDF and SVG).
  opacity?: number
}

export interface AxisSpec {
  label?: string
  range: RangeMode
  min?: number
  max?: number
  ticks: TickMode
  tickValue?: number
  log: boolean
  // Display unit id (see unitChoices); undefined = the metric's default.
  unit?: string
  // Fixed decimals for tick labels; undefined = automatic.
  decimals?: number
  thousands: boolean
}

export interface XAxisSpec extends AxisSpec {time: TimeFormat}

interface MetricBinding {metric: string; axis: AxisSide}

export type Annotation =
  | {id: string; kind: 'hline'; axis: AxisSide; value: number; label?: string}
  | {id: string; kind: 'vline'; at: number; label?: string}
  | {id: string; kind: 'xrange'; from: number; to: number; label?: string}
  | {id: string; kind: 'text'; axis: AxisSide; at: number; value: number; text: string}

export interface PanelSpec {
  // Time series: metrics on the left/right axis (panel 1's first one is the studio's metric).
  metrics: MetricBinding[]
  // X–Y: the X and Y metrics with fit/path options, and an optional second Y metric on the right axis.
  xy?: {x: XYAxisSelection; y: XYAxisSelection; fit: boolean; path: boolean}
  y2?: XYAxisSelection
  x: XAxisSpec
  left: AxisSpec
  right: AxisSpec
  // Annotations in data coordinates: x in seconds since the start of the measured window
  // (time series) or X-axis data units (X–Y); y in the display unit of its axis.
  annotations: Annotation[]
}

export interface FigureSpec {
  version: 1
  type: SpecType
  layout: {grid: PanelGrid; sharedX: boolean; sharedY: boolean; letters: boolean}
  panels: PanelSpec[]
  runMode: RunMode
  palette: PaletteId
  // Per-series overrides, keyed by seriesKey(metric, group).
  series: Record<string, SeriesStyleSpec>
  // Legend and drawing order (later = drawn on top); keys not listed keep their default order.
  order: string[]
  // sloValue: the SLO threshold in milliseconds when the figure overrides the experiment's;
  // sloUnit: how its label and field show it (default: the axis unit); labels: the user's text
  // for automatic labels, by their key ("slo", "warmup" or the phase's own text).
  auto: {phases: boolean; warmup: boolean; slo: boolean; sloValue?: number; sloUnit?: 'ms' | 's'; labels?: Record<string, string>}
  grid: GridMode
  // Line and marker scale of the style preset (1 = paper).
  strokeScale: number
  labels: FigureLabelOverrides
  style: FigureSettings
}

export const seriesKey = (metric: string, group: string) => `${metric}|${group}`

export const defaultAxis = (range: RangeMode = 'zero'): AxisSpec => ({range, ticks: 'auto', log: false, thousands: false})
const defaultXAxis = (): XAxisSpec => ({...defaultAxis('auto'), time: 'min'})
export const defaultPanel = (metrics: MetricBinding[] = []): PanelSpec => ({metrics, x: defaultXAxis(), left: defaultAxis(), right: defaultAxis(), annotations: []})

export function defaultSpec(type: SpecType = 'timeseries', style: FigureSettings = defaultFigureSettings): FigureSpec {
  return {version: 1, type, layout: {grid: '1x1', sharedX: true, sharedY: false, letters: true}, panels: [defaultPanel()], runMode: 'ci', palette: 'okabe-ito', series: {}, order: [], auto: {phases: true, warmup: false, slo: true}, grid: 'major', strokeScale: 1, labels: {}, style}
}

const panelCount = (grid: PanelGrid) => grid === '1x1' ? 1 : grid === '2x2' ? 4 : 2
export const gridShape = (grid: PanelGrid) => ({rows: grid === '2x1' || grid === '2x2' ? 2 : 1, columns: grid === '1x2' || grid === '2x2' ? 2 : 1})

// ---------------------------------------------------------------------------------------
// Validation: anything read from storage or pasted is normalised; unknown fields are dropped.

const isObject = (value: unknown): value is Record<string, unknown> => Boolean(value) && typeof value === 'object' && !Array.isArray(value)
const finite = (value: unknown): number | undefined => typeof value === 'number' && Number.isFinite(value) ? value : undefined
const oneOf = <T extends string>(value: unknown, options: readonly T[], fallback: T): T => options.includes(value as T) ? value as T : fallback
const text = (value: unknown, max = 200): string | undefined => typeof value === 'string' ? value.slice(0, max) : undefined
const hex = (value: unknown): string | undefined => typeof value === 'string' && /^#[0-9a-fA-F]{6}$/.test(value) ? value.toLowerCase() : undefined
const clamp = (value: number | undefined, low: number, high: number) => value === undefined ? undefined : Math.min(high, Math.max(low, value))

function normalizeAxis(value: unknown, range: RangeMode): AxisSpec {
  const source = isObject(value) ? value : {}
  const axis: AxisSpec = {range: oneOf(source.range, ['auto', 'zero', 'custom'] as const, range), ticks: oneOf(source.ticks, ['auto', 'count', 'step'] as const, 'auto'), log: source.log === true, thousands: source.thousands === true}
  const optional: Partial<AxisSpec> = {label: text(source.label), min: finite(source.min), max: finite(source.max), tickValue: finite(source.tickValue), unit: text(source.unit, 20), decimals: clamp(finite(source.decimals), 0, 6)}
  for (const [key, item] of Object.entries(optional)) if (item !== undefined) (axis as unknown as Record<string, unknown>)[key] = item
  if (axis.tickValue !== undefined && !(axis.tickValue > 0)) delete axis.tickValue
  return axis
}

function normalizeAnnotation(value: unknown, index: number): Annotation | undefined {
  if (!isObject(value)) return undefined
  const id = text(value.id, 40) || `a${index + 1}`
  const label = text(value.label)
  const axis = oneOf(value.axis, ['left', 'right'] as const, 'left')
  const withLabel = <T extends object>(item: T) => label ? {...item, label} : item
  if (value.kind === 'hline' && finite(value.value) !== undefined) return withLabel({id, kind: 'hline' as const, axis, value: finite(value.value)!})
  if (value.kind === 'vline' && finite(value.at) !== undefined) return withLabel({id, kind: 'vline' as const, at: finite(value.at)!})
  if (value.kind === 'xrange' && finite(value.from) !== undefined && finite(value.to) !== undefined) return withLabel({id, kind: 'xrange' as const, from: Math.min(finite(value.from)!, finite(value.to)!), to: Math.max(finite(value.from)!, finite(value.to)!)})
  if (value.kind === 'text' && finite(value.at) !== undefined && finite(value.value) !== undefined && text(value.text)) return {id, kind: 'text', axis, at: finite(value.at)!, value: finite(value.value)!, text: text(value.text)!}
  return undefined
}

function normalizePanel(value: unknown): PanelSpec {
  const source = isObject(value) ? value : {}
  const metrics = Array.isArray(source.metrics) ? source.metrics.flatMap(item => isObject(item) && typeof item.metric === 'string' && item.metric ? [{metric: item.metric.slice(0, 120), axis: oneOf(item.axis, ['left', 'right'] as const, 'left')}] : []).slice(0, 8) : []
  const x = normalizeAxis(source.x, 'auto') as XAxisSpec
  x.time = oneOf(isObject(source.x) ? source.x.time : undefined, ['min', 's', 'mmss'] as const, 'min')
  const panel: PanelSpec = {metrics, x, left: normalizeAxis(source.left, 'zero'), right: normalizeAxis(source.right, 'zero'), annotations: Array.isArray(source.annotations) ? source.annotations.slice(0, 40).map(normalizeAnnotation).filter((item): item is Annotation => Boolean(item)) : []}
  const selection = (item: unknown): XYAxisSelection | undefined => isObject(item) && typeof item.metric === 'string' && item.metric ? {metric: item.metric.slice(0, 120), ...(typeof item.service === 'string' && item.service ? {service: item.service.slice(0, 120)} : {})} : undefined
  const y2 = selection(source.y2)
  if (y2) panel.y2 = y2
  if (isObject(source.xy) && selection(source.xy.x) && selection(source.xy.y)) panel.xy = {x: selection(source.xy.x)!, y: selection(source.xy.y)!, fit: source.xy.fit !== false, path: source.xy.path === true}
  return panel
}

function normalizeSeriesStyle(value: unknown): SeriesStyleSpec | undefined {
  if (!isObject(value)) return undefined
  const style: SeriesStyleSpec = {}
  if (hex(value.color)) style.color = hex(value.color)
  if (markerShapes.includes(value.marker as MarkerShape)) style.marker = value.marker as MarkerShape
  if (lineStyles.includes(value.line as LineStyle)) style.line = value.line as LineStyle
  if (typeof value.hollow === 'boolean') style.hollow = value.hollow
  const size = clamp(finite(value.markerSize), .5, 8)
  if (size !== undefined) style.markerSize = size
  const width = clamp(finite(value.lineWidth), .25, 6)
  if (width !== undefined) style.lineWidth = width
  const opacity = clamp(finite(value.opacity), .1, 1)
  if (opacity !== undefined) style.opacity = opacity
  return Object.keys(style).length ? style : undefined
}

function normalizeLabels(value: unknown): FigureLabelOverrides {
  if (!isObject(value)) return {}
  const labels: FigureLabelOverrides = {}
  for (const key of ['title', 'x', 'y'] as const) if (text(value[key])) labels[key] = text(value[key])
  if (isObject(value.series)) {
    const series = Object.fromEntries(Object.entries(value.series).flatMap(([key, item]) => text(item) ? [[key.slice(0, 200), text(item)!]] : []))
    if (Object.keys(series).length) labels.series = series
  }
  return labels
}

export function normalizeSpec(value: unknown, fallbackType: SpecType = 'timeseries'): FigureSpec {
  const source = isObject(value) ? value : {}
  const base = defaultSpec(oneOf(source.type, ['timeseries', 'xy'] as const, fallbackType))
  const layout = isObject(source.layout) ? source.layout : {}
  const grid = base.type === 'xy' ? '1x1' : oneOf(layout.grid, ['1x1', '1x2', '2x1', '2x2'] as const, '1x1')
  const panels = (Array.isArray(source.panels) ? source.panels : []).slice(0, 4).map(normalizePanel)
  while (panels.length < panelCount(grid)) panels.push(defaultPanel())
  const auto = isObject(source.auto) ? source.auto : {}
  const series = isObject(source.series) ? Object.fromEntries(Object.entries(source.series).slice(0, 200).flatMap(([key, item]) => {const style = normalizeSeriesStyle(item); return style ? [[key.slice(0, 200), style]] : []})) : {}
  return {
    version: 1, type: base.type,
    layout: {grid, sharedX: layout.sharedX !== false, sharedY: layout.sharedY === true, letters: layout.letters !== false},
    panels,
    runMode: oneOf(source.runMode, runModes, base.runMode),
    palette: oneOf(source.palette, Object.keys(palettes) as PaletteId[], base.palette),
    series,
    order: Array.isArray(source.order) ? source.order.filter((item): item is string => typeof item === 'string').slice(0, 200) : [],
    auto: {phases: auto.phases !== false, warmup: auto.warmup === true, slo: auto.slo !== false, ...(finite(auto.sloValue) !== undefined && finite(auto.sloValue)! > 0 ? {sloValue: finite(auto.sloValue)} : {}), ...(auto.sloUnit === 'ms' || auto.sloUnit === 's' ? {sloUnit: auto.sloUnit} : {}), ...(auto.labels && typeof auto.labels === 'object' && Object.keys(auto.labels).length ? {labels: Object.fromEntries(Object.entries(auto.labels as Record<string, unknown>).filter((entry): entry is [string, string] => typeof entry[1] === 'string'))} : {})},
    grid: oneOf(source.grid, ['none', 'major', 'minor'] as const, base.grid),
    strokeScale: clamp(finite(source.strokeScale), .5, 3) ?? 1,
    labels: normalizeLabels(source.labels),
    style: source.style === undefined ? base.style : normalizeFigureSettings(source.style)
  }
}

// Paste: accepts the JSON produced by "Copy spec"; throws a readable error otherwise.
export function parseSpec(json: string): FigureSpec {
  let value: unknown
  try {value = JSON.parse(json)} catch {throw new Error('The clipboard does not contain a figure spec (invalid JSON).')}
  if (!isObject(value) || value.version !== 1 || (value.type !== 'timeseries' && value.type !== 'xy')) throw new Error('The clipboard does not contain a KubePhos figure spec (version 1).')
  return normalizeSpec(value)
}

export const serializeSpec = (spec: FigureSpec) => JSON.stringify(spec, null, 2)

// ---------------------------------------------------------------------------------------
// Palettes. Each lists an order for one axis and a second, different hue family for series
// on the right axis, so the two axes never share a colour.

export interface Palette {label: string; hint: string; left: string[]; right: string[]; sequential?: boolean}

export const palettes: Record<PaletteId, Palette> = {
  'okabe-ito': {label: 'Okabe–Ito', hint: 'Colour-blind safe (default)', left: ['#0072b2', '#009e73', '#56b4e9', '#000000'], right: ['#d55e00', '#cc79a7', '#e69f00', '#f0e442']},
  tableau10: {label: 'Tableau 10', hint: 'Familiar, 10 hues', left: ['#4e79a7', '#59a14f', '#76b7b2', '#b07aa1', '#9c755f'], right: ['#e15759', '#f28e2b', '#ff9da7', '#edc948', '#bab0ac']},
  'tol-bright': {label: 'Paul Tol bright', hint: 'Colour-blind safe', left: ['#4477aa', '#228833', '#66ccee', '#bbbbbb'], right: ['#ee6677', '#aa3377', '#ccbb44']},
  'tol-muted': {label: 'Paul Tol muted', hint: 'Colour-blind safe, 9 hues', left: ['#332288', '#117733', '#44aa99', '#88ccee'], right: ['#cc6677', '#882255', '#aa4499', '#999933', '#ddcc77']},
  viridis: {label: 'Viridis', hint: 'Sequential (ordered series)', left: ['#440154', '#3b528b', '#21918c', '#5ec962'], right: ['#7a0403', '#d23105', '#f1731d', '#e6a52a'], sequential: true},
  grayscale: {label: 'Grayscale', hint: 'Black ink, dashes and markers', left: ['#000000', '#4d4d4d', '#7a7a7a', '#a0a0a0'], right: ['#000000', '#4d4d4d', '#7a7a7a', '#a0a0a0']},
  page: {label: 'Page colours', hint: 'Variant colours of the results page', left: ['#1f77b4', '#009e73', '#56b4e9', '#000000'], right: ['#d55e00', '#cc79a7', '#e69f00', '#7f7f7f']}
}

// Interleaved single list (one axis): left[0], right[0], left[1], right[1], …
export function paletteColors(id: PaletteId): string[] {
  const palette = palettes[id]
  return Array.from({length: palette.left.length + palette.right.length}, (_, index) => index % 2 ? palette.right[(index - 1) / 2] : palette.left[index / 2]).filter(Boolean)
}

function interpolate(colors: string[], position: number): string {
  const scaled = Math.min(1, Math.max(0, position)) * (colors.length - 1)
  const low = Math.floor(scaled)
  const high = Math.min(colors.length - 1, low + 1)
  const mix = scaled - low
  const channel = (color: string, offset: number) => parseInt(color.slice(1 + offset, 3 + offset), 16)
  return `#${[0, 2, 4].map(offset => Math.round(channel(colors[low], offset) * (1 - mix) + channel(colors[high], offset) * mix).toString(16).padStart(2, '0')).join('')}`
}

// Default colour of the index-th series of an axis (count = series on that axis). With both
// axes in use each axis draws from its own hue family; a single axis uses the interleaved list.
function defaultColor(palette: PaletteId, side: AxisSide, index: number, count: number, dual: boolean, pageColor?: string): string {
  if (palette === 'page' && pageColor && !dual) return pageColor
  const entry = palettes[palette]
  if (entry.sequential) return interpolate(side === 'right' && dual ? entry.right : entry.left, count <= 1 ? 0 : index / (count - 1))
  if (!dual) {const list = paletteColors(palette); return list[index % list.length]}
  const list = side === 'right' ? entry.right : entry.left
  return list[index % list.length]
}

const defaultShapes: MarkerShape[] = ['circle', 'square', 'triangle', 'diamond', 'cross', 'x']
const grayDashes: LineStyle[] = ['solid', 'dashed', 'dotted', 'dashdot']

export interface ResolvedStyle {color: string; marker: MarkerShape; markerSize: number; hollow: boolean; line: LineStyle; lineWidth: number; opacity: number}

// Effective style of a series: defaults by axis and position, then the user's overrides.
// Right-axis defaults: second hue family, hollow markers, dashed line.
export function resolveStyle({spec, key, side, index, count, dual, pageColor, fontSize}: {spec: Pick<FigureSpec, 'palette' | 'series' | 'strokeScale'>; key: string; side: AxisSide; index: number; count: number; dual: boolean; pageColor?: string; fontSize: number}): ResolvedStyle {
  const right = dual && side === 'right'
  const line = Math.max(.75, fontSize / 9) * 1.2 * spec.strokeScale
  const base: ResolvedStyle = {
    color: defaultColor(spec.palette, side, index, count, dual, pageColor),
    marker: defaultShapes[index % defaultShapes.length],
    // Defaults on a 0.25 pt grid, so the inspector shows round numbers.
    markerSize: Math.max(.5, Math.round(fontSize * .24 * spec.strokeScale * 4) / 4),
    hollow: right,
    line: spec.palette === 'grayscale' ? grayDashes[(index + (right ? 1 : 0)) % grayDashes.length] : right ? 'dashed' : 'solid',
    lineWidth: Math.max(.25, Math.round(line * 4) / 4),
    opacity: 1
  }
  const override = spec.series[key] ?? {}
  return {...base, ...Object.fromEntries(Object.entries(override).filter(([, value]) => value !== undefined))}
}

// SVG dash pattern for a line style, scaled with the line width.
export function dashArray(line: LineStyle, width: number): string | undefined {
  const w = Math.max(.5, width)
  const pattern = line === 'dashed' ? [4, 2.2] : line === 'dotted' ? [1, 1.6] : line === 'dashdot' ? [4, 1.6, 1, 1.6] : undefined
  return pattern?.map(value => Math.round(value * w * 100) / 100).join(' ')
}

// Colour as drawn with the given opacity on white paper.
export function blendWhite(color: string, opacity: number): string {
  if (opacity >= 1) return color
  return `#${[1, 3, 5].map(offset => Math.round(parseInt(color.slice(offset, offset + 2), 16) * opacity + 255 * (1 - opacity)).toString(16).padStart(2, '0')).join('')}`
}

// WCAG contrast against the white paper; graphics need 3:1.
export function paperContrast(color: string): {ratio: number; ok: boolean} {
  const ratio = contrast(color, '#ffffff')
  return {ratio, ok: ratio >= 3}
}

// ---------------------------------------------------------------------------------------
// Units: display conversions per base unit (factor multiplies the stored value).

interface UnitChoice {id: string; label: string; factor: number}
export const unitChoices: Record<string, UnitChoice[]> = {
  milliseconds: [{id: 'ms', label: 'ms', factor: 1}, {id: 's', label: 's', factor: 1e-3}],
  seconds: [{id: 's', label: 's', factor: 1}, {id: 'ms', label: 'ms', factor: 1e3}],
  requests_per_second: [{id: 'req/s', label: 'req/s', factor: 1}, {id: 'req/min', label: 'req/min', factor: 60}],
  bytes: [{id: 'MiB', label: 'MiB', factor: 1 / 1024 ** 2}, {id: 'GiB', label: 'GiB', factor: 1 / 1024 ** 3}, {id: 'KiB', label: 'KiB', factor: 1 / 1024}, {id: 'B', label: 'bytes', factor: 1}],
  bytes_per_second: [{id: 'MiB/s', label: 'MiB/s', factor: 1 / 1024 ** 2}, {id: 'KiB/s', label: 'KiB/s', factor: 1 / 1024}, {id: 'B/s', label: 'B/s', factor: 1}],
  cores: [{id: 'cores', label: 'cores', factor: 1}, {id: 'millicores', label: 'millicores', factor: 1e3}]
}

export function displayUnit(unit: string, id?: string): UnitChoice | undefined {
  const choices = unitChoices[unit]
  if (!choices) return undefined
  return choices.find(choice => choice.id === id) ?? choices[0]
}

// Axis label with its unit suffix replaced by the chosen display unit.
export function relabelUnit(label: string, unit: string, id?: string): string {
  const choice = displayUnit(unit, id)
  if (!choice || choice === unitChoices[unit][0]) return label
  return /\([^()]*\)\s*$/.test(label) ? label.replace(/\([^()]*\)\s*$/, `(${choice.label})`) : `${label} (${choice.label})`
}

// Tick label: fixed decimals or automatic, optional thousands separator.
export function formatTick(value: number, axis: Pick<AxisSpec, 'decimals' | 'thousands'>): string {
  if (!Number.isFinite(value)) return ''
  const absolute = Math.abs(value)
  const digits = axis.decimals ?? (absolute === 0 ? 0 : absolute < 1 ? Math.min(6, Math.max(0, -Math.floor(Math.log10(absolute)) + 1)) : absolute >= 100 ? 0 : absolute >= 10 ? 1 : 2)
  const rounded = Number(value.toFixed(digits))
  const fixed = axis.decimals === undefined ? String(Number(rounded.toPrecision(12))) : rounded.toFixed(digits)
  if (!axis.thousands) return fixed.replace(/^-0(\.0*)?$/, '0$1')
  const [whole, fraction] = fixed.split('.')
  return `${whole.replace(/\B(?=(\d{3})+(?!\d))/g, ',')}${fraction ? `.${fraction}` : ''}`
}

export function formatTime(seconds: number, format: TimeFormat): string {
  if (format === 'mmss') {
    const total = Math.round(seconds)
    const sign = total < 0 ? '−' : ''
    const value = Math.abs(total)
    return `${sign}${Math.floor(value / 60)}:${String(value % 60).padStart(2, '0')}`
  }
  const value = format === 'min' ? seconds / 60 : seconds
  return String(Number(value.toFixed(format === 'min' ? 2 : 1)))
}

export const timeAxisLabel = (format: TimeFormat) => format === 's' ? 'Time (s)' : format === 'mmss' ? 'Time (mm:ss)' : 'Time (min)'

// ---------------------------------------------------------------------------------------
// Style presets: built-in and user-saved (localStorage, shared by every experiment).

export interface StylePreset {name: string; builtIn?: boolean; style: FigureSettings; strokeScale: number; grid: GridMode}

export const builtInPresets: StylePreset[] = [
  {name: 'IEEE/ACM paper', builtIn: true, style: {...defaultFigureSettings, size: 'single', font: 'serif', fontSize: 8, legend: 'bottom'}, strokeScale: 1, grid: 'major'},
  {name: 'Slides', builtIn: true, style: {...defaultFigureSettings, size: 'slide', font: 'sans', fontSize: 12, legend: 'right'}, strokeScale: 1.8, grid: 'major'},
  {name: 'Poster', builtIn: true, style: {...defaultFigureSettings, size: 'custom', customWidth: 10, customHeight: 6, font: 'sans', fontSize: 12, legend: 'top'}, strokeScale: 2.4, grid: 'minor'}
]

const presetKey = 'kubephos.figurePresets.v1'

export function loadPresets(): StylePreset[] {
  try {
    const value = JSON.parse(window.localStorage.getItem(presetKey) ?? '[]') as unknown
    if (!Array.isArray(value)) return []
    return value.flatMap(item => isObject(item) && text(item.name, 60) ? [{name: text(item.name, 60)!, style: normalizeFigureSettings(item.style), strokeScale: clamp(finite(item.strokeScale), .5, 3) ?? 1, grid: oneOf(item.grid, ['none', 'major', 'minor'] as const, 'major')}] : [])
  } catch {
    return []
  }
}

export function savePresets(presets: StylePreset[]): boolean {
  try {
    window.localStorage.setItem(presetKey, JSON.stringify(presets.filter(item => !item.builtIn).slice(0, 30)))
    return true
  } catch {
    return false
  }
}
