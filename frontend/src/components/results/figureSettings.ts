import {useEffect, useState} from 'react'

export type FigureSize = 'single' | 'onehalf' | 'double' | 'slide' | 'custom'
export type FigureUncertainty = 'none' | 'sd' | 'ci'
export type FigureLegend = 'top' | 'right' | 'bottom' | 'inside' | 'none'

export interface FigureSettings {
  size: FigureSize
  font: 'serif' | 'sans'
  // Base font size in points at print size (7–12).
  fontSize: number
  uncertainty: FigureUncertainty
  zeroBased: boolean
  // Logarithmic y axis for latency figures.
  logY: boolean
  // Okabe–Ito palette plus distinct dash patterns and markers.
  grayscale: boolean
  showTitle: boolean
  // Legend placement (bottom = below the plot, wrapped in rows).
  legend: FigureLegend
  // Inside legend: its top-left corner as a share of the figure's width and height (absent: top right of the plot).
  legendAt?: {x: number; y: number}
  // Legend text size in points (absent: the base font size).
  legendFontSize?: number
  // Custom page size in inches (size = 'custom'): width, and height of the plot block.
  customWidth: number
  customHeight: number
}

// Physical sizes in points (1 in = 72 pt). Heights are the plot area before legends.
export const figureSizes: Record<FigureSize, {label: string; short: string; width: number; height: number}> = {
  single: {label: 'Single column · 3.5 in', short: '3.5 in', width: 252, height: 180},
  onehalf: {label: '1.5 column · 5.5 in', short: '5.5 in', width: 396, height: 252},
  double: {label: 'Double column · 7.16 in', short: '7.16 in', width: 515.52, height: 270},
  slide: {label: 'Slide 16:9 · 10 in', short: '10 × 5.63 in', width: 720, height: 405},
  custom: {label: 'Custom size', short: 'W × H', width: 360, height: 216}
}

export const customLimits = {min: 1.5, max: 14}
export const legendFontLimits = {min: 5, max: 16}

// Physical page width and plot-block height (pt) of the selected size.
export function figureSize(settings: FigureSettings): {width: number; height: number; label: string} {
  if (settings.size !== 'custom') return figureSizes[settings.size]
  return {width: settings.customWidth * 72, height: settings.customHeight * 72, label: `Custom · ${settings.customWidth} × ${settings.customHeight} in`}
}

export const defaultFigureSettings: FigureSettings = {size: 'single', font: 'serif', fontSize: 8, uncertainty: 'ci', zeroBased: true, logY: false, grayscale: false, showTitle: false, legend: 'bottom', customWidth: 5, customHeight: 3}

export const fontStacks: Record<FigureSettings['font'], string> = {
  serif: '"Times New Roman", Times, "Liberation Serif", "Nimbus Roman", serif',
  sans: 'Helvetica, Arial, "Liberation Sans", "Nimbus Sans", sans-serif'
}

const storagePrefix = 'kubephos.figureSettings.v1:'

export function normalizeFigureSettings(value: unknown): FigureSettings {
  const source = value && typeof value === 'object' ? value as Partial<FigureSettings> : {}
  const fontSize = Number(source.fontSize)
  const share = (value: unknown) => {const number = Number(value); return Number.isFinite(number) ? Math.min(1, Math.max(0, number)) : undefined}
  const at = source.legendAt
  const legendAt = at && typeof at === 'object' && share(at.x) !== undefined && share(at.y) !== undefined ? {x: share(at.x)!, y: share(at.y)!} : undefined
  const legendFontSize = source.legendFontSize === undefined ? NaN : Number(source.legendFontSize)
  const inches = (value: unknown, fallback: number) => {const number = Number(value); return Number.isFinite(number) && number > 0 ? Math.min(customLimits.max, Math.max(customLimits.min, Math.round(number * 100) / 100)) : fallback}
  return {
    size: source.size && source.size in figureSizes ? source.size : defaultFigureSettings.size,
    font: source.font === 'sans' || source.font === 'serif' ? source.font : defaultFigureSettings.font,
    fontSize: Number.isFinite(fontSize) ? Math.min(12, Math.max(7, Math.round(fontSize))) : defaultFigureSettings.fontSize,
    uncertainty: source.uncertainty === 'none' || source.uncertainty === 'sd' || source.uncertainty === 'ci' ? source.uncertainty : defaultFigureSettings.uncertainty,
    zeroBased: typeof source.zeroBased === 'boolean' ? source.zeroBased : defaultFigureSettings.zeroBased,
    logY: typeof source.logY === 'boolean' ? source.logY : defaultFigureSettings.logY,
    grayscale: typeof source.grayscale === 'boolean' ? source.grayscale : defaultFigureSettings.grayscale,
    showTitle: typeof source.showTitle === 'boolean' ? source.showTitle : defaultFigureSettings.showTitle,
    legend: source.legend && ['top', 'right', 'bottom', 'inside', 'none'].includes(source.legend) ? source.legend : defaultFigureSettings.legend,
    ...(legendAt ? {legendAt} : {}),
    ...(Number.isFinite(legendFontSize) ? {legendFontSize: Math.min(legendFontLimits.max, Math.max(legendFontLimits.min, Math.round(legendFontSize)))} : {}),
    customWidth: inches(source.customWidth, defaultFigureSettings.customWidth),
    customHeight: inches(source.customHeight, defaultFigureSettings.customHeight)
  }
}

export function loadFigureSettings(page: string): FigureSettings {
  try {
    const stored = window.localStorage.getItem(storagePrefix + page)
    return stored ? normalizeFigureSettings(JSON.parse(stored)) : {...defaultFigureSettings}
  } catch {
    return {...defaultFigureSettings}
  }
}

export function saveFigureSettings(page: string, settings: FigureSettings) {
  try {window.localStorage.setItem(storagePrefix + page, JSON.stringify(settings))} catch {/* storage unavailable: keep the in-memory choice */}
}

// Per-page figure settings persisted in localStorage.
export function useFigureSettings(page: string): [FigureSettings, (next: FigureSettings) => void] {
  const [state, setState] = useState(() => ({page, settings: loadFigureSettings(page)}))
  useEffect(() => {if (state.page !== page) setState({page, settings: loadFigureSettings(page)})}, [page])
  const update = (next: FigureSettings) => {
    const settings = normalizeFigureSettings(next)
    saveFigureSettings(page, settings)
    setState({page, settings})
  }
  return [state.page === page ? state.settings : loadFigureSettings(page), update]
}
