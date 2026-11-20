import {createContext, useContext, useLayoutEffect, type ReactNode} from 'react'
import {textWidth} from '../../charts'
import {ChartExportActions} from '../ChartExportActions'
import {exportColor} from './chartColor'
import {AboutMetric, ProvenanceBadge} from './Provenance'
import type {ResolvedProvenance} from './runs'
import {fontStacks, figureSize, type FigureLegend, type FigureSettings} from './figureSettings'
import {formatAxisValue, toCSV} from './format'
import type {Scale} from './scales'

export const defaultColors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#000000']
// Okabe & Ito (2008) colour-blind-safe palette, black first.
export const okabeIto = ['#000000', '#e69f00', '#56b4e9', '#009e73', '#0072b2', '#d55e00', '#cc79a7', '#f0e442']
const grayscaleDashes = ['', '5 2', '1.5 1.5', '6 2 1.5 2', '3 3', '8 2 2 2 2 2', '1 2.5', '10 3']
const colorDashes = ['', '6 3', '6 2 1.5 2', '1.5 2']

export interface SeriesStyle {color: string; dash: string; marker: number}

export function seriesStyle(index: number, color: string | undefined, settings: FigureSettings): SeriesStyle {
  if (settings.grayscale) return {color: okabeIto[index % okabeIto.length], dash: grayscaleDashes[index % grayscaleDashes.length], marker: index % 7}
  // Figures are printed on white: a user colour is kept unless it fails 3:1 there.
  return {color: color ? exportColor(color) : defaultColors[index % defaultColors.length], dash: colorDashes[Math.floor(index / defaultColors.length) % colorDashes.length], marker: index % 7}
}

export interface FigureMetrics {
  width: number
  fontSize: number
  family: string
  kind: 'serif' | 'sans'
  line: number
  // Plot-block height (pt) before legends and rotated category labels.
  height: number
  legend: FigureLegend
  // Space reserved above the plot for the in-figure title.
  titleHeight: number
  text: (value: string, size?: number) => number
}

export function figureMetrics(settings: FigureSettings): FigureMetrics {
  const size = figureSize(settings)
  const fontSize = settings.fontSize
  return {width: size.width, height: size.height, fontSize, family: fontStacks[settings.font], kind: settings.font, line: Math.max(.75, fontSize / 9), legend: settings.legend ?? 'bottom', titleHeight: figureTitleHeight(settings), text: (value, at = fontSize) => textWidth(value, at, settings.font)}
}

// ---------------------------------------------------------------------------------------
// Figure host (the Figure studio): label overrides in, figure description out. Without a
// host a figure renders as a self-contained card with its own export actions and caption.

export interface FigureLabelOverrides {title?: string; x?: string; y?: string; series?: Record<string, string>}
export interface FigureReport {
  id: string
  name: string
  title: string
  // Short caption: what is plotted, n, what the uncertainty means.
  caption: string
  // Methods & provenance: sources, queries, rate windows, statistics.
  methods: string
  width: number
  height: number
  labels: {x?: string; y?: string; series: string[]}
  csv?: () => string
}
export interface FigureHost {overrides: FigureLabelOverrides; report: (figure: FigureReport) => void}
export const FigureHostContext = createContext<FigureHost | undefined>(undefined)

// Effective labels: the host's non-empty overrides win over the figure's defaults.
export function useFigureLabels() {
  const overrides = useContext(FigureHostContext)?.overrides
  const pick = (value: string | undefined, fallback: string) => value?.trim() ? value : fallback
  return {
    title: (fallback: string) => pick(overrides?.title, fallback),
    x: (fallback: string) => pick(overrides?.x, fallback),
    y: (fallback: string) => pick(overrides?.y, fallback),
    series: (fallback: string) => pick(overrides?.series?.[fallback], fallback)
  }
}

