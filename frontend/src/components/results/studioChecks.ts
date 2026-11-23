import type {FigureModel} from './figureModel'
import {paperContrast, type FigureSpec} from './figureSpec'
import type {FigureSettings} from './figureSettings'

// KubeStudio figure checks: readability problems and smart suggestions, each with an
// optional one-click fix the studio applies (undoable).

export type CheckFix = 'palette' | 'one-axis' | 'log-left' | 'seconds' | 'legend' | 'slide-font' | 'zero-left' | 'hide-extra'
export interface FigureCheck {id: string; level: 'warning' | 'suggestion'; message: string; fix?: {id: CheckFix; label: string}}

const median = (values: number[]) => {
  const sorted = [...values].sort((a, b) => a - b)
  const middle = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[middle] : (sorted[middle - 1] + sorted[middle]) / 2
}

export function figureChecks(model: FigureModel | undefined, spec: FigureSpec | undefined, settings: FigureSettings): FigureCheck[] {
  if (!model || model.empty || !spec) return []
  const out: FigureCheck[] = []
  const series = model.legend.map(item => item.series)
  if (series.length > 6) out.push({id: 'many', level: 'warning', message: `${series.length} series are hard to tell apart in print; keep 6 or fewer, or split them into panels.`, fix: {id: 'hide-extra', label: 'Keep the first 6'}})
  const faint = series.filter(item => !paperContrast(item.style.color).ok)
  if (faint.length) out.push({id: 'contrast', level: 'warning', message: `${faint.map(item => `“${item.label}”`).join(', ')} ${faint.length > 1 ? 'are' : 'is'} hard to see on white paper (below 3:1).`, fix: {id: 'palette', label: 'Use Okabe–Ito colours'}})
  const seen = new Map<string, string>()
  for (const item of series) {
    const color = item.style.color.toLowerCase()
    const twin = seen.get(color)
    if (twin && !faint.length) {out.push({id: 'same-colour', level: 'warning', message: `“${twin}” and “${item.label}” share a colour; only the marker tells them apart.`, fix: {id: 'palette', label: 'Recolour from the palette'}}); break}
    seen.set(color, item.label)
  }
  if (model.legend.length > 1 && settings.legend === 'none') out.push({id: 'legend', level: 'warning', message: 'Several series but no legend: readers cannot tell them apart.', fix: {id: 'legend', label: 'Show legend below'}})
  const panel = model.panels[0]
  if (model.type === 'timeseries' && panel?.left && panel.right && panel.left.unit === panel.right.unit && panel.left.unit) out.push({id: 'dual-unit', level: 'warning', message: `Both Y axes show ${panel.left.unit.replaceAll('_', ' ')}: one shared axis compares them directly.`, fix: {id: 'one-axis', label: 'Use one axis'}})
  if (settings.size === 'slide' && settings.fontSize < 10) out.push({id: 'slide-font', level: 'suggestion', message: `${settings.fontSize} pt text is small on a projected slide.`, fix: {id: 'slide-font', label: 'Use 12 pt'}})
  const left = panel?.left
  if (left) {
    const values = panel.series.filter(item => item.axis === 'left').flatMap(item => item.y).filter(value => Number.isFinite(value) && value > 0)
    if (values.length > 4 && !left.spec.log) {
      const ratio = Math.max(...values) / Math.max(median(values), 1e-12)
      if (ratio >= 20 && /second/.test(left.unit)) out.push({id: 'heavy-tail', level: 'suggestion', message: `The largest values are ${Math.round(ratio)}× the median: a log scale keeps the bulk of the data readable.`, fix: {id: 'log-left', label: 'Use log scale'}})
    }
    const maximum = Math.max(...left.scale.ticks)
    if (left.unit === 'milliseconds' && !left.spec.unit && maximum >= 1000) out.push({id: 'seconds', level: 'suggestion', message: `Ticks reach ${maximum.toLocaleString('en-US')} ms; seconds read shorter.`, fix: {id: 'seconds', label: 'Show seconds'}})
    if (/per_second|rps/.test(left.unit) && left.spec.range === 'auto' && !left.spec.log && model.type === 'timeseries') out.push({id: 'zero', level: 'suggestion', message: 'Rates usually start the Y axis at zero, so differences are not exaggerated.', fix: {id: 'zero-left', label: 'Start at 0'}})
  }
  return out
}
