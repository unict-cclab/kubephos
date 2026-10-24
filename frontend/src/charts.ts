export function timeAxis(endMinutes: number, padded = false): {upper: number; ticks: number[]} {
  const upper = Number.isFinite(endMinutes) && endMinutes > 0 ? endMinutes : 1
  if (padded) {
    const ticks = niceAxis(upper).ticks
    const step = ticks[1]
    const quantum = Math.min(1, 10 ** Math.floor(Math.log10(step)))
    const limit = Math.ceil((upper + Math.max(step * .1, upper * .025)) / quantum) * quantum
    return {upper: limit, ticks: ticks.filter(tick => tick <= limit)}
  }
  // Keep the exact endpoint and leave room for its label beside the regular ticks.
  const ticks = [...niceAxis(upper).ticks.filter(tick => tick === 0 || tick < upper * .92), upper]
  return {upper, ticks}
}

export function niceAxis(maximum: number, integer = false): {upper: number; ticks: number[]} {
  if (!Number.isFinite(maximum) || maximum <= 0) maximum = 1
  const rawStep = maximum / 5
  const magnitude = 10 ** Math.floor(Math.log10(rawStep))
  const normalized = rawStep / magnitude
  const candidates = [1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
  let step = (candidates.find(candidate => candidate >= normalized * .98) ?? 10) * magnitude
  if (integer) step = Math.max(1, Math.ceil(step))
  const ticks = Array.from({length: 6}, (_, index) => index * step)
  let upper = Math.max(ticks[5], maximum)
  if (Math.abs(upper - maximum) <= maximum * .001) upper += step * .25
  return {upper, ticks}
}


export function figureDimensions(sourceWidth: number, sourceHeight: number, contentSized = false) {
  const aspect = sourceWidth > 0 && sourceHeight > 0 ? sourceWidth / sourceHeight : 4 / 3
  const pageWidth = contentSized ? Math.max(1, sourceWidth) * .75 : aspect > 4 / 3 ? 504 : 252
  const pageHeight = pageWidth / aspect
  const pixelsWide = pageWidth / 72 * 600
  const pixelsHigh = pageHeight / 72 * 600
  // Large topologies retain their physical size in PDF. Keep raster exports
  // within browser canvas limits while preserving their aspect ratio.
  const rasterScale = contentSized ? Math.min(1, 16384 / Math.max(pixelsWide, pixelsHigh), Math.sqrt(32000000 / (pixelsWide * pixelsHigh))) : 1
  return {pageWidth, pageHeight, pixelWidth: Math.max(1, Math.round(pixelsWide * rasterScale)), pixelHeight: Math.max(1, Math.round(pixelsHigh * rasterScale))}
}

export async function exportChart(id: string, metric: string, format: 'pdf' | 'png', onGenerated?: (value: Uint8Array) => Promise<void>) {
  const source = document.getElementById(id) as SVGSVGElement | null
  if (!source) return
  const serialized = new XMLSerializer().serializeToString(source)
  const name = `kubephos-${metric.replaceAll(/[^a-zA-Z0-9]+/g, '-').toLowerCase()}`
  if (format === 'pdf') {
    const pdf = svgToPDF(source)
    if (onGenerated) await onGenerated(pdf)
    else downloadBlob(`${name}.pdf`, new Blob([pdf.buffer as ArrayBuffer], {type: 'application/pdf'}))
    return
  }
  const image = new Image()
  const url = URL.createObjectURL(new Blob([serialized], {type: 'image/svg+xml'}))
  try {
    await new Promise<void>((resolve, reject) => { image.onload = () => resolve(); image.onerror = () => reject(new Error('Could not render chart')); image.src = url })
    const [,, sourceWidth, sourceHeight] = (source.getAttribute('viewBox') ?? '0 0 700 525').split(/\s+/).map(Number)
    const dimensions = figureDimensions(sourceWidth, sourceHeight, source.getAttribute('data-export-size') === 'content')
    const canvas = document.createElement('canvas')
    canvas.width = dimensions.pixelWidth
    canvas.height = dimensions.pixelHeight
    const context = canvas.getContext('2d')
    if (!context) return
    context.fillStyle = '#ffffff'
    context.fillRect(0, 0, canvas.width, canvas.height)
    context.drawImage(image, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/png', 1))
    if (!blob) throw new Error('Could not render PNG figure.')
    if (blob) {
      const dpi = Math.round(dimensions.pixelWidth / (dimensions.pageWidth / 72))
      const output = setPNGResolution(new Uint8Array(await blob.arrayBuffer()), dpi)
      if (onGenerated) await onGenerated(output)
      else downloadBlob(`${name}-${dpi}dpi.png`, new Blob([output.buffer as ArrayBuffer], {type: 'image/png'}))
    }
  } finally {
    URL.revokeObjectURL(url)
  }
}

export function svgToPDF(source: SVGSVGElement): Uint8Array {
  const [,, svgWidth, svgHeight] = (source.getAttribute('viewBox') ?? '0 0 700 525').split(/\s+/).map(Number)
  const {pageWidth, pageHeight} = figureDimensions(svgWidth, svgHeight, source.getAttribute('data-export-size') === 'content')
  const scale = pageWidth / svgWidth
  const alphaStates = new Map<number, string>()
  const commands = ['1 1 1 rg', `0 0 ${pageWidth} ${pageHeight} re f`, '0 0 0 RG', '0 0 0 rg']
  const sx = (value: number) => value * scale
  const sy = (value: number) => pageHeight - value * scale
  const number = (value: number) => Number.isFinite(value) ? value : 0
  const inherited = (element: Element, name: string): string => element.getAttribute(name) ?? element.parentElement?.getAttribute(name) ?? ''
  const styleRules = [...(source.querySelector('style')?.textContent ?? '').matchAll(/\.([\w-]+)\s*\{([^}]+)\}/g)]
  const styled = (element: Element, property: string) => {
    for (const rule of [...styleRules].reverse()) {
      if (!element.classList.contains(rule[1])) continue
      const value = rule[2].match(new RegExp(`(?:^|;)\\s*${property}\\s*:\\s*([^;]+)`))?.[1]?.trim()
      if (value) return value
    }
    return ''
  }
  const stroke = (element: Element) => {
    const color = inherited(element, 'stroke') || '#111111'
    commands.push(`${pdfColor(color)} RG`)
    commands.push(`${sx(number(Number(inherited(element, 'stroke-width') || 1.5))).toFixed(3)} w`)
    const dash = inherited(element, 'stroke-dasharray').split(/\s+/).filter(Boolean).map(Number).filter(Number.isFinite)
    commands.push(dash.length ? `[${dash.map(value => sx(value).toFixed(3)).join(' ')}] 0 d` : '[] 0 d')
  }
  for (const element of source.querySelectorAll('rect,line,path,circle,polygon,text')) {
    const tag = element.tagName.toLowerCase()
    if (tag === 'text') {
      const x = sx(number(Number(element.getAttribute('x'))))
      const y = sy(number(Number(element.getAttribute('y'))))
      const declaredSize = number(parseFloat(inherited(element, 'font-size') || styled(element, 'font-size')))
      const logicalSize = declaredSize > 0 ? declaredSize : element.classList.contains('axis-label') ? 20 : element.classList.contains('legend-label') ? 15 : 17
      const size = sx(logicalSize)
      const value = pdfText(element.textContent ?? '')
      const estimate = value.replace(/\\(?:[0-7]{3}|.)/g, 'x').length * size * .5
      const anchor = element.getAttribute('text-anchor')
      const offset = anchor === 'middle' ? estimate / 2 : anchor === 'end' ? estimate : 0
      commands.push(`${pdfColor(inherited(element, 'fill') || styled(element, 'fill') || '#111111')} rg`)
      const rotation = (element.getAttribute('transform') ?? '').match(/^rotate\(\s*(-?[\d.]+)(?:[ ,]+(-?[\d.]+)[ ,]+(-?[\d.]+))?\s*\)$/)
      if (rotation) {
        const radians = -Number(rotation[1]) * Math.PI / 180
        const cosine = Math.cos(radians)
        const sine = Math.sin(radians)
        const pivotX = rotation[2] === undefined ? 0 : sx(Number(rotation[2]))
        const pivotY = rotation[3] === undefined ? pageHeight : sy(Number(rotation[3]))
        const rotatedX = pivotX + (x - offset - pivotX) * cosine - (y - pivotY) * sine
        const rotatedY = pivotY + (x - offset - pivotX) * sine + (y - pivotY) * cosine
        commands.push(`BT /F1 ${size.toFixed(3)} Tf ${cosine.toFixed(4)} ${sine.toFixed(4)} ${(-sine).toFixed(4)} ${cosine.toFixed(4)} ${rotatedX.toFixed(3)} ${rotatedY.toFixed(3)} Tm (${value}) Tj ET`)
      } else commands.push(`BT /F1 ${size.toFixed(3)} Tf 1 0 0 1 ${(x - offset).toFixed(3)} ${y.toFixed(3)} Tm (${value}) Tj ET`)
      continue
    }
    if (tag === 'rect') {
      const x = sx(number(Number(element.getAttribute('x'))))
      const y = sy(number(Number(element.getAttribute('y'))) + number(Number(element.getAttribute('height'))))
      const width = sx(number(Number(element.getAttribute('width'))))
      const height = sx(number(Number(element.getAttribute('height'))))
      if (element.classList.contains('axis')) {
        stroke(element)
        commands.push(`${x.toFixed(3)} ${y.toFixed(3)} ${width.toFixed(3)} ${height.toFixed(3)} re S`)
      } else {
        const outline = inherited(element, 'stroke')
        commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`)
        if (outline && outline !== 'none') stroke(element)
        commands.push(`${x.toFixed(3)} ${y.toFixed(3)} ${width.toFixed(3)} ${height.toFixed(3)} re ${outline && outline !== 'none' ? 'B' : 'f'}`)
      }
      continue
    }
    if (tag === 'line') {
      stroke(element)
      commands.push(`${sx(number(Number(element.getAttribute('x1')))).toFixed(3)} ${sy(number(Number(element.getAttribute('y1')))).toFixed(3)} m ${sx(number(Number(element.getAttribute('x2')))).toFixed(3)} ${sy(number(Number(element.getAttribute('y2')))).toFixed(3)} l S`)
      continue
    }
    if (tag === 'circle') {
      const x = sx(number(Number(element.getAttribute('cx'))))
      const y = sy(number(Number(element.getAttribute('cy'))))
      const radius = sx(number(Number(element.getAttribute('r'))))
      const control = radius * .5522848
      commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`, `${(x + radius).toFixed(3)} ${y.toFixed(3)} m ${(x + radius).toFixed(3)} ${(y + control).toFixed(3)} ${(x + control).toFixed(3)} ${(y + radius).toFixed(3)} ${x.toFixed(3)} ${(y + radius).toFixed(3)} c ${(x - control).toFixed(3)} ${(y + radius).toFixed(3)} ${(x - radius).toFixed(3)} ${(y + control).toFixed(3)} ${(x - radius).toFixed(3)} ${y.toFixed(3)} c ${(x - radius).toFixed(3)} ${(y - control).toFixed(3)} ${(x - control).toFixed(3)} ${(y - radius).toFixed(3)} ${x.toFixed(3)} ${(y - radius).toFixed(3)} c ${(x + control).toFixed(3)} ${(y - radius).toFixed(3)} ${(x + radius).toFixed(3)} ${(y - control).toFixed(3)} ${(x + radius).toFixed(3)} ${y.toFixed(3)} c f`)
      continue
    }
    if (tag === 'polygon') {
      const points = (element.getAttribute('points') ?? '').trim().split(/\s+/).map(pair => pair.split(',').map(Number))
      if (!points.length) continue
      commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`, `${sx(points[0][0]).toFixed(3)} ${sy(points[0][1]).toFixed(3)} m`, ...points.slice(1).map(point => `${sx(point[0]).toFixed(3)} ${sy(point[1]).toFixed(3)} l`), 'h f')
      continue
    }
    const path = element.getAttribute('d') ?? ''
    const converted = pdfPath(path, scale, pageHeight)
    if (!converted) continue
    const fill = inherited(element, 'fill')
    const hasFill = Boolean(fill && fill !== 'none')
    const hasStroke = inherited(element, 'stroke') !== 'none'
    commands.push('q')
    if (hasFill) {
      const alpha = Math.max(0, Math.min(1, Number(inherited(element, 'fill-opacity') || 1)))
      if (alpha < 1) {
        if (!alphaStates.has(alpha)) alphaStates.set(alpha, `GS${alphaStates.size}`)
        commands.push(`/${alphaStates.get(alpha)} gs`)
      }
      commands.push(`${pdfColor(fill)} rg`)
    }
    if (hasStroke) stroke(element)
    commands.push(converted, hasFill ? hasStroke ? 'B' : 'f' : 'S', 'Q')
  }
  return pdfFile(commands.join('\n'), pageWidth, pageHeight, [...alphaStates.keys()])
}

function pdfPath(path: string, scale: number, pageHeight: number): string {
  const tokens = path.match(/[MLHVCZ]|-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/gi) ?? []
  const output: string[] = []
  let command = ''
  let x = 0
  let y = 0
  for (let index = 0; index < tokens.length;) {
    if (/^[MLHVCZ]$/i.test(tokens[index])) command = tokens[index++].toUpperCase()
    if (command === 'Z') {
      output.push('h')
      command = ''
    } else if (command === 'H') {
      x = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} l`)
    } else if (command === 'V') {
      y = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} l`)
    } else if (command === 'M' || command === 'L') {
      x = Number(tokens[index++])
      y = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} ${command === 'M' ? 'm' : 'l'}`)
      if (command === 'M') command = 'L'
    } else if (command === 'C') {
      const firstX = Number(tokens[index++])
      const firstY = Number(tokens[index++])
      const secondX = Number(tokens[index++])
      const secondY = Number(tokens[index++])
      x = Number(tokens[index++])
      y = Number(tokens[index++])
      output.push(`${(firstX * scale).toFixed(3)} ${(pageHeight - firstY * scale).toFixed(3)} ${(secondX * scale).toFixed(3)} ${(pageHeight - secondY * scale).toFixed(3)} ${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} c`)
    } else index++
  }
  return output.join('\n')
}