export function SvgText({x, y, children, anchor = 'start', size, family, weight, fill = '#111111', rotate, className}: {x: number; y: number; children: ReactNode; anchor?: 'start' | 'middle' | 'end'; size: number; family: string; weight?: 'bold'; fill?: string; rotate?: number; className?: string}) {
  return <text x={r(x)} y={r(y)} textAnchor={anchor} fontSize={r(size)} fontFamily={family} fontWeight={weight} fill={fill} className={className} style={{fontFamily: family}} transform={rotate ? `rotate(${rotate} ${r(x)} ${r(y)})` : undefined}>{children}</text>
}

export const r = (value: number) => Math.round(value * 100) / 100

export function Marker({shape, x, y, color, size, hollow = false}: {shape: number; x: number; y: number; color: string; size: number; hollow?: boolean}) {
  const outer = size * 1.25
  const paint = hollow ? {fill: '#ffffff', stroke: color, strokeWidth: size * .45} : {fill: color, stroke: 'none'}
  if (shape === 0) return <circle cx={r(x)} cy={r(y)} r={r(size)} {...paint} />
  if (shape === 1) return <rect x={r(x - size)} y={r(y - size)} width={r(size * 2)} height={r(size * 2)} {...paint} />
  if (shape === 2) return <polygon points={`${r(x)},${r(y - outer)} ${r(x - outer)},${r(y + size)} ${r(x + outer)},${r(y + size)}`} {...paint} />
  if (shape === 3) return <polygon points={`${r(x)},${r(y - outer)} ${r(x - outer)},${r(y)} ${r(x)},${r(y + outer)} ${r(x + outer)},${r(y)}`} {...paint} />
  if (shape === 4) return <polygon points={`${r(x - outer)},${r(y - size)} ${r(x + outer)},${r(y - size)} ${r(x)},${r(y + outer)}`} {...paint} />
  if (shape === 5) return <path d={`M${r(x - outer)},${r(y)}H${r(x + outer)}M${r(x)},${r(y - outer)}V${r(y + outer)}`} fill="none" stroke={color} strokeWidth={r(size * .6)} />
  return <path d={`M${r(x - size)},${r(y - size)}L${r(x + size)},${r(y + size)}M${r(x + size)},${r(y - size)}L${r(x - size)},${r(y + size)}`} fill="none" stroke={color} strokeWidth={r(size * .6)} />
}

export interface LegendItem {label: string; style: SeriesStyle; kind?: 'line' | 'marker' | 'band'}

export interface LegendLayout {entries: Array<{x: number; row: number}>; rows: number; rowHeight: number; height: number; width: number; symbol: number; position: FigureLegend}
export interface PlotBox {left: number; right: number; top: number; bottom: number}

// Legend geometry for the chosen position. Only 'bottom' and 'top' add height to the figure
// (top is drawn above the plot by FigureCard shifting the plot down); 'right' needs
// legendWidth() reserved on the right of the plot; 'inside' overlays the plot's top-right.
export function legendLayout(items: LegendItem[], left: number, right: number, metrics: FigureMetrics): LegendLayout {
  const symbol = metrics.fontSize * 2
  const gap = metrics.fontSize * 1.1
  const rowHeight = metrics.fontSize * 1.35
  const position = items.length ? metrics.legend : 'none'
  if (position === 'none') return {entries: [], rows: 0, rowHeight, height: 0, width: 0, symbol, position}
  if (position === 'right' || position === 'inside') {
    const width = Math.max(...items.map(item => symbol + metrics.fontSize * .4 + metrics.text(item.label)))
    return {entries: items.map((_, index) => ({x: 0, row: index})), rows: items.length, rowHeight, height: 0, width, symbol, position}
  }
  const from = position === 'top' ? metrics.fontSize * .9 : left
  let x = from
  let row = 0
  const entries = items.map(item => {
    const width = symbol + metrics.fontSize * .4 + metrics.text(item.label) + gap
    if (x > from && x + width - gap > right) {x = from; row++}
    const entry = {x, row}
    x += width
    return entry
  })
  return {entries, rows: row + 1, rowHeight, height: (row + 1) * rowHeight + metrics.fontSize * .4, width: right - from, symbol, position}
}

