import {useId, useState} from 'react'
import './ComparisonIntegrityNotice.css'
import type {ComparisonView} from '../comparisons'
import {formatDate} from '../lib'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {ResponsiveTable} from './ResponsiveTable'

// Measurement settings the API fingerprints to tell whether sources were measured alike.
export const compatibilityFieldLabels: Record<string, string> = {
  'load.scenarioId': 'Load scenario',
  'load.taskWeights': 'Request mix (task weights)',
  'load.steps': 'Load profile steps',
  'load.geographicSteps': 'Geographic load steps',
  'load.durationSeconds': 'Load duration (s)',
  'load.warmupSeconds': 'Load warm-up (s)',
  'load.replicas': 'Load generator workers',
  'load.maxUsers': 'Maximum users',
  'load.spawnRate': 'Users started / second',
  'network.phases': 'Network phases',
  'metrics.resolution': 'Data resolution',
  'metrics.rateWindowSeconds': 'Rate window (s)',
  'metrics.sloMilliseconds': 'Response time SLO (ms)'
}

export const compatibilityFieldLabel = (field: string) => compatibilityFieldLabels[field] ?? field

// Scalars as they are; objects and arrays as short JSON, with the full value kept for the tooltip.
export function compatibilityValue(value: unknown): {text: string; full: string} {
  if (value === undefined || value === null || value === '') return {text: 'not set', full: 'Not set'}
  if (typeof value !== 'object') return {text: String(value), full: String(value)}
  const full = JSON.stringify(value) ?? ''
  return {text: full.length > 60 ? `${full.slice(0, 57)}…` : full, full}
}

const refreshConflict = 'One of the experiments was deleted or is no longer shared with you, so this comparison can’t be refreshed. You still see the frozen results; edit the comparison to drop the missing experiment.'

function WarningIcon() {
  return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4m0 3.5h.01" /></svg>
}

function SnapshotIcon() {
  return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><rect x="4" y="11" width="16" height="10" rx="2" /><path d="M8 11V7a4 4 0 0 1 8 0v4" /></svg>
}

// Above comparison results: whether they are frozen, an alert when the sources changed since
// (it asks for a refresh), and every way the sources differ in one collapsed line: the
// measurement settings (a table) and the page's own notes about the data.
export function ComparisonIntegrityNotice({view, onRefresh, notes = []}: {view: ComparisonView; onRefresh?: () => Promise<void>; notes?: string[]}) {
  const id = useId()
  const {confirmAction, confirmationDialog} = useDeleteConfirmation()
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const [refreshed, setRefreshed] = useState(false)
  const captured = view.snapshot?.capturedAt
  const reasons = view.staleReasons ?? []
  const stale = Boolean(captured && (view.stale || reasons.length))
  const compatibility = view.compatibility
  const differences = compatibility?.compatible === false ? compatibility.differences : []
  const labels = [...new Set([...(compatibility?.sources ?? []).map(source => source.label), ...differences.flatMap(difference => Object.keys(difference.values))])]
    .filter(label => differences.some(difference => label in difference.values))

  const refresh = async () => {
    if (!onRefresh || refreshing) return
    const confirmed = await confirmAction(captured
      ? 'Refreshing re-reads the source experiments and replaces the frozen runs, labels and configurations. Statistics, plots and exports of this comparison may change, and figures already shared from the current snapshot will no longer match.'
      : 'Freezing records exactly which runs and results this comparison shows now. Later changes to the source experiments will no longer alter it until you refresh it.', {title: captured ? 'Refresh snapshot?' : 'Freeze comparison?', label: captured ? 'Refresh snapshot' : 'Freeze results'})
    if (!confirmed) return
    setRefreshing(true); setError(''); setRefreshed(false)
    try {
      await onRefresh()
      setRefreshed(true)
    } catch (cause) {
      const status = cause && typeof cause === 'object' && 'status' in cause ? cause.status : undefined
      setError(status === 409 ? refreshConflict : cause instanceof Error && cause.message ? cause.message : 'Could not refresh the comparison.')
    } finally {
      setRefreshing(false)
    }
  }

  return <div className="comparison-integrity">
    {confirmationDialog}
    <div className={`comparison-snapshot ${captured ? 'is-frozen' : 'is-live'}`} title={captured ? 'Results stay fixed until you refresh the snapshot.' : 'Results follow the source experiments until you freeze them.'}>
      <SnapshotIcon />
      <span>{captured ? <>Frozen {formatDate(captured)}</> : <>Live: results may change</>}</span>
      {!captured && onRefresh && <button type="button" className="button secondary compact" disabled={refreshing} onClick={() => void refresh()}>{refreshing ? 'Freezing…' : 'Freeze results'}</button>}
    </div>
    {stale && <section className="comparison-integrity-alert" role="status" aria-labelledby={`${id}-stale`}>
      <WarningIcon />
      <div>
        <h3 id={`${id}-stale`}>The experiments have changed since this snapshot</h3>
        {reasons.length > 0 && <ul>{reasons.map(reason => <li key={reason}>{reason}</li>)}</ul>}
        {onRefresh && <div className="comparison-integrity-actions"><button type="button" className="button secondary compact" disabled={refreshing} onClick={() => void refresh()}>{refreshing ? 'Refreshing…' : 'Refresh snapshot'}</button></div>}
      </div>
    </section>}
    {error && <p className="comparison-integrity-error" role="alert">{error}</p>}
    {refreshed && !error && <p className="visually-hidden" role="status">Comparison snapshot updated.</p>}
    {differences.length + notes.length > 0 && <details className="comparison-caveats">
      <summary><WarningIcon /><span>These experiments weren’t measured quite the same way</span><small>{differences.length + notes.length === 1 ? '1 difference' : `${differences.length + notes.length} differences`}</small></summary>
      {notes.length > 0 && <ul>{notes.map(note => <li key={note}>{note}</li>)}</ul>}
      {differences.length > 0 && <div className="resource-selection-scroll"><ResponsiveTable aria-label="Measurement settings that differ"><thead><tr><th>What differs</th>{labels.map(label => <th key={label}>{label}</th>)}</tr></thead><tbody>{differences.map(difference => <tr key={difference.field}><td>{compatibilityFieldLabel(difference.field)}</td>{labels.map(label => {
          const value = compatibilityValue(difference.values[label])
          return <td key={label} title={value.full}>{value.text}</td>
        })}</tr>)}</tbody></ResponsiveTable></div>}
    </details>}
  </div>
}
