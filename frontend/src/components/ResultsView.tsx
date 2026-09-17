import {useEffect, useMemo, useRef, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatBytes, formatDate, isTerminalExperiment} from '../lib'
import type {Artifact, Experiment, ExperimentFigure, ExperimentTrial, MetricPoint, Operation, Session, TimeSeriesDataset, Workspace} from '../types'
import {Dialog} from './Dialog'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {LoadProfilePreview, loadProfileSteps, type LoadProfileStep} from './LoadProfileEditor'

interface Props {
  artifacts: Artifact[]
  experiments: Experiment[]
  operations: Operation[]
  workspaces: Workspace[]
  session: Session
  changed: () => Promise<void>
  openOperation: (id: string) => void
  focusedExperiment?: Experiment
  onBack?: () => void
}

export interface LoadedDataset {
  artifact: Artifact
  dataset: TimeSeriesDataset
  label: string
  color?: string
}

export interface AggregateSeries {
  artifactID: string
  label: string
  unit: string
  points: MetricPoint[]
  sloMilliseconds?: number
  color?: string
}

const colors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#000000']
const dashPatterns = ['', '10 6', '10 4 2 4', '2 5']
const priorityMetrics = ['load.successful_rps', 'load.failure_percentage', 'load.p95_response_time', 'load.slo_violation_percentage', 'deployment.ready_replicas']

type ResultsTab = 'overview' | 'charts' | 'statistics' | 'downloads'

