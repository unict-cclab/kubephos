import {ResponsiveTable} from './ResponsiveTable'
import {ResourceSharing, canManageResource} from './ResourceSharing'
import {ResourceAction} from './ResourceAction'
import {ActionIcon} from './ActionIcon'
import {downloadBlob, exportChart, niceAxis, timeAxis} from '../charts'
export {figureDimensions, niceAxis, setPNGResolution, svgToPDF} from '../charts'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useEffect, useMemo, useRef, useState, Fragment, type FormEvent, type CSSProperties} from 'react'
import {request} from '../api'
import {experimentDeletionMessage, experimentDisplayStatus, formatBytes, formatDate, isTerminalExperiment} from '../lib'
import type {Artifact, Experiment, ExperimentFigure, ExperimentTrial, MetricPoint, Operation, Session, TimeSeriesDataset, Workspace} from '../types'
import {Dialog} from './Dialog'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {LoadProfilePreview, loadProfileSteps, type LoadProfileStep} from './LoadProfileEditor'
import {ResourceAttribution} from './ResourceAttribution'

export interface ResultsViewProps {
  artifacts: Artifact[]
  experiments: Experiment[]
  operations: Operation[]
  workspaces: Workspace[]
  session: Session
  changed: () => Promise<void>
  openOperation: (id: string) => void
  openPipelineRun?: (id: string) => void
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

type ResultsTab = 'statistics' | 'charts' | 'downloads'

export function ResultsView({artifacts, experiments, operations, workspaces, session, changed, openOperation, openPipelineRun, focusedExperiment, onBack}: ResultsViewProps) {
  const {confirmDelete, confirmAction, confirmationDialog} = useDeleteConfirmation()
  const available = useMemo(() => artifacts.filter(item => !item.sensitive && item.type === 'TimeSeriesDataset' && item.version === 'v1alpha1').sort((left, right) => right.verifiedAt.localeCompare(left.verifiedAt)), [artifacts])
  const datasetCache = useRef(new Map<string, TimeSeriesDataset>())
  const availableByID = useMemo(() => new Map(available.map(item => [item.id, item])), [available])
  const focusedArtifactIDs = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(Boolean) ?? []
  const selectable = focusedExperiment ? available.filter(item => focusedArtifactIDs.includes(item.id)) : available
  const focusedKey = focusedArtifactIDs.join('|')
  const availableKey = available.map(item => `${item.id}:${item.digest}`).join('|')
  const knownFocusedIDs = useRef(selectable.map(item => item.id))
  const [selected, setSelected] = useState<string[]>(() => focusedArtifactIDs.filter(id => availableByID.has(id)).slice(0, 20))
  const [loaded, setLoaded] = useState<LoadedDataset[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [printPending, setPrintPending] = useState(false)
  const [activeExperiment, setActiveExperiment] = useState<string | null>(focusedExperiment?.id ?? null)
  const [activeTab, setActiveTab] = useState<ResultsTab>('statistics')
  const [runTab, setRunTab] = useState('summary')
  const [initialized, setInitialized] = useState(Boolean(focusedExperiment))
  useEffect(() => {
    if (!printPending || activeTab !== 'statistics') return
    const frame = requestAnimationFrame(() => {window.print(); setPrintPending(false)})
    return () => cancelAnimationFrame(frame)
  }, [printPending, activeTab])
  const [deleting, setDeleting] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [stopRequestedId, setStopRequestedId] = useState<string | null>(null)
  const stopRequested = Boolean(focusedExperiment && (focusedExperiment.cancelRequested || stopRequestedId === focusedExperiment.id))
  const [actionError, setActionError] = useState('')
  const focusedRuns = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => ({variant, trial}))) ?? []
  const activeRun = focusedRuns.find(item => item.trial.id === runTab)
  const liveRun = focusedRuns.find(item => item.trial.status === 'running')
  const viewSelected = activeRun ? activeRun.trial.resultArtifactId && availableByID.has(activeRun.trial.resultArtifactId) ? [activeRun.trial.resultArtifactId] : [] : selected
  const selectedKey = viewSelected.map(id => `${id}:${availableByID.get(id)?.digest ?? ''}:${responseTimeSLO(focusedExperiment, id) ?? ''}`).join('|')

  useEffect(() => {setRunTab('summary')}, [focusedExperiment?.id])

  useEffect(() => {
    if (!focusedExperiment || isTerminalExperiment(focusedExperiment.status)) setStopRequestedId(null)
  }, [focusedExperiment?.id, focusedExperiment?.status])

  useEffect(() => {
    setSelected(current => current.filter(id => availableByID.has(id)))
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
      setLoading(false)
      return
    }
    const controller = new AbortController()
    setLoading(true)
    setError('')
    Promise.all(viewSelected.map(async id => {
      const artifact = availableByID.get(id)
      if (!artifact) throw new Error('A selected run is no longer available.')
      const slo = responseTimeSLO(focusedExperiment, id)
      const cacheKey = `${id}:${artifact.digest}:${slo ?? ''}`
      let dataset = datasetCache.current.get(cacheKey)
      if (!dataset) {
        dataset = await request<TimeSeriesDataset>(`/artifacts/${id}/download`, {}, undefined, controller.signal)
        if (controller.signal.aborted) throw new DOMException('Aborted', 'AbortError')
        addDerivedMetrics(dataset, slo)
        validateDataset(dataset)
        // Keep memory bounded when navigating between many runs.
        if (datasetCache.current.size >= 20) datasetCache.current.delete(datasetCache.current.keys().next().value!)
        datasetCache.current.set(cacheKey, dataset)
      }
      return {artifact, dataset, label: ''}
    })).then(values => {if (!controller.signal.aborted) setLoaded(values)}).catch(cause => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the selected runs.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [selectedKey])

  const toggle = (id: string) => {
    if (!focusedExperiment) setActiveExperiment(null)
    setActiveTab('statistics')
    setNotice('')
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : current.length < (focusedExperiment ? 20 : 8) ? [...current, id] : current)
  }
  const experiment = focusedExperiment ?? experiments.find(item => item.id === activeExperiment)
  const experimentPresentation = useMemo(() => {
    const presentation = new Map<string, {label: string; color?: string}>()
    for (const variant of experiment?.variants ?? []) for (const trial of variant.trials) if (trial.resultArtifactId) presentation.set(trial.resultArtifactId, {label: `${variant.alias || variant.name} · Run ${trial.position}`, color: variant.color})
    return presentation
  }, [experiment])
  const displayed = useMemo(() => loaded.map(item => ({...item, label: experimentPresentation.get(item.artifact.id)?.label ?? datasetLabel(item.artifact, operations), color: experimentPresentation.get(item.artifact.id)?.color})), [loaded, experimentPresentation, operations, workspaces])
  const metrics = useMemo(() => [...new Set(displayed.flatMap(item => [...item.dataset.spec.series.map(series => series.metric), ...(item.dataset.spec.controlMetrics ?? []).filter(summary => summary.status !== 'not_configured').map(summary => summary.metric)]))].filter(metric => !['load.mean_response_time', 'load.p50_response_time', 'load.p99_response_time'].includes(metric) && (!metric.startsWith('application.') || metric === 'application.p95_response_time')).sort(), [displayed])
  const statisticsMetrics = [...new Set([...metrics, ...displayed.flatMap(item => item.dataset.spec.controlMetrics?.map(summary => summary.metric) ?? [])])].filter(metric => !['application.p95_response_time', 'redis.operation_p95_response_time'].includes(metric))
  const visibleMetrics = [...groupMetrics(metrics.filter(metric => !isControlMetric(metric))).values()].flat()
  const plotHeight = activeTab === 'charts' ? sharedChartHeight(displayed, visibleMetrics, activeRun ? undefined : experiment) : 465



