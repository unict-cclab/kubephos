import type {ReactNode} from 'react'
import {FigureCard, figureMetrics, r, SvgText, type FigureMetrics} from '../chartKit'
import {blendWhite, dashArray, type GridMode, type MarkerShape} from './figureSpec'
import type {FigureModel, ModelAxis, ModelPanel, ModelSeries} from './figureModel'
import type {FigureSettings} from '../figureSettings'
import type {ResolvedProvenance} from '../runs'

// Publication SVG drawn from a FigureModel. Every coordinate is absolute (no group
// transforms), every colour opaque or a fill-opacity, and data are clipped by hand, so the
// built-in PDF writer reproduces the figure exactly.

export function ShapeMarker({shape, x, y, size, color, hollow = false, width}: {shape: MarkerShape; x: number; y: number; size: number; color: string; hollow?: boolean; width?: number}) {
  if (shape === 'none') return null
  const outer = size * 1.25
  const stroke = width ?? size * .45
  const paint = hollow ? {fill: '#ffffff', stroke: color, strokeWidth: r(stroke)} : {fill: color, stroke: 'none'}
  if (shape === 'circle') return <circle cx={r(x)} cy={r(y)} r={r(size)} {...paint} />
  if (shape === 'square') return <rect x={r(x - size * .9)} y={r(y - size * .9)} width={r(size * 1.8)} height={r(size * 1.8)} {...paint} />
  if (shape === 'triangle') return <polygon points={`${r(x)},${r(y - outer)} ${r(x - outer)},${r(y + size * .85)} ${r(x + outer)},${r(y + size * .85)}`} {...paint} />
  if (shape === 'diamond') return <polygon points={`${r(x)},${r(y - outer)} ${r(x - outer)},${r(y)} ${r(x)},${r(y + outer)} ${r(x + outer)},${r(y)}`} {...paint} />
  const line = {fill: 'none', stroke: color, strokeWidth: r(Math.max(stroke, size * .55))}
  if (shape === 'cross') return <path d={`M${r(x - outer)},${r(y)}H${r(x + outer)}M${r(x)},${r(y - outer)}V${r(y + outer)}`} {...line} />
  return <path d={`M${r(x - size)},${r(y - size)}L${r(x + size)},${r(y + size)}M${r(x + size)},${r(y - size)}L${r(x - size)},${r(y + size)}`} {...line} />
}

interface Box {left: number; right: number; top: number; bottom: number}

// Liang–Barsky: the part of a segment inside the box (undefined when outside).
function clip(x0: number, y0: number, x1: number, y1: number, box: Box): [number, number, number, number] | undefined {
  let t0 = 0
  let t1 = 1
  const dx = x1 - x0
  const dy = y1 - y0
  for (const [p, q] of [[-dx, x0 - box.left], [dx, box.right - x0], [-dy, y0 - box.top], [dy, box.bottom - y0]]) {
    if (p === 0) {if (q < 0) return undefined; continue}
    const t = q / p
    if (p < 0) {if (t > t1) return undefined; if (t > t0) t0 = t}
    else {if (t < t0) return undefined; if (t < t1) t1 = t}
  }
  return [x0 + t0 * dx, y0 + t0 * dy, x0 + t1 * dx, y0 + t1 * dy]
}

function linePath(xs: number[], ys: number[], px: (value: number) => number | undefined, py: (value: number) => number | undefined, box: Box): string {
  const parts: string[] = []
  let previous: [number, number] | undefined
  let end: [number, number] | undefined
  for (let index = 0; index < xs.length; index++) {
    const x = px(xs[index])
    const y = py(ys[index])
    if (x === undefined || y === undefined) {previous = undefined; continue}
    if (previous) {
      const segment = clip(previous[0], previous[1], x, y, box)
      if (segment) {
        if (!end || Math.abs(end[0] - segment[0]) > .01 || Math.abs(end[1] - segment[1]) > .01) parts.push(`M${r(segment[0])},${r(segment[1])}`)
        parts.push(`L${r(segment[2])},${r(segment[3])}`)
        end = [segment[2], segment[3]]
      } else end = undefined
    }
    previous = [x, y]
  }
  return parts.join('')
}

