// Colour science for chart marks: WCAG contrast against the chart surface and OKLab ΔE
// (×100) between series under normal vision and simulated protanopia / deuteranopia
// (Machado, Oliveira & Fernandes 2009, severity 1.0) — the same method and thresholds as
// the dataviz palette validator. Used to validate the chart palette (tests) and to adjust
// user-chosen variant colours for display.

export type ChartMode = 'light' | 'dark'

// Chart surfaces = the card colour of each theme (theme.css --card). A test keeps them in sync.
export const chartSurfaces: Record<ChartMode, string> = {light: '#ffffff', dark: '#1e2024'}

// Categorical chart palette (results.css --chart-1…8), same hues in both modes, stepped per
// surface. Light slots 3–5 are one lightness step darker than the reference palette so every
// mark clears 3:1 on the white card. A test keeps these in sync with results.css.
export const chartPalette: Record<ChartMode, string[]> = {
  light: ['#2a78d6', '#eb6834', '#01a773', '#c78600', '#dc7099', '#008300', '#6250d6', '#e34948'],
  dark: ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#0ca30c', '#9085e9', '#e66767']
}

export const colorThresholds = {
  // Marks vs surface (WCAG ratio).
  contrast: 3,
  // OKLab ΔE ×100 between two series under unsimulated vision.
  normal: 15,
  // OKLab ΔE ×100 under simulated protan/deutan: target 8, floor 6 (with secondary encoding).
  cvdTarget: 8,
  cvdFloor: 6,
  // OKLCH lightness band per mode (marks neither washed out nor muddy).
  band: {light: [.43, .77], dark: [.48, .67]} as Record<ChartMode, [number, number]>,
  chroma: .1
} as const

type RGB = [number, number, number]