export function ResultsView({artifacts, experiments, operations, workspaces, session, changed, openOperation, focusedExperiment, onBack}: Props) {
  const available = useMemo(() => artifacts.filter(item => !item.sensitive && item.type === 'TimeSeriesDataset' && item.version === 'v1alpha1').sort((left, right) => right.verifiedAt.localeCompare(left.verifiedAt)), [artifacts])
  const focusedArtifactIDs = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(Boolean) ?? []
  const selectable = focusedExperiment ? available.filter(item => focusedArtifactIDs.includes(item.id)) : available
  const focusedKey = focusedArtifactIDs.join('|')
  const availableKey = available.map(item => `${item.id}:${item.digest}`).join('|')
  const knownFocusedIDs = useRef(selectable.map(item => item.id))
  const [selected, setSelected] = useState<string[]>(() => focusedArtifactIDs.filter(id => available.some(item => item.id === id)).slice(0, 20))
  const [loaded, setLoaded] = useState<LoadedDataset[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [activeExperiment, setActiveExperiment] = useState<string | null>(focusedExperiment?.id ?? null)
  const [activeTab, setActiveTab] = useState<ResultsTab>('overview')
  const [runTab, setRunTab] = useState('summary')
  const [printSummaryPending, setPrintSummaryPending] = useState(false)
  const [metricGroup, setMetricGroup] = useState('Load')
  const [initialized, setInitialized] = useState(Boolean(focusedExperiment))
  const [deleting, setDeleting] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [stopRequested, setStopRequested] = useState(false)
  const [actionError, setActionError] = useState('')
  const [figures, setFigures] = useState<ExperimentFigure[]>([])
  const focusedRuns = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => ({variant, trial}))) ?? []
  const activeRun = focusedRuns.find(item => item.trial.id === runTab)
  const liveRun = focusedRuns.find(item => item.trial.status === 'running')
  const viewSelected = activeRun ? activeRun.trial.resultArtifactId && available.some(item => item.id === activeRun.trial.resultArtifactId) ? [activeRun.trial.resultArtifactId] : [] : selected
  const selectedKey = viewSelected.join('|')

  useEffect(() => {setRunTab('summary')}, [focusedExperiment?.id])

  useEffect(() => {
    if (!focusedExperiment || isTerminalExperiment(focusedExperiment.status)) setStopRequested(false)
  }, [focusedExperiment?.id, focusedExperiment?.status])

  useEffect(() => {
    if (!printSummaryPending || activeTab !== 'overview') return
    const frame = requestAnimationFrame(() => {setPrintSummaryPending(false); window.print()})
    return () => cancelAnimationFrame(frame)
  }, [activeTab, printSummaryPending])

  useEffect(() => {
    setSelected(current => current.filter(id => available.some(item => item.id === id)))
  }, [availableKey])

  useEffect(() => {
    knownFocusedIDs.current = selectable.map(item => item.id)
    if (focusedExperiment) setSelected(knownFocusedIDs.current.slice(0, 20))
  }, [focusedExperiment?.id])

  useEffect(() => {
    if (!focusedExperiment) return
    const currentIDs = selectable.map(item => item.id)
    const added = currentIDs.filter(id => !knownFocusedIDs.current.includes(id))
    knownFocusedIDs.current = currentIDs
    if (added.length) setSelected(current => [...new Set([...current, ...added])].slice(0, 20))
  }, [availableKey, focusedKey, focusedExperiment?.id])

  useEffect(() => {
    if (!initialized && available.length) {
      setSelected([available[0].id])
      setInitialized(true)
    }
  }, [availableKey, initialized])

  useEffect(() => {
    if (!viewSelected.length) {
      setLoaded([])
      setError('')
      return
    }
    const controller = new AbortController()
    setLoading(true)
    setError('')
    Promise.all(viewSelected.map(async id => {
      const artifact = available.find(item => item.id === id)
      if (!artifact) throw new Error('A selected run is no longer available.')
      const dataset = await request<TimeSeriesDataset>(`/artifacts/${id}/download`, {}, undefined, controller.signal)
      addDerivedMetrics(dataset, responseTimeSLO(focusedExperiment, id))
      validateDataset(dataset)
      return {artifact, dataset, label: ''}
    })).then(values => setLoaded(values)).catch(cause => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the selected runs.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [availableKey, selectedKey])

  useEffect(() => {
    if (!focusedExperiment) {
      setFigures([])
      return
    }
    const controller = new AbortController()
    request<{items: ExperimentFigure[]}>(`/experiments/${focusedExperiment.id}/figures`, {}, undefined, controller.signal)
      .then(result => setFigures(result.items))
      .catch(cause => { if (!controller.signal.aborted) setActionError(cause instanceof Error ? cause.message : 'Could not load saved figures.') })
    return () => controller.abort()
  }, [focusedExperiment?.id])

  const toggle = (id: string) => {
    if (!focusedExperiment) setActiveExperiment(null)
    setActiveTab('overview')
    setNotice('')
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : current.length < (focusedExperiment ? 20 : 8) ? [...current, id] : current)
  }
  const experiment = focusedExperiment ?? experiments.find(item => item.id === activeExperiment)
  const experimentPresentation = new Map<string, {label: string; color?: string}>()
  for (const variant of experiment?.variants ?? []) for (const trial of variant.trials) if (trial.resultArtifactId) experimentPresentation.set(trial.resultArtifactId, {label: `${variant.alias || variant.name} · Run ${trial.position}`, color: variant.color})
  const displayed = loaded.map(item => ({...item, label: experimentPresentation.get(item.artifact.id)?.label ?? datasetLabel(item.artifact, operations, workspaces), color: experimentPresentation.get(item.artifact.id)?.color}))
  const metrics = [...new Set(displayed.flatMap(item => item.dataset.spec.series.map(series => series.metric)))].filter(metric => !['load.mean_response_time', 'load.p50_response_time', 'load.p99_response_time'].includes(metric)).sort()
  const groups = groupMetrics(metrics)
  const activeMetricGroup = groups.has(metricGroup) ? metricGroup : groups.keys().next().value ?? ''
  const visibleMetrics = groups.get(activeMetricGroup) ?? []
  const preferredMetrics = priorityMetrics.filter(metric => metrics.includes(metric))
  const overviewMetrics = [...preferredMetrics, ...metrics.filter(metric => !preferredMetrics.includes(metric))].slice(0, 5)
  const visibleFigures = activeRun ? figures.filter(figure => figure.sourceArtifactIds.includes(activeRun.trial.resultArtifactId)) : figures
  const samples = displayed.reduce((total, item) => total + item.dataset.spec.summary.samples, 0)
  const selectedWorkspaceID = available.find(item => item.id === selected[0])?.workspaceId
  const saveFigure = async (metric: string, format: 'pdf' | 'png', value: Uint8Array, sourceIDs: string[]) => {
    if (!focusedExperiment) return
    const saved = await request<ExperimentFigure>(`/experiments/${focusedExperiment.id}/figures?metric=${encodeURIComponent(metric)}&format=${format}`, {
      method: 'POST',
      headers: {'Content-Type': format === 'pdf' ? 'application/pdf' : 'image/png', 'X-KubePhos-Source-Artifacts': sourceIDs.join(',')},
      body: new Blob([value.buffer as ArrayBuffer])
    }, session.csrfToken)
    setFigures(items => [saved, ...items])
  }
  const openExperiment = (item: Experiment) => {
    const artifactIDs = item.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(id => available.some(artifact => artifact.id === id))
    setActiveExperiment(item.id)
    setActiveTab('overview')
    setSelected(artifactIDs.slice(0, 20))
    setNotice(artifactIDs.length > 20 ? 'Showing the first twenty runs.' : artifactIDs.length === 0 && item.status === 'succeeded' ? 'This experiment has no plottable metrics.' : '')
  }
  const removeExperiment = async () => {
    const label = experiment?.kind === 'suite' ? 'suite' : 'experiment'
    if (!experiment || !isTerminalExperiment(experiment.status) || !window.confirm(`Delete ${label} ${experiment.name}? Runs, operations, logs and collected artifacts will be retained.`)) return
    setDeleting(true)
    setActionError('')
    try {
      await request(`/experiments/${experiment.id}`, {method: 'DELETE'}, session.csrfToken)
      await changed()
      if (onBack) onBack()
      else {
        setActiveExperiment(null)
        setSelected([])
      }
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : `Could not delete the ${label}.`)
    } finally {
      setDeleting(false)
    }
  }
  const stopExperiment = async () => {
    if (!focusedExperiment || !['queued', 'running'].includes(focusedExperiment.status)) return
    const label = focusedExperiment.kind === 'suite' ? 'suite' : 'experiment'
    if (!window.confirm(`Stop ${label} ${focusedExperiment.name}? The active run will be interrupted safely and all remaining runs will be canceled.`)) return
    setStopping(true)
    setActionError('')
    try {
      await request(`/experiments/${focusedExperiment.id}/cancel`, {method: 'POST', body: '{}'}, session.csrfToken)
      setStopRequested(true)
      await changed()
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : `Could not stop the ${label}.`)
    } finally {
      setStopping(false)
    }
  }
  const canStop = Boolean(focusedExperiment && ['queued', 'running'].includes(focusedExperiment.status))
  const stopLabel = focusedExperiment?.kind === 'suite' ? 'suite' : 'experiment'
  return <>
    {focusedExperiment && onBack && <DetailBreadcrumb parent="Experiments" current={focusedExperiment.name} back={onBack} />}
    <div className={`results-heading ${focusedExperiment ? 'focused' : ''}`}><div><h2>{focusedExperiment?.name ?? 'Results'}</h2>{focusedExperiment?.description && <p>{focusedExperiment.description}</p>}</div><div className="results-detail-actions">{focusedExperiment && <span className={`status ${focusedExperiment.status}`}>{focusedExperiment.status}</span>}{focusedExperiment && session.user?.role === 'admin' && canStop && <button className="button danger" disabled={stopping || stopRequested} onClick={stopExperiment}>{stopping || stopRequested ? `Stopping ${stopLabel}…` : `Stop ${stopLabel}`}</button>}{focusedExperiment && session.user?.role === 'admin' && <button className="button danger" disabled={deleting || !isTerminalExperiment(focusedExperiment.status)} title={isTerminalExperiment(focusedExperiment.status) ? '' : `Stop this ${stopLabel} before deleting it`} onClick={removeExperiment}>{deleting ? 'Deleting…' : `Delete ${stopLabel}`}</button>}{!focusedExperiment && <div className="results-count" aria-label={`${selectable.length} datasets`}><strong>{selectable.length}</strong><span>datasets</span></div>}</div></div>
    {actionError && <div className="validation-item error">{actionError}</div>}
    {!focusedExperiment && experiments.length > 0 && <div className="experiment-history"><div className="experiment-history-title"><span><b>1</b><strong>Choose an experiment</strong></span><span>{experiments.length} saved</span></div><div className="experiment-cards">{experiments.map(item => <button key={item.id} className={activeExperiment === item.id ? 'active' : ''} onClick={() => openExperiment(item)}><span className={`status-dot ${item.status}`} /><span><strong>{item.name}</strong><small>{item.status === 'queued' && item.scheduledFor && new Date(item.scheduledFor).getTime() > Date.now() ? `Scheduled ${formatDate(item.scheduledFor)}` : `${item.status} · ${item.variants.length} strategies · ${item.variants.reduce((total, variant) => total + variant.trials.length, 0)} runs`}</small></span><b>›</b></button>)}</div></div>}
    {focusedExperiment && <nav className="experiment-run-tabs" aria-label="Experiment runs"><button className="run-tab summary" aria-current={runTab === 'summary' ? 'page' : undefined} data-active={runTab === 'summary'} onClick={() => {setRunTab('summary'); setActiveTab('overview')}}><span className="run-tab-label">Run summary</span></button>{focusedRuns.map(({variant, trial}) => <button key={trial.id} className={`run-tab ${trial.status}`} aria-current={runTab === trial.id ? 'page' : undefined} data-active={runTab === trial.id} onClick={() => {setRunTab(trial.id); setActiveTab('overview')}}><span className={`status-dot ${trial.status}`} /><span className="run-tab-label">{variant.name} · Run {trial.position}</span></button>)}</nav>}
    {experiment && (!activeRun ? <>{experiment.status !== 'succeeded' && <ExperimentProgress experiment={experiment} />}{liveRun && <RunLoadProgress name={`${liveRun.variant.name} · Run ${liveRun.trial.position}`} trial={liveRun.trial} configuration={liveRun.variant.configuration} operation={operations.find(item => item.id === liveRun.trial.operationId)} />}</> : <><RunDiagnostics status={activeRun.trial.status} error={activeRun.trial.error} operationId={activeRun.trial.operationId} openOperation={openOperation} /><RunLoadProgress trial={activeRun.trial} configuration={activeRun.variant.configuration} operation={operations.find(item => item.id === activeRun.trial.operationId)} /></>)}
    {!selectable.length || activeRun && !viewSelected.length ? <EmptyResults experiment={experiment} runStatus={activeRun?.trial.status} /> : <div className={`results-layout ${focusedExperiment ? 'focused' : ''}`}>
      {focusedExperiment && !activeRun && selectable.length > 1 ? <details className="results-run-filter"><summary><span><strong>Included runs</strong><small>{selected.length} of {selectable.length}</small></span><b>Change</b></summary><div className="results-run-filter-body"><button type="button" className="text-button" onClick={() => setSelected(selectable.map(item => item.id).slice(0, 20))}>Select all</button><div className="results-run-filter-options">{selectable.map(artifact => <button type="button" className={selected.includes(artifact.id) ? 'active' : ''} aria-pressed={selected.includes(artifact.id)} key={artifact.id} onClick={() => toggle(artifact.id)}><span className="dataset-check">{selected.includes(artifact.id) ? '✓' : ''}</span><strong>{experimentPresentation.get(artifact.id)?.label ?? artifact.name}</strong></button>)}</div></div></details> : !focusedExperiment ? <aside className="dataset-picker" aria-label="Available datasets">
        <div className="dataset-picker-title"><strong>{focusedExperiment ? 'Runs' : 'Select runs'}</strong><span>{selected.length} selected</span></div>
        {selectable.map(artifact => {
          const operation = operations.find(item => item.id === artifact.operationId)
          const workspace = workspaces.find(item => item.id === artifact.workspaceId)
          const active = selected.includes(artifact.id)
          const incompatibleWorkspace = Boolean(selectedWorkspaceID && artifact.workspaceId !== selectedWorkspaceID)
          return <button className={`dataset-option ${active ? 'active' : ''}`} key={artifact.id} onClick={() => toggle(artifact.id)} disabled={!active && (selected.length >= 8 || incompatibleWorkspace)} aria-pressed={active}>
            <span className="dataset-check">{active ? '✓' : ''}</span><span><strong>{operation?.title ?? artifact.name}</strong><small>{workspace?.name ?? 'Workspace'} · {formatDate(artifact.verifiedAt)}</small><small>{formatBytes(artifact.sizeBytes)} · verified</small></span>
          </button>
        })}
      </aside> : null}
      <div className="results-content">
        {notice && <div className="validation-item warning">{notice}</div>}
        {!viewSelected.length && <div className="results-placeholder"><strong>Select runs</strong><p>Choose which completed runs to include in the summary.</p></div>}
        {loading && <div className="results-placeholder"><span className="results-spinner" /><strong>Loading verified data…</strong></div>}
        {error && <div className="validation-item error">{error}</div>}
        {!loading && !error && loaded.length > 0 && <>
          {!focusedExperiment && <div className="results-workbench-head">
            <div><h3>{activeRun ? `${activeRun.variant.name} · Run ${activeRun.trial.position}` : focusedExperiment ? 'Aggregated results' : experiment?.name ?? displayed[0].label}</h3><p>{displayed.length} run{displayed.length === 1 ? '' : 's'} · {metrics.length} plot{metrics.length === 1 ? '' : 's'}</p></div>
            <div className="results-heading-actions"><button className="button secondary" onClick={() => setActiveTab('downloads')}>Export</button>{!focusedExperiment && <button className="button primary" disabled={selected.length < 2} onClick={() => setSaving(true)}>Save comparison</button>}</div>
          </div>}
          <nav className="results-tabs" aria-label="Result sections">{(['overview', 'charts', 'statistics', 'downloads'] as ResultsTab[]).map(tab => <button key={tab} className={activeTab === tab ? 'active' : ''} onClick={() => setActiveTab(tab)}>{({overview: 'Summary', charts: 'Plots', statistics: 'Statistics', downloads: 'Export'} as Record<ResultsTab, string>)[tab]}</button>)}</nav>
          {activeTab === 'overview' && <>
            {!focusedExperiment && <div className="results-stats">
              <ResultStat label="Runs" value={displayed.length} />
              <ResultStat label="Duration" value={formatDuration(Math.max(...displayed.map(item => new Date(item.dataset.spec.end).getTime() - new Date(item.dataset.spec.start).getTime())))} />
              <ResultStat label="Samples" value={samples.toLocaleString()} />
              <ResultStat label="Metrics" value={metrics.length} />
            </div>}
            <section className="result-summary"><div className="result-section-heading"><div><h3>Key outcomes</h3></div></div><div className="key-metrics">{overviewMetrics.map(metric => <KeyMetric key={metric} metric={metric} series={aggregateMetric(displayed, metric)} />)}</div></section>
          </>}
          {activeTab === 'charts' && <><div className="metric-groups">{[...groups.keys()].map(group => <button key={group} className={activeMetricGroup === group ? 'active' : ''} onClick={() => setMetricGroup(group)}>{group}<span>{groups.get(group)?.length}</span></button>)}</div><div className="chart-grid">{visibleMetrics.map(metric => <MetricChart key={metric} metric={metric} series={aggregateMetric(displayed, metric)} onSave={focusedExperiment ? saveFigure : undefined} />)}</div></>}
          {activeTab === 'statistics' && <StatisticsTable datasets={displayed} metrics={metrics} experiment={activeRun ? undefined : experiment} />}
          {activeTab === 'downloads' && <section className="results-download-panel">
            <div><h3>Export results</h3><p>Download each plot as PNG or PDF from the Plots tab. Statistics and complete run data are here.</p></div>
            <div className="results-export-actions"><button className="button secondary" onClick={() => exportStatisticsCSV(displayed, metrics, activeRun ? undefined : experiment)}>Statistics CSV</button><button className="button secondary" onClick={() => {setPrintSummaryPending(true); setActiveTab('overview')}}>Print summary</button></div>
            {visibleFigures.length > 0 && <div className="saved-figures"><strong>Saved figures</strong><div className="dataset-downloads">{visibleFigures.map(figure => <a key={figure.id} href={`/api/v1/figures/${figure.id}/download`}><span>↓</span><span><strong>{metricTitle(figure.metric)} · {figure.format.toUpperCase()}</strong><small>{figure.sourceArtifactIds.length} runs · {formatDate(figure.createdAt)}</small></span></a>)}</div></div>}
            <div className="results-raw-data"><strong>Complete run data</strong><div className="dataset-downloads">{displayed.map(item => <a key={item.artifact.id} href={`/api/v1/artifacts/${item.artifact.id}/download`}><span>↓</span><span><strong>{item.label}</strong><small>Metric samples</small></span></a>)}</div></div>
          </section>}
        </>}
      </div>
    </div>}
    <SaveExperimentDialog open={saving} close={() => setSaving(false)} selected={selected.map(id => available.find(item => item.id === id)).filter((item): item is Artifact => Boolean(item))} operations={operations} workspaces={workspaces} session={session} changed={changed} />
  </>
}

