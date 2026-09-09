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
  const [comparing, setComparing] = useState(false)
  if (!pipelines.length) return <>
    <PipelineHeading create={create} compare={() => setComparing(true)} enabled={workspaces.length > 0} canCompare={false} />
    <div className="pipeline-empty"><span>⇢</span><h3>No reusable flow yet</h3><p>Combine installed capabilities once, validate them together and reuse the result for development runs, variants and trials.</p><button className="button primary" onClick={create} disabled={!workspaces.length}>Create your first flow</button></div>
  </>
  return <>
    <PipelineHeading create={create} compare={() => setComparing(true)} enabled={workspaces.length > 0} canCompare={hasCompatiblePair(pipelines)} />
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
    <CompareDialog open={comparing} close={() => setComparing(false)} pipelines={pipelines} workspaces={workspaces} session={session} changed={changed} />
  </>
}

function PipelineHeading({create, compare, enabled, canCompare}: {create: () => void; compare: () => void; enabled: boolean; canCompare: boolean}) {
  return <><div className="section-heading"><div><p className="eyebrow">REUSABLE FLOWS</p><h2>Validated pipelines</h2></div><div className="topbar-actions"><button className="button secondary" onClick={compare} disabled={!canCompare}>Compare flows</button><button className="button primary" onClick={create} disabled={!enabled}>Create flow</button></div></div><p className="section-copy pipeline-copy">Choose capabilities in order. KubePhos connects compatible outputs and validates the whole flow before anything runs.</p></>
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

function CompareDialog({open, close, pipelines, workspaces, session, changed}: {open: boolean; close: () => void; pipelines: Pipeline[]; workspaces: Workspace[]; session: Session; changed: () => Promise<void>}) {
  const [workspaceId, setWorkspaceId] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const [repetitions, setRepetitions] = useState(1)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const activeWorkspace = workspaceId || workspaces.find(workspace => compatibleInWorkspace(pipelines, workspace.id).length >= 2)?.id || workspaces[0]?.id || ''
  const available = pipelines.filter(pipeline => pipeline.workspaceId === activeWorkspace)
  const selectedContract = pipelines.find(pipeline => pipeline.id === selected[0])?.resolution.result
  const toggle = (pipeline: Pipeline) => setSelected(current => current.includes(pipeline.id) ? current.filter(id => id !== pipeline.id) : [...current, pipeline.id])
  const changeWorkspace = (value: string) => {
    setWorkspaceId(value)
    setSelected([])
    setError('')
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (selected.length < 2) return
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      await request('/experiment-runs', {method: 'POST', body: JSON.stringify({
        workspaceId: activeWorkspace,
        name: form.get('name'),
        description: form.get('description'),
        repetitions,
        variants: selected.map(id => ({pipelineId: id, name: form.get(`variant-${id}`)}))
      })}, session.csrfToken)
      close()
      setSelected([])
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not start the experiment.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Compare validated flows" eyebrow="AUTOMATIC TRIALS" className="compare-modal">
    <form onSubmit={submit}>
      <div className="field-row"><label>Workspace<select value={activeWorkspace} onChange={event => changeWorkspace(event.target.value)}>{workspaces.map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}</select></label><label>Repetitions<select value={repetitions} onChange={event => setRepetitions(Number(event.target.value))}>{[1, 2, 3, 5, 10].map(value => <option key={value} value={value}>{value} per strategy</option>)}</select></label></div>
      <label>Experiment name<input name="name" maxLength={120} placeholder="Default vs secondary scheduler" required autoFocus /></label>
      <label>Description<textarea name="description" maxLength={500} rows={2} placeholder="What are you comparing?" /></label>
      <div className="compare-pipelines"><div><strong>Select at least two strategies</strong><small>Only flows with the same result type can be selected together.</small></div>{available.map(pipeline => {
        const checked = selected.includes(pipeline.id)
        const incompatible = Boolean(selectedContract && (pipeline.resolution.result.type !== selectedContract.type || pipeline.resolution.result.version !== selectedContract.version))
        return <label key={pipeline.id} className={checked ? 'selected' : ''}><input type="checkbox" checked={checked} disabled={!checked && incompatible} onChange={() => toggle(pipeline)} /><span><strong>{pipeline.name}</strong><small>{pipeline.resolution.stages.length} stages · {pipeline.resolution.result.type}/{pipeline.resolution.result.version}</small></span>{checked && <input name={`variant-${pipeline.id}`} maxLength={80} defaultValue={pipeline.name} aria-label={`Variant name for ${pipeline.name}`} required />}</label>
      })}</div>
      <div className="experiment-fanout"><span>{selected.length}</span><small>strategies</small><strong>×</strong><span>{repetitions}</span><small>repetitions</small><strong>=</strong><span>{selected.length * repetitions}</span><small>background trials</small></div>
      <div className="validation-callout"><span>✓</span><p><strong>Validated before fan-out.</strong> Workspace, result contract, pipeline hashes and every plug-in version are checked before all trials are created in one database transaction.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || selected.length < 2 || selected.length * repetitions > 40}>{pending ? 'Creating trials…' : `Start ${selected.length * repetitions} trials`}</button></div>
    </form>
  </Dialog>
}

export function compatibleInWorkspace(pipelines: Pipeline[], workspaceId: string): Pipeline[] {
  const groups = new Map<string, Pipeline[]>()
  for (const pipeline of pipelines.filter(item => item.workspaceId === workspaceId)) {
    const key = `${pipeline.resolution.result.type}/${pipeline.resolution.result.version}`
    groups.set(key, [...(groups.get(key) ?? []), pipeline])
  }
  return [...groups.values()].find(items => items.length >= 2) ?? []
}

export function hasCompatiblePair(pipelines: Pipeline[]): boolean {
  return [...new Set(pipelines.map(item => item.workspaceId))].some(workspaceId => compatibleInWorkspace(pipelines, workspaceId).length >= 2)
}
