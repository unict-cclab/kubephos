import {useEffect, useMemo, useState} from 'react'
import {request} from '../../../api'
import {defaultFigureSettings, type FigureSettings} from '../figureSettings'
import {groupMetrics, metricTitle} from '../format'
import {defaultXYState, timeMetrics, type ImageState, type StudioData} from './image'
import {newChartId, type ChartState} from './layout'
import {storedCharts} from './memory'

// What a results page shows, arranged by each user and kept on the server
// (/account/result-layouts): the charts, each a KubeStudio image, and the optional
// sections. A user who has not changed anything sees only the default charts: the
// placement heatmap, then each metric over time followed by the same metric by service.

export interface ChartItem extends ImageState {
  id: string
  name: string
  caption: string
  // The page style of an image edited in KubeStudio; default charts use the page's.
  settings?: FigureSettings
  // Share of the row the chart takes (absent: a third); a narrower page shows it wider.
  size?: ChartSize
  // Hidden from the grid: listed under "Hidden charts", from where it is shown again.
  concealed?: boolean
}

// Width steps of a chart in the grid, narrowest first.
export const chartSizes = [
  {id: 'third', label: '⅓ width', fraction: 1 / 3},
  {id: 'half', label: '½ width', fraction: 1 / 2},
  {id: 'two-thirds', label: '⅔ width', fraction: 2 / 3},
  {id: 'full', label: 'Full width', fraction: 1}
] as const
export type ChartSize = (typeof chartSizes)[number]['id']
export const defaultSize: ChartSize = 'third'
const sizeIDs: readonly string[] = chartSizes.map(size => size.id)
// Layouts saved before the size steps counted columns of three.
const legacySpan: Record<number, ChartSize> = {2: 'two-thirds', 3: 'full'}

export interface LayoutScope {kind: 'experiments' | 'comparisons'; id: string}

// The sections a user can add to the page, in page order. "sources" exists only on saved comparisons.
export const pageSections = [
  {id: 'summary', label: 'Summary'},
  {id: 'timeline', label: 'Load and latency'},
  {id: 'services', label: 'Services'},
  {id: 'sources', label: 'Sources'},
  {id: 'endpoints', label: 'Endpoints'},
  {id: 'callchain', label: 'Call chain'},
  {id: 'statistics', label: 'Statistics'},
  {id: 'environment', label: 'Environment & methods'},
  {id: 'placement', label: 'Placement'}
] as const

export type PageSection = (typeof pageSections)[number]['id']

// items absent: the default charts, which follow the metrics of the results.
interface PageLayout {items?: ChartItem[]; sections: PageSection[]}
interface StoredLayout {version: 1; items?: ChartItem[]; sections?: PageSection[]}

// Shown by service only, as before the charts could be arranged.
const servicesOnly = ['application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time']

export function defaultCharts(data: StudioData): ChartItem[] {
  const xy = defaultXYState(data)
  const item = (kind: ChartState['kind'], metric: string, name: string): ChartItem => {
    const id = metric ? `${kind}:${metric}` : kind
    return {id, name, caption: '', charts: [{id, kind, metric, xy}], sharedLegend: false, hidden: []}
  }
  const title = (metric: string) => metricTitle(metric, data.provenance(metric))
  const time = timeMetrics(data).filter(metric => !servicesOnly.includes(metric))
  const metrics = [...groupMetrics([...new Set([...time, ...data.serviceMetrics])]).values()].flat()
  return [
    ...(data.placement ? [item('placement', '', 'Placement heatmap')] : []),
    ...metrics.flatMap(metric => [
      ...(time.includes(metric) ? [item('timeseries', metric, title(metric))] : []),
      ...(data.serviceMetrics.includes(metric) ? [item('services', metric, `${title(metric)} by service`)] : [])
    ])
  ]
}

const sectionIDs: readonly string[] = pageSections.map(section => section.id)