// Width to reserve right of the plot for a 'right' legend (0 otherwise).
export function legendWidth(items: LegendItem[], metrics: FigureMetrics): number {
  if (metrics.legend !== 'right' || !items.length) return 0
  return Math.max(...items.map(item => metrics.fontSize * 2.4 + metrics.text(item.label))) + metrics.fontSize * .9
}

export function Legend({items, layout, top, metrics, plot}: {items: LegendItem[]; layout: LegendLayout; top: number; metrics: FigureMetrics; plot?: PlotBox}) {
  if (layout.position === 'none' || !items.length) return null
  const fs = metrics.fontSize
  const origin = (() => {
    if (layout.position === 'right' && plot) return {x: plot.right + fs * .9, y: plot.top}
    if (layout.position === 'inside' && plot) return {x: plot.right - layout.width - fs * .7, y: plot.top + fs * .35}
    // Top: FigureCard shifts everything down by layout.height; draw back into that band.
    if (layout.position === 'top') return {x: 0, y: metrics.titleHeight - layout.height + fs * .1}
    return {x: 0, y: top}
  })()
  return <g className="figure-legend">
    {layout.position === 'inside' && plot && <rect x={r(origin.x - fs * .35)} y={r(origin.y - fs * .1)} width={r(layout.width + fs * .7)} height={r(items.length * layout.rowHeight + fs * .25)} fill="#ffffff" fillOpacity="0.88" stroke="#bdbdbd" strokeWidth={r(metrics.line * .4)} />}
    {items.map((item, index) => {
      const {x: entryX, row} = layout.entries[index]
      const x = origin.x + entryX
      const y = origin.y + row * layout.rowHeight + fs * .5
      const mid = x + layout.symbol / 2
      return <g key={`${item.label}-${index}`}>
        {item.kind !== 'marker' && <line x1={r(x)} x2={r(x + layout.symbol)} y1={r(y)} y2={r(y)} stroke={item.style.color} strokeWidth={r(metrics.line * 1.2)} strokeDasharray={item.style.dash || undefined} />}
        <Marker shape={item.style.marker} x={mid} y={y} color={item.style.color} size={fs * .27} />
        <SvgText x={x + layout.symbol + fs * .4} y={y + fs * .35} size={fs} family={metrics.family}>{item.label}</SvgText>
      </g>
    })}
  </g>
}

// Left margin that fits the y tick labels and the rotated axis label.
export function leftMargin(scale: Scale, unit: string, metrics: FigureMetrics): number {
  const widest = Math.max(...scale.ticks.map(tick => metrics.text(formatAxisValue(tick, unit))), metrics.fontSize)
  return widest + metrics.fontSize * 2.1
}

export function YAxis({scale, unit, left, top, bottom, right, metrics, label, grid = true}: {scale: Scale; unit: string; left: number; top: number; bottom: number; right: number; metrics: FigureMetrics; label: string; grid?: boolean}) {
  label = useFigureLabels().y(label)
  const y = (value: number) => bottom - (scale.position(value) ?? 0) * (bottom - top)
  return <g className="figure-y-axis">
    {scale.ticks.map(tick => <g key={tick}>
      {grid && <line x1={r(left)} x2={r(right)} y1={r(y(tick))} y2={r(y(tick))} stroke="#e3e3e3" strokeWidth={r(metrics.line * .5)} />}
      <line x1={r(left - metrics.fontSize * .35)} x2={r(left)} y1={r(y(tick))} y2={r(y(tick))} stroke="#111111" strokeWidth={r(metrics.line * .6)} />
      <SvgText x={left - metrics.fontSize * .55} y={y(tick) + metrics.fontSize * .35} anchor="end" size={metrics.fontSize} family={metrics.family} className="tick">{formatAxisValue(tick, unit)}</SvgText>
    </g>)}
    <SvgText x={metrics.fontSize * .95} y={(top + bottom) / 2} anchor="middle" size={metrics.fontSize} family={metrics.family} rotate={-90} className="axis-label">{label}</SvgText>
  </g>
}

