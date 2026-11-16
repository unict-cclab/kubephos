import {ChartExportActions} from './ChartExportActions'
import {useId, useMemo} from 'react'
import {timeAxis} from '../charts'
import type {PlacementHistory, TimeSeriesDataset} from '../types'
import {usablePlacement} from './PlacementReplay'

export function zonePlacement(history: PlacementHistory, start: number, end: number) {
  const key = (cluster: string, node: string) => JSON.stringify([cluster, node])
  const nodes = new Map(history.nodes.map(n => [key(n.cluster, n.name), {cluster: n.cluster, zone: n.zone || 'Unassigned zone'}]))
  const groups = new Map<string, {cluster: string; zone: string}>()
  for (const n of nodes.values()) groups.set(key(n.cluster, n.zone), n)
  const samples = history.snapshots.map(frame => {
    const counts = new Map<string, number>()
    const seen = new Set<string>()
    for (const pod of frame.observed ? frame.pods : []) {
      const identity = key(pod.cluster, pod.uid)
      if (seen.has(identity)) continue
      seen.add(identity)
      const group = nodes.get(key(pod.cluster, pod.node)) ?? {cluster: pod.cluster, zone: 'Unassigned zone'}
      const id = key(group.cluster, group.zone)
      groups.set(id, group)
      counts.set(id, (counts.get(id) ?? 0) + 1)
    }
    return {time: Date.parse(frame.timestamp), observed: frame.observed, counts}
  })
  const multipleClusters = new Set([...groups.values()].map(g => g.cluster)).size > 1
  const rows = [...groups.entries()].sort(([, a], [, b]) => a.cluster.localeCompare(b.cluster) || a.zone.localeCompare(b.zone)).map(([id, g]) => ({id, label: multipleClusters ? `${g.cluster} · ${g.zone}` : g.zone}))
  const halfStep = history.stepSeconds * 500
  const cells = samples.filter(s => s.time >= start && s.time <= end).map(s => ({...s, from: Math.max(start, s.time - halfStep), to: Math.min(end, s.time + halfStep)}))
  const maximum = cells.reduce((max, s) => {for (const count of s.counts.values()) max = Math.max(max, count); return max}, 1)
  return {rows, cells, maximum}
}

export function placementColorScale(maximum: number) {
  const bins = Math.min(4, Math.max(1, Math.ceil(maximum)))
  const step = Math.max(1, Math.ceil(maximum / bins))
  const colors = ['#9ecae1', '#4292c6', '#08519c', '#08306b']
  return [{lower: 0, upper: 0, color: '#f7fbff'}, ...Array.from({length: bins}, (_, i) => ({lower: i * step, upper: (i + 1) * step, color: colors[bins === 1 ? 3 : Math.round(i * 3 / (bins - 1))]}))]
}

export function placementCellColor(value: number, scale: ReturnType<typeof placementColorScale>) {
  return (scale.find(band => value <= band.upper) ?? scale[scale.length - 1]).color
}

export function zoneColorBlocks(cells: ReturnType<typeof zonePlacement>['cells'], rowID: string) {
  const blocks: {time: number; from: number; to: number; count: number}[] = []
  for (const cell of cells) {
    if (!cell.observed || cell.to <= cell.from) continue
    const count = cell.counts.get(rowID) ?? 0
    const last = blocks[blocks.length - 1]
    if (last && last.count === count && Math.abs(last.to - cell.from) < .01) last.to = cell.to
    else blocks.push({time: cell.time, from: cell.from, to: cell.to, count})
  }
  return blocks
}

export function aggregateZonePlacement(datasets: TimeSeriesDataset[]) {
  if (!datasets.length) return undefined
  const runs = []
  for (const dataset of datasets) {
    const h = dataset.spec.placement
    const start = Date.parse(dataset.spec.workloadStartedAt || dataset.spec.start), end = Date.parse(dataset.spec.end)
    if (!usablePlacement(h) || !Number.isFinite(start) || !(end > start)) return undefined
    const data = zonePlacement(h, start, end)
    runs.push({...data, duration: end - start, step: h.stepSeconds * 1000, cells: data.cells.map(c => ({...c, time: c.time - start, from: c.from - start, to: c.to - start}))})
  }
  const end = Math.min(...runs.map(r => r.duration))
  if (runs.length === 1) return {...runs[0], end}
  const step = Math.max(...runs.map(r => r.step))
  const rowsByID = new Map(runs.flatMap(r => r.rows.map(row => [row.id, row] as const)))
  const clusters = new Set([...rowsByID.keys()].map(id => JSON.parse(id)[0]))
  const rows = [...rowsByID.values()].sort((a, b) => a.id.localeCompare(b.id)).map(row => {const [cluster, zone] = JSON.parse(row.id); return {...row, label: clusters.size > 1 ? `${cluster} · ${zone}` : zone}})
  const grids = runs.map(run => {
    const grid = new Map<number, typeof run.cells[number]>()
    for (const cell of run.cells) {
      const bin = Math.round(cell.time / step)
      const previous = grid.get(bin)
      if (!previous || Math.abs(cell.time - bin * step) < Math.abs(previous.time - bin * step)) grid.set(bin, cell)
    }
    return grid
  })
  const cells = Array.from({length: Math.floor(end / step) + 1}, (_, i) => {
    const time = i * step
    const samples = grids.map(grid => grid.get(i))
    const observed = samples.every(s => s?.observed)
    const counts = new Map<string, number>()
    if (observed) for (const row of rows) counts.set(row.id, samples.reduce((sum, s) => sum + (s!.counts.get(row.id) ?? 0), 0) / runs.length)
    return {time, from: Math.max(0, time - step / 2), to: Math.min(end, time + step / 2), observed, counts}
  })
  const maximum = cells.reduce((max, s) => {for (const count of s.counts.values()) max = Math.max(max, count); return max}, 1)
  return {rows, cells, maximum, end}
}

