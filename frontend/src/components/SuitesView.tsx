import {useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate} from '../lib'
import type {Experiment, ExperimentConfiguration, ManagedResource, Session} from '../types'
import {Dialog} from './Dialog'

interface Props {
  configurations: ExperimentConfiguration[]
  experiments: Experiment[]
  clusters: ManagedResource[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  navigate: () => void
}

export function SuitesView(props: Props) {
  const [creating, setCreating] = useState(false)
  const suites = props.experiments.filter(item => item.kind === 'suite')
  return <>
    <div className="section-heading"><div><p className="eyebrow">COMPARE STRATEGIES</p><h2>Experiment suites</h2></div>{props.isAdmin && <button className="button primary" disabled={props.configurations.length < 2} onClick={() => setCreating(true)}>New suite</button>}</div>
    <p className="section-copy infrastructure-copy">Choose reusable configurations. KubePhos validates comparability, runs every repetition in sequence and groups the resulting evidence.</p>
    <div className="stats-grid infrastructure-stats"><article className="stat-card"><span>Suites</span><strong>{suites.length}</strong><small>immutable comparisons</small></article><article className="stat-card"><span>Configurations</span><strong>{props.configurations.length}</strong><small>available strategies</small></article><article className="stat-card"><span>Active</span><strong>{suites.filter(item => item.status === 'running' || item.status === 'queued').length}</strong><small>queued or running</small></article></div>
    <div className="managed-grid">{suites.length ? suites.map(suite => {
      const completed = suite.variants.flatMap(item => item.trials).filter(item => ['succeeded', 'failed', 'canceled'].includes(item.status)).length
      const total = suite.variants.reduce((sum, item) => sum + item.trials.length, 0)
      return <article className="managed-card" key={suite.id}><div className="managed-card-head"><span className="feature-icon">⇄</span><span className={`status ${suite.status}`}>{suite.status}</span></div><h3>{suite.name}</h3><p>{suite.description || 'Controlled strategy comparison'}</p><dl><div><dt>Strategies</dt><dd>{suite.variants.length}</dd></div><div><dt>Runs</dt><dd>{completed}/{total}</dd></div><div><dt>Result</dt><dd>{suite.resultType}</dd></div><div><dt>Created</dt><dd>{formatDate(suite.createdAt)}</dd></div></dl><div className="trait-list">{suite.variants.map(item => <span key={item.id}>{item.name}</span>)}</div><div className="managed-actions"><button className="button secondary compact" onClick={props.navigate}>Open results</button></div></article>
    }) : <div className="empty-state"><div><strong>No experiment suite</strong>Select at least two compatible configurations to compare strategies with the same application, cluster and result contract.</div></div>}</div>
    <SuiteDialog open={creating} close={() => setCreating(false)} {...props} />
  </>
}

function SuiteDialog({open, close, configurations, clusters, session, changed}: Props & {open: boolean; close: () => void}) {
  const [selected, setSelected] = useState<string[]>([])
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const toggle = (id: string) => setSelected(current => current.includes(id) ? current.filter(item => item !== id) : current.length < 8 ? [...current, id] : current)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      await request('/experiment-suites', {method: 'POST', body: JSON.stringify({name: values.get('name'), description: values.get('description'), configurationIds: selected, runs: Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      setSelected([])
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The suite could not be created.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create experiment suite" eyebrow="CONTROLLED COMPARISON" className="template-modal experiment-modal"><form onSubmit={submit}>
    <label>Name<input name="name" maxLength={120} placeholder="Default vs custom scheduler" required autoFocus /></label>
    <label>Description<textarea name="description" rows={2} maxLength={1000} placeholder="What this suite compares" /></label>
    <div className="form-section"><strong>Strategies</strong><p>Select two to eight configurations using the same cluster and application. Every run is executed sequentially.</p></div>
    <div className="suite-configuration-list">{configurations.map(item => {
      const cluster = clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      return <label key={item.id} className={selected.includes(item.id) ? 'selected' : ''}><input type="checkbox" checked={selected.includes(item.id)} onChange={() => toggle(item.id)} /><span><strong>{item.name}</strong><small>{cluster?.name ?? 'Unavailable cluster'} · {item.applicationRef}</small></span></label>
    })}</div>
    <div className="field-row"><label>Runs per strategy<input type="number" name="runs" min={1} max={20} defaultValue={3} required /></label><label>Start later (optional)<input type="datetime-local" name="scheduledFor" /></label></div>
    <div className="validation-callout"><span>✓</span><p>Cluster, application, result contracts, plugin identities and every generated pipeline are validated before the suite is queued.</p></div>
    <p className="form-error">{error}</p><div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || selected.length < 2}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
  </form></Dialog>
}
