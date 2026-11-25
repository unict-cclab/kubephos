import {normalizeSpec} from './figureSpec'
import {maxCharts, newChartId, type ChartLayout, type ChartState} from './layout'
import {kindOrder} from './recipes'
import type {FigureKind} from './image'
import {defaultXYOptions, defaultXYSelection, type XYState} from '../xy'

// The image KubeStudio reopens on: per experiment, in this browser (localStorage;
// failures fall back to memory). The key keeps its historical "figureStudio" name.

interface StudioMemory {charts?: ChartState[]; layout?: ChartLayout; sharedLegend?: boolean; name?: string; caption?: string; hidden?: string[]; zoom?: number | 'fit'}

const memoryPrefix = 'kubephos.figureStudio.v1:'
const isKind = (value: unknown): value is FigureKind => kindOrder.includes(value as FigureKind)

function validXY(value: unknown): XYState | undefined {
  const xy = value as XYState | undefined
  return xy && typeof xy === 'object' && xy.x?.metric && xy.y?.metric ? {...xy, options: {...defaultXYOptions, ...xy.options}} : undefined
}

// Charts read back from storage: unknown kinds are dropped, missing fields get defaults.
export function storedCharts(value: unknown): ChartState[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item): ChartState[] => item && isKind(item.kind) ? [{
    id: typeof item.id === 'string' ? item.id : newChartId(),
    kind: item.kind,
    metric: typeof item.metric === 'string' ? item.metric : '',
    xy: validXY(item.xy) ?? {...defaultXYSelection([]), options: defaultXYOptions},
    spec: (item.kind === 'timeseries' || item.kind === 'xy') && item.spec ? normalizeSpec(item.spec, item.kind) : undefined,
    overrides: item.overrides && typeof item.overrides === 'object' ? item.overrides : undefined,
    origin: item.origin === 'custom' || item.origin === 'recipe' ? item.origin : undefined
  }] : []).slice(0, maxCharts)
}

const text = (value: unknown) => typeof value === 'string' ? value : undefined

export function loadStudioMemory(key?: string): StudioMemory {
  if (!key) return {}
  try {
    const memory = JSON.parse(window.localStorage.getItem(memoryPrefix + key) ?? '{}') as StudioMemory | null
    if (!memory || typeof memory !== 'object') return {}
    const charts = storedCharts(memory.charts)
    return {
      charts: charts.length ? charts : undefined, layout: memory.layout, sharedLegend: memory.sharedLegend === true, name: text(memory.name), caption: text(memory.caption),
      hidden: Array.isArray(memory.hidden) ? memory.hidden.filter(item => typeof item === 'string') : undefined,
      zoom: memory.zoom === 'fit' || typeof memory.zoom === 'number' ? memory.zoom : undefined
    }
  } catch {
    return {}
  }
}

export function saveStudioMemory(key: string | undefined, memory: StudioMemory): boolean {
  if (!key) return true
  try {window.localStorage.setItem(memoryPrefix + key, JSON.stringify({version: 2, ...memory})); return true} catch {return false}
}