function bandPaths(series: ModelSeries, px: (value: number) => number | undefined, py: (value: number) => number | undefined, box: Box): string[] {
  const out: string[] = []
  let run: number[] = []
  const flush = () => {
    if (run.length > 1) {
      const clampX = (value: number) => Math.min(box.right, Math.max(box.left, value))
      const clampY = (value: number) => Math.min(box.bottom, Math.max(box.top, value))
      const edge = (index: number, values: number[]) => `${r(clampX(px(series.x[index])!))},${r(clampY(py(values[index]) ?? box.bottom))}`
      out.push(`M${run.map(index => edge(index, series.high!)).join(' L')} L${[...run].reverse().map(index => edge(index, series.low!)).join(' L')} Z`)
    }
    run = []
  }
  series.x.forEach((value, index) => {
    if (!Number.isFinite(value) || !Number.isFinite(series.y[index]) || px(value) === undefined || !Number.isFinite(series.low![index]) || !Number.isFinite(series.high![index])) flush()
    else run.push(index)
  })
  flush()
  return out
}

const position = (axis: ModelAxis | undefined, from: number, to: number) => (value: number) => {
  if (!axis || !Number.isFinite(value)) return undefined
  const at = axis.scale.position(value)
  return at === undefined ? undefined : from + at * (to - from)
}

function widest(axis: ModelAxis | undefined, metrics: FigureMetrics) {
  return axis ? Math.max(metrics.fontSize, ...axis.scale.ticks.map(tick => metrics.text(axis.format(tick)))) : 0
}

interface LegendEntry {kind: 'header' | 'item'; text: string; series?: ModelSeries; break?: boolean}

function legendEntries(model: FigureModel): LegendEntry[] {
  const entries: LegendEntry[] = []
  model.legend.forEach((item, index) => {
    if (item.header) entries.push({kind: 'header', text: item.header, break: index > 0})
    entries.push({kind: 'item', text: item.series.label, series: item.series})
  })
  return entries
}

function LegendSymbol({series, x, y, width, metrics}: {series: ModelSeries; x: number; y: number; width: number; metrics: FigureMetrics}) {
  const style = series.style
  const color = blendWhite(style.color, style.opacity)
  const hasLine = style.line !== 'none' && (series.kind === 'line' || series.fit)
  return <>
    {series.low && <rect x={r(x)} y={r(y - metrics.fontSize * .32)} width={r(width)} height={r(metrics.fontSize * .64)} fill={style.color} fillOpacity="0.16" />}
    {hasLine && <line x1={r(x)} x2={r(x + width)} y1={r(y)} y2={r(y)} stroke={color} strokeWidth={r(style.lineWidth)} strokeDasharray={series.kind === 'line' ? dashArray(style.line, style.lineWidth) : undefined} />}
    {series.kind === 'scatter' && <ShapeMarker shape={style.marker} x={x + width / 2} y={y} size={style.markerSize * 1.1} color={color} hollow={style.hollow} />}
  </>
}

// Pixel ↔ data mapping of each drawn panel (points), for direct manipulation in KubeStudio.
interface PanelGeometry {index: number; box: Box; x: (pt: number) => number | undefined; y: (side: 'left' | 'right', pt: number) => number | undefined}
export interface FigureGeometry {width: number; height: number; panels: PanelGeometry[]}

const invert = (axis: ModelAxis | undefined, from: number, to: number) => (pt: number) => {
  if (!axis) return undefined
  const t = (pt - from) / (to - from)
  const {lower, upper, log} = axis.scale
  if (!log) return lower + t * (upper - lower)
  if (lower <= 0 || upper <= 0) return undefined
  return 10 ** (Math.log10(lower) + t * (Math.log10(upper) - Math.log10(lower)))
}

interface SpecFigureProps {
  id: string
  name: string
  model: FigureModel
  settings: FigureSettings
  grid: GridMode
  strokeScale: number
  provenance: Array<{metric: string; provenance: ResolvedProvenance}>
  ariaLabel: string
  geometry?: {current: FigureGeometry | undefined}
}

