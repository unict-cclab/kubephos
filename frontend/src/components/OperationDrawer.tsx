import {useCallback, useEffect, useState} from 'react'
import {request} from '../api'
import {formatBytes, formatDate, shortID} from '../lib'
import type {LogEntry, Operation, Plugin, Session} from '../types'
import {Status} from './Views'

interface Props {
  operationID: string | null
  session: Session
  plugins: Plugin[]
  close: () => void
  open: (id: string) => void
  changed: () => Promise<void>
  notify: (message: string, error?: boolean) => void
}

export function OperationDrawer({operationID, session, plugins, close, open, changed, notify}: Props) {
  const [operation, setOperation] = useState<Operation | null>(null)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [pending, setPending] = useState(false)

  const load = useCallback(async () => {
    if (!operationID) return
    const [current, logResult] = await Promise.all([
      request<Operation>(`/operations/${operationID}`),
      request<{items: LogEntry[]}>(`/operations/${operationID}/logs`)
    ])
    setOperation(current)
    setLogs(logResult.items)
  }, [operationID])

  useEffect(() => {
    setOperation(null)
    setLogs([])
    if (!operationID) return
    load().catch(cause => notify(cause instanceof Error ? cause.message : 'Could not open operation.', true))
  }, [load, notify, operationID])

  useEffect(() => {
    if (!operationID || !operation || terminal(operation.status)) return
    const source = new EventSource(`/api/v1/operations/${operationID}/events`)
    source.addEventListener('log', event => {
      const entry = JSON.parse((event as MessageEvent).data) as LogEntry
      setLogs(items => items.some(item => item.sequence === entry.sequence) ? items : [...items, entry])
    })
    source.addEventListener('status', event => {
      const current = JSON.parse((event as MessageEvent).data) as Operation
      setOperation(current)
      if (terminal(current.status)) {
        source.close()
        changed().catch(() => undefined)
        notify(current.status === 'succeeded' ? 'Operation completed successfully.' : `Operation ${current.status}.`, current.status === 'failed')
      }
    })
    return () => source.close()
  }, [changed, notify, operation, operationID])

  const queue = async () => {
    if (!operation) return
    setPending(true)
    try {
      await request(`/operations/${operation.id}/queue`, {method: 'POST', body: JSON.stringify({planHash: operation.planHash, acceptWarnings: true})}, session.csrfToken)
      await load()
      await changed()
      notify('Operation queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not queue operation.', true)
    } finally {
      setPending(false)
    }
  }

  const cancel = async () => {
    if (!operation) return
    setPending(true)
    try {
      await request(`/operations/${operation.id}/cancel`, {method: 'POST', body: '{}'}, session.csrfToken)
      await load()
      notify('Cancellation requested.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not cancel operation.', true)
    } finally {
      setPending(false)
    }
  }

  const prepareCleanup = async () => {
    if (!operation) return
    setPending(true)
    try {
      const created = await request<Operation>(`/operations/${operation.id}/cleanup`, {method: 'POST', body: '{}'}, session.csrfToken)
      await changed()
      open(created.id)
      notify('Cleanup plan validated. Review and confirm it before execution.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not prepare cleanup.', true)
    } finally {
      setPending(false)
    }
  }

  const canQueue = operation?.status === 'ready'
  const canCancel = operation && ['ready', 'queued', 'prechecking', 'running', 'verifying'].includes(operation.status)
  const canCleanup = operation && terminal(operation.status) && !operation.plan.steps.some(step => step.cleanup) && plugins.find(plugin => plugin.id === operation.pluginId)?.capabilities?.includes('lifecycle.cleanup')
  return <aside className={`drawer ${operationID ? 'open' : ''}`} aria-hidden={!operationID}>
    <button className="drawer-backdrop" onClick={close} aria-label="Close operation details" />
    <div className="drawer-panel" role="dialog" aria-modal="true" aria-label="Operation details">
      <div className="drawer-header"><div><p className="eyebrow">OPERATION</p><h2>{operation?.title ?? 'Loading…'}</h2></div><button className="icon-button" onClick={close} aria-label="Close">×</button></div>
      {operation && <>
        <div className="detail-summary"><div><Status value={operation.status} /><p>{shortID(operation.id)} · {formatDate(operation.createdAt)}</p></div></div>
        <div className="detail-actions">{canQueue && <button className="button primary" disabled={pending} onClick={queue}>Confirm plan and start</button>}{canCleanup && <button className="button danger" disabled={pending} onClick={prepareCleanup}>Prepare cleanup</button>}{canCancel && <button className="button danger" disabled={pending} onClick={cancel}>Cancel</button>}</div>
        {operation.error && <div className="validation-item error">{operation.error}</div>}
        <p className="eyebrow">VALIDATED PLAN</p>
        <div className="plan-hash" title={operation.planHash}>SHA-256 {operation.planHash}</div>
        <div className="validation-list">{operation.validation.issues.map((issue, index) => <div className={`validation-item ${issue.level}`} key={`${issue.path ?? ''}-${index}`}><strong>{issue.level.toUpperCase()}</strong> {issue.message}</div>)}</div>
        <p className="eyebrow">HEALTH-GATED STEPS</p>
        <div className="step-list">{operation.steps.map((step, index) => <div className="step-row" key={step.id}><span className="step-index">{step.position}</span><div><h4>{step.name}</h4><p>{step.error || step.health?.summary || effectSummary(operation, index)}</p></div><Status value={step.status} /></div>)}</div>
        {!!operation.artifacts?.length && <><p className="eyebrow">VERIFIED ARTIFACTS</p><div className="artifact-list">{operation.artifacts.map(artifact => artifact.sensitive
          ? <div className="artifact-row" key={artifact.id}><span>⌁</span><div><strong>{artifact.name}</strong><small>{artifact.type}/{artifact.version} · protected</small></div></div>
          : <a className="artifact-row" href={`/api/v1/artifacts/${artifact.id}/download`} key={artifact.id}><span>↓</span><div><strong>{artifact.name}</strong><small>{artifact.type}/{artifact.version} · {formatBytes(artifact.sizeBytes)} · {artifact.digest.slice(0, 20)}…</small></div></a>)}</div></>}
        <p className="eyebrow">LIVE LOGS</p>
        <div className="log-console">{logs.length ? logs.map(log => <div className={`log-line ${log.level}`} key={log.sequence}><span className="time">{new Date(log.createdAt).toLocaleTimeString()}</span><span className="source">{log.source}</span><span className="message">{log.message}</span></div>) : <div className="log-empty">Waiting for logs…</div>}</div>
      </>}
    </div>
  </aside>
}

function terminal(status: string): boolean {
  return ['succeeded', 'failed', 'canceled'].includes(status)
}

function effectSummary(operation: Operation, index: number): string {
  const effects = operation.plan.steps[index]?.effects ?? []
  if (effects.length) return effects.map(effect => `${effect.action} ${effect.kind} ${effect.name}`).join(', ')
  const step = operation.plan.steps[index]
  if (step?.artifactInputs?.length || step?.outputs?.length) return `${step.artifactInputs?.length ?? 0} typed inputs · ${step.outputs?.length ?? 0} typed outputs`
  return 'Waiting for precheck'
}