function ExperimentProgress({experiment}: {experiment: Experiment}) {
  const progress = experimentProgress(experiment)
  const trials = experiment.variants.flatMap(variant => variant.trials.map(trial => ({variant: variant.name, trial})))
  const running = trials.find(item => item.trial.status === 'running')
  const failed = trials.filter(item => ['failed', 'canceled'].includes(item.trial.status)).length
  const detail = running ? `${running.variant} · Run ${running.trial.position} in progress` : failed ? `${failed} run${failed === 1 ? '' : 's'} did not complete` : progress.completed === progress.total ? 'All runs completed' : 'Waiting for the next run'
  return <article className="experiment-progress-card">
    <div><strong>{progress.completed} of {progress.total} runs finished</strong><small>{detail}</small></div>
    <div className="experiment-progress" aria-label={`${progress.completed} of ${progress.total} runs finished`}><span style={{width: `${progress.total ? progress.completed / progress.total * 100 : 0}%`}} /></div>
  </article>
}

function RunDiagnostics({status, error, operationId, openOperation}: {status: string; error?: string; operationId: string; openOperation: (id: string) => void}) {
  const running = ['running', 'starting', 'prechecking', 'verifying'].includes(status)
  const failed = ['failed', 'canceled'].includes(status)
  if (!error && !running && !failed) return null
  return <div className={`run-diagnostics ${failed ? 'failed' : ''}`}><div><strong>{failed ? 'Run needs attention' : 'Run in progress'}</strong>{error && <p>{error}</p>}</div>{operationId && <button className="button secondary compact" onClick={() => openOperation(operationId)}>{running ? 'Live logs' : 'Error logs'}</button>}</div>
}

function RunLoadProgress({trial, configuration, operation, name}: {trial: ExperimentTrial; configuration: Record<string, unknown>; operation?: Operation; name?: string}) {
  const steps = loadStepsFromConfiguration(configuration)
  const duration = steps.reduce((total, step) => total + step.durationSeconds, 0)
  const [now, setNow] = useState(Date.now())
  const loadOperation = operation?.pluginId === 'io.kubephos.load.kubernetes.session'
  const running = trial.status === 'running'
  const active = running && loadOperation && operation?.status === 'running'
  useEffect(() => {
    if (!running) return
    const timer = window.setInterval(() => setNow(Date.now()), 1000)
    return () => window.clearInterval(timer)
  }, [running])
  if (!steps.length) return null
  let progress = 0
  let label = trial.status === 'queued' ? 'Waiting to start' : 'Preparing the run'
  if (active && operation) {
    progress = Math.floor(Math.min(duration, Math.max(0, (now - new Date(operation.createdAt).getTime()) / 1000)))
    label = 'Load running'
  } else if (trial.status === 'succeeded') {
    progress = duration
    label = 'Load completed'
  } else if (loadOperation && operation && operation.status === 'succeeded') {
    progress = duration
    label = 'Load completed'
  } else if (loadOperation && operation && ['failed', 'canceled'].includes(operation.status)) {
    progress = Math.floor(Math.min(duration, Math.max(0, ((trial.completedAt ? new Date(trial.completedAt).getTime() : now) - new Date(operation.createdAt).getTime()) / 1000)))
    label = 'Load stopped'
  } else if (loadOperation) {
    label = 'Load starting'
  } else if (operation?.pluginId === 'io.kubephos.metrics.prometheus.collect') {
    progress = duration
    label = 'Load completed · processing results'
  }
  const runElapsed = trial.startedAt ? Math.max(0, (Math.min(trial.completedAt ? new Date(trial.completedAt).getTime() : now, now) - new Date(trial.startedAt).getTime())) : 0
  return <section className="run-load-progress"><div className="run-load-progress-head"><span className={`status-dot ${trial.status}`} /><div><strong>{name ?? 'Input load progress'}</strong><small>{label}{trial.startedAt ? ` · run elapsed ${formatDuration(runElapsed)}` : ''}</small></div></div><LoadProfilePreview steps={steps} progressSeconds={progress} progressLabel={label} /></section>
}

function loadStepsFromConfiguration(configuration: Record<string, unknown> | undefined): LoadProfileStep[] {
  const components = Array.isArray(configuration?.components) ? configuration.components : []
  for (const value of components) {
    if (!value || typeof value !== 'object') continue
    const component = value as Record<string, unknown>
    if (!String(component.capability ?? '').startsWith('load.')) continue
    const settings = component.configuration
    if (settings && typeof settings === 'object') return loadProfileSteps((settings as Record<string, unknown>).steps)
  }
  return []
}

function responseTimeSLO(experiment: Experiment | undefined, artifactID: string): number {
  const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === artifactID))
  const components = Array.isArray(variant?.configuration?.components) ? variant.configuration.components : []
  for (const value of components) {
    if (!value || typeof value !== 'object') continue
    const component = value as Record<string, unknown>
    if (!String(component.capability ?? '').startsWith('metrics.')) continue
    const settings = component.configuration
    const objective = settings && typeof settings === 'object' ? Number((settings as Record<string, unknown>).sloMilliseconds) : 0
    if (Number.isFinite(objective) && objective > 0) return objective
  }
  return 250
}

function addDerivedMetrics(dataset: TimeSeriesDataset, sloMilliseconds: number) {
  dataset.spec.sloMilliseconds ||= sloMilliseconds
  if (dataset.spec.series.some(series => series.metric === 'load.slo_violation_percentage')) return
  const response = dataset.spec.series.find(series => series.metric === 'load.p95_response_time')
  if (!response) return
  dataset.spec.series.push({metric: 'load.slo_violation_percentage', unit: 'percent', aggregation: 'mean', labels: {objective: `${sloMilliseconds}ms`}, points: response.points.map(point => ({timestamp: point.timestamp, value: point.value > sloMilliseconds ? 100 : 0}))})
}

