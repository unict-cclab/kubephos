import {ResourcePickerDialog, type CandidateLoader, type CandidatePage, type PickerResource} from './ResourcePickerDialog'
import {DisabledActionHint} from './DisabledActionHint'
import {useCallback, useEffect, useState} from 'react'
import type {Artifact, Experiment, Operation, Session, Workspace} from '../types'
import {request} from '../api'
import {formatDate} from '../lib'
import {useDetailRoute} from '../detailRoute'
import {comparisonColors, selectableComparisonRuns, type ComparisonView, type ResultComparison} from '../comparisons'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAction} from './ResourceAction'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {ResultsView} from './LazyResultsView'
import {Dialog} from './Dialog'
import {ResponsiveTable} from './ResponsiveTable'
import {DetailBreadcrumb} from './DetailBreadcrumb'

const loadComparisonCandidates: CandidateLoader = (search, owner, offset, signal) => request<CandidatePage>(`/comparisons/candidates?${new URLSearchParams({search, owner, offset: String(offset)})}`, {}, undefined, signal)

interface Props {
  experiments: Experiment[]; artifacts: Artifact[]; operations: Operation[]; workspaces: Workspace[]; session: Session
  changed: () => Promise<void>; openOperation: (id: string) => void; openPipelineRun?: (id: string) => void
}
export function ComparisonsView(props: Props) {
  const detail = useDetailRoute('comparisons')
  const [items, setItems] = useState<ResultComparison[]>([])
  const [sourceCount, setSourceCount] = useState<number>()
  const [loaded, setLoaded] = useState<ComparisonView>()
  const [loading, setLoading] = useState(true)
  const [revision, setRevision] = useState(0)
  const [editing, setEditing] = useState<ResultComparison | null | undefined>()
  const [busy, setBusy] = useState(false)
  const [error, setError] = useErrorState()
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  const reload = useCallback(() => setRevision(value => value + 1), [])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setLoaded(undefined); setError('')
    const load = async () => {
      const response = await request<{items: ResultComparison[]; eligibleExperimentCount?: number}>('/comparisons', {}, undefined, controller.signal)
      if (controller.signal.aborted) return
      setItems(response.items)
      setSourceCount(response.eligibleExperimentCount)
      if (detail.route?.kind === 'comparison') {
        const view = await request<ComparisonView>(`/comparisons/${encodeURIComponent(detail.route.id)}`, {}, undefined, controller.signal)
        if (!controller.signal.aborted) setLoaded(view)
      }
    }
    void load().catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load comparisons.')}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [detail.route?.id, revision, props.session.user?.id])
  const remove = async (item: ResultComparison) => {
    if (!await confirmDelete(`Delete comparison ${item.name}? Source experiments and their results will be kept.`)) return
    setBusy(true); setError('')
    try {await request(`/comparisons/${item.id}`, {method: 'DELETE'}, props.session.csrfToken); detail.close(); reload()}
    catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not delete the comparison.')}
    finally {setBusy(false)}
  }
  const comparableExperiments=new Set(selectableComparisonRuns(props.experiments).map(run=>run.experiment.id)).size
  const canCreateComparison=!loading&&(sourceCount ?? comparableExperiments)>=2
  const creationHint=loading?'Loading comparisons…':'Requires two experiments with results.'
  const selected = items.find(item => item.id === detail.route?.id)
  const actions = (item: ResultComparison) => <><ResourceAction action="edit" label={`Edit ${item.name}`} disabled={busy} onClick={() => setEditing(item)} /><ResourceAction action="delete" label={`Delete ${item.name}`} disabled={busy} onClick={() => void remove(item)} /></>
  return <>
    {confirmationDialog}
    {loaded ? <ResultsView key={`${loaded.comparison.id}:${loaded.comparison.updatedAt}`} {...props} artifacts={loaded.artifacts} focusedExperiment={loaded.result} onBack={detail.close} comparison={{sourceConfigurations: loaded.sourceConfigurations, actions: actions(loaded.comparison), sources: <div className="resource-selection-scroll"><ResponsiveTable aria-label="Comparison source experiments"><thead><tr><th>Experiment</th><th>Created</th></tr></thead><tbody>{loaded.sources.map(source => <tr key={source.id}><td><a href={`#results/instance/${encodeURIComponent(source.id)}`}>{source.name}</a></td><td>{formatDate(source.createdAt)}</td></tr>)}</tbody></ResponsiveTable></div>}} /> : <>
      <header className="page-header">{detail.route && <DetailBreadcrumb parent="Comparisons" current={selected?.name ?? 'Comparison'} back={detail.close} />}
      <ResourceListHeading title={selected?.name ?? 'Comparisons'} icon="experiments" action={selected ? actions(selected) : <DisabledActionHint disabled={!canCreateComparison} reason={creationHint}><ResourceAction action="create" label="New comparison" disabled={!canCreateComparison} title={canCreateComparison?'New comparison':creationHint} onClick={() => {if(canCreateComparison)setEditing(null)}} /></DisabledActionHint>} /></header>
      <ErrorFeedback message={error} />
      {loading ? <p role="status">Loading comparisons…</p> : !detail.route && <ResourceListTable open={item => detail.open('comparison', item.id)} items={items} editable label="Comparisons" searchText={item => item.name} columns={[{label: 'Name', render: item => <h3>{item.name}</h3>}, {label: 'Experiments', render: item => new Set(item.series.flatMap(group => group.runs.map(run => run.experimentId))).size}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('comparison', item.id)} />{actions(item)}</>} empty={<><strong>No comparisons yet</strong><p>Compare results from completed experiments.</p></>} />}
    </>}
    {editing !== undefined && <ComparisonDialog loadCandidates={loadComparisonCandidates} key={editing?.id ?? 'new'} item={editing ?? undefined} experiments={[...props.experiments, ...(loaded?.sources ?? []).filter(source => !props.experiments.some(item => item.id === source.id))]} session={props.session} close={() => setEditing(undefined)} saved={item => {setEditing(undefined); reload(); detail.open('comparison', item.id)}} />}
  </>
}