function pdfColor(value: string): string {
  const named: Record<string, string> = {white: '#ffffff', black: '#000000'}
  const normalized = named[value.toLowerCase()] ?? value.replace(/^#([0-9a-f])([0-9a-f])([0-9a-f])$/i, '#$1$1$2$2$3$3')
  const match = normalized.match(/^#([0-9a-f]{6})$/i)
  if (!match) return '0 0 0'
  return [0, 2, 4].map(offset => (parseInt(match[1].slice(offset, offset + 2), 16) / 255).toFixed(4)).join(' ')
}

function pdfText(value: string): string {
  return value.normalize('NFKD').replaceAll(/[^\x20-\x7e±]/g, '-').replaceAll('\\', '\\\\').replaceAll('(', '\\(').replaceAll(')', '\\)').replaceAll('±', '\\261')
}

function pdfFile(content: string, width: number, height: number, alphas: number[] = []): Uint8Array {
  const encoder = new TextEncoder()
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${width} ${height}] /Resources << /Font << /F1 5 0 R >> /ExtGState << ${alphas.map((_, index) => `/GS${index} ${6 + index} 0 R`).join(' ')} >> >> /Contents 4 0 R >>`,
    `<< /Length ${encoder.encode(content).length} >>\nstream\n${content}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>',
    ...alphas.map(alpha => `<< /Type /ExtGState /ca ${alpha} /CA ${alpha} >>`)
  ]
  let document = '%PDF-1.4\n'
  const offsets = [0]
  objects.forEach((object, index) => {
    offsets.push(encoder.encode(document).length)
    document += `${index + 1} 0 obj\n${object}\nendobj\n`
  })
  const xref = encoder.encode(document).length
  document += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n `).join('\n')}\ntrailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return encoder.encode(document)
}

export function setPNGResolution(source: Uint8Array, dpi: number): Uint8Array {
  if (source.length < 33 || source.slice(0, 8).some((value, index) => value !== [137, 80, 78, 71, 13, 10, 26, 10][index])) throw new Error('Invalid PNG output')
  const chunks: Uint8Array[] = [source.slice(0, 8)]
  const view = new DataView(source.buffer, source.byteOffset, source.byteLength)
  let offset = 8
  while (offset + 12 <= source.length) {
    const length = view.getUint32(offset)
    const end = offset + length + 12
    if (end > source.length) throw new Error('Invalid PNG chunk')
    const type = String.fromCharCode(...source.slice(offset + 4, offset + 8))
    if (type !== 'pHYs') chunks.push(source.slice(offset, end))
    if (type === 'IHDR') chunks.push(pngResolutionChunk(dpi))
    offset = end
  }
  if (offset !== source.length) throw new Error('Invalid PNG length')
  const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const result = new Uint8Array(size)
  let cursor = 0
  for (const chunk of chunks) {
    result.set(chunk, cursor)
    cursor += chunk.length
  }
  return result
}

function pngResolutionChunk(dpi: number): Uint8Array {
  const chunk = new Uint8Array(21)
  const view = new DataView(chunk.buffer)
  view.setUint32(0, 9)
  chunk.set([112, 72, 89, 115], 4)
  const pixelsPerMeter = Math.round(dpi / 0.0254)
  view.setUint32(8, pixelsPerMeter)
  view.setUint32(12, pixelsPerMeter)
  chunk[16] = 1
  view.setUint32(17, crc32(chunk.slice(4, 17)))
  return chunk
}

function crc32(source: Uint8Array): number {
  let crc = 0xffffffff
  for (const byte of source) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
  }
  return (crc ^ 0xffffffff) >>> 0
}

export function downloadBlob(name: string, blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}