export function SpecFigure({id, name, model, settings, grid, strokeScale, provenance, ariaLabel, geometry}: SpecFigureProps) {
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  const axisLine = metrics.line * Math.sqrt(strokeScale)
  const entries = legendEntries(model)
  const showLegend = metrics.legend !== 'none' && (model.legend.length > 1 || model.legend.some(item => item.series.low || item.series.runs)) && model.legend.length > 0
  const symbol = fs * 2
  const rowHeight = fs * 1.35
  const entryWidth = (entry: LegendEntry) => entry.kind === 'header' ? metrics.text(entry.text) + fs * .5 : symbol + fs * .4 + metrics.text(entry.text) + fs * 1.1
  const legendColumnWidth = showLegend ? Math.max(...entries.map(entry => entry.kind === 'header' ? metrics.text(entry.text) : symbol + fs * .4 + metrics.text(entry.text))) : 0
  const reserveRight = showLegend && metrics.legend === 'right' ? legendColumnWidth + fs * 1.6 : 0
  // Flowing legend (top/bottom): rows within the page width; a new row starts each axis group.
  const flow = (() => {
    if (!showLegend || (metrics.legend !== 'top' && metrics.legend !== 'bottom')) return {placed: [] as Array<{x: number; row: number}>, rows: 0}
    const from = fs * .9
    const to = metrics.width - fs * .9
    let x = from
    let row = 0
    const placed = entries.map(entry => {
      const width = entryWidth(entry)
      if (x > from && (entry.break || x + width - fs * 1.1 > to)) {x = from; row++}
      const at = {x, row}
      x += width
      return at
    })
    return {placed, rows: row + 1}
  })()
  const legendHeight = flow.rows ? flow.rows * rowHeight + fs * .4 : 0
  const titleHeight = metrics.titleHeight
  const blockTop = titleHeight + (metrics.legend === 'top' ? legendHeight : 0)
  const blockHeight = metrics.height * (model.rows === 2 ? 1.5 : 1) - titleHeight
  const available = metrics.width - reserveRight
  const cellWidth = available / model.columns
  const cellHeight = blockHeight / model.rows
  const multi = model.panels.length > 1
  const sharedX = multi && model.panels.every(panel => panel.x.scale.lower === model.panels[0].x.scale.lower && panel.x.scale.upper === model.panels[0].x.scale.upper)
  const sharedY = multi && model.panels.every(panel => panel.left && model.panels[0].left && panel.left.scale.lower === model.panels[0].left.scale.lower && panel.left.scale.upper === model.panels[0].left.scale.upper && panel.left.label === model.panels[0].left.label)
  const showX = (panel: ModelPanel) => !sharedX || Math.floor(panel.index / model.columns) === model.rows - 1
  const showLeft = (panel: ModelPanel) => !sharedY || panel.index % model.columns === 0
  const leftMargin = Math.max(fs * .9, ...model.panels.map(panel => panel.left ? (showLeft(panel) ? widest(panel.left, metrics) + fs * 2.1 : fs * .9) : fs * .9))
  const rightMargin = Math.max(fs * .9, ...model.panels.map(panel => panel.right ? widest(panel.right, metrics) + fs * 2.1 : fs * .9))
  const topMargin = multi && model.panels.some(panel => panel.letter) ? fs * 1.6 : multi ? fs * .6 : 0
  const boxes = model.panels.map(panel => {
    const row = Math.floor(panel.index / model.columns)
    const column = panel.index % model.columns
    const cellLeft = column * cellWidth
    const cellTop = blockTop + row * cellHeight
    return {cellLeft, cellRight: cellLeft + cellWidth, cellTop, left: cellLeft + leftMargin, right: cellLeft + cellWidth - rightMargin, top: cellTop + topMargin, bottom: cellTop + cellHeight - (showX(panel) ? fs * 2.9 : fs * .8)}
  })
  const height = blockTop + blockHeight + (metrics.legend === 'bottom' ? legendHeight + fs * .2 : 0)
  const text = (props: {x: number; y: number; children: ReactNode; anchor?: 'start' | 'middle' | 'end'; size?: number; fill?: string; rotate?: number; className?: string; weight?: 'bold'}) => <SvgText {...props} size={props.size ?? fs} family={metrics.family} />

  if (geometry) geometry.current = {width: metrics.width, height, panels: model.panels.map((panel, index) => {
    const box = boxes[index]
    return {index: panel.index, box, x: invert(panel.x, box.left, box.right), y: (side, pt) => invert(side === 'left' ? panel.left : panel.right, box.bottom, box.top)(pt)}
  })}

  const drawPanel = (panel: ModelPanel, box: typeof boxes[number]) => {
    const select = (item: ModelPanel['annotations'][number]) => `annotation:${panel.index}:${item.id ?? item.source}`
    const px = position(panel.x, box.left, box.right)
    const yOf = (side: 'left' | 'right') => position(side === 'left' ? panel.left : panel.right, box.bottom, box.top)
    const gridAxis = panel.left ?? panel.right
    const gy = yOf(panel.left ? 'left' : 'right')
    const tick = fs * .35
    const children: ReactNode[] = []
    // Grid: horizontal on the main axis; vertical too for X–Y scatters.
    if (grid !== 'none' && gridAxis) {
      if (grid === 'minor') gridAxis.minor.forEach(value => {const y = gy(value); if (y !== undefined && y >= box.top - .01 && y <= box.bottom + .01) children.push(<line key={`gm-${value}`} className="grid-minor" x1={r(box.left)} x2={r(box.right)} y1={r(y)} y2={r(y)} stroke="#f0f0f0" strokeWidth={r(axisLine * .4)} />)})
      gridAxis.scale.ticks.forEach(value => {const y = gy(value); if (y !== undefined) children.push(<line key={`g-${value}`} className="grid-major" x1={r(box.left)} x2={r(box.right)} y1={r(y)} y2={r(y)} stroke="#e3e3e3" strokeWidth={r(axisLine * .5)} />)})
      if (model.type === 'xy') panel.x.scale.ticks.forEach(value => {const x = px(value); if (x !== undefined) children.push(<line key={`gx-${value}`} className="grid-major" x1={r(x)} x2={r(x)} y1={r(box.top)} y2={r(box.bottom)} stroke="#e3e3e3" strokeWidth={r(axisLine * .5)} />)})
    }
    // Shaded x ranges behind the data.
    panel.annotations.filter(item => item.kind === 'xrange').forEach((item, index) => {
      const a = px(item.x!)
      const b = px(item.x2!)
      if (a === undefined || b === undefined) return
      const from = Math.max(box.left, Math.min(a, b))
      const to = Math.min(box.right, Math.max(a, b))
      if (to <= from) return
      children.push(<g key={`xr-${index}`} className="annotation annotation-xrange" data-select={select(item)}><rect x={r(from)} y={r(box.top)} width={r(to - from)} height={r(box.bottom - box.top)} fill="#000000" fillOpacity="0.07" />{item.label && text({x: (from + to) / 2, y: box.top + fs * 1.9, anchor: 'middle', size: fs * .85, fill: '#333333', className: 'annotation-label', children: item.label})}</g>)
    })
    // Data, in legend order (later on top).
    panel.series.forEach(series => {
      const py = yOf(series.axis)
      const style = series.style
      const color = blendWhite(style.color, style.opacity)
      const parts: ReactNode[] = []
      if (series.low && series.high) parts.push(<g key="band" className="deviation-band">{bandPaths(series, px, py, box).map((d, index) => <path key={index} d={d} fill={style.color} fillOpacity={r(.16 * style.opacity)} stroke="none" />)}</g>)
      series.runs?.forEach((run, index) => {
        const d = linePath(series.x, run.y, px, py, box)
        const thin = series.showMean ? {color: blendWhite(style.color, .38 * style.opacity), width: style.lineWidth * .45} : {color, width: style.lineWidth * .7}
        if (d) parts.push(<path key={`r-${index}`} className="series-run" d={d} fill="none" stroke={thin.color} strokeWidth={r(thin.width)} strokeLinejoin="round" />)
      })
      if (series.showMean && style.line !== 'none') {
        const d = linePath(series.x, series.y, px, py, box)
        const width = series.runs && series.showMean ? style.lineWidth * 1.35 : style.lineWidth
        if (d) parts.push(<path key="line" className="series-line" d={d} fill="none" stroke={color} strokeWidth={r(width)} strokeDasharray={dashArray(style.line, width)} strokeLinejoin="round" />)
      }
      // Points only where they are the data (scatter); line charts draw lines alone.
      if (series.kind === 'scatter' && series.showMean && style.marker !== 'none') {
        const inside = series.x.map((value, index) => ({x: px(value), y: py(series.y[index])})).filter((point): point is {x: number; y: number} => point.x !== undefined && point.y !== undefined && point.x >= box.left - .01 && point.x <= box.right + .01 && point.y >= box.top - .01 && point.y <= box.bottom + .01)
        inside.forEach((point, index) => parts.push(<ShapeMarker key={`m-${index}`} shape={style.marker} x={point.x} y={point.y} size={style.markerSize * .85} color={color} hollow={style.hollow} />))
      }
      if (series.fit) {
        const d = linePath(series.fit.x, series.fit.y, px, py, box)
        if (d) parts.push(<path key="fit" className="series-fit" d={d} fill="none" stroke={color} strokeWidth={r(style.lineWidth)} strokeDasharray={series.axis === 'right' ? dashArray('dashed', style.lineWidth) : undefined} />)
      }
      children.push(<g key={`s-${series.key}`} className="series" data-series-key={series.key} data-select={`series:${series.key}`}>{parts}</g>)
    })
    // Lines and labels on top of the data.
    panel.annotations.forEach((item, index) => {
      if (item.kind === 'vline') {
        const x = px(item.x!)
        if (x === undefined || x < box.left - .01 || x > box.right + .01) return
        const user = item.source === 'user'
        const near = x > box.right - fs * 4
        children.push(<g key={`v-${index}`} className={`annotation annotation-vline annotation-${item.source}`} data-select={select(item)}><line x1={r(x)} x2={r(x)} y1={r(box.top)} y2={r(box.bottom)} stroke={item.color} strokeWidth={r(axisLine * (user ? .8 : .6))} strokeDasharray={user ? '3 2' : '1 1.6'} />{item.label && text({x: near ? x - fs * .25 : x + fs * .25, y: box.top + fs * (item.source === 'warmup' ? 1.75 : .85), anchor: near ? 'end' : 'start', size: fs * .78, fill: '#555555', className: 'annotation-label', children: item.label})}</g>)
      } else if (item.kind === 'hline') {
        const y = yOf(item.axis)(item.y!)
        if (y === undefined || y < box.top - .01 || y > box.bottom + .01) return
        children.push(<g key={`h-${index}`} className={`annotation annotation-hline annotation-${item.source}`} data-select={select(item)}><line x1={r(box.left)} x2={r(box.right)} y1={r(y)} y2={r(y)} stroke={item.color} strokeWidth={r(axisLine * (item.source === 'slo' ? 1 : .8))} strokeDasharray={item.source === 'slo' ? '4 3' : '3 2'} />{item.label && text({x: box.right - fs * .3, y: y - fs * .35, anchor: 'end', size: fs * .9, fill: item.color, className: 'annotation-label slo-label', children: item.label})}</g>)
      } else if (item.kind === 'text') {
        const x = px(item.x!)
        const y = yOf(item.axis)(item.y!)
        if (x === undefined || y === undefined) return
        children.push(<g key={`t-${index}`} className="annotation annotation-text" data-select={select(item)}>{text({x: Math.min(box.right, Math.max(box.left, x)), y: Math.min(box.bottom, Math.max(box.top + fs, y)), size: fs * .9, fill: item.color, className: 'annotation-label', children: item.label})}</g>)
      }
    })
    // Axes.
    children.push(<rect key="frame" className="axis" x={r(box.left)} y={r(box.top)} width={r(box.right - box.left)} height={r(box.bottom - box.top)} fill="none" stroke="#111" strokeWidth={r(axisLine * .6)} />)
    const yTicks = (side: 'left' | 'right', axis: ModelAxis | undefined) => {
      if (!axis) return
      const y = yOf(side)
      const labels = side === 'right' || showLeft(panel)
      const ink = axis.color ?? '#111111'
      const sign = side === 'left' ? -1 : 1
      const edge = side === 'left' ? box.left : box.right
      axis.scale.ticks.forEach(value => {
        const at = y(value)
        if (at === undefined || at < box.top - .01 || at > box.bottom + .01) return
        children.push(<g key={`${side}-${value}`} className={`tick-${side}`} data-select={`axis:${side}:${panel.index}`}><line x1={r(edge)} x2={r(edge + sign * tick)} y1={r(at)} y2={r(at)} stroke={axis.color ?? '#111111'} strokeWidth={r(axisLine * .6)} />{labels && text({x: edge + sign * fs * .55, y: at + fs * .35, anchor: side === 'left' ? 'end' : 'start', fill: ink, className: 'tick', children: axis.format(value)})}</g>)
      })
      if (labels) children.push(<g key={`${side}-title`} className={`axis-title-${side}`} data-select={`axis:${side}:${panel.index}`}>{text({x: side === 'left' ? box.cellLeft + fs * .95 : box.cellRight - fs * .7, y: (box.top + box.bottom) / 2, anchor: 'middle', rotate: side === 'left' ? -90 : 90, fill: ink, className: 'axis-label', children: axis.label})}</g>)
    }
    yTicks('left', panel.left)
    yTicks('right', panel.right)
    panel.x.scale.ticks.forEach(value => {
      const x = px(value)
      if (x === undefined || x < box.left - .01 || x > box.right + .01) return
      children.push(<g key={`x-${value}`} className="tick-x" data-select={`axis:x:${panel.index}`}><line x1={r(x)} x2={r(x)} y1={r(box.bottom)} y2={r(box.bottom + tick)} stroke="#111111" strokeWidth={r(axisLine * .6)} />{showX(panel) && text({x, y: box.bottom + fs * 1.3, anchor: 'middle', className: 'tick', children: panel.x.format(value)})}</g>)
    })
    if (showX(panel)) children.push(<g key="x-title" className="axis-title-x" data-select={`axis:x:${panel.index}`}>{text({x: (box.left + box.right) / 2, y: box.bottom + fs * 2.55, anchor: 'middle', className: 'axis-label', children: panel.x.label})}</g>)
    if (panel.letter) children.push(<g key="letter" className="panel-letter">{text({x: box.cellLeft + fs * .3, y: box.top - fs * .7, weight: 'bold', children: panel.letter})}</g>)
    return <g key={`panel-${panel.index}`} className="figure-panel" data-panel={panel.index + 1}>{children}</g>
  }

  const legend = (() => {
    if (!showLegend) return null
    const items: ReactNode[] = []
    const draw = (entry: LegendEntry, x: number, y: number, index: number) => {
      if (entry.kind === 'header') {items.push(<g key={`h-${index}`} className="legend-header">{text({x, y: y + fs * .35, fill: '#555555', children: entry.text})}</g>); return}
      items.push(<g key={`i-${index}`} className="legend-item" data-series-key={entry.series!.key} data-select={`series:${entry.series!.key}`}><LegendSymbol series={entry.series!} x={x} y={y} width={symbol} metrics={metrics} />{text({x: x + symbol + fs * .4, y: y + fs * .35, className: 'legend-label', children: entry.text})}</g>)
    }
    if (metrics.legend === 'top' || metrics.legend === 'bottom') {
      const origin = metrics.legend === 'top' ? titleHeight + fs * .1 : blockTop + blockHeight + fs * .2
      entries.forEach((entry, index) => draw(entry, flow.placed[index].x, origin + flow.placed[index].row * rowHeight + fs * .5, index))
    } else {
      const first = boxes[0]
      const inside = metrics.legend === 'inside'
      const x = inside ? first.right - legendColumnWidth - fs * .7 : metrics.width - reserveRight + fs * .7
      const y = inside ? first.top + fs * .35 : boxes[0].top
      if (inside) items.push(<rect key="box" x={r(x - fs * .35)} y={r(y - fs * .1)} width={r(legendColumnWidth + fs * .7)} height={r(entries.length * rowHeight + fs * .25)} fill="#ffffff" fillOpacity="0.88" stroke="#bdbdbd" strokeWidth={r(metrics.line * .4)} />)
      entries.forEach((entry, index) => draw(entry, x, y + index * rowHeight + fs * .5, index))
    }
    return <g className="figure-legend" data-select="legend">{items}</g>
  })()

  const first = model.panels[0]
  return <FigureCard id={id} name={name} title={model.title} caption={model.caption} methods={model.methods} ariaLabel={ariaLabel} width={metrics.width} height={height} settings={settings} provenance={provenance} csv={model.csv}
    labels={{x: first?.x.defaultLabel, y: first?.left?.defaultLabel ?? first?.right?.defaultLabel, series: model.legend.length > 1 || model.legend.some(item => item.series.low) ? model.legend.map(item => item.series.defaultLabel) : []}}>
    {model.panels.map((panel, index) => drawPanel(panel, boxes[index]))}
    {legend}
  </FigureCard>
}