export function experimentProgress(experiment: Experiment): {completed: number; total: number} {
  const trials = experiment.variants.flatMap(variant => variant.trials)
  return {completed: trials.filter(trial => ['succeeded', 'failed', 'canceled'].includes(trial.status)).length, total: trials.length}
}

function SaveExperimentDialog({open, close, selected, operations, workspaces, session, changed}: {open: boolean; close: () => void; selected: Artifact[]; operations: Operation[]; workspaces: Workspace[]; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const workspace = workspaces.find(item => item.id === selected[0]?.workspaceId)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!workspace) return
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    const grouped = new Map<string, {name: string; trialArtifactIds: string[]}>()
    for (const artifact of selected) {
      const name = String(form.get(`variant-${artifact.id}`) ?? '').trim()
      const key = name.toLocaleLowerCase()
      const variant = grouped.get(key) ?? {name, trialArtifactIds: []}
      variant.trialArtifactIds.push(artifact.id)
      grouped.set(key, variant)
    }
    if ([...grouped.values()].some(item => !item.name) || grouped.size < 2) {
      setError('Provide at least two distinct variant names.')
      setPending(false)
      return
    }
    try {
      await request('/experiments', {method: 'POST', body: JSON.stringify({workspaceId: workspace.id, name: form.get('name'), description: form.get('description'), variants: [...grouped.values()].map(item => ({...item, configuration: {}}))})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save the experiment.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Save experiment" eyebrow="IMMUTABLE HISTORY">
    <form onSubmit={submit}>
      <label>Name<input name="name" maxLength={120} placeholder="Default vs custom strategy" required autoFocus /></label>
      <label>Description<textarea name="description" maxLength={500} rows={2} placeholder="What does this comparison test?" /></label>
      <div className="trial-mapping"><p className="field-description">Use the same variant name on multiple datasets to group repeated trials.</p>{selected.map(artifact => <label key={artifact.id}><span>{operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name}</span><input name={`variant-${artifact.id}`} maxLength={80} defaultValue={operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name} required /></label>)}</div>
      <div className="validation-callout"><span>✓</span><p><strong>Validated before saving.</strong> Every trial must be verified, successful and owned by {workspace?.name ?? 'one workspace'}.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Saving…' : 'Save experiment'}</button></div>
    </form>
  </Dialog>
}

function groupMetrics(metrics: string[]): Map<string, string[]> {
  const values = new Map<string, string[]>()
  for (const metric of metrics) {
    const group = metric.startsWith('load.') ? 'Load' : metric.startsWith('application.') ? 'Application' : metric.startsWith('network.') ? 'Network' : ['cpu', 'memory'].includes(metric.split('.')[0]) ? 'Resources' : 'Kubernetes'
    values.set(group, [...(values.get(group) ?? []), metric])
  }
  return new Map(['Load', 'Application', 'Kubernetes', 'Resources', 'Network'].flatMap(group => values.has(group) ? [[group, values.get(group)!] as [string, string[]]] : []))
}

function KeyMetric({metric, series}: {metric: string; series: AggregateSeries[]}) {
  const values = series.map(item => {
    const summary = summarize(item.points.map(point => point.value))
    return {label: item.label, value: summary.mean, peak: summary.max, last: item.points.at(-1)?.value ?? 0, unit: item.unit}
  })
  const first = values[0]
  const low = Math.min(...values.map(item => item.value))
  const high = Math.max(...values.map(item => item.value))
  const slo = series.find(item => item.sloMilliseconds)?.sloMilliseconds
  const detail = !first ? '' : series.length > 1 ? `Mean range across ${series.length} runs` : metric === 'deployment.ready_replicas' ? `Peak ${formatMetric(first.peak, first.unit)} · final ${formatMetric(first.last, first.unit)}` : metric === 'load.slo_violation_percentage' ? `Windows above${slo ? ` the ${formatMetric(slo, 'milliseconds')}` : ''} SLO` : metric === 'load.p95_response_time' ? `Mean P95 per window${slo ? ` · SLO ${formatMetric(slo, 'milliseconds')}` : ''}` : 'Mean during the run'
  return <article><span>{metricTitle(metric)}</span><strong>{first ? series.length === 1 ? formatMetric(first.value, first.unit) : `${formatMetric(low, first.unit)}–${formatMetric(high, first.unit)}` : '—'}</strong><small>{detail}</small></article>
}

export function niceAxis(maximum: number, integer = false): {upper: number; ticks: number[]} {
  if (!Number.isFinite(maximum) || maximum <= 0) maximum = 1
  const rawStep = maximum / 5
  const magnitude = 10 ** Math.floor(Math.log10(rawStep))
  const normalized = rawStep / magnitude
  const candidates = [1, 1.25, 1.5, 2, 2.5, 3, 4, 5, 6, 8, 10]
  let step = (candidates.find(candidate => candidate >= normalized * .98) ?? 10) * magnitude
  if (integer) step = Math.max(1, Math.ceil(step))
  const ticks = Array.from({length: 6}, (_, index) => index * step)
  let upper = Math.max(ticks[5], maximum)
  if (Math.abs(upper - maximum) <= maximum * .001) upper += step * .25
  return {upper, ticks}
}

function MetricChart({metric, series, onSave}: {metric: string; series: AggregateSeries[]; onSave?: (metric: string, format: 'pdf' | 'png', value: Uint8Array, sources: string[]) => Promise<void>}) {
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState('')
  const width = 700
  const height = 525
  const multiple = series.length > 1
  const padding = {top: 28, right: 28, bottom: multiple ? 142 : 82, left: 112}
  const allPoints = series.flatMap(item => item.points)
  const timestamps = allPoints.map(point => new Date(point.timestamp).getTime())
  const values = allPoints.map(point => point.value)
  const maxX = Math.max(0, ...timestamps) / 60000
  const sloMilliseconds = metric === 'load.p95_response_time' ? series.find(item => item.sloMilliseconds)?.sloMilliseconds : undefined
  const maxY = Math.max(0, ...values, sloMilliseconds ?? 0)
  const unit = series[0]?.unit ?? ''
  const xAxis = niceAxis(maxX)
  const yAxis = niceAxis(maxY, unit === 'replicas' || unit === 'count')
  const plotBottom = height - padding.bottom
  const plotRight = width - padding.right
  const x = (value: number) => padding.left + (value / xAxis.upper) * (plotRight - padding.left)
  const y = (value: number) => plotBottom - (value / yAxis.upper) * (plotBottom - padding.top)
  const chartID = `chart-${metric.replaceAll(/[^a-zA-Z0-9]/g, '-')}`
  const save = async (format: 'pdf' | 'png') => {
    if (!onSave) return
    setSaving(true)
    setSaveError('')
    try {
      await exportChart(chartID, metric, format, value => onSave(metric, format, value, series.map(item => item.artifactID)))
    } catch (cause) {
      setSaveError(cause instanceof Error ? cause.message : 'Could not save the figure.')
    } finally {
      setSaving(false)
    }
  }
  return <article className="metric-card">
    <div className="metric-card-header"><div><h3>{metricTitle(metric)}</h3></div><div className="chart-export-actions"><button type="button" onClick={() => exportChart(chartID, metric, 'pdf')}>PDF</button><button type="button" onClick={() => exportChart(chartID, metric, 'png')}>PNG 600 DPI</button>{onSave && <details className="chart-save-menu"><summary>{saving ? 'Saving…' : 'Save figure'}</summary><div><button type="button" disabled={saving} onClick={() => save('png')}>Save PNG</button><button type="button" disabled={saving} onClick={() => save('pdf')}>Save PDF</button></div></details>}</div></div>
    {saveError && <p className="form-error">{saveError}</p>}
    <div className="chart-wrap"><svg id={chartID} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={`${metricTitle(metric)} comparison chart`}>
      <style>{`text{fill:#111;font-family:Arial,Helvetica,"DejaVu Sans",sans-serif}.tick{font-size:17px}.axis-label{font-size:20px}.legend-label{font-size:15px}.slo-label{fill:#b42318}.axis{stroke:#111;stroke-width:2;fill:none}.tick-line{stroke:#111;stroke-width:1.5}`}</style>
      <rect x={padding.left} y={padding.top} width={plotRight - padding.left} height={plotBottom - padding.top} className="axis" />
      {xAxis.ticks.map(tick => <g key={`x-${tick}`}><line x1={x(tick)} y1={plotBottom} x2={x(tick)} y2={plotBottom + 7} className="tick-line" /><text x={x(tick)} y={plotBottom + 28} textAnchor="middle" className="tick">{formatAxis(tick)}</text></g>)}
      {yAxis.ticks.map(tick => <g key={`y-${tick}`}><line x1={padding.left - 7} y1={y(tick)} x2={padding.left} y2={y(tick)} className="tick-line" /><text x={padding.left - 13} y={y(tick) + 6} textAnchor="end" className="tick">{formatAxisValue(tick, unit)}</text></g>)}
      {sloMilliseconds !== undefined && <g><line x1={padding.left} x2={plotRight} y1={y(sloMilliseconds)} y2={y(sloMilliseconds)} stroke="#b42318" strokeWidth="2" strokeDasharray="8 6" /><text x={plotRight - 5} y={y(sloMilliseconds) - 8} textAnchor="end" className="legend-label slo-label">SLO {formatMetric(sloMilliseconds, 'milliseconds')}</text></g>}
      <text x={(padding.left + plotRight) / 2} y={plotBottom + 58} textAnchor="middle" className="axis-label">Time (min)</text>
      <text x="27" y={(padding.top + plotBottom) / 2} textAnchor="middle" className="axis-label" transform={`rotate(-90 27 ${(padding.top + plotBottom) / 2})`}>{metricAxisLabel(metric, unit)}</text>
      {series.map((item, index) => { const color = item.color ?? colors[index % colors.length]; return <g key={item.artifactID}><path d={paperChartPath(item.points, x, y)} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={dashPatterns[Math.floor(index / colors.length) % dashPatterns.length]} strokeLinejoin="round" />{item.points.filter((_, pointIndex) => pointIndex % Math.max(1, Math.floor(item.points.length / 12)) === 0).map((point, pointIndex) => <ChartMarker key={pointIndex} shape={index % 7} x={x(new Date(point.timestamp).getTime() / 60000)} y={y(point.value)} color={color} />)}</g>})}
      {multiple && series.map((item, index) => {
        const column = index % 2
        const row = Math.floor(index / 2)
        const legendX = padding.left + column * 280
        const legendY = plotBottom + 86 + row * 25
        const color = item.color ?? colors[index % colors.length]
        return <g key={`legend-${item.artifactID}`}><line x1={legendX} y1={legendY} x2={legendX + 35} y2={legendY} stroke={color} strokeWidth="2.5" strokeDasharray={dashPatterns[Math.floor(index / colors.length) % dashPatterns.length]} /><ChartMarker shape={index % 7} x={legendX + 17} y={legendY} color={color} /><text x={legendX + 45} y={legendY + 5} className="legend-label">{compactLabel(item.label)}</text></g>
      })}
    </svg></div>
    <div className="chart-legend">{series.map((item, index) => {
      const values = item.points.map(point => point.value)
      const mean = values.reduce((sum, value) => sum + value, 0) / Math.max(1, values.length)
      return <div key={item.artifactID}><span style={{background: item.color ?? colors[index % colors.length]}} /><strong>{item.label}</strong><small>mean {formatMetric(mean, item.unit)} · last {formatMetric(values.at(-1) ?? 0, item.unit)}</small></div>
    })}</div>
  </article>
}

function ChartMarker({shape, x, y, color}: {shape: number; x: number; y: number; color: string}) {
  if (shape === 0) return <circle cx={x} cy={y} r="4" fill={color} />
  if (shape === 1) return <rect x={x - 4} y={y - 4} width="8" height="8" fill={color} />
  if (shape === 2) return <polygon points={`${x},${y - 5} ${x - 5},${y + 4} ${x + 5},${y + 4}`} fill={color} />
  if (shape === 3) return <polygon points={`${x},${y - 5} ${x - 5},${y} ${x},${y + 5} ${x + 5},${y}`} fill={color} />
  if (shape === 4) return <polygon points={`${x - 5},${y - 4} ${x + 5},${y - 4} ${x},${y + 5}`} fill={color} />
  if (shape === 5) return <path d={`M${x - 5},${y}H${x + 5}M${x},${y - 5}V${y + 5}`} stroke={color} strokeWidth="3" />
  return <path d={`M${x - 4},${y - 4}L${x + 4},${y + 4}M${x + 4},${y - 4}L${x - 4},${y + 4}`} stroke={color} strokeWidth="3" />
}

export function aggregateMetric(datasets: LoadedDataset[], metric: string): AggregateSeries[] {
  return datasets.map(item => {
    const matching = item.dataset.spec.series.filter(series => series.metric === metric)
    const values = new Map<number, number>()
    const counts = new Map<number, number>()
    for (const series of matching) {
      for (const point of series.points) {
        const timestamp = new Date(point.timestamp).getTime()
        values.set(timestamp, (values.get(timestamp) ?? 0) + point.value)
        counts.set(timestamp, (counts.get(timestamp) ?? 0) + 1)
      }
    }
    const aggregation = matching[0]?.aggregation ?? 'sum'
    const startedAt = new Date(item.dataset.spec.start).getTime()
    return {
      artifactID: item.artifact.id,
      label: item.label,
      unit: matching[0]?.unit ?? '',
      sloMilliseconds: item.dataset.spec.sloMilliseconds,
      color: item.color,
      points: [...values.entries()].sort((left, right) => left[0] - right[0]).map(([timestamp, value]) => ({timestamp: new Date(Math.max(0, timestamp - startedAt)).toISOString(), value: aggregation === 'mean' ? value / (counts.get(timestamp) ?? 1) : value}))
    }
  }).filter(item => item.points.length > 0)
}

export interface ScientificSummary {
  count: number
  mean: number
  standardDeviation: number
  min: number
  max: number
  median: number
  p05: number
  p25: number
  p75: number
  p95: number
  p99: number
  coefficientOfVariation: number
  confidence95Low: number
  confidence95High: number
}

export function summarize(values: number[]): ScientificSummary {
  const sorted = values.filter(Number.isFinite).sort((left, right) => left - right)
  const count = sorted.length
  if (!count) return {count: 0, mean: 0, standardDeviation: 0, min: 0, max: 0, median: 0, p05: 0, p25: 0, p75: 0, p95: 0, p99: 0, coefficientOfVariation: 0, confidence95Low: 0, confidence95High: 0}
  const mean = sorted.reduce((sum, value) => sum + value, 0) / count
  const variance = count > 1 ? sorted.reduce((sum, value) => sum + (value - mean) ** 2, 0) / (count - 1) : 0
  const standardDeviation = Math.sqrt(variance)
  const margin = count > 1 ? tCritical95(count - 1) * standardDeviation / Math.sqrt(count) : 0
  return {count, mean, standardDeviation, min: sorted[0], max: sorted[count - 1], median: percentile(sorted, .5), p05: percentile(sorted, .05), p25: percentile(sorted, .25), p75: percentile(sorted, .75), p95: percentile(sorted, .95), p99: percentile(sorted, .99), coefficientOfVariation: mean === 0 ? 0 : standardDeviation / Math.abs(mean), confidence95Low: mean - margin, confidence95High: mean + margin}
}

function tCritical95(degrees: number): number {
  const values = [0, 12.706, 4.303, 3.182, 2.776, 2.571, 2.447, 2.365, 2.306, 2.262, 2.228, 2.201, 2.179, 2.16, 2.145, 2.131, 2.12, 2.11, 2.101, 2.093, 2.086, 2.08, 2.074, 2.069, 2.064, 2.06, 2.056, 2.052, 2.048, 2.045, 2.042]
  return degrees < values.length ? values[Math.max(1, degrees)] : degrees < 40 ? 2.021 : degrees < 60 ? 2 : degrees < 120 ? 1.98 : 1.96
}

function percentile(sorted: number[], ratio: number): number {
  if (sorted.length === 1) return sorted[0]
  const position = (sorted.length - 1) * ratio
  const lower = Math.floor(position)
  const fraction = position - lower
  return sorted[lower] + (sorted[Math.min(lower + 1, sorted.length - 1)] - sorted[lower]) * fraction
}

function StatisticsTable({datasets, metrics, experiment}: {datasets: LoadedDataset[]; metrics: string[]; experiment?: Experiment}) {
  const rows = datasets.flatMap(dataset => metrics.map(metric => {
    const series = aggregateMetric([dataset], metric)[0]
    return series ? {dataset: dataset.label, metric, unit: series.unit, summary: summarize(series.points.map(point => point.value))} : null
  }).filter((item): item is {dataset: string; metric: string; unit: string; summary: ScientificSummary} => Boolean(item)))
  const aggregates = experiment ? variantStatistics(experiment, datasets, metrics) : []
  const effects = experiment ? variantEffectSizes(experiment, datasets, metrics) : []
  return <>
    {!!effects.length && <section className="statistics-card"><div><h3>Difference between strategies</h3></div><div className="statistics-table-wrap"><table className="effects-table"><thead><tr><th>Comparison</th><th>Metric</th><th title="Standardized effect size">Cohen's d</th><th>Difference</th><th>Runs</th></tr></thead><tbody>{effects.map(row => <tr key={row.metric}><td>{row.left} vs {row.right}</td><td>{metricTitle(row.metric)}</td><td>{row.value.toFixed(3)}</td><td>{effectMagnitude(row.value)}</td><td>{row.leftCount} / {row.rightCount}</td></tr>)}</tbody></table></div></section>}
    {!!aggregates.length && <section className="statistics-card"><div><h3>Across runs</h3></div><div className="statistics-table-wrap"><table className="aggregate-table"><thead><tr><th>Strategy</th><th>Metric</th><th>Runs</th><th>Runs missing</th><th>Mean</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>95th percentile</th><th>Variation</th><th>95% interval</th></tr></thead><tbody>{aggregates.map(row => <tr key={`${row.variant}-${row.metric}`}><td>{row.variant}</td><td>{metricTitle(row.metric)}</td><td>{row.summary.count}</td><td>{row.missing}</td><td>{formatMetric(row.summary.mean, row.unit)}</td><td>{formatMetric(row.summary.standardDeviation, row.unit)}</td><td>{formatMetric(row.summary.min, row.unit)}</td><td>{formatMetric(row.summary.median, row.unit)}</td><td>{formatMetric(row.summary.max, row.unit)}</td><td>{formatMetric(row.summary.p95, row.unit)}</td><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(row.summary.confidence95Low, row.unit)}–{formatMetric(row.summary.confidence95High, row.unit)}</td></tr>)}</tbody></table></div></section>}
    <section className="statistics-card"><div><h3>Within each run</h3></div><div className="statistics-table-wrap"><table className="runs-table"><thead><tr><th>Run</th><th>Metric</th><th>Samples</th><th>Mean</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>95th percentile</th><th>99th percentile</th><th>Variation</th><th>95% interval</th></tr></thead><tbody>{rows.map((row, index) => <tr key={`${row.dataset}-${row.metric}-${index}`}><td>{row.dataset}</td><td>{metricTitle(row.metric)}</td><td>{row.summary.count}</td><td>{formatMetric(row.summary.mean, row.unit)}</td><td>{formatMetric(row.summary.standardDeviation, row.unit)}</td><td>{formatMetric(row.summary.min, row.unit)}</td><td>{formatMetric(row.summary.median, row.unit)}</td><td>{formatMetric(row.summary.max, row.unit)}</td><td>{formatMetric(row.summary.p95, row.unit)}</td><td>{formatMetric(row.summary.p99, row.unit)}</td><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(row.summary.confidence95Low, row.unit)}–{formatMetric(row.summary.confidence95High, row.unit)}</td></tr>)}</tbody></table></div></section>
  </>
}

export function variantStatistics(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return experiment.variants.flatMap(variant => metrics.map(metric => {
    const values: number[] = []
    let unit = ''
    for (const trial of variant.trials) {
      const dataset = byArtifact.get(trial.resultArtifactId)
      if (!dataset) continue
      const series = aggregateMetric([dataset], metric)[0]
      if (!series?.points.length) continue
      unit ||= series.unit
      values.push(series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length)
    }
    return values.length ? {variant: variant.alias || variant.name, metric, unit, missing: variant.trials.length - values.length, summary: summarize(values)} : null
  }).filter((item): item is {variant: string; metric: string; unit: string; missing: number; summary: ScientificSummary} => Boolean(item)))
}

export function variantEffectSizes(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  if (experiment.variants.length !== 2) return []
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return metrics.map(metric => {
    const left = variantRunMeans(experiment.variants[0], byArtifact, metric)
    const right = variantRunMeans(experiment.variants[1], byArtifact, metric)
    if (left.length < 2 || right.length < 2) return null
    return {metric, left: experiment.variants[0].alias || experiment.variants[0].name, right: experiment.variants[1].alias || experiment.variants[1].name, value: cohenD(left, right), leftCount: left.length, rightCount: right.length}
  }).filter((item): item is {metric: string; left: string; right: string; value: number; leftCount: number; rightCount: number} => Boolean(item))
}

function variantRunMeans(variant: Experiment['variants'][number], datasets: Map<string, LoadedDataset>, metric: string): number[] {
  const values: number[] = []
  for (const trial of variant.trials) {
    const dataset = datasets.get(trial.resultArtifactId)
    if (!dataset) continue
    const series = aggregateMetric([dataset], metric)[0]
    if (series?.points.length) values.push(series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length)
  }
  return values
}

export function cohenD(left: number[], right: number[]): number {
  const leftSummary = summarize(left)
  const rightSummary = summarize(right)
  const degrees = leftSummary.count + rightSummary.count - 2
  if (degrees <= 0) return 0
  const pooled = Math.sqrt(((leftSummary.count - 1) * leftSummary.standardDeviation ** 2 + (rightSummary.count - 1) * rightSummary.standardDeviation ** 2) / degrees)
  return pooled === 0 ? 0 : (rightSummary.mean - leftSummary.mean) / pooled
}

function effectMagnitude(value: number): string {
  const absolute = Math.abs(value)
  if (absolute < .2) return 'Negligible'
  if (absolute < .5) return 'Small'
  if (absolute < .8) return 'Medium'
  return 'Large'
}

function exportStatisticsCSV(datasets: LoadedDataset[], metrics: string[], experiment?: Experiment) {
  const header = ['scope', 'strategy', 'dataset', 'metric', 'unit', 'missing_runs', 'count', 'mean', 'std', 'min', 'median', 'max', 'p05', 'p25', 'p75', 'p95', 'p99', 'cv', 'ci95_low', 'ci95_high', 'comparison', 'cohen_d']
  const rows: Array<Array<string | number>> = [header]
  for (const dataset of datasets) for (const metric of metrics) {
    const series = aggregateMetric([dataset], metric)[0]
    if (!series) continue
    const value = summarize(series.points.map(point => point.value))
    rows.push(['run', experimentStrategy(experiment, dataset.artifact.id), dataset.label, metric, series.unit, '', value.count, value.mean, value.standardDeviation, value.min, value.median, value.max, value.p05, value.p25, value.p75, value.p95, value.p99, value.coefficientOfVariation, value.confidence95Low, value.confidence95High, '', ''])
  }
  if (experiment) for (const item of variantStatistics(experiment, datasets, metrics)) {
    const value = item.summary
    rows.push(['strategy', item.variant, '', item.metric, item.unit, item.missing, value.count, value.mean, value.standardDeviation, value.min, value.median, value.max, value.p05, value.p25, value.p75, value.p95, value.p99, value.coefficientOfVariation, value.confidence95Low, value.confidence95High, '', ''])
  }
  if (experiment) for (const item of variantEffectSizes(experiment, datasets, metrics)) rows.push(['effect', '', '', item.metric, '', '', item.leftCount + item.rightCount, '', '', '', '', '', '', '', '', '', '', '', '', '', `${item.left} vs ${item.right}`, item.value])
  const filename = experiment ? `kubephos-${safeName(experiment.name)}-statistics.csv` : 'kubephos-statistics.csv'
  downloadBlob(filename, new Blob([rows.map(row => row.map(csvValue).join(',')).join('\n')], {type: 'text/csv;charset=utf-8'}))
}

function experimentStrategy(experiment: Experiment | undefined, artifactID: string): string {
  const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === artifactID))
  return variant?.alias || variant?.name || ''
}

