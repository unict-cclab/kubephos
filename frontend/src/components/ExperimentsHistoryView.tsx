import {DisabledActionHint} from './DisabledActionHint'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceSharing, canManageResource} from './ResourceSharing'
import {ResourceListTable} from './ResourceListTable'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useCallback, useEffect, useRef, useState} from 'react'
import {request} from '../api'
import {experimentDeletionMessage, experimentDisplayStatus, isTerminalExperiment} from '../lib'
import {isNewExperimentRoute, useDetailRoute} from '../detailRoute'
import type {Artifact, Experiment, ExperimentConfiguration, Operation, Session, Workspace} from '../types'
import {ResultsView} from './LazyResultsView'
import {ResourceAction} from './ResourceAction'
import {ExperimentExecutionDialog, runnableConfigurations} from './ExperimentExecutionDialog'

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
  const runnable = runnableConfigurations(props.session, props.configurations)
  const runnableCount = useRef(runnable.length)
  runnableCount.current = runnable.length
  const [creating, setCreating] = useState(false)
  // Direct links and all launch buttons obey the same eligibility check.
  const launch = useCallback(() => {
    if (runnableCount.current) setCreating(true)
  }, [])
  useEffect(() => {
    const fromRoute = () => {if (isNewExperimentRoute(window.location.hash)) launch()}
    fromRoute()
    window.addEventListener('hashchange', fromRoute)
    return () => window.removeEventListener('hashchange', fromRoute)
  }, [launch])
  const closeLauncher = () => {
    setCreating(false)
    if (isNewExperimentRoute(window.location.hash)) window.location.hash = 'results'
  }
  const [starting, setStarting] = useState<string | null>(null)
  const [error, setError] = useErrorState()
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
  const start = async (item: Experiment) => {
    const runId = item.variants.flatMap(variant => variant.trials).find(trial => trial.status === 'initialized')?.pipelineRunId
    if (starting || !runId || item.status !== 'initialized' || item.cancelRequested || !canManageResource(props.session, item.ownerUserId)) return
    setStarting(item.id)
    setError('')
    try {
      await request(`/experiments/${item.id}/start-traffic`, {method: 'POST', body: JSON.stringify({runId})}, props.session.csrfToken)
      await props.changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not start the experiment.')
    } finally {
      setStarting(null)
    }
  }
  const selected = detail.route?.kind === 'instance' ? props.experiments.find(item => item.id === detail.route?.id) : undefined
  if (selected) return <ResultsView artifacts={props.artifacts} experiments={props.experiments} operations={props.operations} workspaces={props.workspaces} session={props.session} changed={props.changed} openOperation={props.openOperation} openPipelineRun={props.openPipelineRun} focusedExperiment={selected} onBack={detail.close} />

  const items = props.experiments.filter(item => (!owner || item.ownerUserId === owner) && (filter === 'all' || (filter === 'active' ? ['queued', 'running', 'initializing', 'initialized'].includes(item.status) : ['succeeded', 'failed', 'canceled'].includes(item.status))))
  return <>
    {confirmationDialog}
    <ResourceListHeading title="Runs" icon="experiments" action={<>{Boolean(props.session.user) && <DisabledActionHint disabled={!runnable.length} reason="Requires a valid configuration."><ResourceAction action="create" label="New run" disabled={!runnable.length} title={runnable.length ? 'New run' : 'Requires a valid configuration.'} onClick={launch} /></DisabledActionHint>}</>} />
    {error && <ErrorFeedback message={error} />}
    <div className="history-filters" role="group" aria-label="Filter experiments">{(['all', 'active', 'completed'] as Filter[]).map(value => <button key={value} type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'all' ? 'All' : value === 'active' ? 'In progress' : 'Completed'}</button>)}</div>
    <ResourceListTable open={item => detail.open('instance', item.id)} filters={<OwnerFilter items={props.experiments} value={owner} onChange={setOwner} />} empty={<><strong>{filter === 'all' && !owner ? 'No runs yet' : 'No matching runs'}</strong><p>{filter === 'all' && !owner ? 'Run an experiment from a validated configuration to start collecting results.' : 'Choose another status or owner filter.'}</p></>} searchText={item => `${item.name} ${ownerLabel(item)} ${item.kind} ${experimentDisplayStatus(item)}`} items={items} label="Runs" columns={[
      {label: 'Name', render: item => <><h3>{item.name}</h3><small>{item.kind === 'suite' ? `Comparative · ${item.variants.length} configurations` : item.variants[0]?.name ?? props.configurations.find(value => value.id === item.configurationId)?.name ?? 'Single experiment'}</small></>},
      {label: 'Executions', render: item => {const trials = item.variants.flatMap(variant => variant.trials); return `${trials.filter(trial => ['succeeded', 'failed', 'canceled'].includes(trial.status)).length}/${trials.length}`}},
      {label: 'Status', render: item => <span className={`status ${experimentDisplayStatus(item)}`}>{experimentDisplayStatus(item)}</span>}
    ]} actions={item => <>{item.status === 'initialized' && !item.cancelRequested && canManageResource(props.session, item.ownerUserId) && <ResourceAction action="start" label={starting === item.id ? `Starting ${item.name}…` : `Start ${item.name}`} disabled={starting !== null || !item.variants.some(variant => variant.trials.some(trial => trial.status === 'initialized' && trial.pipelineRunId))} onClick={() => void start(item)} />}<ResourceSharing kind="experiments" id={item.id} name={item.name} ownerUserId={item.ownerUserId} session={props.session} changed={props.changed} /><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('instance', item.id)} />{canManageResource(props.session, item.ownerUserId) && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!isTerminalExperiment(item.status) || deleting === item.id} onClick={() => void remove(item)} />}</>} />
    <ExperimentExecutionDialog open={creating && runnable.length > 0} configurations={runnable} close={closeLauncher} session={props.session} changed={props.changed} onCreated={id => detail.open('instance', id)} />
  </>
}
