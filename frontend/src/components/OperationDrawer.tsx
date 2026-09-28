import {useCallback, useEffect, useRef, useState} from 'react'
import {request} from '../api'
import type {LogEntry, Operation, PipelineRun, PipelineRunLogEntry, PipelineStrategyLog, Plugin, Session} from '../types'
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
  const progressNodes = operation?.plan.steps.map(planStep => {
    const step = operation.steps.find(item => item.id === planStep.id)
    return {id: planStep.id, title: planStep.name, status: step?.status ?? 'queued', detail: step?.error || undefined}
  }) ?? []
  if (!operationID) return null
  return <div className="detail-page operation-detail-page">
    <DetailBreadcrumb parent="Back" current="Activity" back={close} />
    <div className="detail-page-heading"><h2>{operation?.title ?? 'Loading…'}</h2>{operation && <div className="detail-page-actions operation-heading-actions"><Status value={operation.status} />{canQueue && <button className="button primary" disabled={pending} onClick={queue}>Confirm plan and start</button>}{canCleanup && <button className="button danger" disabled={pending} onClick={prepareCleanup}>Prepare cleanup</button>}{canCancel && <button className="button danger" disabled={pending} onClick={cancel}>Cancel</button>}</div>}</div>
    {operation && <>
      {operation.error && <div className="validation-item error">{operation.error}</div>}
      <div className="operation-live-layout">
        <section className="operation-page-section"><div className="operation-section-heading"><h3>Progress</h3></div><ExecutionProgressGraph nodes={progressNodes} label={`${operation.title} execution progress`} showLogs={() => logsRef.current?.scrollIntoView({behavior: 'smooth', block: 'start'})} /></section>
        <section className="operation-page-section operation-log-panel" ref={logsRef}><div className="operation-section-heading"><h3>Logs</h3></div><div className="log-console">{logs.length ? logs.map(log => <div className={`log-line ${log.level}`} key={log.sequence}><span className="time">{new Date(log.createdAt).toLocaleTimeString()}</span><span className="message">{log.message}</span></div>) : <div className="log-empty">Waiting for logs…</div>}</div></section>
      </div>
    </>}
  </div>
}

export function PipelineRunLogsPage({runID, close, notify}: {runID: string; close: () => void; notify: (message: string, error?: boolean) => void}) {
  const [run, setRun] = useState<PipelineRun | null>(null)
  const [logs, setLogs] = useState<PipelineRunLogEntry[]>([])
  const [strategyLogs, setStrategyLogs] = useState<PipelineStrategyLog[]>([])
  const lastSequence = useRef(0)
  const load = useCallback(async () => {
    const [current, runtimeLogs] = await Promise.all([
      request<PipelineRun>(`/pipeline-runs/${runID}`),
      request<{items: PipelineStrategyLog[]}>(`/pipeline-runs/${runID}/strategy-logs`)
    ])
    let after = lastSequence.current
    const added: PipelineRunLogEntry[] = []
    for (;;) {
      const result = await request<{items: PipelineRunLogEntry[]}>(`/pipeline-runs/${runID}/logs?after=${after}`)
      if (!result.items.length) break
      added.push(...result.items)
      after = result.items[result.items.length - 1].sequence
      if (result.items.length < 5000) break
    }
    setRun(current)
    setStrategyLogs(runtimeLogs.items)
    if (added.length) {
      lastSequence.current = after
      setLogs(items => [...items, ...added])
    }
  }, [runID])

  useEffect(() => {
    setRun(null)
    setLogs([])
    setStrategyLogs([])
    lastSequence.current = 0
    load().catch(cause => notify(cause instanceof Error ? cause.message : 'Could not open run logs.', true))
  }, [load, notify])

  useEffect(() => {
    if (!run || terminal(run.status)) return
    const timer = window.setInterval(() => load().catch(() => undefined), 1000)
    return () => window.clearInterval(timer)
  }, [load, run?.status])

  const progressNodes = run?.stages.map(stage => ({id: stage.stageId, title: stage.title, status: stage.status, detail: stage.error || undefined})) ?? []
  return <div className="detail-page operation-detail-page pipeline-log-page">
    <DetailBreadcrumb parent="Back" current="Run logs" back={close} />
    <div className="detail-page-heading"><div><h2>{run?.name ?? 'Loading…'}</h2><p>Complete chronological process log</p></div>{run && <Status value={run.status} />}</div>
    {run && <div className="operation-live-layout">
      <section className="operation-page-section"><div className="operation-section-heading"><h3>Progress</h3></div><ExecutionProgressGraph nodes={progressNodes} label={`${run.name} execution progress`} /></section>
      <div className="operation-log-stack">
        <section className="operation-page-section operation-log-panel"><div className="operation-section-heading"><h3>Process logs</h3><span>{logs.length} entries</span></div><div className="log-console">{logs.length ? logs.map(log => <div className={`log-line ${log.level}`} key={log.sequence}><span className="time">{new Date(log.createdAt).toLocaleTimeString()}</span><span className="message"><span className="run-log-context">{log.stagePosition}. {log.stageTitle}{log.phase === 'cleanup' ? ' · cleanup' : ''}</span>{log.message}</span></div>) : <div className="log-empty">Waiting for process logs…</div>}</div></section>
        {strategyLogs.map(runtime => <section className="operation-page-section runtime-log-panel" key={runtime.kind}><div className="operation-section-heading"><div><h3>{runtime.title} logs</h3>{runtime.namespace && <small>{runtime.namespace}{runtime.resource ? ` · ${runtime.resource}` : ''}</small>}</div><span>{runtime.state}</span></div><div className="log-console">{runtime.lines.length ? runtime.lines.map((line, index) => <div className="runtime-log-line" key={`${runtime.kind}-${index}`}>{line}</div>) : <div className="log-empty">{runtime.message ?? 'Waiting for runtime logs…'}</div>}</div></section>)}
      </div>
    </div>}
  </div>
}

export const OperationDrawer = OperationDetailsPage

function terminal(status: string): boolean {
  return ['succeeded', 'failed', 'canceled'].includes(status)
}
