import {ResourcePickerDialog, type CandidateLoader, type CandidatePage, type PickerResource} from './ResourcePickerDialog'
import {DisabledActionHint} from './DisabledActionHint'
import {lazy, Suspense, useCallback, useEffect, useState} from 'react'
import type {Artifact, Experiment, Operation, Session, Workspace} from '../types'
import {refreshComparison, request} from '../api'
import {formatDate} from '../lib'
import {useDetailRoute} from '../detailRoute'
import {comparisonColors, selectableComparisonRuns, type ComparisonView, type ResultComparison, type UnavailableComparisonSource} from '../comparisons'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAction} from './ResourceAction'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {ResultsView} from './LazyResultsView'
import {Dialog} from './Dialog'
import {ResponsiveTable} from './ResponsiveTable'
import {DetailBreadcrumb} from './DetailBreadcrumb'

// Only comparison pages need the integrity notice, so it loads with them.
const ComparisonIntegrityNotice = lazy(() => import('./ComparisonIntegrityNotice').then(module => ({default: module.ComparisonIntegrityNotice})))

const loadComparisonCandidates: CandidateLoader = (search, owner, offset, signal) => request<CandidatePage>(`/comparisons/candidates?${new URLSearchParams({search, owner, offset: String(offset)})}`, {}, undefined, signal)

function WarningIcon() {
  return <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4m0 3.5h.01" /></svg>
}

const sourceReason = (source: UnavailableComparisonSource) => source.reason === 'deleted' ? 'Deleted' : 'No longer shared'

// Counts what a comparison is missing in the words the user acts on.
export function unavailableSummary(sources: UnavailableComparisonSource[]): string {
  const count = sources.length
  const noun = count === 1 ? 'experiment' : 'experiments'
  return sources.every(source => source.reason === 'deleted') ? `${count} ${noun} deleted` : `${count} ${noun} unavailable`
}

// Comparisons saved before snapshots existed still follow their sources.
function LiveBadge() {
  return <span className="comparison-live-badge" title="Live comparison — results may change; save or refresh to freeze">Live<span className="visually-hidden"> comparison, results may change</span></span>
}

function UnavailableBadge({sources}: {sources: UnavailableComparisonSource[]}) {
  return <span className="comparison-source-badge" title={sources.map(source => `${source.label} · ${sourceReason(source)}`).join('\n')}><WarningIcon />{unavailableSummary(sources)}</span>
}

