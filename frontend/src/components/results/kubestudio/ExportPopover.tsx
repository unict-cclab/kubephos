import {useEffect, useId, useRef, type KeyboardEvent} from 'react'
import {ActionIcon} from '../../ActionIcon'
import {KubeStudioMark} from './KubeStudioMark'

// KubeStudio export: one primary button opening a popover with the format, what the file
// will be (physical size, resolution, fonts, file names), download, copy and save.

export type ExportAction = 'pdf' | 'svg' | 'png300' | 'png600' | 'csv' | 'python' | 'pgfplots'
export const exportLabels: Record<ExportAction, string> = {pdf: 'PDF', svg: 'SVG', png300: 'PNG 300 dpi', png600: 'PNG 600 dpi', csv: 'Data CSV', python: 'Python (matplotlib)', pgfplots: 'pgfplots (LaTeX)'}
const exportHints: Record<ExportAction, string> = {pdf: 'Vector, real text — for papers', svg: 'Vector, editable in Inkscape/Illustrator', png300: 'Raster for slides and web', png600: 'Raster for print', csv: 'Plotted values + methods header', python: 'Script + CSV that redraw it', pgfplots: '.tex snippet + CSV for LaTeX'}
const formatKey = 'kubephos.kubestudio.exportFormat'

export function rememberedFormat(): ExportAction {
  try {
    const value = window.localStorage.getItem(formatKey)
    return value && value in exportLabels ? value as ExportAction : 'pdf'
  } catch {return 'pdf'}
}
export function rememberFormat(action: ExportAction) {
  try {window.localStorage.setItem(formatKey, action)} catch {/* storage unavailable */}
}

const round = (value: number, digits = 2) => String(Number(value.toFixed(digits)))

function exportFacts(action: ExportAction, page: {width: number; height: number; font: string; fontSize: number}, base: string): Array<[string, string]> {
  const inchesW = page.width / 72
  const inchesH = page.height / 72
  const size = `${round(inchesW)} × ${round(inchesH)} in (${Math.round(inchesW * 25.4)} × ${Math.round(inchesH * 25.4)} mm)`
  const dpi = action === 'png300' ? 300 : action === 'png600' ? 600 : undefined
  const files: Record<ExportAction, string> = {pdf: `${base}.pdf`, svg: `${base}.svg`, png300: `${base}-300dpi.png`, png600: `${base}-600dpi.png`, csv: `${base}-data.csv`, python: `${base}.py + ${base}-plot-data.csv`, pgfplots: `${base}.tex + ${base}-plot-data.csv`}
  const facts: Array<[string, string]> = []
  if (action !== 'csv') facts.push(['Size', size])
  if (dpi) facts.push(['Pixels', `${Math.round(inchesW * dpi)} × ${Math.round(inchesH * dpi)} px at ${dpi} dpi`])
  else if (action === 'pdf' || action === 'svg') facts.push(['Resolution', 'Vector (any zoom)'])
  if (action !== 'csv') facts.push(['Fonts', `${page.font} ${page.fontSize} pt${action === 'pdf' ? ', real text' : action === 'svg' ? ', editable text' : action === 'python' || action === 'pgfplots' ? ', set in the code' : ''}`])
  facts.push([action === 'python' || action === 'pgfplots' ? 'Files' : 'File', files[action]])
  return facts
}

export function ExportPopover({open, onOpenChange, format, onFormat, busy, csv, code, page, base, onExport, onCopy, onSave, saving}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  format: ExportAction
  onFormat: (format: ExportAction) => void
  busy: boolean
  csv: boolean
  code: boolean
  page?: {width: number; height: number; font: string; fontSize: number}
  base: string
  onExport: (action: ExportAction) => void
  onCopy?: (action: 'png300' | 'svg') => void
  onSave?: () => void
  saving: boolean
}) {
  const id = useId()
  const trigger = useRef<HTMLButtonElement>(null)
  const panel = useRef<HTMLDivElement>(null)
  const actions: ExportAction[] = ['pdf', 'svg', 'png300', 'png600', ...(csv ? ['csv' as const] : []), ...(code ? ['python' as const, 'pgfplots' as const] : [])]
  const active = actions.includes(format) ? format : 'pdf'
  useEffect(() => {
    if (!open) return
    panel.current?.querySelector<HTMLElement>('input:checked')?.focus()
    const outside = (event: PointerEvent) => {if (!panel.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) onOpenChange(false)}
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === 'Escape') {event.preventDefault(); event.stopPropagation(); onOpenChange(false); trigger.current?.focus()}
  }
  const copyable = active === 'png300' || active === 'png600' ? 'png300' : active === 'svg' ? 'svg' : undefined
  return <div className="ks-export">
    <button ref={trigger} type="button" className="ks-button ks-primary ks-export-button" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? `${id}-panel` : undefined} aria-keyshortcuts="E" disabled={busy} onClick={() => onOpenChange(!open)}>
      {busy ? <span className="loading-indicator" aria-hidden="true" /> : <ActionIcon name="download" />}{busy ? 'Exporting…' : 'Export'}
    </button>
    {open && <div ref={panel} id={`${id}-panel`} className="ks-popover ks-export-panel" role="dialog" aria-label="Export figure" onKeyDown={onKey}>
      <div className="ks-popover-head"><KubeStudioMark size={18} /><strong>Export figure</strong></div>
      <fieldset className="ks-formats">
        <legend className="visually-hidden">Format</legend>
        {actions.map(action => <label key={action} className="ks-format" data-checked={action === active}>
          <input type="radio" name={`${id}-format`} value={action} checked={action === active} onChange={() => onFormat(action)} onKeyDown={event => {if (event.key === 'Enter') {event.preventDefault(); onExport(action)}}} />
          <span><strong>{exportLabels[action]}</strong><small>{exportHints[action]}</small></span>
        </label>)}
      </fieldset>
      {page && <dl className="ks-facts" aria-label="Output">
        {exportFacts(active, page, base).map(([term, value]) => <div key={term}><dt>{term}</dt><dd>{value}</dd></div>)}
      </dl>}
      <div className="ks-popover-actions">
        <button type="button" className="ks-button ks-primary" disabled={busy} onClick={() => onExport(active)}><ActionIcon name="download" />Download {exportLabels[active]}</button>
        {copyable && onCopy && <button type="button" className="ks-button" disabled={busy} onClick={() => onCopy(copyable)}>Copy {copyable === 'svg' ? 'SVG' : 'image'}</button>}
      </div>
      {onSave && <button type="button" className="ks-link ks-save" disabled={busy || saving} onClick={onSave}>{saving ? 'Saving…' : 'Save PDF to the experiment'}</button>}
    </div>}
  </div>
}
