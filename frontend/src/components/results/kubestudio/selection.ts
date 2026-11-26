import type {FigureGeometry} from './SpecFigure'

// What is selected in the image: a chart, or an element inside it. Keys match the
// figures' data-select attributes ("series:…", "axis:left:0", "annotation:0:slo"…).

export type Target = {type: 'series'; key: string} | {type: 'group'; label: string} | {type: 'axis'; side: 'x' | 'left' | 'right'; panel: number} | {type: 'legend'} | {type: 'title'} | {type: 'annotation'; panel: number; id: string}
export interface Selection {chart: string; target?: Target}

export function parseTarget(value: string | null | undefined): Target | undefined {
  if (!value) return undefined
  if (value === 'legend' || value === 'title') return {type: value}
  if (value.startsWith('series:')) return {type: 'series', key: value.slice(7)}
  if (value.startsWith('group:')) return {type: 'group', label: value.slice(6)}
  const [type, first, second] = value.split(':')
  if (type === 'axis' && (first === 'x' || first === 'left' || first === 'right')) return {type: 'axis', side: first, panel: Number(second) || 0}
  if (type === 'annotation' && second) return {type: 'annotation', panel: Number(first) || 0, id: second}
  return undefined
}

export function targetKey(target: Target | undefined): string {
  if (!target) return ''
  if (target.type === 'series') return `series:${target.key}`
  if (target.type === 'group') return `group:${target.label}`
  if (target.type === 'axis') return `axis:${target.side}:${target.panel}`
  if (target.type === 'annotation') return `annotation:${target.panel}:${target.id}`
  return target.type
}

export const selectionKey = (selection: Selection | undefined) => selection ? `${selection.chart}/${targetKey(selection.target)}` : ''

export function parseKey(key: string): Selection | undefined {
  if (!key) return undefined
  const slash = key.indexOf('/')
  return {chart: key.slice(0, slash), target: parseTarget(key.slice(slash + 1))}
}

// Lines drawn automatically from the experiment, selectable like the user's annotations.
export const autoAnnotations: Record<string, {label: string; key: 'slo' | 'phases' | 'warmup'}> = {slo: {label: 'SLO line', key: 'slo'}, phase: {label: 'Load phases', key: 'phases'}, warmup: {label: 'Warm-up end', key: 'warmup'}}

// Canvas highlight: hugs the element in chart coordinates (points), on a layer above
// the paper that never reaches the exports.
// clip: the plot area a line's highlight stays inside (figure coordinates).
export interface Shape {kind: 'path' | 'rect'; d?: string; x?: number; y?: number; width?: number; height?: number; handles?: Array<{x: number; y: number}>; clip?: {x: number; y: number; width: number; height: number}}

// The plot frame of the panel an element belongs to.
function plotClip(element: Element): Shape['clip'] {
  const frame = element.closest('.figure-panel')?.querySelector(':scope > rect.axis')
  if (!frame) return undefined
  const [x, y, width, height] = ['x', 'y', 'width', 'height'].map(name => Number(frame.getAttribute(name)))
  return [x, y, width, height].every(Number.isFinite) ? {x, y, width, height} : undefined
}

export function highlightShapes(cell: Element | null, key: string, geometry: FigureGeometry | undefined): Shape[] {
  if (!cell || !key || !geometry) return []
  const svg = cell.querySelector('svg.pub-figure')
  if (!svg) return []
  const elements = [...svg.querySelectorAll('[data-select]')].filter(element => element.getAttribute('data-select') === key)
  if (!elements.length) return []
  const paths = elements.flatMap(element => element.classList.contains('series') ? [...element.querySelectorAll('path.series-line, path.series-fit')] : []).map(path => path.getAttribute('d')).filter((d): d is string => Boolean(d))
  if (paths.length) {const clip = plotClip(elements[0]); return paths.map(d => ({kind: 'path', d, clip}))}
  const line = elements.length === 1 ? elements[0].querySelector(':scope > line') : null
  if (line && elements[0].classList.contains('annotation')) {
    const [x1, y1, x2, y2] = ['x1', 'y1', 'x2', 'y2'].map(name => Number(line.getAttribute(name)))
    const clip = plotClip(elements[0])
    // The handle sits on the line, inside the plot even when the line runs along its edge.
    const inside = (value: number, from: number, size: number) => Math.min(Math.max(value, from + 2.25), from + size - 2.25)
    const handle = {x: (x1 + x2) / 2, y: (y1 + y2) / 2}
    return [{kind: 'path', d: `M${x1},${y1}L${x2},${y2}`, clip, handles: [clip ? {x: inside(handle.x, clip.x, clip.width), y: inside(handle.y, clip.y, clip.height)} : handle]}]
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
export function snap(value: number, span: number) {
  if (!Number.isFinite(value)) return value
  const raw = Math.max(Math.abs(span), 1e-9) / 20
  const power = 10 ** Math.floor(Math.log10(raw))
  const step = [1, 2, 5, 10].map(factor => factor * power).find(candidate => candidate >= raw) ?? raw
  return Number((Math.round(value / step) * step).toPrecision(10))
}
