import {useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate} from '../lib'
import type {Pipeline, PipelineRun, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'

interface Props {
  pipelines: Pipeline[]
  runs: PipelineRun[]
  workspaces: Workspace[]
  plugins: Plugin[]
  session: Session
  create: () => void
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

export function PipelinesView({pipelines, runs, workspaces, plugins, session, create, changed, openOperation}: Props) {
  const [selected, setSelected] = useState<Pipeline | null>(null)
  if (!pipelines.length) return <>
    <PipelineHeading create={create} enabled={workspaces.length > 0} />
    <div className="pipeline-empty"><span>⇢</span><h3>No reusable flow yet</h3><p>Combine installed capabilities once, validate them together and reuse the result for development runs, variants and trials.</p><button className="button primary" onClick={create} disabled={!workspaces.length}>Create your first flow</button></div>
  </>
  return <>
    <PipelineHeading create={create} enabled={workspaces.length > 0} />
    <div className="pipeline-grid">{pipelines.map(pipeline => {
      const pipelineRuns = runs.filter(run => run.pipelineId === pipeline.id)
      const latest = pipelineRuns[0]
      return <article className="pipeline-card" key={pipeline.id}>
        <div className="pipeline-card-head"><span className="status-dot succeeded" /><div><h3>{pipeline.name}</h3><p>{workspaces.find(item => item.id === pipeline.workspaceId)?.name ?? 'Workspace'} · {formatDate(pipeline.createdAt)}</p></div><span className="pipeline-result-badge">{pipeline.resolution.result.type}</span><button className="button primary compact" onClick={() => setSelected(pipeline)}>Run</button></div>
        <p>{pipeline.description || 'A validated, reusable execution flow.'}</p>
        <div className="pipeline-track">{pipeline.resolution.stages.map((stage, index) => <div key={stage.id}><span>{index + 1}</span><strong>{pipeline.definition.stages[index]?.title ?? plugins.find(item => item.id === stage.pluginId)?.name ?? stage.pluginId}</strong><small>{stage.pluginVersion}</small></div>)}</div>
        {latest && <RunSummary run={latest} session={session} changed={changed} openOperation={openOperation} />}
        <div className="pipeline-card-foot"><span>✓ {pipeline.resolution.stages.length} stages validated</span><span>{pipelineRuns.length} run{pipelineRuns.length === 1 ? '' : 's'}</span><code>{pipeline.hash.slice(0, 12)}</code></div>
      </article>
    })}</div>
    <RunDialog pipeline={selected} close={() => setSelected(null)} session={session} changed={changed} />
  </>
}

function PipelineHeading({create, enabled}: {create: () => void; enabled: boolean}) {
  return <><div className="section-heading"><div><p className="eyebrow">REUSABLE FLOWS</p><h2>Validated pipelines</h2></div><button className="button primary" onClick={create} disabled={!enabled}>Create flow</button></div><p className="section-copy pipeline-copy">Choose capabilities in order. KubePhos connects compatible outputs and validates the whole flow before anything runs.</p></>
}

function RunSummary({run, session, changed, openOperation}: {run: PipelineRun; session: Session; changed: () => Promise<void>; openOperation: (id: string) => void}) {
  const active = ['queued', 'running', 'prechecking', 'verifying'].includes(run.status)
  const cancel = async () => {
    await request(`/pipeline-runs/${run.id}/cancel`, {method: 'POST', body: '{}'}, session.csrfToken)
    await changed()
  }
  return <div className="pipeline-run">
    <div className="pipeline-run-head"><div><span className={`status-dot ${run.status}`} /><strong>{run.name}</strong><small>{formatDate(run.createdAt)}</small></div><span className={`status-pill ${run.status}`}>{run.cancelRequested && active ? 'canceling' : run.status}</span>{active && <button className="text-button danger" onClick={cancel}>Cancel</button>}</div>
    <div className="pipeline-run-stages">{run.stages.map(stage => <button type="button" key={stage.id} disabled={!stage.operationId} onClick={() => stage.operationId && openOperation(stage.operationId)}><span className={`status-dot ${stage.status}`} /><span><strong>{stage.title}</strong><small>{stage.operationId ? 'Open logs and health gates' : 'Waiting for prior stage'}</small></span></button>)}</div>
    {run.error && <p className="pipeline-run-error">{run.error}</p>}
  </div>
}

function RunDialog({pipeline, close, session, changed}: {pipeline: Pipeline | null; close: () => void; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!pipeline) return
    setPending(true)
    setError('')
    try {
      await request(`/pipelines/${pipeline.id}/runs`, {method: 'POST', body: JSON.stringify({name: new FormData(event.currentTarget).get('name')})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not start the run.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={Boolean(pipeline)} onClose={close} title="Run validated flow" eyebrow="BACKGROUND RUN">
    <form onSubmit={submit} key={pipeline?.id}>
      <p className="field-description">{pipeline?.name} · {pipeline?.resolution.stages.length ?? 0} validated stages</p>
      <label>Run name<input name="name" maxLength={120} defaultValue={pipeline ? `${pipeline.name} · ${new Date().toLocaleString()}` : ''} required autoFocus /></label>
      <div className="validation-callout"><span>✓</span><p><strong>Version locked.</strong> The saved hash and every plug-in version are checked again before the first stage is queued. Each later stage waits for verified outputs.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Starting…' : 'Run in background'}</button></div>
    </form>
  </Dialog>
}