// The detail page explains why the plots cannot open and offers the fix in place.
function UnavailableSourcesNotice({item, edit, remove, busy}: {item: ResultComparison; edit: () => void; remove: () => void; busy: boolean}) {
  const sources = item.unavailableSources ?? []
  const deleted = sources.every(source => source.reason === 'deleted')
  const color = (source: UnavailableComparisonSource) => item.series.find(series => series.runs.some(run => run.experimentId === source.experimentId))?.color
  const title = sources.length === 1
    ? deleted ? 'An experiment in this comparison was deleted' : 'An experiment in this comparison is no longer available'
    : deleted ? `${sources.length} experiments in this comparison were deleted` : `${sources.length} experiments in this comparison are no longer available`
  return <section className="source-alert" role="alert" aria-labelledby="source-alert-title">
    <span className="source-alert-icon"><WarningIcon /></span>
    <div className="source-alert-body">
      <h3 id="source-alert-title">{title}</h3>
      <p>Plots and statistics open again once you remove {sources.length === 1 ? 'it' : 'them'} from the comparison. The other experiments and their results are not affected.</p>
      <ul className="source-alert-list">{sources.map(source => <li key={source.experimentId}><span className="source-alert-swatch" style={{background: color(source)}} aria-hidden="true" /><strong>{source.label || 'Unnamed experiment'}</strong><span className="source-alert-reason">{sourceReason(source)}</span></li>)}</ul>
      <div className="source-alert-actions"><button type="button" className="button primary compact" disabled={busy} onClick={edit}>Edit comparison</button><button type="button" className="button secondary compact" disabled={busy} onClick={remove}>Delete comparison</button></div>
    </div>
  </section>
}

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
  // Replaces the frozen snapshot; errors (409 when a source is gone) are shown by the notice.
  const refresh = async () => {
    if (!loaded) return
    const view = await refreshComparison(loaded.comparison.id, props.session.csrfToken)
    setLoaded(view)
    setItems(current => current.map(item => item.id === view.comparison.id ? view.comparison : item))
  }
  const comparableExperiments=new Set(selectableComparisonRuns(props.experiments).map(run=>run.experiment.id)).size
  const canCreateComparison=!loading&&(sourceCount ?? comparableExperiments)>=2
  const creationHint=loading?'Loading comparisons…':'Requires two experiments with results.'
  const selected = items.find(item => item.id === detail.route?.id)
  const affected = items.filter(item => item.unavailableSources?.length).length
  const actions = (item: ResultComparison) => <><ResourceAction action="edit" label={`Edit ${item.name}`} disabled={busy} onClick={() => setEditing(item)} /><ResourceAction action="delete" label={`Delete ${item.name}`} disabled={busy} onClick={() => void remove(item)} /></>
  return <>
    {confirmationDialog}
    {loaded ? <><ResultsView key={`${loaded.comparison.id}:${loaded.comparison.updatedAt}:${loaded.snapshot?.capturedAt ?? ''}`} {...props} artifacts={loaded.artifacts} focusedExperiment={loaded.result} onBack={detail.close} comparison={{sourceConfigurations: loaded.sourceConfigurations, actions: actions(loaded.comparison), notice: notes => <Suspense fallback={null}><ComparisonIntegrityNotice view={loaded} notes={notes} onRefresh={refresh} /></Suspense>, sources: <div className="resource-selection-scroll"><ResponsiveTable aria-label="Comparison source experiments"><thead><tr><th>Experiment</th><th>Created</th></tr></thead><tbody>{loaded.sources.map(source => <tr key={source.id}><td><a href={`#results/instance/${encodeURIComponent(source.id)}`}>{source.name}</a></td><td>{formatDate(source.createdAt)}</td></tr>)}</tbody></ResponsiveTable></div>}} /></> : <>
      <header className="page-header">{detail.route && <DetailBreadcrumb parent="Comparisons" current={selected?.name ?? 'Comparison'} back={detail.close} />}
      <ResourceListHeading title={selected?.name ?? 'Comparisons'} icon="experiments" action={selected ? actions(selected) : <DisabledActionHint disabled={!canCreateComparison} reason={creationHint}><ResourceAction action="create" label="New comparison" disabled={!canCreateComparison} title={canCreateComparison?'New comparison':creationHint} onClick={() => {if(canCreateComparison)setEditing(null)}} /></DisabledActionHint>} /></header>
      {selected?.unavailableSources?.length ? <UnavailableSourcesNotice item={selected} busy={busy} edit={() => setEditing(selected)} remove={() => void remove(selected)} /> : <ErrorFeedback message={error} />}
      {!loading && !detail.route && affected > 0 && <div className="source-alert-banner" role="status"><WarningIcon /><span>{affected === 1 ? '1 comparison includes' : `${affected} comparisons include`} experiments that are no longer available. Open {affected === 1 ? 'it' : 'them'} to review.</span></div>}
      {loading ? <p role="status">Loading comparisons…</p> : !detail.route && <ResourceListTable open={item => detail.open('comparison', item.id)} items={items} editable label="Comparisons" searchText={item => item.name} columns={[{label: 'Name', render: item => <div className="comparison-name-cell"><h3>{item.name}</h3>{!item.snapshotCapturedAt && <LiveBadge />}{item.unavailableSources?.length ? <UnavailableBadge sources={item.unavailableSources} /> : null}</div>}, {label: 'Experiments', render: item => new Set(item.series.flatMap(group => group.runs.map(run => run.experimentId))).size}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('comparison', item.id)} />{actions(item)}</>} empty={<><strong>No comparisons yet</strong><p>Compare results from completed experiments.</p></>} />}
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
      const missing = source ? undefined : item?.unavailableSources?.find(entry => entry.experimentId === selection.experimentId)
      const title = source?.name ?? (missing?.label || 'Unavailable experiment')
      return <tr key={selection.experimentId} className={source ? undefined : 'is-unavailable'}><td>{title}</td><td>{source?.createdAt ? formatDate(source.createdAt) : <span className="comparison-source-badge"><WarningIcon />{missing ? sourceReason(missing) : 'Unavailable'}</span>}</td><td><input type="text" aria-label={`${title} plot label`} placeholder={title} value={selection.label} maxLength={120} onChange={event => setSelected(current => current.map(item => item.experimentId === selection.experimentId ? {...item, label: event.target.value} : item))} /></td><td><input type="color" aria-label={`${title} plot color`} value={selection.color} onChange={event => setSelected(current => current.map(item => item.experimentId === selection.experimentId ? {...item, color: event.target.value} : item))} /></td><td><ResourceAction action="delete" label={`Remove ${title}`} onClick={() => setSelected(current => current.filter(item => item.experimentId !== selection.experimentId))} /></td></tr>
    })}</tbody></ResponsiveTable></div>}
    <div className="dialog-actions"><button type="button" className="button secondary" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending || !name.trim() || selected.length < 2 || selected.some(item => !sources.some(source => source.id === item.experimentId))}>{pending ? 'Saving…' : item ? 'Save changes' : 'Create comparison'}</button></div>
  </form></Dialog>{adding && <ResourcePickerDialog resource="experiments" items={available} selectedIds={selected.map(item => item.experimentId)} session={session} close={() => setAdding(false)} add={add} load={loadCandidates} />}</>
}
