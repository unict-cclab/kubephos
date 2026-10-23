import {ResourceListHeading} from './ResourceListHeading'
import {ResourceSharing, canManageResource} from './ResourceSharing'
import {ResourceListTable} from './ResourceListTable'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useState} from 'react'
import {request} from '../api'
import {experimentDeletionMessage, experimentDisplayStatus, isTerminalExperiment} from '../lib'
import {useDetailRoute} from '../detailRoute'
import type {Artifact, Experiment, ExperimentConfiguration, Operation, Session, Workspace} from '../types'
import {ResultsView} from './LazyResultsView'
import {ResourceAction} from './ResourceAction'
import {ExperimentExecutionDialog} from './ExperimentExecutionDialog'

import {OwnerFilter, ownerLabel} from './OwnerFilter'
interface Props {
  experiments: Experiment[]
  configurations: ExperimentConfiguration[]
  artifacts: Artifact[]
  operations: Operation[]
  workspaces: Workspace[]
  session: Session
  changed: () => Promise<void>
  openOperation: (id: string) => void
  openPipelineRun?: (id: string) => void
}

type Filter = 'all' | 'active' | 'completed'

export function ExperimentsHistoryView(props: Props) {
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  const detail = useDetailRoute('results')
  const [filter, setFilter] = useState<Filter>('all')
  const [deleting, setDeleting] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [owner, setOwner] = useState('')
  const remove = async (item: Experiment) => {
    if (!await confirmDelete(experimentDeletionMessage(item))) return
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
  if (selected) return <ResultsView artifacts={props.artifacts} experiments={props.experiments} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun} focusedExperiment={selected} onBack={detail.close} />

  const items = props.experiments.filter(item => (!owner || item.ownerUserId === owner) && (filter === 'all' || (filter === 'active' ? ['queued', 'running'].includes(item.status) : ['succeeded', 'failed', 'canceled'].includes(item.status))))
  return <>
    {confirmationDialog}
    <ResourceListHeading title="Experiments" icon="experiments" action={<>{Boolean(props.session.user) && <ResourceAction action="create" label="New experiment" disabled={!props.configurations.some(item => item.validation.valid && canManageResource(props.session,item.ownerUserId))} onClick={() => setCreating(true)} />}</>} />
    {error && <p className="form-error page-error">{error}</p>}
    <div className="history-filters" role="group" aria-label="Filter experiments">{(['all', 'active', 'completed'] as Filter[]).map(value => <button key={value} type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'all' ? 'All' : value === 'active' ? 'In progress' : 'Completed'}</button>)}</div>
    <ResourceListTable filters={<OwnerFilter items={props.experiments} value={owner} onChange={setOwner} />} empty={<><strong>{filter === 'all' && !owner ? 'No experiments yet' : 'No matching experiments'}</strong><p>{filter === 'all' && !owner ? 'Run an experiment from a validated configuration to start collecting results.' : 'Choose another status or owner filter.'}</p></>} searchText={item => `${item.name} ${ownerLabel(item)} ${item.kind} ${experimentDisplayStatus(item)}`} items={items} label="Experiments" columns={[
      {label: 'Name', render: item => <><h3>{item.name}</h3><small>{item.kind === 'suite' ? `Comparative · ${item.variants.length} configurations` : item.variants[0]?.name ?? props.configurations.find(value => value.id === item.configurationId)?.name ?? 'Single experiment'}</small></>},
      {label: 'Runs', render: item => {const trials = item.variants.flatMap(variant => variant.trials); return `${trials.filter(trial => ['succeeded', 'failed', 'canceled'].includes(trial.status)).length}/${trials.length}`}},
      {label: 'Status', render: item => <span className={`status ${experimentDisplayStatus(item)}`}>{experimentDisplayStatus(item)}</span>}
    ]} actions={item => <><ResourceSharing kind="experiments" id={item.id} name={item.name} ownerUserId={item.ownerUserId} session={props.session} changed={props.changed} /><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('instance', item.id)} />{canManageResource(props.session, item.ownerUserId) && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!isTerminalExperiment(item.status) || deleting === item.id} onClick={() => void remove(item)} />}</>} />
    <ExperimentExecutionDialog open={creating} configurations={props.configurations.filter(item => canManageResource(props.session,item.ownerUserId))} close={() => setCreating(false)} session={props.session} changed={props.changed} onCreated={id => detail.open('instance', id)} />
  </>
}
