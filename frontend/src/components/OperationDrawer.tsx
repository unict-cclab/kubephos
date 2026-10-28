import {useCallback, useEffect, useRef, useState} from 'react'
import {request} from '../api'
import type {LogEntry, Operation, PipelineRun, PipelineRunLogEntry, PipelineStrategyLog, Plugin, Session} from '../types'
import {ExecutionProgressGraph} from './ExecutionProgressGraph'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {Status} from './Views'
import {LogViewer} from './LogViewer'
import {operationProgressNodes} from '../operationProgress'

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

  const load = useCallback(async (signal?: AbortSignal) => {
    if (!operationID) return
    const readLogs = async () => {
      const entries: LogEntry[] = []
      let after = 0
      for (;;) {
        const result = await request<{items: LogEntry[]}>(`/operations/${operationID}/logs?after=${after}`, {}, undefined, signal)
        if (signal?.aborted) return entries
        entries.push(...result.items)
        if (result.items.length < 1000) return entries
        after = result.items[result.items.length - 1].sequence
      }
    }
    const [current, entries] = await Promise.all([
      request<Operation>(`/operations/${operationID}`, {}, undefined, signal), readLogs()
    ])
    if (signal?.aborted) return
    setOperation(current)
    setLogs(entries)
  }, [operationID])

  useEffect(() => {
    const controller = new AbortController()
    setOperation(null)
    setLogs([])
    if (operationID) load(controller.signal).catch(cause => {if (!controller.signal.aborted) notify(cause instanceof Error ? cause.message : 'Could not open operation.', true)})
    return () => controller.abort()
  }, [load, notify, operationID])

  useEffect(() => {
    if (!operationID || !operation || terminal(operation.status)) return
    const source = new EventSource(`/api/v1/operations/${operationID}/events?after=${logs.at(-1)?.sequence ?? 0}`)
    source.addEventListener('log', event => {
      const entry = JSON.parse((event as MessageEvent).data) as LogEntry
      setLogs(items => (items.at(-1)?.sequence ?? 0) >= entry.sequence ? items : [...items, entry])
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
  }, [changed, notify, operationID, Boolean(operation), operation ? terminal(operation.status) : false])

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
  const progressNodes = operation ? operationProgressNodes(operation) : []
  if (!operationID) return null
  return <div className="detail-page operation-detail-page">
    <DetailBreadcrumb parent="Back" current="Activity" back={close} />
    <div className="detail-page-heading"><h2>{operation?.title ?? 'Loading…'}</h2>{operation && <div className="detail-page-actions operation-heading-actions"><Status value={operation.status} />{canQueue && <button className="button primary" disabled={pending} onClick={queue}>Confirm plan and start</button>}{canCleanup && <button className="button danger" disabled={pending} onClick={prepareCleanup}>Prepare cleanup</button>}{canCancel && <button className="button danger" disabled={pending} onClick={cancel}>Cancel</button>}</div>}</div>
    {operation && <>
      {operation.error && <div className="validation-item error">{operation.error}</div>}
      <div className="operation-live-layout">
        <section className="operation-page-section"><div className="operation-section-heading"><h3>Progress</h3></div><ExecutionProgressGraph nodes={progressNodes} label={`${operation.title} execution progress`} showLogs={() => logsRef.current?.scrollIntoView({behavior: 'smooth', block: 'start'})} /></section>
        <section className="operation-page-section operation-log-panel" ref={logsRef}><div className="operation-section-heading"><h3>Logs</h3></div><LogViewer entries={logs} label="Operation logs" filename={`${operationID}-logs.txt`} emptyMessage={terminal(operation.status) ? 'This operation has no log entries.' : 'Waiting for the first log entry…'} /></section>
      </div>
    </>}
  </div>
}

export function PipelineRunLogsPage({runID, close, notify}: {runID: string; close: () => void; notify: (message: string, error?: boolean) => void}) {
  const [run, setRun] = useState<PipelineRun | null>(null)
  const [logs, setLogs] = useState<PipelineRunLogEntry[]>([])
  const [strategyLogs, setStrategyLogs] = useState<PipelineStrategyLog[]>([])
  const [runtimeError, setRuntimeError] = useState('')
  const [runtimeLoading, setRuntimeLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    let after = 0
    let finished = false
    let processTimer: number | undefined
    let runtimeTimer: number | undefined
    setRun(null)
    setLogs([])
    setStrategyLogs([])
    setRuntimeError('')
    setRuntimeLoading(true)

    const loadProcess = async (initial = false) => {
      try {
        const current = await request<PipelineRun>(`/pipeline-runs/${runID}`, {}, undefined, controller.signal)
        if (controller.signal.aborted) return
        setRun(current)
        for (;;) {
          const result = await request<{items: PipelineRunLogEntry[]}>(`/pipeline-runs/${runID}/logs?after=${after}`, {}, undefined, controller.signal)
          if (controller.signal.aborted) return
          if (result.items.length) {
            after = result.items[result.items.length - 1].sequence
            setLogs(items => [...items, ...result.items])
          }
          if (result.items.length < 5000) break
        }
        finished = terminal(current.status)
      } catch (cause) {
        if (initial && !controller.signal.aborted) notify(cause instanceof Error ? cause.message : 'Could not open run logs.', true)
      } finally {
        if (!controller.signal.aborted && !finished) processTimer = window.setTimeout(() => void loadProcess(), 1000)
      }
    }
    const loadRuntime = async () => {
      try {
        const result = await request<{items: PipelineStrategyLog[]}>(`/pipeline-runs/${runID}/strategy-logs`, {}, undefined, controller.signal)
        if (!controller.signal.aborted) {setStrategyLogs(result.items); setRuntimeError('')}
      } catch (cause) {
        if (!controller.signal.aborted) setRuntimeError(cause instanceof Error ? cause.message : 'Could not load runtime logs.')
      } finally {
        if (!controller.signal.aborted) setRuntimeLoading(false)
        if (!controller.signal.aborted && !finished) runtimeTimer = window.setTimeout(() => void loadRuntime(), 5000)
      }
    }
    void loadProcess(true)
    void loadRuntime()
    return () => {
      controller.abort()
      window.clearTimeout(processTimer)
      window.clearTimeout(runtimeTimer)
    }
  }, [runID, notify])

  const progressNodes = run?.stages.map(stage => ({id: stage.stageId, title: stage.title, status: stage.status, detail: stage.error || undefined})) ?? []
  return <div className="detail-page operation-detail-page pipeline-log-page">
    <DetailBreadcrumb parent="Experiment" current="Run logs" back={close} />
    <div className="detail-page-heading"><div><h2>{run?.name ?? 'Loading…'}</h2></div>{run && <Status value={run.status} />}</div>
    {run && <div className="operation-live-layout">
      <section className="operation-page-section"><div className="operation-section-heading"><h3>Progress</h3></div><ExecutionProgressGraph nodes={progressNodes} label={`${run.name} execution progress`} /></section>
      <div className="operation-log-stack">
        <section className="operation-page-section operation-log-panel"><div className="operation-section-heading"><h3>Process logs</h3><span>{logs.length} entries</span></div><LogViewer entries={logs} label="Process logs" context={processLogContext} filename={`${runID}-logs.txt`} emptyMessage={terminal(run.status) ? 'This run has no process log entries.' : 'Waiting for the first process log entry…'} /></section>
        {runtimeLoading && <div className="runtime-log-loading" role="status"><span className="loading-indicator" aria-hidden="true" />Loading strategy logs…</div>}
        {runtimeError && <p className="form-error" role="alert">{runtimeError}</p>}
        {strategyLogs.map(runtime => <section className="operation-page-section runtime-log-panel" key={runtime.kind}><div className="operation-section-heading"><div><h3>{runtime.title} logs</h3>{runtime.namespace && <small>{runtime.namespace}{runtime.resource ? ` · ${runtime.resource}` : ''}</small>}</div><span>{runtime.state}</span></div><div className="log-console">{runtime.lines.length ? runtime.lines.map((line, index) => <div className="runtime-log-line" key={`${runtime.kind}-${index}`}>{line}</div>) : <div className="log-empty">{runtime.message ?? 'Waiting for runtime logs…'}</div>}</div></section>)}
      </div>
    </div>}
  </div>
}

export const OperationDrawer = OperationDetailsPage

function terminal(status: string): boolean {
  return ['succeeded', 'failed', 'canceled'].includes(status)
}

function processLogContext(log: PipelineRunLogEntry): string {
  return `${log.stagePosition}. ${log.stageTitle}${log.phase === 'cleanup' ? ' · cleanup' : ''} `
}