export function figureTitleHeight(settings: FigureSettings): number {
  return settings.showTitle ? settings.fontSize * 2 : settings.fontSize * .8
}

export interface FigureCardProps {
  id: string
  title: string
  // Text used for export file names.
  name: string
  caption: string
  // Sources, queries and statistics: kept out of the visible caption in the studio.
  methods?: string
  ariaLabel: string
  width: number
  height: number
  settings: FigureSettings
  provenance?: Array<{metric: string; provenance: ResolvedProvenance}>
  csv?: () => string
  className?: string
  children: ReactNode
  footer?: ReactNode
  // Default axis and series labels (the studio offers them for editing).
  labels?: {x?: string; y?: string; series?: string[]}
  // Height of a 'top' legend: the plot is drawn this much lower.
  legendHeight?: number
}

export function FigureCard({id, title: defaultTitle, name, caption, methods = '', ariaLabel, width, height, settings, provenance = [], csv, className = '', children, footer, labels, legendHeight = 0}: FigureCardProps) {
  const host = useContext(FigureHostContext)
  const title = useFigureLabels().title(defaultTitle)
  const metrics = figureMetrics(settings)
  const shift = metrics.legend === 'top' ? legendHeight : 0
  useLayoutEffect(() => {
    host?.report({id, name, title, caption, methods, width, height, labels: {x: labels?.x, y: labels?.y, series: labels?.series ?? []}, csv})
  })
  const svg = <svg id={id} className="pub-figure" viewBox={`0 0 ${r(width)} ${r(height)}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={ariaLabel} data-export-size="physical" data-figure-size={settings.size} fontFamily={metrics.family}>
    <rect width={r(width)} height={r(height)} fill="#ffffff" />
    {settings.showTitle && <SvgText x={width / 2} y={settings.fontSize * 1.3} anchor="middle" size={settings.fontSize * 1.1} family={metrics.family} weight="bold" className="figure-title">{title}</SvgText>}
    {shift ? <g transform={`translate(0 ${r(shift)})`}>{children}</g> : children}
  </svg>
  if (host) return svg
  const unique = provenance.filter((item, index) => provenance.findIndex(other => other.metric === item.metric) === index)
  const badges = unique.filter((item, index) => unique.findIndex(other => other.provenance.source === item.provenance.source && other.provenance.vantage === item.provenance.vantage) === index)
  const full = [caption, methods].filter(Boolean).join(' ')
  return <article className={`metric-card figure-card ${className}`}>
    <div className="metric-card-header"><div><h3>{title}</h3>{badges.length > 0 && <span className="provenance-badges">{badges.map(item => <ProvenanceBadge key={item.metric} provenance={item.provenance} />)}</span>}</div><ChartExportActions chartID={id} metric={name} figure={{title, caption: full, csv}} /></div>
    <div className="chart-wrap figure-wrap">{svg}</div>
    {footer}
    <p className="figure-caption">{full}</p>
    {unique.map(item => <AboutMetric key={item.metric} metric={item.metric} provenance={item.provenance} />)}
  </article>
}

export function csvWithProvenance(header: string[], rows: Array<Array<string | number | undefined>>, provenance?: {metric: string; provenance: ResolvedProvenance}): string {
  const extra = provenance ? [provenance.metric, provenance.provenance.source, provenance.provenance.vantage, provenance.provenance.rateWindowSeconds ?? ''] : []
  return toCSV([[...header, ...(provenance ? ['metric', 'source', 'vantage', 'rate_window_seconds'] : [])], ...rows.map(row => [...row, ...extra])])
}
