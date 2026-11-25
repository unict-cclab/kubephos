import type {FigureLabelOverrides} from '../chartKit'
import {figureSize, type FigureSettings} from '../figureSettings'
import type {FigureSpec} from './figureSpec'
import type {FigureKind} from './image'
import type {XYState} from '../xy'

// KubeStudio documents: one image made of 1–4 charts, each with its own figure type and
// data, laid out on the page (side by side, stacked, 2 × 2 or 2 + 1) with panel letters.

export interface ChartState {
  id: string
  kind: FigureKind
  metric: string
  xy: XYState
  // Time series / X–Y charts: the FigureSpec (one chart; older specs may hold several panels).
  spec?: FigureSpec
  overrides?: FigureLabelOverrides
  // 'custom': made from a chart form (Line over time, Scatter X–Y…), not a starting point.
  origin?: 'recipe' | 'custom'
}

export type ChartLayout = 'row' | 'column' | 'grid' | 'top2'
export const maxCharts = 4
export const letters = 'abcd'

let counter = 0
export const newChartId = () => `c${Date.now().toString(36)}${(counter++).toString(36)}`

export function layoutChoices(count: number): Array<{value: ChartLayout; label: string; hint: string}> {
  if (count === 2) return [{value: 'row', label: '1 × 2', hint: 'Side by side'}, {value: 'column', label: '2 × 1', hint: 'Stacked'}]
  if (count === 3) return [{value: 'row', label: '3 in a row', hint: 'Three side by side'}, {value: 'top2', label: '2 + 1', hint: 'Two on top, one full width below'}, {value: 'column', label: 'Stacked', hint: 'One under the other'}]
  if (count >= 4) return [{value: 'grid', label: '2 × 2', hint: 'Two rows of two'}, {value: 'row', label: '4 in a row', hint: 'Four side by side'}, {value: 'column', label: 'Stacked', hint: 'One under the other'}]
  return []
}
export const defaultLayout = (count: number): ChartLayout => count === 3 ? 'top2' : count >= 4 ? 'grid' : 'row'
export const validLayout = (count: number, layout: ChartLayout | undefined) => layout && layoutChoices(count).some(choice => choice.value === layout) ? layout : defaultLayout(count)

// Cells as fractions of the page width, row by row.
export function cellBoxes(count: number, layout: ChartLayout): Array<{x: number; width: number; row: number}> {
  if (count <= 1) return [{x: 0, width: 1, row: 0}]
  if (layout === 'column') return Array.from({length: count}, (_, index) => ({x: 0, width: 1, row: index}))
  if (layout === 'grid') return Array.from({length: count}, (_, index) => ({x: index % 2 / 2, width: .5, row: Math.floor(index / 2)}))
  if (layout === 'top2') return Array.from({length: count}, (_, index) => index < 2 ? {x: index / 2, width: .5, row: 0} : {x: 0, width: 1, row: 1})
  return Array.from({length: count}, (_, index) => ({x: index / count, width: 1 / count, row: 0}))
}

// Style of one chart inside the page: its own physical width and plot height.
export function cellSettings(settings: FigureSettings, count: number, width: number, hideLegend: boolean): FigureSettings {
  if (count <= 1) return settings
  const page = figureSize(settings)
  const cellWidth = page.width * width
  const height = Math.max(108, Math.min(page.height * 1.1, cellWidth * .68))
  return {...settings, size: 'custom', customWidth: cellWidth / 72, customHeight: height / 72, legend: hideLegend ? 'none' : settings.legend}
}

// One SVG for the whole page: each chart's drawing translated into its cell, with letters.
// Built only for export (the PDF writer honours group translations).
export function composeFigure(id: string, parts: Array<{svg: SVGSVGElement; x: number; y: number; letter?: string}>, width: number, height: number, font: {family: string; size: number}): SVGSVGElement {
  const ns = 'http://www.w3.org/2000/svg'
  const root = document.createElementNS(ns, 'svg')
  root.setAttribute('id', id)
  root.setAttribute('viewBox', `0 0 ${round(width)} ${round(height)}`)
  root.setAttribute('data-export-size', 'physical')
  root.setAttribute('font-family', font.family)
  const background = document.createElementNS(ns, 'rect')
  for (const [key, value] of Object.entries({x: '0', y: '0', width: String(round(width)), height: String(round(height)), fill: '#ffffff'})) background.setAttribute(key, value)
  root.appendChild(background)
  for (const part of parts) {
    const group = document.createElementNS(ns, 'g')
    group.setAttribute('transform', `translate(${round(part.x)} ${round(part.y)})`)
    const family = part.svg.getAttribute('font-family')
    if (family) group.setAttribute('font-family', family)
    for (const child of [...part.svg.children]) {
      const tag = child.tagName.toLowerCase()
      if (tag === 'title' || tag === 'desc' || tag === 'metadata') continue
      group.appendChild(child.cloneNode(true))
    }
    root.appendChild(group)
    if (part.letter) {
      const text = document.createElementNS(ns, 'text')
      for (const [key, value] of Object.entries({x: String(round(part.x + font.size * .2)), y: String(round(part.y - font.size * .45)), 'font-size': String(round(font.size)), 'font-family': font.family, 'font-weight': 'bold', fill: '#111111'})) text.setAttribute(key, value)
      text.setAttribute('style', `font-family: ${font.family}`)
      text.textContent = part.letter
      root.appendChild(text)
    }
  }
  return root
}

const round = (value: number) => Math.round(value * 100) / 100