export function ComparisonDialog({item, experiments, session, close, saved, loadCandidates}: {item?: ResultComparison; experiments: Experiment[]; session: Session; close: () => void; saved: (item: ResultComparison) => void; loadCandidates?: CandidateLoader}) {
  const available = experiments.filter(experiment => selectableComparisonRuns([experiment]).length > 0)
  const [known, setKnown] = useState<PickerResource[]>([])
  const sources = [...available, ...known.filter(item => !available.some(source => source.id === item.id))]
  const [adding, setAdding] = useState(false)
  const [name, setName] = useState(item?.name ?? '')
  const [selected, setSelected] = useState<Array<{experimentId: string; label: string; color: string}>>(() => item ? [...new Set(item.series.flatMap(group => group.runs.map(run => run.experimentId)))].map(experimentId => {
    const group = item.series.find(group => group.runs.some(run => run.experimentId === experimentId))!
    return {experimentId, color: group.color, label: group.labelOverride ?? ''}
  }) : [])
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const add = (items: PickerResource[]) => {
    setKnown(current => [...current, ...items.filter(item => !current.some(source => source.id === item.id))])
    setSelected(current => {
      const next = [...current]
      for (const item of items) if (next.length < 8 && !next.some(source => source.experimentId === item.id)) next.push({experimentId: item.id, label: '', color: comparisonColors.find(color => !next.some(item => item.color === color)) ?? comparisonColors[next.length % comparisonColors.length]})
      return next
    })
    setAdding(false)
  }
  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault(); if (pending || selected.length < 2) return
    setError(''); setPending(true)
    try {
      const experiments = selected.map(({experimentId, color, label}) => ({experimentId, color, ...(label.trim() ? {label: label.trim()} : {})}))
      const result = await request<ResultComparison>(item ? `/comparisons/${item.id}` : '/comparisons', {method: item ? 'PUT' : 'POST', body: JSON.stringify({name, experiments})}, session.csrfToken)
      saved(result)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save the comparison.')}
    finally {setPending(false)}
  }
  return <><Dialog open className="comparison-modal" title={item ? 'Edit comparison' : 'New comparison'} onClose={() => {if (!pending) close()}}><form className="form-stack" onSubmit={submit} aria-busy={pending}><ErrorFeedback message={error} />
    <label className="comparison-name">Name<input required maxLength={120} value={name} onChange={event => setName(event.target.value)} /></label>
    <div className="form-section-actions"><ResourceAction action="add" label="Add experiments" disabled={selected.length >= 8} title={selected.length >= 8 ? 'Maximum: 8 experiments.' : undefined} onClick={() => setAdding(true)} /></div>
    {selected.length > 0 && <div className="resource-selection-scroll"><ResponsiveTable aria-label="Selected experiments"><thead><tr><th>Experiment</th><th>Created</th><th>Plot label</th><th>Plot color</th><th>Actions</th></tr></thead><tbody>{selected.map(selection => {
      const source = sources.find(item => item.id === selection.experimentId)
      const title = source?.name ?? 'Unavailable experiment'
      return <tr key={selection.experimentId}><td>{title}</td><td>{source?.createdAt ? formatDate(source.createdAt) : '—'}</td><td><input type="text" aria-label={`${title} plot label`} placeholder={title} value={selection.label} maxLength={120} onChange={event => setSelected(current => current.map(item => item.experimentId === selection.experimentId ? {...item, label: event.target.value} : item))} /></td><td><input type="color" aria-label={`${title} plot color`} value={selection.color} onChange={event => setSelected(current => current.map(item => item.experimentId === selection.experimentId ? {...item, color: event.target.value} : item))} /></td><td><ResourceAction action="delete" label={`Remove ${title}`} onClick={() => setSelected(current => current.filter(item => item.experimentId !== selection.experimentId))} /></td></tr>
    })}</tbody></ResponsiveTable></div>}
    <div className="dialog-actions"><button type="button" className="button secondary" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending || !name.trim() || selected.length < 2 || selected.some(item => !sources.some(source => source.id === item.experimentId))}>{pending ? 'Saving…' : item ? 'Save changes' : 'Create comparison'}</button></div>
  </form></Dialog>{adding && <ResourcePickerDialog resource="experiments" items={available} selectedIds={selected.map(item => item.experimentId)} session={session} close={() => setAdding(false)} add={add} load={loadCandidates} />}</>
}