function safeName(value: string): string {
  return value.trim().toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replaceAll(/(^-|-$)/g, '') || 'experiment'
}

function csvValue(value: string | number): string {
  const text = String(value)
  return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

export async function exportChart(id: string, metric: string, format: 'pdf' | 'png', onGenerated?: (value: Uint8Array) => Promise<void>) {
  const source = document.getElementById(id) as SVGSVGElement | null
  if (!source) return
  const serialized = new XMLSerializer().serializeToString(source)
  const name = `kubephos-${metric.replaceAll(/[^a-zA-Z0-9]+/g, '-').toLowerCase()}`
  if (format === 'pdf') {
    const pdf = svgToPDF(source)
    if (onGenerated) await onGenerated(pdf)
    else downloadBlob(`${name}.pdf`, new Blob([pdf.buffer as ArrayBuffer], {type: 'application/pdf'}))
    return
  }
  const image = new Image()
  const url = URL.createObjectURL(new Blob([serialized], {type: 'image/svg+xml'}))
  try {
    await new Promise<void>((resolve, reject) => { image.onload = () => resolve(); image.onerror = () => reject(new Error('Could not render chart')); image.src = url })
    const scale = 3
    const [,, sourceWidth, sourceHeight] = (source.getAttribute('viewBox') ?? '0 0 700 525').split(/\s+/).map(Number)
    const aspect = sourceWidth > 0 && sourceHeight > 0 ? sourceWidth / sourceHeight : 4 / 3
    const renderWidth = aspect >= 4 / 3 ? 700 : 525 * aspect
    const renderHeight = aspect >= 4 / 3 ? 700 / aspect : 525
    const canvas = document.createElement('canvas')
    canvas.width = Math.round(renderWidth * scale)
    canvas.height = Math.round(renderHeight * scale)
    const context = canvas.getContext('2d')
    if (!context) return
    context.fillStyle = '#ffffff'
    context.fillRect(0, 0, canvas.width, canvas.height)
    context.drawImage(image, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/png', 1))
    if (!blob) throw new Error('Could not render PNG figure.')
    if (blob) {
      const output = setPNGResolution(new Uint8Array(await blob.arrayBuffer()), 600)
      if (onGenerated) await onGenerated(output)
      else downloadBlob(`${name}-600dpi.png`, new Blob([output.buffer as ArrayBuffer], {type: 'image/png'}))
    }
  } finally {
    URL.revokeObjectURL(url)
  }
}

export function svgToPDF(source: SVGSVGElement): Uint8Array {
  const [,, svgWidth, svgHeight] = (source.getAttribute('viewBox') ?? '0 0 700 525').split(/\s+/).map(Number)
  const aspect = svgWidth > 0 && svgHeight > 0 ? svgWidth / svgHeight : 4 / 3
  const pageWidth = aspect > 4 / 3 ? 504 : 252
  const pageHeight = pageWidth / aspect
  const scale = pageWidth / svgWidth
  const commands = ['1 1 1 rg', `0 0 ${pageWidth} ${pageHeight} re f`, '0 0 0 RG', '0 0 0 rg']
  const sx = (value: number) => value * scale
  const sy = (value: number) => pageHeight - value * scale
  const number = (value: number) => Number.isFinite(value) ? value : 0
  const inherited = (element: Element, name: string): string => element.getAttribute(name) ?? element.parentElement?.getAttribute(name) ?? ''
  const stroke = (element: Element) => {
    const color = inherited(element, 'stroke') || '#111111'
    commands.push(`${pdfColor(color)} RG`)
    commands.push(`${sx(number(Number(inherited(element, 'stroke-width') || 1.5))).toFixed(3)} w`)
    const dash = inherited(element, 'stroke-dasharray').split(/\s+/).filter(Boolean).map(Number).filter(Number.isFinite)
    commands.push(dash.length ? `[${dash.map(value => sx(value).toFixed(3)).join(' ')}] 0 d` : '[] 0 d')
  }
  for (const element of source.querySelectorAll('rect,line,path,circle,polygon,text')) {
    const tag = element.tagName.toLowerCase()
    if (tag === 'text') {
      const x = sx(number(Number(element.getAttribute('x'))))
      const y = sy(number(Number(element.getAttribute('y'))))
      const size = sx(element.classList.contains('axis-label') ? 20 : element.classList.contains('legend-label') ? 15 : 17)
      const value = pdfText(element.textContent ?? '')
      const estimate = value.length * size * .5
      const anchor = element.getAttribute('text-anchor')
      const offset = anchor === 'middle' ? estimate / 2 : anchor === 'end' ? estimate : 0
      if ((element.getAttribute('transform') ?? '').startsWith('rotate(')) commands.push(`BT /F1 ${size.toFixed(3)} Tf 0 1 -1 0 ${(x + size * .3).toFixed(3)} ${(y - estimate / 2).toFixed(3)} Tm (${value}) Tj ET`)
      else commands.push(`BT /F1 ${size.toFixed(3)} Tf 1 0 0 1 ${(x - offset).toFixed(3)} ${y.toFixed(3)} Tm (${value}) Tj ET`)
      continue
    }
    if (tag === 'rect') {
      const x = sx(number(Number(element.getAttribute('x'))))
      const y = sy(number(Number(element.getAttribute('y'))) + number(Number(element.getAttribute('height'))))
      const width = sx(number(Number(element.getAttribute('width'))))
      const height = sx(number(Number(element.getAttribute('height'))))
      if (element.classList.contains('axis')) {
        stroke(element)
        commands.push(`${x.toFixed(3)} ${y.toFixed(3)} ${width.toFixed(3)} ${height.toFixed(3)} re S`)
      } else {
        const outline = inherited(element, 'stroke')
        commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`)
        if (outline && outline !== 'none') stroke(element)
        commands.push(`${x.toFixed(3)} ${y.toFixed(3)} ${width.toFixed(3)} ${height.toFixed(3)} re ${outline && outline !== 'none' ? 'B' : 'f'}`)
      }
      continue
    }
    if (tag === 'line') {
      stroke(element)
      commands.push(`${sx(number(Number(element.getAttribute('x1')))).toFixed(3)} ${sy(number(Number(element.getAttribute('y1')))).toFixed(3)} m ${sx(number(Number(element.getAttribute('x2')))).toFixed(3)} ${sy(number(Number(element.getAttribute('y2')))).toFixed(3)} l S`)
      continue
    }
    if (tag === 'circle') {
      const x = sx(number(Number(element.getAttribute('cx'))))
      const y = sy(number(Number(element.getAttribute('cy'))))
      const radius = sx(number(Number(element.getAttribute('r'))))
      const control = radius * .5522848
      commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`, `${(x + radius).toFixed(3)} ${y.toFixed(3)} m ${(x + radius).toFixed(3)} ${(y + control).toFixed(3)} ${(x + control).toFixed(3)} ${(y + radius).toFixed(3)} ${x.toFixed(3)} ${(y + radius).toFixed(3)} c ${(x - control).toFixed(3)} ${(y + radius).toFixed(3)} ${(x - radius).toFixed(3)} ${(y + control).toFixed(3)} ${(x - radius).toFixed(3)} ${y.toFixed(3)} c ${(x - radius).toFixed(3)} ${(y - control).toFixed(3)} ${(x - control).toFixed(3)} ${(y - radius).toFixed(3)} ${x.toFixed(3)} ${(y - radius).toFixed(3)} c ${(x + control).toFixed(3)} ${(y - radius).toFixed(3)} ${(x + radius).toFixed(3)} ${(y - control).toFixed(3)} ${(x + radius).toFixed(3)} ${y.toFixed(3)} c f`)
      continue
    }
    if (tag === 'polygon') {
      const points = (element.getAttribute('points') ?? '').trim().split(/\s+/).map(pair => pair.split(',').map(Number))
      if (!points.length) continue
      commands.push(`${pdfColor(inherited(element, 'fill') || '#000000')} rg`, `${sx(points[0][0]).toFixed(3)} ${sy(points[0][1]).toFixed(3)} m`, ...points.slice(1).map(point => `${sx(point[0]).toFixed(3)} ${sy(point[1]).toFixed(3)} l`), 'h f')
      continue
    }
    const path = element.getAttribute('d') ?? ''
    const converted = pdfPath(path, scale, pageHeight)
    if (!converted) continue
    stroke(element)
    commands.push(converted, inherited(element, 'fill') && inherited(element, 'fill') !== 'none' ? 'B' : 'S')
  }
  return pdfFile(commands.join('\n'), pageWidth, pageHeight)
}

function pdfPath(path: string, scale: number, pageHeight: number): string {
  const tokens = path.match(/[MLHVCZ]|-?(?:\d+\.?\d*|\.\d+)(?:e[-+]?\d+)?/gi) ?? []
  const output: string[] = []
  let command = ''
  let x = 0
  let y = 0
  for (let index = 0; index < tokens.length;) {
    if (/^[MLHVCZ]$/i.test(tokens[index])) command = tokens[index++].toUpperCase()
    if (command === 'Z') {
      output.push('h')
      command = ''
    } else if (command === 'H') {
      x = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} l`)
    } else if (command === 'V') {
      y = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} l`)
    } else if (command === 'M' || command === 'L') {
      x = Number(tokens[index++])
      y = Number(tokens[index++])
      output.push(`${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} ${command === 'M' ? 'm' : 'l'}`)
      if (command === 'M') command = 'L'
    } else if (command === 'C') {
      const firstX = Number(tokens[index++])
      const firstY = Number(tokens[index++])
      const secondX = Number(tokens[index++])
      const secondY = Number(tokens[index++])
      x = Number(tokens[index++])
      y = Number(tokens[index++])
      output.push(`${(firstX * scale).toFixed(3)} ${(pageHeight - firstY * scale).toFixed(3)} ${(secondX * scale).toFixed(3)} ${(pageHeight - secondY * scale).toFixed(3)} ${(x * scale).toFixed(3)} ${(pageHeight - y * scale).toFixed(3)} c`)
    } else index++
  }
  return output.join('\n')
}

