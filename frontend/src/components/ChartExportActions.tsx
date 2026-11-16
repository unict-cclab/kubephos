import {useEffect, useRef, useState} from 'react'
import {exportChart} from '../charts'
import {ErrorFeedback, useErrorState} from './ToastRegion'

export function ChartExportActions({chartID, metric}: {chartID: string; metric: string}) {
  const [busy, setBusy] = useState<'pdf' | 'png' | null>(null)
  const pending = useRef(false)
  const mounted = useRef(true)
  const [error, setError] = useErrorState()
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  const output = async (format: 'pdf' | 'png') => {
    if (pending.current) return
    pending.current = true
    setBusy(format)
    setError('')
    try {await exportChart(chartID, metric, format)}
    catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not export the plot. Try again.')}
    finally {pending.current = false; if (mounted.current) setBusy(null)}
  }
  return <div className="chart-export-actions" role="group" aria-label="Export plot" aria-busy={Boolean(busy)}>
    {(['pdf', 'png'] as const).map(format => <button key={format} className="button secondary compact chart-export-button" type="button" disabled={Boolean(busy)} aria-label={busy === format ? `Preparing ${format.toUpperCase()}` : format.toUpperCase()} onClick={() => void output(format)}><span className="chart-export-indicator" aria-hidden="true">{busy === format && <span className="loading-indicator" />}</span>{format.toUpperCase()}</button>)}
    <span className="visually-hidden" role="status">{busy ? `Preparing ${busy.toUpperCase()}…` : ''}</span>
    <ErrorFeedback message={error} />
  </div>
}