  const selectedWorkspaceID = availableByID.get(selected[0])?.workspaceId
  const openExperiment = (item: Experiment) => {
    const artifactIDs = item.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(id => available.some(artifact => artifact.id === id))
    setActiveExperiment(item.id)
    setActiveTab('statistics')
    setSelected(artifactIDs.slice(0, 20))
    setNotice(artifactIDs.length > 20 ? 'Showing the first twenty runs.' : artifactIDs.length === 0 && item.status === 'succeeded' ? 'This experiment has no plottable metrics.' : '')
  }
  const removeExperiment = async () => {
    const label = experiment?.kind === 'suite' ? 'suite' : 'experiment'
    if (!experiment || !isTerminalExperiment(experiment.status) || !await confirmDelete(experimentDeletionMessage(experiment))) return
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
    if (!await confirmAction(`Stop ${label} ${focusedExperiment.name}? The current run will stop and the remaining runs will be canceled.`, {title: 'Stop experiment', label: 'Stop'})) return
    setStopping(true)
    setActionError('')
    try {
      await request(`/experiments/${focusedExperiment.id}/cancel`, {method: 'POST', body: '{}'}, session.csrfToken)
      setStopRequestedId(focusedExperiment.id)
      await changed()
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : `Could not stop the ${label}.`)
    } finally {
      setStopping(false)
    }
  }
  const displayStatus = focusedExperiment ? experimentDisplayStatus({...focusedExperiment, cancelRequested: stopRequested}) : ''
  const canStop = Boolean(focusedExperiment && ['queued', 'running'].includes(focusedExperiment.status))
  const stopLabel = focusedExperiment?.kind === 'suite' ? 'suite' : 'experiment'
  return <>
    {confirmationDialog}
    {focusedExperiment && onBack && <DetailBreadcrumb parent="Experiments" current={focusedExperiment.name} back={onBack} />}
    <div className={`results-heading ${focusedExperiment ? 'focused' : ''}`}><div><h2>{focusedExperiment?.name ?? 'Results'}</h2></div><div className="results-detail-actions">{focusedExperiment && <ResourceSharing kind="experiments" id={focusedExperiment.id} name={focusedExperiment.name} ownerUserId={focusedExperiment.ownerUserId} session={session} changed={changed} />}{focusedExperiment && <span className={`status ${activeRun?.trial.status ?? displayStatus}`}>{activeRun?.trial.status ?? displayStatus}</span>}{focusedExperiment && canManageResource(session,focusedExperiment.ownerUserId) && canStop && <button className="button danger" disabled={stopping || stopRequested} onClick={stopExperiment}>{stopping || stopRequested ? `Stopping ${stopLabel}…` : `Stop ${stopLabel}`}</button>}{focusedExperiment && canManageResource(session,focusedExperiment.ownerUserId) && <ResourceAction action="delete" label={deleting ? 'Deleting…' : `Delete ${stopLabel}`} disabled={deleting || !isTerminalExperiment(focusedExperiment.status)} title={isTerminalExperiment(focusedExperiment.status) ? undefined : `Stop this ${stopLabel} before deleting it`} onClick={removeExperiment} />}{!focusedExperiment && <div className="results-count" aria-label={`${selectable.length} datasets`}><strong>{selectable.length}</strong><span>datasets</span></div>}</div></div>
    {focusedExperiment && <ResourceAttribution resource={focusedExperiment} />}
    {actionError && <div className="validation-item error">{actionError}</div>}
    {!focusedExperiment && experiments.length > 0 && <div className="experiment-history"><div className="experiment-history-title"><span><b>1</b><strong>Choose an experiment</strong></span><span>{experiments.length} saved</span></div><div className="experiment-cards">{experiments.map(item => <button key={item.id} className={activeExperiment === item.id ? 'active' : ''} onClick={() => openExperiment(item)}><span className={`status-dot ${experimentDisplayStatus(item)}`} /><span><strong>{item.name}</strong><small>{item.status === 'queued' && item.scheduledFor && new Date(item.scheduledFor).getTime() > Date.now() ? `Scheduled ${formatDate(item.scheduledFor)}` : `${experimentDisplayStatus(item)} · ${item.variants.length} strategies · ${item.variants.reduce((total, variant) => total + variant.trials.length, 0)} runs`}</small></span><ActionIcon name="right" /></button>)}</div></div>}
    {focusedExperiment && <nav className="experiment-run-tabs" aria-label="Experiment runs"><button className="run-tab summary" aria-current={runTab === 'summary' ? 'page' : undefined} data-active={runTab === 'summary'} onClick={() => {setRunTab('summary'); setActiveTab('statistics')}}><span className="run-tab-label">All runs</span></button>{focusedRuns.map(({variant, trial}) => <button key={trial.id} className={`run-tab ${trial.status}`} aria-current={runTab === trial.id ? 'page' : undefined} data-active={runTab === trial.id} onClick={() => {setRunTab(trial.id); setActiveTab('statistics')}}><span className={`status-dot ${trial.status}`} /><span className="run-tab-label">{variant.name} · Run {trial.position}</span></button>)}</nav>}
    {experiment && (!activeRun ? <>{experiment.status !== 'succeeded' && <ExperimentProgress experiment={experiment} />}{liveRun && <RunLoadProgress name={`${liveRun.variant.name} · Run ${liveRun.trial.position}`} trial={liveRun.trial} configuration={liveRun.variant.configuration} operation={operations.find(item => item.id === liveRun.trial.operationId)} />}</> : <><RunDiagnostics status={activeRun.trial.status} error={activeRun.trial.error} pipelineRunId={activeRun.trial.pipelineRunId} operationId={activeRun.trial.operationId} openPipelineRun={openPipelineRun} openOperation={openOperation} /><RunLoadProgress trial={activeRun.trial} configuration={activeRun.variant.configuration} operation={operations.find(item => item.id === activeRun.trial.operationId)} /></>)}
    {!selectable.length || activeRun && !viewSelected.length ? <EmptyResults experiment={experiment} runStatus={activeRun?.trial.status} /> : <div className={`results-layout ${focusedExperiment ? 'focused' : ''}`}>
      {focusedExperiment && !activeRun && selectable.length > 1 ? <details className="results-run-filter"><summary><span><strong>Included runs</strong><small>{selected.length} of {selectable.length}</small></span><b>Change</b></summary><div className="results-run-filter-body"><button type="button" className="text-button" onClick={() => setSelected(selectable.map(item => item.id).slice(0, 20))}>Select all</button><div className="results-run-filter-options">{selectable.map(artifact => <button type="button" className={selected.includes(artifact.id) ? 'active' : ''} aria-pressed={selected.includes(artifact.id)} aria-label={`Include ${experimentPresentation.get(artifact.id)?.label ?? artifact.name}`} key={artifact.id} onClick={() => toggle(artifact.id)}><span className="dataset-check">{selected.includes(artifact.id) ? <ActionIcon name="check" /> : null}</span><strong>{experimentPresentation.get(artifact.id)?.label ?? artifact.name}</strong></button>)}</div></div></details> : !focusedExperiment ? <aside className="dataset-picker" aria-label="Available datasets">
        <div className="dataset-picker-title"><strong>{focusedExperiment ? 'Runs' : 'Select runs'}</strong><span>{selected.length} selected</span></div>
        {selectable.map(artifact => {
          const operation = operations.find(item => item.id === artifact.operationId)
          const active = selected.includes(artifact.id)
          const incompatibleWorkspace = Boolean(selectedWorkspaceID && artifact.workspaceId !== selectedWorkspaceID)
          return <button className={`dataset-option ${active ? 'active' : ''}`} key={artifact.id} onClick={() => toggle(artifact.id)} disabled={!active && (selected.length >= 8 || incompatibleWorkspace)} aria-pressed={active}>
            <span className="dataset-check">{active ? <ActionIcon name="check" /> : null}</span><span><strong>{operation?.title ?? artifact.name}</strong><small>{formatDate(artifact.verifiedAt)}</small><small>{formatBytes(artifact.sizeBytes)} · verified</small></span>
          </button>
        })}
      </aside> : null}
      <div className="results-content">
        {notice && <div className="validation-item warning">{notice}</div>}
        {!viewSelected.length && <div className="results-placeholder"><strong>Select runs</strong></div>}
        {loading && <div className="results-placeholder"><span className="results-spinner" /><strong>Loading verified data…</strong></div>}
        {error && <div className="validation-item error">{error}</div>}
        {!loading && !error && loaded.length > 0 && <>
          {!focusedExperiment && <div className="results-workbench-head">
            <div><h3>{activeRun ? `${activeRun.variant.name} · Run ${activeRun.trial.position}` : focusedExperiment ? 'Aggregated results' : experiment?.name ?? displayed[0].label}</h3><p>{displayed.length} run{displayed.length === 1 ? '' : 's'} · {metrics.length} metric{metrics.length === 1 ? '' : 's'}</p></div>
            <div className="results-heading-actions">{!focusedExperiment && <button className="button primary" disabled={selected.length < 2} onClick={() => setSaving(true)}>Save comparison</button>}</div>
          </div>}
          <nav className="results-tabs" aria-label="Result sections">{(['statistics', 'charts', 'downloads'] as ResultsTab[]).map(tab => <button key={tab} className={activeTab === tab ? 'active' : ''} aria-current={activeTab === tab ? 'page' : undefined} onClick={() => setActiveTab(tab)}>{({statistics: 'Statistics', charts: 'Plots', downloads: 'Export'} as Record<ResultsTab, string>)[tab]}</button>)}</nav>
          {activeTab === 'charts' && <div className="chart-grid" style={{'--plot-aspect-ratio': plotHeight / 860} as CSSProperties}>{visibleMetrics.map(metric => <Fragment key={metric}>{!['application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time'].includes(metric) && <MetricChart metric={metric} series={aggregateMetric(displayed, metric)} canvasHeight={plotHeight} />}{serviceStatisticsMetrics.includes(metric) && <ServiceMetricChart metric={metric} datasets={displayed} experiment={activeRun ? undefined : experiment} canvasHeight={plotHeight} />}</Fragment>)}</div>}
          {activeTab === 'statistics' && <><StatisticsTable datasets={displayed} metrics={statisticsMetrics} experiment={activeRun ? undefined : experiment} /></>}
          {activeTab === 'downloads' && <ResultsExport datasets={displayed} metrics={statisticsMetrics} experiment={experiment} activeRun={Boolean(activeRun)} onPrint={() => {setActiveTab('statistics'); setPrintPending(true)}} />}
        </>}
      </div>
    </div>}
    <SaveExperimentDialog open={saving} close={() => setSaving(false)} selected={selected.map(id => availableByID.get(id)).filter((item): item is Artifact => Boolean(item))} operations={operations} workspaces={workspaces} session={session} changed={changed} />
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

