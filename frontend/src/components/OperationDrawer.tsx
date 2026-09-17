import {useCallback, useEffect, useRef, useState} from 'react'
import {request} from '../api'
import {formatBytes, formatDate, shortID} from '../lib'
import type {Artifact, LogEntry, Operation, Plugin, Session} from '../types'
import {JsonPreview} from './JsonPreview'
import {ExecutionProgressGraph} from './ExecutionProgressGraph'
import {DetailBreadcrumb} from './DetailBreadcrumb'
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

export function OperationDetailsPage({operationID, session, plugins, close, open, changed, notify}: Props) {
  const [operation, setOperation] = useState<Operation | null>(null)
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [pending, setPending] = useState(false)
  const [preview, setPreview] = useState<{artifact: Artifact; value: unknown} | null>(null)
  const logsRef = useRef<HTMLDivElement>(null)

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
    setPreview(null)
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

  const previewArtifact = async (artifact: Artifact) => {
    setPending(true)
    try {
      const response = await fetch(`/api/v1/artifacts/${artifact.id}/download`)
      if (!response.ok) throw new Error('Could not open the artifact preview.')
      setPreview({artifact, value: await response.json()})
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not open the artifact preview.', true)
    } finally {
      setPending(false)
    }
  }

  const canQueue = operation?.status === 'ready'
  const canCancel = operation && ['ready', 'queued', 'prechecking', 'running', 'verifying'].includes(operation.status)
  const canCleanup = operation && terminal(operation.status) && !operation.plan.steps.some(step => step.cleanup) && plugins.find(plugin => plugin.id === operation.pluginId)?.capabilities?.includes('lifecycle.cleanup')
  const progressNodes = operation?.plan.steps.map((planStep, index) => {
    const step = operation.steps.find(item => item.id === planStep.id)
    const dependencies = [...new Set(planStep.artifactInputs?.map(input => operation.plan.steps.find(item => item.id === input.fromStep)?.name ?? input.fromStep).filter((name): name is string => Boolean(name)) ?? (index ? [operation.plan.steps[index - 1].name] : []))]
    return {id: planStep.id, title: planStep.name, status: step?.status ?? 'queued', detail: step?.error ?? step?.health?.summary ?? effectSummary(operation, index), dependencies}
  }) ?? []
  if (!operationID) return null
  return <div className="detail-page operation-detail-page">
    <DetailBreadcrumb parent="Back" current="Activity" back={close} />
    <div className="detail-page-heading"><div><p className="eyebrow">ACTIVITY</p><h2>{operation?.title ?? 'Loading…'}</h2>{operation && <p>{shortID(operation.id)} · started {formatDate(operation.createdAt)}</p>}</div>{operation && <div className="detail-page-actions operation-heading-actions"><Status value={operation.status} />{canQueue && <button className="button primary" disabled={pending} onClick={queue}>Confirm plan and start</button>}{canCleanup && <button className="button danger" disabled={pending} onClick={prepareCleanup}>Prepare cleanup</button>}{canCancel && <button className="button danger" disabled={pending} onClick={cancel}>Cancel</button>}</div>}</div>
    {operation && <>
      {operation.error && <div className="validation-item error">{operation.error}</div>}
      <div className="operation-live-layout">
        <section className="operation-page-section"><div className="operation-section-heading"><div><p className="eyebrow">PROGRESS</p><h3>Execution steps</h3></div><span>{operation.steps.filter(step => step.status === 'succeeded').length}/{operation.plan.steps.length}</span></div><ExecutionProgressGraph nodes={progressNodes} label={`${operation.title} execution progress`} showLogs={() => logsRef.current?.scrollIntoView({behavior: 'smooth', block: 'start'})} /></section>
        <section className="operation-page-section operation-log-panel" ref={logsRef}><div className="operation-section-heading"><div><p className="eyebrow">LIVE LOGS</p><h3>Activity output</h3></div><span>{logs.length} entries</span></div><div className="log-console">{logs.length ? logs.map(log => <div className={`log-line ${log.level}`} key={log.sequence}><span className="time">{new Date(log.createdAt).toLocaleTimeString()}</span><span className="source">{log.source}</span><span className="message">{log.message}</span></div>) : <div className="log-empty">Waiting for logs…</div>}</div></section>
      </div>
      <section className="operation-page-section"><div className="operation-section-heading"><div><p className="eyebrow">VALIDATION</p><h3>Validated plan</h3></div></div><div className="plan-hash" title={operation.planHash}>SHA-256 {operation.planHash}</div><div className="validation-list">{operation.validation.issues.map((issue, index) => <div className={`validation-item ${issue.level}`} key={`${issue.path ?? ''}-${index}`}><strong>{issue.level.toUpperCase()}</strong> {issue.message}</div>)}</div></section>
      {!!operation.artifacts?.length && <section className="operation-page-section"><div className="operation-section-heading"><div><p className="eyebrow">OUTPUT</p><h3>Verified artifacts</h3></div></div><div className="artifact-list">{operation.artifacts.map(artifact => artifact.sensitive
          ? <div className="artifact-row" key={artifact.id}><span>⌁</span><div><strong>{artifact.name}</strong><small>{artifact.type}/{artifact.version} · protected</small></div></div>
          : <div className="artifact-row" key={artifact.id}><span>▤</span><div><strong>{artifact.name}</strong><small>{artifact.type}/{artifact.version} · {formatBytes(artifact.sizeBytes)} · {artifact.digest.slice(0, 20)}…</small></div><div className="artifact-actions">{artifact.mediaType === 'application/json' && <button className="text-button" disabled={pending} onClick={() => previewArtifact(artifact)}>Preview</button>}<a className="text-button" href={`/api/v1/artifacts/${artifact.id}/download`}>Download</a></div></div>)}</div>{preview && <section className="artifact-preview"><div className="artifact-preview-header"><p className="eyebrow">ARTIFACT PREVIEW</p><button className="icon-button" onClick={() => setPreview(null)} aria-label="Close artifact preview">×</button></div><h3>{preview.artifact.name}</h3><JsonPreview value={preview.value} /></section>}</section>}
    </>}
  </div>
}

export const OperationDrawer = OperationDetailsPage

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