export function PlacementHeatmap({dataset, datasets, canvasHeight = 470}: {dataset?: TimeSeriesDataset; datasets?: TimeSeriesDataset[]; canvasHeight?: number}) {
  const id = `placement-heatmap-${useId().replace(/[^a-z0-9]/gi, '')}`
  const data = useMemo(() => aggregateZonePlacement(datasets ?? (dataset ? [dataset] : [])), [dataset, datasets])
  if (!data || !data.rows.length) return null
  const start = 0, end = data.end
  const mean = (datasets?.length ?? 1) > 1
  const scale = placementColorScale(data.maximum)
  const width = 860, height = Math.max(canvasHeight, data.rows.length * 40 + 145)
  const left = Math.min(290, Math.max(125, ...data.rows.map(r => r.label.length * 12 + 30)))
  const right = width - 55, top = 20, bottom = height - 125
  const rowHeight = (bottom - top) / data.rows.length
  const minutes = (end - start) / 60000
  const axis = timeAxis(minutes, true)
  const x = (t: number) => left + ((t - start) / 60000 / axis.upper) * (right - left)
  const font = {fontFamily: 'Arial, Helvetica, DejaVu Sans, sans-serif', fill: '#111'}
  return <article className="metric-card">
    <div className="metric-card-header"><h3>Application placement by zone</h3><ChartExportActions chartID={id} metric="placement-by-zone" /></div>
    <div className="chart-wrap"><svg id={id} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Application placement by zone heatmap">
      <rect width={width} height={height} fill="#fff" />
      {data.rows.map((row, i) => <g key={row.id}>
        <text {...font} x={left - 14} y={top + (i + .5) * rowHeight + 8} fontSize="23" textAnchor="end">{row.label}</text>
        <rect x={left} y={top + i * rowHeight} width={x(end) - left} height={rowHeight} fill="#d1d5db" shapeRendering="crispEdges" />
        {zoneColorBlocks(data.cells, row.id).map(s => {
          const count = s.count
          return <rect key={s.time} x={x(s.from)} y={top + i * rowHeight} width={x(s.to) - x(s.from)} height={rowHeight} fill={placementCellColor(count, scale)} shapeRendering="crispEdges"><title>{row.label} · {((s.time - start) / 60000).toFixed(2)} min · {Number(count.toFixed(2))} {mean ? 'mean scheduled replicas' : 'scheduled replicas'}</title></rect>
        })}
        <line x1={left} x2={x(end)} y1={top + i * rowHeight} y2={top + i * rowHeight} stroke="#fff" strokeWidth="1" />
      </g>)}
      <rect className="axis" x={left} y={top} width={x(end) - left} height={bottom - top} fill="none" stroke="#111" strokeWidth="1.5" />
      {axis.ticks.map(t => <g key={t}><line x1={x(start + t * 60000)} x2={x(start + t * 60000)} y1={bottom} y2={bottom + 6} stroke="#111" /><text {...font} x={x(start + t * 60000)} y={bottom + 30} textAnchor="middle" fontSize="23">{t}</text></g>)}
      <text {...font} x={(left + right) / 2} y={bottom + 64} textAnchor="middle" fontSize="25">Time (min)</text>
      <text {...font} x={left} y={height - 22} fontSize="20">{mean ? 'Mean replicas' : 'Scheduled replicas'}</text>
      {scale.map((band, i) => <g key={band.upper}><title>{band.upper === 0 ? 'Zero replicas' : `More than ${band.lower} and up to ${band.upper} replicas`}</title><rect x={left + 195 + i * 56} y={height - 39} width="50" height="16" fill={band.color} shapeRendering="crispEdges" /><text {...font} x={left + 220 + i * 56} y={height - 3} textAnchor="middle" fontSize="16">{band.upper === 0 ? '0' : `${mean ? band.lower : band.lower + 1}–${band.upper}`}</text></g>)}
      <rect x={right - 150} y={height - 39} width="17" height="16" fill="#d1d5db" /><text {...font} x={right - 124} y={height - 24} fontSize="20">No samples</text>
    </svg></div>
  </article>
}
