import {useState} from 'react'
import {SectionIcon} from './SectionIcon'
import {request} from '../api'
import {formatDate, isTerminalExperiment} from '../lib'
import {useDetailRoute} from '../detailRoute'
import type {Artifact, Experiment, ExperimentConfiguration, Operation, Session, Workspace} from '../types'
import {ResultsView} from './ResultsView'
import {ResourceAction} from './ResourceAction'

interface Props {
  experiments: Experiment[]
  configurations: ExperimentConfiguration[]
  artifacts: Artifact[]
  operations: Operation[]
  workspaces: Workspace[]
  session: Session
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

type Filter = 'all' | 'active' | 'completed'

export function ExperimentsHistoryView(props: Props) {
  const detail = useDetailRoute('results')
  const [filter, setFilter] = useState<Filter>('all')
  const [deleting, setDeleting] = useState<string | null>(null)
  const [error, setError] = useState('')
  const remove = async (item: Experiment) => {
    if (!window.confirm(`Delete experiment ${item.name}? Runs, logs and collected results will be retained.`)) return
    setDeleting(item.id)
    setError('')
    try {
      await request(`/experiments/${item.id}`, {method: 'DELETE'}, props.session.csrfToken)
      await props.changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not delete the experiment.')
    } finally {
      setDeleting(null)
    }
  }
  const selected = detail.route?.kind === 'instance' ? props.experiments.find(item => item.id === detail.route?.id) : undefined
  if (selected) return <ResultsView artifacts={props.artifacts} experiments={props.experiments} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} focusedExperiment={selected} onBack={detail.close} />

  const items = props.experiments.filter(item => filter === 'all' || (filter === 'active' ? ['queued', 'running'].includes(item.status) : ['succeeded', 'failed', 'canceled'].includes(item.status)))
  return <>
    <div className="section-heading"><div><SectionIcon kind="experiments" /><h2>Experiments</h2><p className="section-copy">{props.experiments.length} runs and comparisons</p></div></div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="history-filters" role="group" aria-label="Filter experiments">{(['all', 'active', 'completed'] as Filter[]).map(value => <button key={value} type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'all' ? 'All' : value === 'active' ? 'In progress' : 'Completed'}</button>)}</div>
    <div className="experiment-history-list">{items.length ? items.map(item => {
      const configuration = props.configurations.find(value => value.id === item.configurationId)
      const trials = item.variants.flatMap(variant => variant.trials)
      const finished = trials.filter(trial => ['succeeded', 'failed', 'canceled'].includes(trial.status)).length
      return <article className="experiment-history-row" key={item.id}>
        <span className={`status-dot ${item.status}`} aria-hidden="true" />
        <span className="history-row-main"><strong>{item.name}</strong><small>{item.kind === 'suite' ? `${item.variants.length} strategies` : configuration?.name ?? 'Experiment'} · {finished}/{trials.length} runs</small></span>
        <span className="history-row-date">{formatDate(item.createdAt)}</span>
        <span className={`status ${item.status}`}>{item.status}</span>
        <span className="history-row-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('instance', item.id)} />{props.session.user?.role === 'admin' && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!isTerminalExperiment(item.status) || deleting === item.id} onClick={() => void remove(item)} />}</span>
      </article>
    }) : <div className="empty-state"><div><strong>{filter === 'all' ? 'No experiments yet' : 'No matching experiments'}</strong>{filter === 'all' ? 'Run a configuration to see its history here.' : 'Try another filter.'}</div></div>}</div>
  </>
}