function pdfColor(value: string): string {
  const match = value.match(/^#([0-9a-f]{6})$/i)
  if (!match) return '0 0 0'
  return [0, 2, 4].map(offset => (parseInt(match[1].slice(offset, offset + 2), 16) / 255).toFixed(4)).join(' ')
}

function pdfText(value: string): string {
  return value.normalize('NFKD').replaceAll(/[^\x20-\x7e]/g, '-').replaceAll('\\', '\\\\').replaceAll('(', '\\(').replaceAll(')', '\\)')
}

function pdfFile(content: string, width: number, height: number): Uint8Array {
  const encoder = new TextEncoder()
  const objects = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    `<< /Type /Page /Parent 2 0 R /MediaBox [0 0 ${width} ${height}] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>`,
    `<< /Length ${encoder.encode(content).length} >>\nstream\n${content}\nendstream`,
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>'
  ]
  let document = '%PDF-1.4\n'
  const offsets = [0]
  objects.forEach((object, index) => {
    offsets.push(encoder.encode(document).length)
    document += `${index + 1} 0 obj\n${object}\nendobj\n`
  })
  const xref = encoder.encode(document).length
  document += `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n${offsets.slice(1).map(offset => `${String(offset).padStart(10, '0')} 00000 n `).join('\n')}\ntrailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xref}\n%%EOF\n`
  return encoder.encode(document)
}

