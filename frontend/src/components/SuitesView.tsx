import {useState, type FormEvent} from 'react'
import {SectionIcon} from './SectionIcon'
import {ResourceAction} from './ResourceAction'
import {isTerminalExperiment} from '../lib'
import {request} from '../api'
import type {Application, Experiment, ExperimentConfiguration, ManagedResource, Session} from '../types'
import {Dialog} from './Dialog'
import {NumericInput} from './NumericInput'

const suiteColors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#000000', '#7f7f7f']

interface Props {
  configurations: ExperimentConfiguration[]
  applications: Application[]
  experiments: Experiment[]
  clusters: ManagedResource[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  navigate: (id: string) => void
}

export function SuitesView(props: Props) {
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const suites = props.experiments.filter(item => item.kind === 'suite')
  const remove = async (suite: Experiment) => {
    if (!window.confirm(`Delete suite ${suite.name}? Runs, logs and collected results will be retained.`)) return
    setError('')
    try {
      await request(`/experiments/${suite.id}`, {method: 'DELETE'}, props.session.csrfToken)
      await props.changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not delete the suite.')
    }
  }
  return <>
    <div className="section-heading"><div><SectionIcon kind="suites" /><h2>Suites</h2><p className="section-copy">{suites.length} comparisons</p></div>{props.isAdmin && <ResourceAction action="create" label="New suite" disabled={props.configurations.length < 2} onClick={() => setCreating(true)} />}</div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="managed-grid">{suites.length ? suites.map(suite => {
      const completed = suite.variants.flatMap(item => item.trials).filter(item => ['succeeded', 'failed', 'canceled'].includes(item.status)).length
      const total = suite.variants.reduce((sum, item) => sum + item.trials.length, 0)
      return <article className="managed-card" key={suite.id}><div className="managed-card-head"><span className="feature-icon">⇄</span><span className={`status ${suite.status}`}>{suite.status}</span></div><h3>{suite.name}</h3><p>{suite.variants.length} strategies · {completed}/{total} runs</p><div className="trait-list">{suite.variants.map(item => <span key={item.id}>{item.name}</span>)}</div><div className="managed-actions"><ResourceAction action="view" label={`View ${suite.name} details`} onClick={() => props.navigate(suite.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${suite.name}`} disabled={!isTerminalExperiment(suite.status)} onClick={() => void remove(suite)} />}</div></article>
    }) : <div className="empty-state"><div><strong>No experiment suite</strong>Select at least two configurations using the same cluster and application.</div></div>}</div>
    <SuiteDialog open={creating} close={() => setCreating(false)} {...props} />
  </>
}

function SuiteDialog({open, close, configurations, applications, clusters, session, changed, navigate}: Props & {open: boolean; close: () => void}) {
  const [selected, setSelected] = useState<string[]>([])
  const [presentation, setPresentation] = useState<Record<string, {alias: string; color: string}>>({})
  const [runs, setRuns] = useState(3)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const toggle = (configuration: ExperimentConfiguration) => setSelected(current => {
    if (current.includes(configuration.id)) {
      setPresentation(values => { const next = {...values}; delete next[configuration.id]; return next })
      return current.filter(item => item !== configuration.id)
    }
    if (current.length >= 8) return current
    const used = new Set(Object.values(presentation).map(item => item.color))
    const color = suiteColors.find(item => !used.has(item)) ?? suiteColors[current.length % suiteColors.length]
    setPresentation(values => ({...values, [configuration.id]: {alias: configuration.name, color}}))
    return [...current, configuration.id]
  })
  const updatePresentation = (id: string, field: 'alias' | 'color', value: string) => setPresentation(current => ({...current, [id]: {...current[id], [field]: value}}))
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      const created = await request<Experiment>('/experiment-suites', {method: 'POST', body: JSON.stringify({name: values.get('name'), description: values.get('description'), variants: selected.map(configurationId => ({configurationId, alias: presentation[configurationId]?.alias, color: presentation[configurationId]?.color})), runs, scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      setSelected([])
      setPresentation({})
      close()
      await changed()
      navigate(created.id)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'The suite could not be created.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create experiment suite" eyebrow="COMPARE STRATEGIES" className="template-modal experiment-modal"><form onSubmit={submit}>
    <label>Name<input name="name" maxLength={120} placeholder="Default vs custom scheduler" required autoFocus /></label>
    <label>Description<textarea name="description" rows={2} maxLength={1000} placeholder="What this suite compares" /></label>
    <div className="form-section"><strong>Strategies</strong><p>Select two to eight configurations using the same cluster and application. Every run is executed sequentially.</p></div>
    <div className="suite-configuration-list">{configurations.map(item => {
      const cluster = clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      const isSelected = selected.includes(item.id)
      return <div key={item.id} className={`suite-configuration-option${isSelected ? ' selected' : ''}`}><label><input type="checkbox" checked={isSelected} onChange={() => toggle(item)} /><span><strong>{item.name}</strong><small>{cluster?.name ?? 'Unavailable cluster'} · {applications.find(application => application.reference === item.applicationRef)?.name ?? 'Unavailable application'}</small></span></label>{isSelected && <div className="suite-presentation"><label>Plot alias<input value={presentation[item.id]?.alias ?? item.name} maxLength={80} required onChange={event => updatePresentation(item.id, 'alias', event.target.value)} /></label><label>Line color<input type="color" value={presentation[item.id]?.color ?? suiteColors[0]} aria-label={`${item.name} plot color`} onChange={event => updatePresentation(item.id, 'color', event.target.value)} /></label></div>}</div>
    })}</div>
    <div className="field-row"><label>Runs per strategy<NumericInput name="runs" min={1} max={20} value={runs} onValueChange={setRuns} required /></label><label>Start later (optional)<input type="datetime-local" name="scheduledFor" /></label></div>
    {selected.length > 0 && <p className="field-hint">{runs} per strategy × {selected.length} strategies = {runs * selected.length} sequential runs.</p>}
    <div className="validation-callout"><span>✓</span><p>Every setup and its dependencies are checked before the suite starts.</p></div>
    <p className="form-error">{error}</p><div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || selected.length < 2}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
  </form></Dialog>
}