function RunDiagnostics({status, error, pipelineRunId, operationId, openPipelineRun, openOperation}: {status: string; error?: string; pipelineRunId?: string; operationId: string; openPipelineRun?: (id: string) => void; openOperation?: (id: string) => void}) {
  const running = ['running', 'starting', 'prechecking', 'verifying'].includes(status)
  const failed = ['failed', 'canceled'].includes(status)
  const label = status === 'queued' ? 'Run waiting to start' : status === 'canceled' ? 'Run canceled' : failed ? 'Run needs attention' : running ? 'Run in progress' : status === 'succeeded' ? 'Run completed' : 'Run status unavailable'
  if (!error && !running && !failed && !(pipelineRunId && openPipelineRun)) return null
  return <div className={`run-diagnostics ${failed ? 'failed' : ''}`}><div><strong>{label}</strong>{error && <p>{error}</p>}</div>{pipelineRunId && openPipelineRun ? <button className="button secondary compact" onClick={() => openPipelineRun(pipelineRunId)}>{running ? 'Live process logs' : 'Process logs'}</button> : operationId && openOperation ? <button className="button secondary compact" onClick={() => openOperation(operationId)}>{running ? 'Live logs' : 'Error logs'}</button> : null}</div>
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

function responseTimeSLO(experiment: Experiment | undefined, artifactID: string): number | undefined {
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
  return undefined
}

function addDerivedMetrics(dataset: TimeSeriesDataset, configuredSLO?: number) {
  const datasetSLO = Number(dataset.spec.sloMilliseconds)
  const sloMilliseconds = Number.isFinite(datasetSLO) && datasetSLO > 0
    ? datasetSLO
    : Number.isFinite(configuredSLO) && Number(configuredSLO) > 0 ? Number(configuredSLO) : undefined
  if (sloMilliseconds === undefined) return
  dataset.spec.sloMilliseconds = sloMilliseconds
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
      await request('/experiments', {method: 'POST', body: JSON.stringify({workspaceId: workspace.id, name: form.get('name'), description: '', variants: [...grouped.values()].map(item => ({...item, configuration: {}}))})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save the experiment.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Save experiment">
    <form onSubmit={submit}>
      <label>Name<input name="name" maxLength={120} placeholder="Default vs custom strategy" required autoFocus /></label>
      <div className="trial-mapping"><span className="field-description">Shared names group repeated runs.</span>{selected.map(artifact => <label key={artifact.id}><span>{operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name}</span><input name={`variant-${artifact.id}`} maxLength={80} defaultValue={operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name} required /></label>)}</div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Saving…' : 'Save experiment'}</button></div>
    </form>
  </Dialog>
}

const colors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#000000']
const dashPatterns = ['', '10 6', '10 4 2 4', '2 5']

function groupMetrics(metrics: string[]): Map<string, string[]> {
  const values = new Map<string, string[]>()
  for (const metric of metrics) {
    const group = metric.startsWith('load.') || metric === 'application.p95_response_time' ? 'Load' : metric.startsWith('application.') ? 'Application' : metric.startsWith('redis.') ? 'Redis' : metric.startsWith('network.') ? 'Network' : ['cpu', 'memory'].includes(metric.split('.')[0]) ? 'Resources' : 'Kubernetes'
    values.set(group, [...(values.get(group) ?? []), metric])
  }
  return new Map(['Load', 'Application', 'Redis', 'Kubernetes', 'Resources', 'Network'].flatMap(group => values.has(group) ? [[group, values.get(group)!] as [string, string[]]] : []))
}

function chartLegendLayout(labels: string[], left: number, right: number, symbolWidth: number, fontSize: number) {
  let x = left
  let row = 0
  const entries = labels.map(label => {
    const width = symbolWidth + compactLabel(label).length * fontSize * .6 + 18
    if (x > left && x + width > right) { x = left; row++ }
    const entry = {x, row}
    x += width
    return entry
  })
  return {entries, rows: labels.length ? row + 1 : 0}
}

function sharedChartHeight(datasets: LoadedDataset[], metrics: string[], experiment?: Experiment) {
  let height = 465
  for (const metric of metrics) {
    const series = aggregateMetric(datasets, metric)
    if (series.length > 1) height = Math.max(height, 465 + chartLegendLayout(series.map(item => item.label), 112, 832, 37, 15).rows * 21)
    if (!['deployment.ready_replicas', 'cpu.cores', 'memory.working_set', 'application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time'].includes(metric)) continue
    const {rows} = serviceMetricStatistics(datasets, metric, experiment)
    if (!rows.length) continue
    const services = [...new Set(rows.map(row => row.service))]
    const labels = [...new Set(rows.map(row => row.group))]
    const width = 860
    const labelHeight = Math.max(...services.map(service => service.length)) * 7 + 25
    height = Math.max(height, 383 + labelHeight + 20 + chartLegendLayout(labels, 110, width - 64, 22, 14).rows * 21 + 12)
  }
  return height
}


function MetricChart({metric, series, canvasHeight}: {metric: string; series: AggregateSeries[]; canvasHeight: number}) {
  const width = 860
  const multiple = series.length > 1
  const legend = chartLegendLayout(series.map(item => item.label), 112, width - 28, 37, 15)
  const legendRows = multiple ? legend.rows : 0
  const footerHeight = multiple ? 82 + legendRows * 21 : 72
  const height = Math.max(canvasHeight, 383 + footerHeight)
  const plotBottom = height - footerHeight
  const padding = {top: 16, right: 64, bottom: footerHeight, left: 96}
  const allPoints = series.flatMap(item => item.points)
  const timestamps = allPoints.map(point => new Date(point.timestamp).getTime())
  const values = allPoints.map(point => point.value)
  const maxX = Math.max(0, ...timestamps) / 60000
  const sloMilliseconds = metric === 'load.p95_response_time' ? series.find(item => item.sloMilliseconds)?.sloMilliseconds : undefined
  const maxY = Math.max(0, ...values, sloMilliseconds ?? 0)
  const unit = series[0]?.unit ?? ''
  const xAxis = timeAxis(maxX, true)
  const yAxis = niceAxis(maxY, unit === 'replicas' || unit === 'count')
  const plotRight = width - padding.right
  const x = (value: number) => padding.left + (value / xAxis.upper) * (plotRight - padding.left)
  const y = (value: number) => plotBottom - (value / yAxis.upper) * (plotBottom - padding.top)
  const chartID = `chart-${metric.replaceAll(/[^a-zA-Z0-9]/g, '-')}`
  return <article className="metric-card">
    <div className="metric-card-header"><div><h3>{metricTitle(metric)}</h3></div><div className="chart-export-actions"><button className="button secondary compact" type="button" onClick={() => exportChart(chartID, metric, 'pdf')}>PDF</button><button className="button secondary compact" type="button" onClick={() => exportChart(chartID, metric, 'png')}>PNG</button></div></div>
    <div className="chart-wrap"><svg id={chartID} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={`${metricTitle(metric)} comparison chart`}>
      <rect width={width} height={height} fill="#ffffff" />
      <rect x={padding.left} y={padding.top} width={plotRight - padding.left} height={plotBottom - padding.top} className="axis" fill="none" stroke="#111" strokeWidth="2" />
      {xAxis.ticks.map(tick => <g key={`x-${tick}`}><line x1={x(tick)} y1={plotBottom} x2={x(tick)} y2={plotBottom + 7} className="tick-line" stroke="#111" strokeWidth="1.5" /><text x={x(tick)} y={plotBottom + 28} textAnchor="middle" className="tick" fontSize="17" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{formatAxis(tick)}</text></g>)}
      {yAxis.ticks.map(tick => <g key={`y-${tick}`}><line x1={padding.left - 7} y1={y(tick)} x2={padding.left} y2={y(tick)} className="tick-line" stroke="#111" strokeWidth="1.5" /><text x={padding.left - 13} y={y(tick) + 6} textAnchor="end" className="tick" fontSize="17" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{formatAxisValue(tick, unit)}</text></g>)}
      {sloMilliseconds !== undefined && <g><line x1={padding.left} x2={plotRight} y1={y(sloMilliseconds)} y2={y(sloMilliseconds)} stroke="#b42318" strokeWidth="2" strokeDasharray="8 6" /><text x={plotRight - 5} y={y(sloMilliseconds) - 8} textAnchor="end" className="legend-label slo-label" fontSize="15" fill="#b42318" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">SLO {formatMetric(sloMilliseconds, 'milliseconds')}</text></g>}
      <text x={(padding.left + plotRight) / 2} y={plotBottom + 58} textAnchor="middle" className="axis-label" fontSize="20" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">Time (min)</text>
      <text x="27" y={(padding.top + plotBottom) / 2} textAnchor="middle" className="axis-label" transform={`rotate(-90 27 ${(padding.top + plotBottom) / 2})`} fontSize="20" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{metricAxisLabel(metric, unit)}</text>
      {series.map((item, index) => { const color = item.color ?? colors[index % colors.length]; return <g key={`${item.artifactID}-${item.label}-${index}`}><path d={paperChartPath(item.points, x, y)} fill="none" stroke={color} strokeWidth="2.5" strokeDasharray={dashPatterns[Math.floor(index / colors.length) % dashPatterns.length]} strokeLinejoin="round" />{item.points.filter((_, pointIndex) => pointIndex % Math.max(1, Math.floor(item.points.length / 12)) === 0).map((point, pointIndex) => <ChartMarker key={pointIndex} shape={index % 7} x={x(new Date(point.timestamp).getTime() / 60000)} y={y(point.value)} color={color} />)}</g>})}
      {multiple && series.map((item, index) => {
        const legendX = legend.entries[index].x
        const legendY = plotBottom + 80 + legend.entries[index].row * 21
        const color = item.color ?? colors[index % colors.length]
        return <g key={`legend-${item.artifactID}-${item.label}-${index}`}><line x1={legendX} y1={legendY} x2={legendX + 27} y2={legendY} stroke={color} strokeWidth="2.5" strokeDasharray={dashPatterns[Math.floor(index / colors.length) % dashPatterns.length]} /><ChartMarker shape={index % 7} x={legendX + 13} y={legendY} color={color} /><text x={legendX + 37} y={legendY + 5} className="legend-label" fontSize="15" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{compactLabel(item.label)}</text></g>
      })}
    </svg></div>
    <div className="chart-legend">{series.map((item, index) => <div key={`${item.artifactID}-${item.label}-${index}`}><span style={{background: item.color ?? colors[index % colors.length]}} /><strong>{item.label}</strong></div>)}</div>
  </article>
}


function ServiceMetricChart({metric, datasets, experiment, canvasHeight}: {metric: string; datasets: LoadedDataset[]; experiment?: Experiment; canvasHeight: number}) {
  const responseTime = metric.endsWith('p95_response_time')
  const title = responseTime ? metric.startsWith('redis.') ? 'Mean window P95 per Redis operation' : 'Mean window P95 per service' : metric.startsWith('redis.') ? metricTitle(metric) : `${metricTitle(metric)} by service`
  const {rows} = serviceMetricStatistics(datasets, metric, experiment)
  if (!rows.length) return null
  const services = [...new Set(rows.map(row => row.service))]
  const labels = [...new Set(rows.map(row => row.group))]
  const width = 860
  const left = 110
  const right = width - 64
  const top = 28
  const bottom = 383
  const labelHeight = Math.max(...services.map(service => service.length)) * 7 + 25
  const legendTop = bottom + labelHeight + 20
  const legend = chartLegendLayout(labels, left, right, 22, 14)
  const height = Math.max(canvasHeight, legendTop + legend.rows * 21 + 12)
  const unit = rows[0].unit
  const axis = niceAxis(Math.max(...rows.map(row => row.summary.mean + row.summary.standardDeviation)) * 1.15)
  const y = (value: number) => bottom - value / axis.upper * (bottom - top)
  const band = (right - left) / services.length
  const barSlot = Math.max(1, band - Math.min(16, band * .2)) / labels.length
  const barWidth = Math.min(40, barSlot)
  const serviceX = (index: number) => left + band * (index + .5)
  const chartID = `chart-${metric.replaceAll(/[^a-zA-Z0-9]/g, '-')}-by-service`
  return <article className="metric-card">
    <div className="metric-card-header"><div><h3>{title}</h3></div><div className="chart-export-actions"><button className="button secondary compact" type="button" onClick={() => exportChart(chartID, `${metric}-by-service`, 'pdf')}>PDF</button><button className="button secondary compact" type="button" onClick={() => exportChart(chartID, `${metric}-by-service`, 'png')}>PNG</button></div></div>
    <div className="chart-wrap"><svg className="service-metric-chart" id={chartID} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={`${title}: mean and standard deviation`}>
      <rect width={width} height={height} fill="#ffffff" />
      {axis.ticks.map(tick => <g key={tick}><line x1={left} y1={y(tick)} x2={right} y2={y(tick)} stroke="#e5e7eb" /><text x={left - 12} y={y(tick) + 5} textAnchor="end" className="tick" fontSize="14" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{formatAxisValue(tick, unit)}</text></g>)}
      {rows.map((row, index) => {
        const groupIndex = labels.indexOf(row.group)
        const center = serviceX(services.indexOf(row.service)) + (groupIndex - (labels.length - 1) / 2) * barSlot
        const color = row.color ?? colors[groupIndex % colors.length]
        const low = y(Math.max(0, row.summary.mean - row.summary.standardDeviation))
        const high = y(row.summary.mean + row.summary.standardDeviation)
        return <g key={`${row.service}-${row.group}-${index}`}><title>{`${row.service} · ${row.group}: mean ${formatMetric(row.summary.mean, unit)}, standard deviation ${formatMetric(row.summary.standardDeviation, unit)}`}</title><rect x={center - barWidth * .4} y={y(row.summary.mean)} width={barWidth * .8} height={Math.max(0, bottom - y(row.summary.mean))} fill={color} opacity="0.8" /><path d={`M${center},${low}V${high}M${center - 6},${low}H${center + 6}M${center - 6},${high}H${center + 6}`} fill="none" stroke="#111" strokeWidth="2" /></g>
      })}
      <path d={`M${left},${top}V${bottom}H${right}`} fill="none" stroke="#111" />
      {services.map((service, index) => <text key={service} x={serviceX(index) + 4} y={bottom + 12} fontSize="12" textAnchor="end" transform={`rotate(-90 ${serviceX(index) + 4} ${bottom + 12})`} fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{service}</text>)}
      <text x="27" y={(top + bottom) / 2} textAnchor="middle" transform={`rotate(-90 27 ${(top + bottom) / 2})`} className="axis-label" fontSize="18" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{metricAxisLabel(metric, unit)}</text>
      {labels.map((label, index) => {
        const legendX = legend.entries[index].x
        const legendY = legendTop + legend.entries[index].row * 21
        const color = rows.find(row => row.group === label)?.color ?? colors[index % colors.length]
        return <g key={label}><rect x={legendX} y={legendY - 12} width="16" height="16" fill={color} /><text x={legendX + 24} y={legendY + 1} className="tick" fontSize="14" fill="#111" fontFamily="Arial, Helvetica, DejaVu Sans, sans-serif">{compactLabel(label)}</text></g>
      })}
    </svg></div>
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


function paperChartPath(points: MetricPoint[], x: (value: number) => number, y: (value: number) => number): string {
  return points.map((point, index) => `${index ? 'L' : 'M'}${x(new Date(point.timestamp).getTime() / 60000).toFixed(2)},${y(point.value).toFixed(2)}`).join(' ')
}


export function formatAxis(value: number): string {
  if (Math.abs(value) < 1) return Number(value.toPrecision(3)).toString()
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
    'load.successful_rps': 'Successful input requests (req/s)',
    'load.failed_rps': 'Failed requests (req/s)',
    'load.failure_percentage': 'Failed requests (%)',
    'load.p95_response_time': 'P95 response time (ms)',
    'load.slo_violation_percentage': 'SLO violation windows (%)',
    'application.request_rps': 'Internal service calls (req/s)',
    'application.p95_response_time': 'P95 internal response time (ms)',
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


export function compactLabel(value: string): string {
  if (value.length <= 34) return value
  const run = value.match(/^(.*)( · Run \d+)$/)
  if (run) return `${run[1].slice(0, Math.max(1, 31 - run[2].length))}...${run[2]}`
  return `${value.slice(0, 31)}...`
}

export function serviceMetricStatistics(datasets: LoadedDataset[], metric: string, experiment?: Experiment) {
  const groups = new Map<string, {service: string; group: string; color?: string; unit: string; runs: ScientificSummary[]}>()
  let unmatched = 0
  for (const dataset of datasets) {
    const deployments = [...new Set(dataset.dataset.spec.series.filter(series => series.metric === 'deployment.ready_replicas').map(series => series.labels.deployment || series.labels.statefulset).filter(Boolean))].sort((a, b) => b.length - a.length)
    const variant = experiment?.variants.find(item => item.trials.some(trial => trial.resultArtifactId === dataset.artifact.id))
    const group = variant?.alias || variant?.name || plotLabel(dataset.label)
    const groupID = variant?.id || dataset.artifact.id
    const services = new Map<string, {unit: string; points: Map<string, number>; counts: Map<string, number>}>()
    for (const series of dataset.dataset.spec.series.filter(series => series.metric === metric)) {
      if (series.labels['app.kubernetes.io/name'] === 'kubephos-load' || ['deployment', 'pod', 'destination_workload', 'service'].some(label => series.labels[label]?.startsWith('kubephos-load-'))) continue
      const pod = series.labels.pod
      // Older datasets contain pod labels only. Match against deployments actually present in the same run.
      const service = series.labels.deployment || series.labels.statefulset || series.labels.destination_workload || series.labels.service || series.labels.operation || deployments.find(name => pod?.startsWith(`${name}-`) && /^(?:[a-z0-9]+-[a-z0-9]+|[0-9]+)$/.test(pod.slice(name.length + 1)))
      if (!service) {unmatched++; continue}
      if (metric === 'application.p95_response_time' && (service === 'node-proxy' || service.startsWith('gateway-'))) continue
      const values = services.get(service) ?? {unit: series.unit, points: new Map<string, number>(), counts: new Map<string, number>()}
      for (const point of series.points) if (Number.isFinite(point.value)) {
        values.points.set(point.timestamp, (values.points.get(point.timestamp) ?? 0) + point.value)
        values.counts.set(point.timestamp, (values.counts.get(point.timestamp) ?? 0) + 1)
      }
      services.set(service, values)
    }
    for (const [service, values] of services) {
      const key = JSON.stringify([service, groupID])
      const row = groups.get(key) ?? {service, group, color: variant?.color ?? dataset.color, unit: values.unit, runs: []}
      if (values.points.size) row.runs.push(summarize([...values.points].map(([timestamp, value]) => metric.endsWith('p95_response_time') ? value / values.counts.get(timestamp)! : value)))
      groups.set(key, row)
    }
  }
  return {unmatched, rows: [...groups.values()].filter(row => row.runs.length).sort((a, b) => a.service.localeCompare(b.service, undefined, {numeric: true}) || a.group.localeCompare(b.group)).map(row => ({...row, summary: row.runs.length > 1 ? summarize(row.runs.map(run => run.mean)) : row.runs[0], acrossRuns: row.runs.length > 1}))}
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
    const startedAt = new Date(!isControlMetric(metric) && item.dataset.spec.workloadStartedAt ? item.dataset.spec.workloadStartedAt : item.dataset.spec.start).getTime()
    return {
      artifactID: item.artifact.id,
      label: item.label,
      unit: matching[0]?.unit ?? '',
      sloMilliseconds: item.dataset.spec.sloMilliseconds,
      color: item.color,
      points: (metric === 'scheduler.pod_scheduling_latency' ? matching.flatMap(series => series.points.map(point => [Number.isFinite(Date.parse(series.labels.scheduled_at)) ? Date.parse(series.labels.scheduled_at) : Date.parse(point.timestamp), point.value] as [number, number])) : [...values.entries()]).sort((left, right) => left[0] - right[0]).map(([timestamp, value]) => ({timestamp: new Date(Math.max(0, timestamp - startedAt)).toISOString(), value: aggregation === 'mean' && metric !== 'scheduler.pod_scheduling_latency' ? value / (counts.get(timestamp) ?? 1) : value}))
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

function isControlMetric(metric: string): boolean {
  return ['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration', 'descheduler.cycle_duration', 'descheduler.evictions'].includes(metric)
}

function controlMetricDescription(metric: string, scope: string, reason?: string, resolution?: number): string {
  const description = metric === 'scheduler.pod_scheduling_latency' ? 'Time from pod creation to node assignment; includes queueing and scheduling.' : metric === 'scheduler.cycle_duration' ? 'Time spent in the scheduler scheduling algorithm per attempt; excludes queueing and pod startup.' : metric === 'descheduler.cycle_duration' ? 'Time spent in a descheduler loop.' : 'Successful application pod evictions.'
  return [description, scope === 'cluster' ? 'Scope: entire cluster.' : 'Scope: experiment.', resolution ? `Timestamp resolution: ${resolution} s.` : '', reason ?? ''].filter(Boolean).join(' ')
}

const serviceStatisticsMetrics = ['deployment.ready_replicas', 'cpu.cores', 'memory.working_set', 'application.p95_response_time', 'redis.operation_rps', 'redis.operation_p95_response_time']

function serviceStatisticsRows(datasets: LoadedDataset[], experiment?: Experiment) {
  return serviceStatisticsMetrics.flatMap(metric => serviceMetricStatistics(datasets, metric, experiment).rows.map(row => ({...row, metric})))
}

function ServiceStatisticsTable({datasets, experiment}: {datasets: LoadedDataset[]; experiment?: Experiment}) {
  const [query, setQuery] = useState('')
  const rows = serviceStatisticsRows(datasets, experiment).filter(row => !experiment || row.acrossRuns)
  if (!rows.length) return null
  const allServices = [...new Set(rows.map(row => row.service))]
  const services = allServices.filter(service => service.toLowerCase().includes(query.trim().toLowerCase())).sort((left, right) => left.localeCompare(right, undefined, {numeric: true}))
  return <div className="service-statistics"><h4>{experiment ? 'By service · Across runs' : 'By service'}</h4><div className="list-toolbar"><input type="search" aria-label={`Search services for ${experiment ? 'all runs' : datasets[0].label}`} placeholder="Search services…" value={query} onChange={event => setQuery(event.target.value)} /><span>{services.length} of {allServices.length}</span></div>{!services.length && <p className="empty-inline">No services match your search.</p>}<div className="statistics-table-wrap"><ResponsiveTable className="runs-table" aria-label={experiment ? 'Service statistics across runs' : `Service statistics for ${datasets[0].label}`}><thead><tr><th>Metric</th>{experiment && <th>Strategy</th>}<th>{experiment ? 'Runs' : 'Samples'}</th><th>Mean</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th></tr></thead>{services.map(service => <tbody key={service}><tr className="service-statistics-group"><th scope="rowgroup" colSpan={experiment ? 8 : 7}>{service}</th></tr>{rows.filter(row => row.service === service).map(row => <tr key={`${row.metric}-${row.group}`}><td>{statisticsMetricTitle(row.metric)}</td>{experiment && <td>{row.group}</td>}<td>{row.summary.count}</td><SummaryCells summary={row.summary} unit={row.unit} /></tr>)}</tbody>)}</ResponsiveTable></div></div>
}

function SummaryCells({summary, unit}: {summary: ScientificSummary; unit: string}) {
  return <>{[summary.mean, summary.standardDeviation, summary.min, summary.median, summary.max].map((value, index) => <td key={index}>{formatMetric(value, unit)}</td>)}</>
}

function ResultsExport({datasets, metrics, experiment, activeRun, onPrint}: {datasets: LoadedDataset[]; metrics: string[]; experiment?: Experiment; activeRun: boolean; onPrint: () => void}) {
  const [figures, setFigures] = useState<ExperimentFigure[]>([])
  const [error, setError] = useState('')
  useEffect(() => {
    if (!experiment) return
    const controller = new AbortController()
    request<{items: ExperimentFigure[]}>(`/experiments/${experiment.id}/figures`, {}, undefined, controller.signal).then(result => {if (!controller.signal.aborted) setFigures(result.items ?? [])}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load saved figures.')})
    return () => controller.abort()
  }, [experiment?.id])
  const visibleFigures = figures.filter(figure => figure.sourceArtifactIds.some(id => datasets.some(dataset => dataset.artifact.id === id)))
  return <section className="results-download-panel"><div><h3>Export results</h3></div><div className="results-export-actions"><button className="button secondary" onClick={() => downloadBlob('kubephos-statistics.csv', new Blob([statisticsCSV(datasets, metrics, activeRun ? undefined : experiment)], {type: 'text/csv;charset=utf-8'}))}>Statistics CSV</button><button className="button secondary" onClick={onPrint}>Print statistics</button></div>{error && <p className="form-error">{error}</p>}{visibleFigures.length > 0 && <div className="saved-figures"><strong>Saved figures</strong><div className="dataset-downloads">{visibleFigures.map(figure => <a key={figure.id} href={`/api/v1/figures/${figure.id}/download`}><span>↓</span><span><strong>{metricTitle(figure.metric)} · {figure.format.toUpperCase()}</strong><small>{figure.sourceArtifactIds.length} runs · {formatDate(figure.createdAt)}</small></span></a>)}</div></div>}<div className="results-raw-data"><strong>Complete run data</strong><div className="dataset-downloads">{datasets.map(item => <a key={item.artifact.id} href={`/api/v1/artifacts/${item.artifact.id}/download`}><span>↓</span><span><strong>{item.label}</strong><small>Metric samples</small></span></a>)}</div></div></section>
}

export function statisticsCSV(datasets: LoadedDataset[], metrics: string[], experiment?: Experiment): string {
  const rows: Array<Array<string | number>> = [['scope', 'strategy', 'dataset', 'metric', 'service', 'unit', 'status', 'count', 'mean', 'total', 'std', 'min', 'median', 'max', 'missing_runs']]
  const summaryValues = (summary: ScientificSummary) => [summary.count, summary.mean, '', summary.standardDeviation, summary.min, summary.median, summary.max]
  for (const dataset of datasets) {
    for (const metric of metrics) {
      const series = aggregateMetric([dataset], metric)[0]
      const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
      const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
      if (podSummary) rows.push(['run', '', dataset.label, metric, '', 'milliseconds', 'available', ...summaryValues(podSummary), ''])
      else if (isControlMetric(metric) && control) rows.push(['run', '', dataset.label, metric, '', metric === 'descheduler.evictions' ? 'count' : 'milliseconds', control.status, control.count ?? '', control.mean ?? '', control.total ?? '', '', '', '', '', ''])
      else if (series) rows.push(['run', '', dataset.label, metric, '', series.unit, 'available', ...summaryValues(summarize(series.points.map(point => point.value))), ''])
    }
    for (const row of serviceStatisticsRows([dataset])) rows.push(['service_run', row.group, dataset.label, row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), ''])
  }
  if (experiment) {
    for (const row of variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2)) rows.push(['across_runs', row.variant, '', row.metric, '', row.unit, 'available', ...summaryValues(row.summary), row.missing])
    for (const row of serviceStatisticsRows(datasets, experiment).filter(row => row.acrossRuns)) rows.push(['service_across_runs', row.group, '', row.metric, row.service, row.unit, 'available', ...summaryValues(row.summary), ''])
  }
  return rows.map(row => row.map(value => {const text = String(value); return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text}).join(',')).join('\n')
}

function StatisticsTable({datasets, metrics, experiment}: {datasets: LoadedDataset[]; metrics: string[]; experiment?: Experiment}) {
  const runPositions = new Map(experiment?.variants.flatMap(variant => variant.trials.map(trial => [trial.resultArtifactId, trial.position] as const)) ?? [])
  const orderedDatasets = [...datasets].sort((left, right) => {
    const position = (dataset: LoadedDataset) => runPositions.get(dataset.artifact.id) ?? Number(dataset.label.match(/Run (\d+)/i)?.[1] ?? 0)
    return position(left) - position(right)
  })
  const comparison = Boolean(experiment && experiment.variants.length > 1)
  const aggregates = experiment && !comparison ? variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2) : []
  const effects = experiment ? variantEffectSizes(experiment, datasets, metrics) : []
  return <>
    {experiment && experiment.variants.length > 1 && <StrategyComparison datasets={datasets} metrics={metrics} experiment={experiment} />}
    {!!effects.length && <section className="statistics-card"><div><h3>Difference between strategies</h3></div><div className="statistics-table-wrap"><ResponsiveTable className="effects-table"><thead><tr><th>Comparison</th><th>Metric</th><th title="Standardized effect size">Cohen's d</th><th>Difference</th><th>Runs</th></tr></thead><tbody>{effects.map(row => <tr key={row.metric}><td>{row.left} vs {row.right}</td><td>{statisticsMetricTitle(row.metric)}</td><td>{row.value.toFixed(3)}</td><td>{effectMagnitude(row.value)}</td><td>{row.leftCount} / {row.rightCount}</td></tr>)}</tbody></ResponsiveTable></div></section>}
    {!!aggregates.length && <section className="statistics-card"><div><h3>Across runs</h3></div><div className="statistics-table-wrap"><ResponsiveTable className="aggregate-table"><thead><tr><th>Strategy</th><th>Metric</th><th>Runs</th><th>Runs missing</th><th>Mean across runs</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>Variation</th><th>95% interval</th></tr></thead><tbody>{aggregates.map(row => <tr key={`${row.variant}-${row.metric}`}><td>{row.variant}</td><td>{statisticsMetricTitle(row.metric)}</td><td>{row.summary.count}</td><td>{row.missing}</td><td>{formatMetric(row.summary.mean, row.unit)}</td><td>{formatMetric(row.summary.standardDeviation, row.unit)}</td><td>{formatMetric(row.summary.min, row.unit)}</td><td>{formatMetric(row.summary.median, row.unit)}</td><td>{formatMetric(row.summary.max, row.unit)}</td><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(row.summary.confidence95Low, row.unit)}–{formatMetric(row.summary.confidence95High, row.unit)}</td></tr>)}</tbody></ResponsiveTable></div></section>}
    {!comparison && <><h3>Within each run</h3>
    {orderedDatasets.map(dataset => <section className="statistics-card" key={dataset.artifact.id}><div><h3>{dataset.label}</h3></div><div className="statistics-table-wrap"><ResponsiveTable className="runs-table" aria-label={`Statistics for ${dataset.label}`}><thead><tr><th>Metric</th><th>Samples / events</th><th>Mean / total</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>Variation</th><th>95% interval</th></tr></thead><tbody>{metrics.map(metric => <RunStatisticsRow key={metric} dataset={dataset} metric={metric} />)}</tbody></ResponsiveTable></div><ServiceStatisticsTable datasets={[dataset]} /></section>)}
    </>}
    {!comparison && experiment && serviceStatisticsRows(datasets, experiment).some(row => row.acrossRuns) && <section className="statistics-card"><ServiceStatisticsTable datasets={datasets} experiment={experiment} /></section>}
  </>
}

function StrategyComparison({datasets, metrics, experiment}: {datasets: LoadedDataset[]; metrics: string[]; experiment: Experiment}) {
  const columns = experiment.variants.map(variant => ({
    variant,
    rows: variantStatistics({...experiment, variants: [variant]}, datasets, metrics),
    available: variant.trials.filter(trial => datasets.some(dataset => dataset.artifact.id === trial.resultArtifactId)).length,
  }))
  const visibleMetrics = metrics.filter(metric => columns.some(column => column.rows.some(row => row.metric === metric)))
  if (!visibleMetrics.length) return null
  return <section className="statistics-card">
    <div><h3>Strategy comparison</h3></div>
    <div className="statistics-table-wrap"><ResponsiveTable aria-label="Strategy comparison">
      <thead><tr><th>Metric</th>{columns.map(({variant, available}) => <th key={variant.id}>{variant.alias || variant.name}<small className="muted"> · {available}/{variant.trials.length} runs</small></th>)}</tr></thead>
      <tbody>{visibleMetrics.map(metric => <tr key={metric}>
        <td>{statisticsMetricTitle(metric)}</td>
        {columns.map(({variant, rows}) => {
          const row = rows.find(item => item.metric === metric)
          const aggregation = metric === 'descheduler.evictions' ? 'Total evictions per run' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : isControlMetric(metric) ? 'Mean per cycle' : 'Mean over time windows'
          return <td key={variant.id} title={row ? `${aggregation}; ${row.summary.count} run(s) with this metric${row.summary.count > 1 ? '; mean ± sample standard deviation across runs' : ''}` : 'No collected data'}>{row ? <>{formatMetric(row.summary.mean, row.unit)}{row.summary.count > 1 && <> ± {formatMetric(row.summary.standardDeviation, row.unit)}</>}</> : '—'}</td>
        })}
      </tr>)}</tbody>
    </ResponsiveTable></div>
  </section>
}

function RunStatisticsRow({dataset, metric}: {dataset: LoadedDataset; metric: string}) {
  const series = aggregateMetric([dataset], metric)[0]
  const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
  if (!series && !control) return null
  const unit = series?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
  const name = <td title={control ? controlMetricDescription(metric, control.scope, control.reason, control.timestampResolutionSeconds) : undefined}>{statisticsMetricTitle(metric)}</td>
  const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
  if (podSummary) return <tr>{name}<td>{podSummary.count}</td><td title="Mean per pod">{formatMetric(podSummary.mean, unit)}</td><td>{formatMetric(podSummary.standardDeviation, unit)}</td><td>{formatMetric(podSummary.min, unit)}</td><td>{formatMetric(podSummary.median, unit)}</td><td>{formatMetric(podSummary.max, unit)}</td><td>{(podSummary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(podSummary.confidence95Low, unit)}–{formatMetric(podSummary.confidence95High, unit)}</td></tr>
  if (isControlMetric(metric)) {
    const value = control?.total ?? control?.mean
    return <tr>{name}<td>{control?.count ?? '—'}</td><td title={control?.total !== undefined || metric === 'descheduler.evictions' ? 'Run total' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : 'Mean per cycle'}>{value === undefined ? '—' : formatMetric(value, unit)}</td>{Array.from({length: 6}, (_, index) => <td key={index}>—</td>)}</tr>
  }
  const summary = summarize(series!.points.map(point => point.value))
  return <tr>{name}<td>{summary.count}</td><td title="Mean over time">{formatMetric(summary.mean, unit)}</td><td>{formatMetric(summary.standardDeviation, unit)}</td><td>{formatMetric(summary.min, unit)}</td><td>{formatMetric(summary.median, unit)}</td><td>{formatMetric(summary.max, unit)}</td><td>{(summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(summary.confidence95Low, unit)}–{formatMetric(summary.confidence95High, unit)}</td></tr>
}

export function podSchedulingStatistics(dataset: LoadedDataset): ScientificSummary | undefined {
  const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === 'scheduler.pod_scheduling_latency')
  if (control?.status !== 'available') return undefined
  const values = new Map<string, number>()
  for (const series of dataset.dataset.spec.series.filter(item => item.metric === control.metric)) {
    const uid = series.labels.pod_uid
    if (!uid) return undefined
    for (const point of series.points) {
      if (!Number.isFinite(point.value) || point.value < 0 || values.has(uid) && values.get(uid) !== point.value) return undefined
      values.set(uid, point.value)
    }
  }
  if (!values.size || values.size !== control.count) return undefined
  const summary = summarize([...values.values()])
  if (control.mean === undefined || Math.abs(summary.mean - control.mean) > Math.max(1, control.mean) * 1e-6) return undefined
  return summary
}

function metricRunValue(dataset: LoadedDataset, metric: string): number | undefined {
  if (isControlMetric(metric)) {
    const summary = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
    return summary?.total ?? summary?.mean
  }
  const series = aggregateMetric([dataset], metric)[0]
  return series?.points.length ? series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length : undefined
}

export function variantStatistics(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return experiment.variants.flatMap(variant => metrics.map(metric => {
    const values: number[] = []
    let unit = ''
    for (const trial of variant.trials) {
      const dataset = byArtifact.get(trial.resultArtifactId)
      if (!dataset) continue
      const value = metricRunValue(dataset, metric)
      if (value === undefined) continue
      unit ||= aggregateMetric([dataset], metric)[0]?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
      values.push(value)
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
    const value = metricRunValue(dataset, metric)
    if (value !== undefined) values.push(value)
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

function formatDuration(milliseconds: number): string {
  const seconds = Math.max(0, Math.round(milliseconds / 1000))
  if (seconds >= 3600) return `${(seconds / 3600).toFixed(1)}h`
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`
  return `${seconds}s`
}

function plotLabel(value: string): string {
  return value.replace(/ · Run \d+$/i, '')
}

function validateDataset(dataset: TimeSeriesDataset) {
  if (dataset.apiVersion !== 'artifacts.kubephos.dev/v1alpha1' || dataset.kind !== 'TimeSeriesDataset' || dataset.metadata?.version !== 'v1alpha1' || !dataset.spec?.applicationRef || !dataset.spec?.namespace || !Array.isArray(dataset.spec?.series) || !dataset.spec.series.length) throw new Error('This run has no compatible metric data.')
  for (const series of dataset.spec.series) {
    if (!series.metric || !series.unit || !Array.isArray(series.points) || !series.points.length || series.points.some(point => !Number.isFinite(point.value) || !Number.isFinite(new Date(point.timestamp).getTime()))) throw new Error('This run contains invalid metric samples.')
  }
}

function datasetLabel(artifact: Artifact, operations: Operation[]): string {
  const operation = operations.find(item => item.id === artifact.operationId)
  return operation?.title ?? artifact.name
}

function isWindowP95(metric: string): boolean {
  return metric.endsWith('p95_response_time')
}

function statisticsMetricTitle(metric: string): string {
  return isWindowP95(metric) ? metric === 'load.p95_response_time' ? 'Response time P95 per window' : metric === 'application.p95_response_time' ? 'Service response time P95 per window' : 'Redis operation P95 per window' : metricTitle(metric)
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
    'load.successful_rps': 'Throughput',
    'load.failed_rps': 'Failed request rate',
    'load.failure_percentage': 'Failure percentage',
    'load.p95_response_time': 'P95 response time',
    'load.slo_violation_percentage': 'SLO violation windows',
    'redis.operation_rps': 'Throughput per Redis operation',
    'redis.operation_p95_response_time': 'P95 per Redis operation',
    'application.request_rps': 'Throughput per service',
    'application.p95_response_time': 'Response time per service',
    'cpu.cores': 'CPU usage',
    'memory.working_set': 'Memory working set',
    'scheduler.pod_scheduling_latency': 'Scheduling latency',
    'scheduler.cycle_duration': 'Scheduling cycle duration',
    'descheduler.cycle_duration': 'Descheduling cycle duration',
    'descheduler.evictions': 'Descheduler evictions',
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
  if (unit === 'cores') return value < 1 ? `${(value * 1000).toFixed(0)} mCPU` : `${value.toFixed(2)} cores`
  if (unit === 'requests_per_second') return `${value.toFixed(2)} req/s`
  if (unit === 'milliseconds') return `${value.toFixed(1)} ms`
  if (unit === 'percent') return `${value.toFixed(2)}%`
  if (unit === 'bytes_per_second') return `${formatMetric(value, 'bytes')}/s`
  return value.toLocaleString(undefined, {maximumFractionDigits: 2})
}

function EmptyResults({experiment, runStatus}: {experiment?: Experiment; runStatus?: string}) {
  const status = runStatus ?? experiment?.status
  const queued = status === 'queued'
  const running = status && ['running', 'starting', 'prechecking', 'verifying'].includes(status)
  const failed = status === 'failed'
  return <div className="empty-state"><div><strong>{queued ? 'Waiting to start' : running ? 'Runs in progress' : failed ? 'No results from this experiment' : 'No results yet'}</strong>{queued ? 'Results will appear after this run starts and finishes.' : running ? 'Results and plots appear here as runs finish.' : failed ? 'Open the run above to inspect its logs and health checks.' : experiment ? 'Run this experiment to collect metrics and plots.' : 'Choose an experiment or run to explore verified data.'}</div></div>
}