export function setPNGResolution(source: Uint8Array, dpi: number): Uint8Array {
  if (source.length < 33 || source.slice(0, 8).some((value, index) => value !== [137, 80, 78, 71, 13, 10, 26, 10][index])) throw new Error('Invalid PNG output')
  const chunks: Uint8Array[] = [source.slice(0, 8)]
  const view = new DataView(source.buffer, source.byteOffset, source.byteLength)
  let offset = 8
  while (offset + 12 <= source.length) {
    const length = view.getUint32(offset)
    const end = offset + length + 12
    if (end > source.length) throw new Error('Invalid PNG chunk')
    const type = String.fromCharCode(...source.slice(offset + 4, offset + 8))
    if (type !== 'pHYs') chunks.push(source.slice(offset, end))
    if (type === 'IHDR') chunks.push(pngResolutionChunk(dpi))
    offset = end
  }
  if (offset !== source.length) throw new Error('Invalid PNG length')
  const size = chunks.reduce((total, chunk) => total + chunk.length, 0)
  const result = new Uint8Array(size)
  let cursor = 0
  for (const chunk of chunks) {
    result.set(chunk, cursor)
    cursor += chunk.length
  }
  return result
}

function pngResolutionChunk(dpi: number): Uint8Array {
  const chunk = new Uint8Array(21)
  const view = new DataView(chunk.buffer)
  view.setUint32(0, 9)
  chunk.set([112, 72, 89, 115], 4)
  const pixelsPerMeter = Math.round(dpi / 0.0254)
  view.setUint32(8, pixelsPerMeter)
  view.setUint32(12, pixelsPerMeter)
  chunk[16] = 1
  view.setUint32(17, crc32(chunk.slice(4, 17)))
  return chunk
}

