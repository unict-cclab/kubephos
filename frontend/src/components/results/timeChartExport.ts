import {exportChart, textWidth, type ChartExportFormat, type ChartExportOptions} from '../../charts'

// On-screen time charts (TimeChart) are HTML panel titles and legends over one SVG
// per panel, coloured by CSS classes and theme tokens. Their download flattens them
// into one standalone SVG whose colours, strokes and fonts are presentation
// attributes: what the SVG, PNG and PDF exporters of charts.ts read.

const svgNS = 'http://www.w3.org/2000/svg'
const presentation = ['stroke', 'stroke-width', 'stroke-dasharray', 'stroke-opacity', 'fill', 'fill-opacity', 'opacity', 'font-size', 'font-family', 'font-weight'] as const
const titleBand = 26
const panelHead = 20
const ink = '#111111'

// rgb()/rgba() → #rrggbb plus alpha (the PDF writer understands hexadecimal colours only).
function hexColor(value: string): {hex: string; alpha: number} | undefined {
  const match = value.match(/^rgba?\(\s*([\d.]+)[\s,]+([\d.]+)[\s,]+([\d.]+)(?:[\s,/]+([\d.]+%?))?\s*\)$/)
  if (!match) return undefined
  const alpha = match[4] === undefined ? 1 : match[4].endsWith('%') ? Number(match[4].slice(0, -1)) / 100 : Number(match[4])
  return {hex: `#${match.slice(1, 4).map(channel => Math.round(Number(channel)).toString(16).padStart(2, '0')).join('')}`, alpha}
}

// Copies the computed style of every element of `source` onto its clone as attributes.
function inlineStyles(source: Element, clone: Element) {
  const sources = [source, ...source.querySelectorAll('*')]
  const clones = [clone, ...clone.querySelectorAll('*')]
  sources.forEach((element, index) => {
    const target = clones[index]
    const style = getComputedStyle(element)
    for (const property of presentation) {
      const value = style.getPropertyValue(property).trim()
      if (!value) continue
      const color = (property === 'fill' || property === 'stroke') ? hexColor(value) : undefined
      if (color) {
        if (color.alpha === 0) {target.setAttribute(property, 'none'); continue}
        target.setAttribute(property, color.hex)
        if (color.alpha < 1) target.setAttribute(`${property}-opacity`, String(color.alpha))
      } else if (!(property === 'opacity' && value === '1')) target.setAttribute(property, value)
    }
    target.removeAttribute('class')
    target.removeAttribute('style')
  })
}

function text(content: string, x: number, y: number, size: number, options: {bold?: boolean; fill?: string} = {}) {
  const element = document.createElementNS(svgNS, 'text')
  element.setAttribute('x', String(x))
  element.setAttribute('y', String(y))
  element.setAttribute('font-size', String(size))
  element.setAttribute('font-family', 'Helvetica, Arial, sans-serif')
  if (options.bold) element.setAttribute('font-weight', '600')
  element.setAttribute('fill', options.fill ?? ink)
  element.textContent = content
  return element
}

// One SVG for the whole chart: optional title, then each panel's title, legend and plot.
export function flattenTimeChart(chart: HTMLElement, id: string, title?: string): SVGSVGElement {
  const panels = [...chart.querySelectorAll<HTMLElement>('.time-chart-panel')].flatMap(panel => {
    const plot = panel.querySelector('svg')
    return plot ? [{panel, plot}] : []
  })
  if (!panels.length) throw new Error('The figure has nothing to export yet.')
  const width = Number(panels[0].plot.getAttribute('viewBox')?.split(/\s+/)[2]) || 720
  const root = document.createElementNS(svgNS, 'svg')
  root.setAttribute('id', id)
  const background = document.createElementNS(svgNS, 'rect')
  background.setAttribute('fill', '#ffffff')
  root.appendChild(background)
  let y = 0
  if (title) {root.appendChild(text(title, 8, 18, 14, {bold: true})); y = titleBand}
  for (const {panel, plot} of panels) {
    const heading = panel.querySelector('.time-chart-panel-title')?.textContent?.trim()
    const legend = [...panel.querySelectorAll<HTMLElement>('.time-chart-legend > span')]
    if (heading || legend.length) {
      if (heading) root.appendChild(text(heading, 8, y + 14, 11, {bold: true}))
      // A legend written next to the title, each entry led by its line colour.
      let x = heading ? 16 + textWidth(heading, 11, 'sans', true) : 8
      for (const entry of legend) {
        const key = entry.querySelector('line')
        const color = key ? hexColor(getComputedStyle(key).stroke)?.hex ?? ink : ink
        const line = document.createElementNS(svgNS, 'line')
        Object.entries({x1: x, x2: x + 12, y1: y + 10, y2: y + 10, stroke: color, 'stroke-width': 2}).forEach(([name, value]) => line.setAttribute(name, String(value)))
        root.appendChild(line)
        const label = entry.textContent?.trim() ?? ''
        root.appendChild(text(label, x + 16, y + 14, 10))
        x += 28 + textWidth(label, 10)
      }
      y += panelHead
    }
    const height = Number(plot.getAttribute('viewBox')?.split(/\s+/)[3]) || Number(plot.getAttribute('height')) || 160
    const group = document.createElementNS(svgNS, 'g')
    group.setAttribute('transform', `translate(0 ${y})`)
    const clone = plot.cloneNode(true) as SVGSVGElement
    inlineStyles(plot, clone)
    group.append(...clone.childNodes)
    root.appendChild(group)
    y += height
  }
  background.setAttribute('width', String(width))
  background.setAttribute('height', String(y))
  root.setAttribute('viewBox', `0 0 ${width} ${y}`)
  return root
}

// Downloads a time chart as PDF, PNG (600 dpi) or SVG.
export async function downloadTimeChart(chart: HTMLElement, format: ChartExportFormat, options: ChartExportOptions & {fileName: string; title?: string}) {
  const id = `time-chart-export-${Date.now()}`
  const svg = flattenTimeChart(chart, id, options.title)
  svg.setAttribute('style', 'position:absolute;left:-10000px;top:0;width:1px;height:1px')
  document.body.appendChild(svg)
  try {await exportChart(id, options.fileName, format, undefined, options)} finally {svg.remove()}
}