function storedLayout(value: unknown): PageLayout | undefined {
  const layout = value as Partial<StoredLayout> | null
  if (!layout || typeof layout !== 'object') return undefined
  const sections = Array.isArray(layout.sections) ? layout.sections.filter((id): id is PageSection => sectionIDs.includes(id)) : []
  return {items: Array.isArray(layout.items) ? storedItems(layout.items) : undefined, sections}
}

function storedItems(entries: ChartItem[]): ChartItem[] {
  return entries.flatMap((entry): ChartItem[] => {
    const charts = entry && typeof entry === 'object' ? storedCharts(entry.charts) : []
    if (!charts.length) return []
    return [{
      id: typeof entry.id === 'string' ? entry.id : charts[0].id, name: typeof entry.name === 'string' ? entry.name : 'Chart', caption: typeof entry.caption === 'string' ? entry.caption : '',
      charts, layout: entry.layout, sharedLegend: entry.sharedLegend === true, hidden: Array.isArray(entry.hidden) ? entry.hidden.filter((label: unknown) => typeof label === 'string') : [],
      settings: entry.settings && typeof entry.settings === 'object' ? {...defaultFigureSettings, ...entry.settings} : undefined,
      size: typeof entry.size === 'string' && sizeIDs.includes(entry.size) && entry.size !== defaultSize ? entry.size : legacySpan[(entry as {span?: number}).span ?? 1],
      concealed: entry.concealed === true || undefined
    }]
  })
}

// The user's page for these results, with the changes saved as they are made.
// Without a scope (a free selection of runs) changes last until the page closes.
export function useResultsLayout(scope: LayoutScope | undefined, data: StudioData | undefined, csrfToken?: string) {
  const path = scope ? `/account/result-layouts/${scope.kind}/${encodeURIComponent(scope.id)}` : undefined
  // undefined: loading; null: the default page; otherwise the user's own arrangement.
  const [stored, setStored] = useState<PageLayout | null | undefined>(scope ? undefined : null)
  const [error, setError] = useState('')
  useEffect(() => {
    if (!path) {setStored(null); return}
    let active = true
    setStored(undefined)
    // Any unreadable answer shows the default page rather than nothing.
    request<{layout?: unknown} | undefined>(path).then(response => storedLayout(response?.layout) ?? null, () => null).then(layout => {if (active) setStored(layout)})
    return () => {active = false}
  }, [path])
  const save = (layout: PageLayout | null) => {
    setStored(layout)
    setError('')
    if (!path) return
    const pending = layout ? request(path, {method: 'PUT', body: JSON.stringify({layout: {version: 1, ...layout}})}, csrfToken) : request(path, {method: 'DELETE'}, csrfToken)
    pending.catch((cause: unknown) => setError(cause instanceof Error ? cause.message : 'Could not save the page layout.'))
  }
  const defaults = useMemo(() => data ? defaultCharts(data) : [], [data])
  const items = stored?.items ?? defaults
  const sections = stored?.sections ?? []
  return {
    items,
    sections,
    loading: stored === undefined,
    customized: Boolean(stored),
    error,
    change: (next: ChartItem[]) => save({items: next, sections}),
    // Shows or hides an optional section, keeping the page order.
    toggleSection: (id: PageSection, shown: boolean) => save({items: stored?.items, sections: pageSections.map(section => section.id).filter(section => section === id ? shown : sections.includes(section))}),
    // Shows exactly these optional sections.
    setSections: (ids: PageSection[]) => save({items: stored?.items, sections: pageSections.map(section => section.id).filter(section => ids.includes(section))}),
    reset: () => {const before = stored ?? null; save(null); return () => save(before)},
    // A KubeStudio image added at the end, or replacing the chart it was opened from (keeping its width).
    keep: (figure: Omit<ChartItem, 'id'>, id?: string): ChartItem => {
      const previous = id ? items.find(item => item.id === id) : undefined
      const kept: ChartItem = {size: previous?.size, ...figure, concealed: undefined, id: previous ? previous.id : newChartId()}
      save({items: items.some(item => item.id === kept.id) ? items.map(item => item.id === kept.id ? kept : item) : [...items, kept], sections})
      return kept
    }
  }
}
