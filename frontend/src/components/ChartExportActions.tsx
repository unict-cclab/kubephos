import {useEffect, useRef, useState} from 'react'
import {downloadBlob, exportChart} from '../charts'
import {ActionIcon} from './ActionIcon'
import {ErrorFeedback, useErrorState} from './ToastRegion'

// Publication figures: caption/provenance for SVG <desc> and PDF metadata, and the
// figure's underlying data as CSV.
export interface FigureExport {
  title: string
  caption: string
  csv?: () => string
}

type Action = 'pdf' | 'png' | 'svg' | 'png300' | 'png600' | 'csv'
const labels: Record<Action, string> = {pdf: 'PDF', png: 'PNG', svg: 'SVG', png300: 'PNG 300 dpi', png600: 'PNG 600 dpi', csv: 'Data CSV'}

export function ChartExportActions({chartID, metric, figure}: {chartID: string; metric: string; figure?: FigureExport}) {
  const [busy, setBusy] = useState<Action | null>(null)
  const pending = useRef(false)
  const mounted = useRef(true)
  const [error, setError] = useErrorState()
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  const output = async (action: Action) => {
    if (pending.current) return
    pending.current = true
    setBusy(action)
    setError('')
    try {
      if (!figure) await exportChart(chartID, metric, action === 'png' || action === 'svg' ? action : 'pdf')
      else if (action === 'csv') {
        if (!figure.csv) throw new Error('This figure has no exportable data.')
        downloadBlob(`kubephos-${metric.replaceAll(/[^a-zA-Z0-9]+/g, '-').toLowerCase()}-data.csv`, new Blob([figure.csv()], {type: 'text/csv;charset=utf-8'}))
      } else {
        const options = {title: figure.title, caption: figure.caption}
        if (action === 'png300' || action === 'png600') await exportChart(chartID, metric, 'png', undefined, {...options, dpi: action === 'png300' ? 300 : 600})
        else await exportChart(chartID, metric, action === 'svg' ? 'svg' : 'pdf', undefined, options)
      }
    }
    catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not export the plot. Try again.')}
    finally {pending.current = false; if (mounted.current) setBusy(null)}
  }
  const actions: Action[] = figure ? ['pdf', 'svg', 'png300', 'png600', ...(figure.csv ? ['csv' as const] : [])] : ['pdf', 'svg', 'png']
  return <div className="chart-export-actions" role="group" aria-label="Export plot" aria-busy={Boolean(busy)}>
    {actions.map(action => <button key={action} className="button secondary compact chart-export-button" type="button" disabled={Boolean(busy)} aria-label={busy === action ? `Preparing ${labels[action]}` : labels[action]} onClick={() => void output(action)}><span className="chart-export-indicator" aria-hidden="true">{busy === action ? <span className="loading-indicator" /> : <ActionIcon name="download" />}</span>{labels[action]}</button>)}
    <span className="visually-hidden" role="status">{busy ? `Preparing ${labels[busy]}…` : ''}</span>
    <ErrorFeedback message={error} />
  </div>
}
