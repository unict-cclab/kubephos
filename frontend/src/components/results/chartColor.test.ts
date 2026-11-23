import {beforeAll, describe, expect, it} from 'vitest'
import {chartPalette, chartSurfaces, contrast, displaySeriesColors, exportColor, oklch, separation, validatePalette} from './chartColor'

const variables = (text: string) => Object.fromEntries([...text.matchAll(/(--[\w-]+):\s*([^;]+);/g)].map(([, key, value]) => [key, value.trim()]))
const block = (css: string, selector: string) => {const start = css.indexOf(selector); return start < 0 ? '' : css.slice(start, css.indexOf('}', start))}
const tokens = (css: string, selector: string) => Array.from({length: 8}, (_, index) => variables(block(css, selector))[`--chart-${index + 1}`])

// Read the stylesheets from disk (CSS imports are stubbed in tests).
let themeCss = ''
let resultsCss = ''
beforeAll(async () => {
  const fsModule = 'node:fs'
  const fs = await import(/* @vite-ignore */ fsModule) as {readFileSync(path: string, encoding: string): string}
  // Vitest runs from the frontend directory.
  const root = (globalThis as unknown as {process: {cwd(): string}}).process.cwd()
  themeCss = fs.readFileSync(`${root}/src/design/theme.css`, 'utf8')
  resultsCss = fs.readFileSync(`${root}/src/components/results/results.css`, 'utf8')
})

describe('chart palette', () => {
  it('mirrors the CSS tokens and the theme card surfaces', () => {
    expect(tokens(resultsCss, '.results-page {\n  --chart-1')).toEqual(chartPalette.light)
    expect(tokens(resultsCss, ':root[data-theme="dark"] .results-page {\n  --chart-1')).toEqual(chartPalette.dark)
    expect(variables(block(themeCss, ':root {'))['--card']).toBe(chartSurfaces.light)
    expect(variables(block(themeCss, ':root[data-theme="dark"] {'))['--card']).toBe(chartSurfaces.dark)
  })

  // Same checks as the dataviz validator: band, chroma, ≥ 3:1 on the card, adjacent
  // series ≥ 15 ΔE (normal vision) and ≥ 6 ΔE under protanopia/deuteranopia.
  it.each(['light', 'dark'] as const)('passes the categorical checks on the %s card', mode => {
    const result = validatePalette(chartPalette[mode], mode)
    expect(result.failures).toEqual([])
    expect(result.warnings).toEqual([])
  })

  it.each(['light', 'dark'] as const)('keeps the first three slots distinct as all pairs (scatter) on %s', mode => {
    expect(validatePalette(chartPalette[mode].slice(0, 3), mode, undefined, 'all').failures).toEqual([])
  })

  it('detects a failing palette', () => {
    expect(validatePalette(['#eda100', '#f0b000'], 'light').ok).toBe(false)
  })
})

describe('user-chosen series colours', () => {
  it('keeps a valid user colour and the palette tokens untouched', () => {
    expect(displaySeriesColors([{label: 'A', color: 'var(--chart-1)'}, {label: 'B', color: '#8e44ad', user: true}], 'light')).toEqual([{color: 'var(--chart-1)'}, {color: '#8e44ad'}])
  })

  it('darkens a pale colour on the light card and lightens a dark one on the dark card, keeping the hue', () => {
    const [pale] = displaySeriesColors([{label: 'Pale', color: '#ffe680', user: true}], 'light')
    expect(contrast(pale.color, chartSurfaces.light)).toBeGreaterThanOrEqual(3)
    expect(Math.abs(oklch(pale.color).h - oklch('#ffe680').h)).toBeLessThan(6)
    expect(pale.note).toMatch(/darker.*#ffe680.*low contrast on the light background/)
    const [dark] = displaySeriesColors([{label: 'Navy', color: '#1a237e', user: true}], 'dark')
    expect(contrast(dark.color, chartSurfaces.dark)).toBeGreaterThanOrEqual(3)
    expect(dark.note).toMatch(/lighter/)
  })

  it('separates a user colour that is too close to another series', () => {
    const [first, second] = displaySeriesColors([{label: 'Baseline', color: 'var(--chart-1)'}, {label: 'Tuned', color: '#2d7bd8', user: true}], 'light')
    expect(first).toEqual({color: 'var(--chart-1)'})
    expect(second.note).toMatch(/too similar to “Baseline”/)
    expect(separation(second.color, chartPalette.light[0]).normal).toBeGreaterThanOrEqual(15)
    expect(contrast(second.color, chartSurfaces.light)).toBeGreaterThanOrEqual(3)
  })

  it('exports keep the user colour unless it fails on white', () => {
    expect(exportColor('#8e44ad')).toBe('#8e44ad')
    expect(exportColor('var(--chart-2)')).toBe('var(--chart-2)')
    expect(contrast(exportColor('#ffe680'), '#ffffff')).toBeGreaterThanOrEqual(3)
  })
})

describe('legend notice', () => {
  it('notes under the chart why a variant colour was adjusted', async () => {
    const {render, screen} = await import('@testing-library/react')
    const {SeriesNotesContext, TimeChart} = await import('./TimeChart')
    const {createElement} = await import('react')
    const series = (label: string, color: string) => ({id: label, label, color, points: [{t: 0, v: 1}, {t: 15000, v: 2}]})
    render(createElement(SeriesNotesContext.Provider, {value: {Tuned: 'Shown darker than the chosen colour #ffe680'}}, createElement(TimeChart, {ariaLabel: 'chart', panels: [{id: 'p', title: 'P', unit: 'cores', series: [series('Baseline', 'var(--chart-1)'), series('Tuned', '#c9a800')]}]})))
    expect(screen.getByText('*Tuned: Shown darker than the chosen colour #ffe680.')).toBeInTheDocument()
  })
})