export function parseHex(value: string): RGB | undefined {
  const text = value.trim().replace(/^#/, '')
  const full = /^[\da-f]{3}$/i.test(text) ? text.split('').map(item => item + item).join('') : text
  if (!/^[\da-f]{6}$/i.test(full)) return undefined
  return [0, 2, 4].map(index => parseInt(full.slice(index, index + 2), 16) / 255) as RGB
}

const toHexChannel = (value: number) => Math.round(Math.min(1, Math.max(0, value)) * 255).toString(16).padStart(2, '0')
export const toHex = (rgb: RGB) => `#${rgb.map(toHexChannel).join('')}`

const toLinear = (channel: number) => channel <= .04045 ? channel / 12.92 : ((channel + .055) / 1.055) ** 2.4
const fromLinear = (channel: number) => channel <= .0031308 ? 12.92 * channel : 1.055 * channel ** (1 / 2.4) - .055

function linear(hex: string): RGB {
  const rgb = parseHex(hex)
  if (!rgb) throw new Error(`Not a hex colour: ${hex}`)
  return rgb.map(toLinear) as RGB
}

export function luminance(hex: string): number {
  const [r, g, b] = linear(hex)
  return .2126 * r + .7152 * g + .0722 * b
}

export function contrast(a: string, b: string): number {
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (high + .05) / (low + .05)
}

function oklabFromLinear([r, g, b]: RGB): RGB {
  const l = Math.cbrt(.4122214708 * r + .5363325363 * g + .0514459929 * b)
  const m = Math.cbrt(.2119034982 * r + .6806995451 * g + .1073969566 * b)
  const s = Math.cbrt(.0883024619 * r + .2817188376 * g + .6299787005 * b)
  return [.2104542553 * l + .793617785 * m - .0040720468 * s, 1.9779984951 * l - 2.428592205 * m + .4505937099 * s, .0259040371 * l + .7827717662 * m - .808675766 * s]
}

function linearFromOklab([L, a, b]: RGB): RGB {
  const l = (L + .3963377774 * a + .2158037573 * b) ** 3
  const m = (L - .1055613458 * a - .0638541728 * b) ** 3
  const s = (L - .0894841775 * a - 1.291485548 * b) ** 3
  return [4.0767416621 * l - 3.3077115913 * m + .2309699292 * s, -1.2684380046 * l + 2.6097574011 * m - .3413193965 * s, -.0041960863 * l - .7034186147 * m + 1.707614701 * s]
}

export function oklch(hex: string): {l: number; c: number; h: number} {
  const [L, a, b] = oklabFromLinear(linear(hex))
  return {l: L, c: Math.hypot(a, b), h: (Math.atan2(b, a) * 180 / Math.PI + 360) % 360}
}

// Back to sRGB; out-of-gamut colours lose chroma (hue and lightness kept).
export function fromOklch(l: number, c: number, h: number): string {
  const radians = h * Math.PI / 180
  for (let chroma = c; chroma >= 0; chroma -= .002) {
    const rgb = linearFromOklab([l, chroma * Math.cos(radians), chroma * Math.sin(radians)])
    if (rgb.every(channel => channel >= -1e-4 && channel <= 1 + 1e-4)) return toHex(rgb.map(fromLinear) as RGB)
  }
  return toHex(linearFromOklab([l, 0, 0]).map(fromLinear) as RGB)
}

const machado: Record<'protan' | 'deutan', number[][]> = {
  protan: [[.152286, 1.052583, -.204868], [.114503, .786281, .099216], [-.003882, -.048116, 1.051998]],
  deutan: [[.367322, .860646, -.227968], [.280085, .672501, .047413], [-.01182, .04294, .968881]]
}

function oklabOf(hex: string, kind?: 'protan' | 'deutan'): RGB {
  const rgb = linear(hex)
  if (!kind) return oklabFromLinear(rgb)
  const simulated = machado[kind].map(row => Math.min(1, Math.max(0, row[0] * rgb[0] + row[1] * rgb[1] + row[2] * rgb[2]))) as RGB
  return oklabFromLinear(simulated)
}

export function deltaE(a: string, b: string, kind?: 'protan' | 'deutan'): number {
  const [x, y] = [oklabOf(a, kind), oklabOf(b, kind)]
  return Math.hypot(x[0] - y[0], x[1] - y[1], x[2] - y[2]) * 100
}

// Normal-vision and worst simulated colour-vision-deficiency distance between two marks.
export function separation(a: string, b: string): {normal: number; cvd: number} {
  return {normal: deltaE(a, b), cvd: Math.min(deltaE(a, b, 'protan'), deltaE(a, b, 'deutan'))}
}

const distinct = (a: string, b: string) => {const value = separation(a, b); return value.normal >= colorThresholds.normal && value.cvd >= colorThresholds.cvdFloor}

// The palette token behind var(--chart-n), or the hex itself; undefined for anything else.
export function resolveChartColor(color: string | undefined, mode: ChartMode): string | undefined {
  if (!color) return undefined
  const token = color.match(/^var\(--chart-(\d)\)$/)
  if (token) return chartPalette[mode][Number(token[1]) - 1]
  const rgb = parseHex(color)
  return rgb ? toHex(rgb) : undefined
}

// Shift lightness only (hue kept, chroma kept where the gamut allows) until the colour
// clears the surface contrast and, when possible, is distinct from every other mark. The
// smallest shift wins; ties prefer moving away from the surface.
function shiftLightness(hex: string, surface: string, others: string[]): {color: string; distinct: boolean} | undefined {
  const {l, c, h} = oklch(hex)
  const awayFromSurface = luminance(surface) > .5 ? -1 : 1
  let fallback: string | undefined
  for (let step = 1; step <= 50; step++) {
    for (const direction of [awayFromSurface, -awayFromSurface]) {
      const lightness = l + direction * step * .01
      if (lightness <= .05 || lightness >= .97) continue
      const candidate = fromOklch(lightness, c, h)
      if (contrast(candidate, surface) < colorThresholds.contrast) continue
      if (others.every(other => distinct(candidate, other))) return {color: candidate, distinct: true}
      fallback ??= candidate
    }
  }
  return fallback ? {color: fallback, distinct: false} : undefined
}

export interface DisplayColor {color: string; note?: string}

// Display colours for a set of series. Palette tokens are validated as a set and kept;
// user-chosen hex colours are adjusted when they fail the surface contrast or sit too
// close to an earlier series, with a short note for the legend tooltip.
export function displaySeriesColors(series: Array<{label: string; color: string; user?: boolean}>, mode: ChartMode): DisplayColor[] {
  const surface = chartSurfaces[mode]
  const resolved: Array<string | undefined> = series.map(item => resolveChartColor(item.color, mode))
  return series.map((item, index) => {
    const hex = resolved[index]
    if (!item.user || !hex) return {color: item.color}
    const earlier = resolved.slice(0, index).filter((value): value is string => Boolean(value))
    const later = series.slice(index + 1).map((other, offset) => other.user ? undefined : resolved[index + 1 + offset]).filter((value): value is string => Boolean(value))
    const others = [...earlier, ...later]
    const lowContrast = contrast(hex, surface) < colorThresholds.contrast
    const clash = series.findIndex((other, otherIndex) => otherIndex !== index && (otherIndex < index || !other.user) && resolved[otherIndex] && !distinct(hex, resolved[otherIndex]!))
    if (!lowContrast && clash < 0) return {color: item.color}
    const shifted = shiftLightness(hex, surface, others)
    if (!shifted) return {color: item.color}
    resolved[index] = shifted.color
    const reasons = [lowContrast ? `low contrast on the ${mode} background` : '', clash >= 0 ? `too similar to “${series[clash].label}”` : ''].filter(Boolean).join(' and ')
    const direction = oklch(shifted.color).l < oklch(hex).l ? 'darker' : 'lighter'
    return {color: shifted.color, note: `Shown ${direction} than the chosen colour ${hex} (${reasons})${shifted.distinct ? '' : '; still close to another series, use the legend and table'}. Exports keep the chosen colour.`}
  })
}

// Exported figures sit on white: keep the user's colour unless it fails 3:1 on white.
export function exportColor(color: string): string {
  const hex = resolveChartColor(color, 'light')
  if (!hex || !color.startsWith('#') || contrast(hex, '#ffffff') >= colorThresholds.contrast) return color
  return shiftLightness(hex, '#ffffff', [])?.color ?? color
}

// Full categorical check (contrast, band, chroma, adjacent CVD/normal separation).
export function validatePalette(palette: string[], mode: ChartMode, surface = chartSurfaces[mode], pairs: 'adjacent' | 'all' = 'adjacent') {
  const failures: string[] = []
  const warnings: string[] = []
  const [low, high] = colorThresholds.band[mode]
  for (const color of palette) {
    const ratio = contrast(color, surface)
    if (ratio < colorThresholds.contrast) failures.push(`${color} contrast ${ratio.toFixed(2)}:1 on ${surface}`)
    const {l, c} = oklch(color)
    if (l < low - 1e-9 || l > high + 1e-9) failures.push(`${color} OKLCH L ${l.toFixed(3)} outside ${low}–${high}`)
    if (c < colorThresholds.chroma) failures.push(`${color} chroma ${c.toFixed(3)} below ${colorThresholds.chroma}`)
  }
  const list = pairs === 'all' ? palette.flatMap((a, i) => palette.slice(i + 1).map(b => [a, b])) : palette.slice(1).map((b, i) => [palette[i], b])
  for (const [a, b] of list) {
    const value = separation(a, b)
    if (value.normal < colorThresholds.normal) failures.push(`${a}/${b} normal ΔE ${value.normal.toFixed(1)}`)
    if (value.cvd < colorThresholds.cvdFloor) failures.push(`${a}/${b} CVD ΔE ${value.cvd.toFixed(1)}`)
    else if (value.cvd < colorThresholds.cvdTarget) warnings.push(`${a}/${b} CVD ΔE ${value.cvd.toFixed(1)} (floor band)`)
  }
  return {ok: failures.length === 0, failures, warnings}
}

// Groups (variants) with display colours for the current theme. Colours that are not palette
// tokens were chosen by the user.
export function withDisplayColors<T extends {label: string; color: string}>(groups: T[], mode: ChartMode): Array<T & {colorNote?: string}> {
  const colors = displaySeriesColors(groups.map(group => ({label: group.label, color: group.color, user: !group.color.startsWith('var(')})), mode)
  return groups.map((group, index) => colors[index].note ? {...group, color: colors[index].color, colorNote: colors[index].note} : group)
}