function crc32(source: Uint8Array): number {
  let crc = 0xffffffff
  for (const byte of source) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (crc & 1 ? 0xedb88320 : 0)
  }
  return (crc ^ 0xffffffff) >>> 0
}

function downloadBlob(name: string, blob: Blob) {
  const url = URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = name
  link.click()
  setTimeout(() => URL.revokeObjectURL(url), 0)
}

function formatDuration(milliseconds: number): string {
  const seconds = Math.max(0, Math.round(milliseconds / 1000))
  if (seconds >= 3600) return `${(seconds / 3600).toFixed(1)}h`
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`
  return `${seconds}s`
}

function paperChartPath(points: MetricPoint[], x: (value: number) => number, y: (value: number) => number): string {
  return points.map((point, index) => `${index ? 'L' : 'M'}${x(new Date(point.timestamp).getTime() / 60000).toFixed(2)},${y(point.value).toFixed(2)}`).join(' ')
}

function formatAxis(value: number): string {
  return value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1).replace(/\.0$/, '') : value.toFixed(2).replace(/\.0+$/, '').replace(/(\.\d)0$/, '$1')
}

function formatAxisValue(value: number, unit: string): string {
  if (unit === 'bytes') return formatAxis(value / 1024 ** 2)
  if (unit === 'bytes_per_second') return formatAxis(value / 1024 ** 2)
  return formatAxis(value)
}

function metricAxisLabel(metric: string, unit: string): string {
  const labels: Record<string, string> = {
    'load.configured_rps': 'Input rate (req/s)',
    'load.successful_rps': 'Throughput (req/s)',
    'load.failed_rps': 'Failed requests (req/s)',
    'load.failure_percentage': 'Failed requests (%)',
    'load.p95_response_time': 'P95 response time (ms)',
    'load.slo_violation_percentage': 'SLO violation windows (%)',
    'application.request_rps': 'Service throughput (req/s)',
    'cpu.cores': 'CPU usage (cores)',
    'memory.working_set': 'Working set (MiB)',
    'pod.restarts': 'Restart count',
    'deployment.ready_replicas': 'Replica count',
    'network.rtt': 'Network RTT (ms)',
    'network.packet_loss': 'Packet loss (%)',
    'network.bandwidth': 'Bandwidth (MiB/s)'
  }
  return labels[metric] ?? `${metricTitle(metric)}${unit ? ` (${unit.replaceAll('_', ' ')})` : ''}`
}

function compactLabel(value: string): string {
  return value.length > 34 ? `${value.slice(0, 31)}...` : value
}

function validateDataset(dataset: TimeSeriesDataset) {
  if (dataset.apiVersion !== 'artifacts.kubephos.dev/v1alpha1' || dataset.kind !== 'TimeSeriesDataset' || dataset.metadata?.version !== 'v1alpha1' || !dataset.spec?.applicationRef || !dataset.spec?.namespace || !Array.isArray(dataset.spec?.series) || !dataset.spec.series.length) throw new Error('This run has no compatible metric data.')
  for (const series of dataset.spec.series) {
    if (!series.metric || !series.unit || !Array.isArray(series.points) || !series.points.length || series.points.some(point => !Number.isFinite(point.value) || !Number.isFinite(new Date(point.timestamp).getTime()))) throw new Error('This run contains invalid metric samples.')
  }
}

function datasetLabel(artifact: Artifact, operations: Operation[], workspaces: Workspace[]): string {
  const operation = operations.find(item => item.id === artifact.operationId)
  const workspace = workspaces.find(item => item.id === artifact.workspaceId)
  return `${workspace?.name ?? 'Workspace'} · ${operation?.title ?? artifact.name}`
}

function metricTitle(metric: string): string {
  const label = metricAxisLabelName(metric)
  if (label) return label
  const acronyms: Record<string, string> = {cpu: 'CPU', rps: 'request rate', rtt: 'round-trip time', hpa: 'HPA', cpa: 'CPA', p95: 'P95', p99: 'P99'}
  return metric.split(/[._]/).filter(Boolean).map((word, index) => acronyms[word.toLowerCase()] ?? (index === 0 ? word.slice(0, 1).toUpperCase() + word.slice(1) : word)).join(' ')
}

function metricAxisLabelName(metric: string): string | undefined {
  return ({
    'load.configured_rps': 'Configured input rate',
    'load.successful_rps': 'Request throughput',
    'load.failed_rps': 'Failed request rate',
    'load.failure_percentage': 'Failure percentage',
    'load.p95_response_time': 'P95 response time',
    'load.slo_violation_percentage': 'SLO violation windows',
    'application.request_rps': 'Throughput by service',
    'cpu.cores': 'CPU usage',
    'memory.working_set': 'Memory working set',
    'pod.restarts': 'Pod restarts',
    'deployment.ready_replicas': 'Ready replicas',
    'network.rtt': 'Network RTT',
    'network.packet_loss': 'Packet loss',
    'network.bandwidth': 'Network bandwidth'
  } as Record<string, string>)[metric]
}

function formatMetric(value: number, unit: string): string {
  if (!Number.isFinite(value)) return '—'
  if (unit === 'bytes') {
    const divisor = value >= 1024 ** 3 ? 1024 ** 3 : value >= 1024 ** 2 ? 1024 ** 2 : value >= 1024 ? 1024 : 1
    const suffix = divisor === 1024 ** 3 ? 'GiB' : divisor === 1024 ** 2 ? 'MiB' : divisor === 1024 ? 'KiB' : 'B'
    return `${(value / divisor).toFixed(divisor === 1 ? 0 : 1)} ${suffix}`
  }
  if (unit === 'cores') return value < 1 ? `${(value * 1000).toFixed(0)}m` : value.toFixed(2)
  if (unit === 'requests_per_second') return `${value.toFixed(2)} req/s`
  if (unit === 'milliseconds') return `${value.toFixed(1)} ms`
  if (unit === 'percent') return `${value.toFixed(2)}%`
  if (unit === 'bytes_per_second') return `${formatMetric(value, 'bytes')}/s`
  return value.toLocaleString(undefined, {maximumFractionDigits: 2})
}

function ResultStat({label, value}: {label: string; value: string | number}) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}

function EmptyResults({experiment, runStatus}: {experiment?: Experiment; runStatus?: string}) {
  const status = runStatus ?? experiment?.status
  const running = status && ['queued', 'running', 'starting'].includes(status)
  const failed = status === 'failed'
  return <div className="empty-state"><div><strong>{running ? 'Runs in progress' : failed ? 'No results from this experiment' : 'No results yet'}</strong>{running ? 'Results and plots appear here as runs finish.' : failed ? 'Open the run above to inspect its logs and health checks.' : experiment ? 'Run this experiment to collect metrics and plots.' : 'Choose an experiment or run to explore verified data.'}</div></div>
}
