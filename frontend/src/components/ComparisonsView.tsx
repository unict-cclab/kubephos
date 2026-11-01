import {useCallback, useEffect, useState} from 'react'
import type {Artifact, Experiment, Operation, Session, Workspace} from '../types'
import {request} from '../api'
import {useDetailRoute} from '../detailRoute'
import {comparisonColors, runKey, selectableComparisonRuns, type ComparisonRun, type ComparisonSeries, type ComparisonView, type ResultComparison} from '../comparisons'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAction} from './ResourceAction'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {ResultsView} from './LazyResultsView'
import {Dialog} from './Dialog'
import {WizardForm, WizardStep} from './WizardForm'
import {ResponsiveTable} from './ResponsiveTable'
import {DetailBreadcrumb} from './DetailBreadcrumb'

interface Props {
  experiments: Experiment[]; artifacts: Artifact[]; operations: Operation[]; workspaces: Workspace[]; session: Session
  changed: () => Promise<void>; openOperation: (id: string) => void; openPipelineRun?: (id: string) => void
}
export function ComparisonsView(props: Props) {
  const detail = useDetailRoute('comparisons')
  const [items, setItems] = useState<ResultComparison[]>([])
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
      const response = await request<{items: ResultComparison[]}>('/comparisons', {}, undefined, controller.signal)
      if (controller.signal.aborted) return
      setItems(response.items)
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
  const selected = items.find(item => item.id === detail.route?.id)
  const actions = (item: ResultComparison) => <><ResourceAction action="edit" label={`Edit ${item.name}`} disabled={busy} onClick={() => setEditing(item)} /><ResourceAction action="delete" label={`Delete ${item.name}`} disabled={busy} onClick={() => void remove(item)} /></>
  return <>
    {confirmationDialog}
    {loaded ? <ResultsView key={`${loaded.comparison.id}:${loaded.comparison.updatedAt}`} {...props} artifacts={loaded.artifacts} focusedExperiment={loaded.result} onBack={detail.close} comparison={{sourceConfigurations: loaded.sourceConfigurations, actions: actions(loaded.comparison), sources: <details className="comparison-sources"><summary>Sources</summary><ResponsiveTable aria-label="Comparison source runs"><thead><tr><th>Series</th><th>Experiment</th><th>Run</th></tr></thead><tbody>{loaded.comparison.series.flatMap(group => group.runs.map(ref => {
      const source = loaded.sources.find(source => source.id === ref.experimentId)
      const trial = source?.variants.flatMap(variant => variant.trials).find(trial => trial.id === ref.trialId)
      return <tr key={runKey(ref)}><td>{group.label}</td><td><a href={`#results/instance/${encodeURIComponent(ref.experimentId)}`}>{source?.name ?? 'Experiment'}</a></td><td>{trial?.position ?? '—'}</td></tr>
    }))}</tbody></ResponsiveTable></details>}} /> : <>
      {detail.route && <DetailBreadcrumb parent="Comparisons" current={selected?.name ?? 'Comparison'} back={detail.close} />}
      <ResourceListHeading title={selected?.name ?? 'Comparisons'} icon="experiments" action={selected ? actions(selected) : <ResourceAction action="create" label="New comparison" disabled={loading || selectableComparisonRuns(props.experiments).length < 2} onClick={() => setEditing(null)} />} />
      <ErrorFeedback message={error} persistent />
      {loading ? <p role="status">Loading comparisons…</p> : !detail.route && <ResourceListTable items={items} editable label="Comparisons" searchText={item => item.name} columns={[{label: 'Name', render: item => <h3>{item.name}</h3>}, {label: 'Series', render: item => item.series.length}, {label: 'Runs', render: item => item.series.reduce((sum, series) => sum + series.runs.length, 0)}]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('comparison', item.id)} />{actions(item)}</>} empty={<><strong>No comparisons yet</strong><p>Compare results from completed experiments.</p></>} />}
    </>}
    {editing !== undefined && <ComparisonDialog key={editing?.id ?? 'new'} item={editing ?? undefined} experiments={[...props.experiments, ...(loaded?.sources ?? []).filter(source => !props.experiments.some(item => item.id === source.id))]} session={props.session} close={() => setEditing(undefined)} saved={item => {setEditing(undefined); reload(); detail.open('comparison', item.id)}} />}
  </>
}

export function ComparisonDialog({item, experiments, session, close, saved}: {item?: ResultComparison; experiments: Experiment[]; session: Session; close: () => void; saved: (item: ResultComparison) => void}) {
  const runs = selectableComparisonRuns(experiments)
  const [name, setName] = useState(item?.name ?? '')
  const [series, setSeries] = useState<ComparisonSeries[]>(() => item ? structuredClone(item.series) : [])
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const [query, setQuery] = useState('')
  const selected = new Set(series.flatMap(group => group.runs.map(runKey)))
  const count = selected.size
  const toggle = (key: string) => {
    if (selected.has(key)) {setSeries(current => current.map(group => ({...group, runs: group.runs.filter(run => runKey(run) !== key)})).filter(group => group.runs.length)); return}
    if (count >= 80) {setError('Select at most 80 runs.'); return}
    const source = runs.find(run => run.key === key)!
    const ref = {experimentId: source.experiment.id, trialId: source.trial.id}
    const label = source.variant.alias || source.variant.name || source.experiment.name
    const index = series.findIndex(group => group.label === label)
    if (index < 0 && series.length >= 8) {setError('A comparison supports at most 8 series. Assign runs to an existing series.'); return}
    setSeries(current => index < 0 ? [...current, {label, color: source.variant.color || comparisonColors[current.length], runs: [ref]}] : current.map((group, i) => i === index ? {...group, runs: [...group.runs, ref]} : group))
  }
  const move = (ref: ComparisonRun, target: number) => setSeries(current => current.map((group, i) => ({...group, runs: i === target ? [...group.runs.filter(run => runKey(run) !== runKey(ref)), ref] : group.runs.filter(run => runKey(run) !== runKey(ref))})))
  const update = (index: number, patch: Partial<ComparisonSeries>) => setSeries(current => current.map((group, i) => i === index ? {...group, ...patch} : group))
  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault(); if (pending) return
    setError(''); setPending(true)
    try {
      const result = await request<ResultComparison>(item ? `/comparisons/${item.id}` : '/comparisons', {method: item ? 'PUT' : 'POST', body: JSON.stringify({name, series: series.filter(group => group.runs.length)})}, session.csrfToken)
      saved(result)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save the comparison.')}
    finally {setPending(false)}
  }
  return <Dialog open className="comparison-modal" title={item ? 'Edit comparison' : 'New comparison'} onClose={() => {if (!pending) close()}}><WizardForm onSubmit={submit} onCancel={close} pending={pending} submitLabel={item ? 'Save changes' : 'Create comparison'} submitDisabled={count < 2} error={error}>
    <WizardStep title="Runs">
      <div className="form-grid"><label>Name<input required maxLength={120} value={name} onChange={event => setName(event.target.value)} /></label><label>Search runs<input type="search" value={query} onChange={event => setQuery(event.target.value)} /></label></div>
      <div className="comparison-run-picker"><ResponsiveTable aria-label="Available runs"><thead><tr><th>Select</th><th>Experiment</th><th>Configuration</th><th>Run</th></tr></thead><tbody>{runs.filter(run => `${run.experiment.name} ${run.variant.name}`.toLowerCase().includes(query.toLowerCase())).map(run => <tr key={run.key}><td><input type="checkbox" aria-label={`Include ${run.experiment.name} · ${run.variant.name} · Run ${run.trial.position}`} checked={selected.has(run.key)} onChange={() => toggle(run.key)} /></td><td>{run.experiment.name}</td><td>{run.variant.name}</td><td>{run.trial.position}</td></tr>)}</tbody></ResponsiveTable></div>
      {series.flatMap(group => group.runs).filter(ref => !runs.some(run => run.key === runKey(ref))).map(ref => <div key={runKey(ref)} className="validation-item warning">Unavailable run <button type="button" className="text-button" onClick={() => toggle(runKey(ref))}>Remove</button></div>)}
    </WizardStep>
    <WizardStep title="Series" blocked={count < 2}>
      <div className="comparison-series-layout"><div><ResponsiveTable aria-label="Comparison series"><thead><tr><th>Label</th><th>Color</th><th>Runs</th><th>Actions</th></tr></thead><tbody>{series.map((group, i) => <tr key={i}><td><input aria-label={`Series ${i + 1} label`} required maxLength={80} value={group.label} onChange={event => update(i, {label: event.target.value})} /></td><td><input type="color" aria-label={`Series ${i + 1} color`} value={group.color} onChange={event => update(i, {color: event.target.value})} /></td><td>{group.runs.length}</td><td><ResourceAction action="delete" label={`Remove series ${i + 1}`} onClick={() => setSeries(current => current.filter((_, index) => index !== i))} /></td></tr>)}</tbody></ResponsiveTable>
      <div className="form-section-actions"><button type="button" className="button secondary" disabled={series.length >= 8} onClick={() => setSeries(current => [...current, {label: `Series ${current.length + 1}`, color: comparisonColors[current.length], runs: []}])}>Add series</button></div>
      </div><div className="comparison-run-picker"><ResponsiveTable aria-label="Run assignment"><thead><tr><th>Experiment</th><th>Run</th><th>Series</th></tr></thead><tbody>{series.flatMap(group => group.runs.map(ref => {
        const run = runs.find(run => run.key === runKey(ref))
        return <tr key={runKey(ref)}><td>{run?.experiment.name ?? 'Unavailable run'}</td><td>{run?.trial.position ?? '—'}</td><td><select aria-label={`Series for ${run?.experiment.name ?? ref.experimentId} run ${run?.trial.position ?? ref.trialId}`} value={series.indexOf(group)} onChange={event => move(ref, Number(event.target.value))}>{series.map((option, i) => <option key={i} value={i}>{option.label || `Series ${i + 1}`}</option>)}</select></td></tr>
      }))}</tbody></ResponsiveTable></div></div>
    </WizardStep>
    <WizardStep title="Review" blocked={count < 2}>
      <ResponsiveTable aria-label="Comparison review"><thead><tr><th>Name</th><th>Series</th><th>Runs</th></tr></thead><tbody><tr><td>{name}</td><td>{series.filter(group => group.runs.length).length}</td><td>{count}</td></tr></tbody></ResponsiveTable>
      <ResponsiveTable aria-label="Series review"><thead><tr><th>Label</th><th>Color</th><th>Runs</th></tr></thead><tbody>{series.filter(group => group.runs.length).map((group, index) => <tr key={index}><td>{group.label}</td><td><span className="comparison-color" style={{backgroundColor: group.color}} role="img" aria-label={`${group.label} plot color`} /></td><td>{group.runs.length}</td></tr>)}</tbody></ResponsiveTable>
    </WizardStep>
  </WizardForm></Dialog>
}
