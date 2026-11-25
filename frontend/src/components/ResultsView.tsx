import {unpackSeries} from '../comparisonPreparation'
import {usePreparedComparison} from '../usePreparedComparison'
import {responseTimeSLO, addDerivedMetrics, aggregateMetric, aggregatePlotMetric, isControlMetric, comparisonCSV, compareRunValues, runLevelValues, serviceMetricStatistics, serviceStatisticsMetrics, serviceStatisticsRows, statisticsCSV, validateDataset, type AggregateSeries, type LoadedDataset, type RunLevelMetric, type VariantComparison} from '../resultsData'
export {serviceMetricStatistics, aggregateMetric, aggregatePlotMetric, summarize, statisticsCSV, podSchedulingStatistics, variantStatistics, cohenD} from '../resultsData'
export type {LoadedDataset, AggregateSeries} from '../resultsData'
import {PlacementReplay} from './PlacementReplay'
import {PlacementHeatmap} from './PlacementHeatmap'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {ResourceSharing, canManageResource} from './ResourceSharing'
import {ActionIcon} from './ActionIcon'
import {downloadBlob} from '../charts'
export {figureDimensions, niceAxis, setPNGResolution, svgToPDF} from '../charts'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useCallback, useEffect, useMemo, useRef, useState, type FormEvent, type ReactNode} from 'react'
import {request} from '../api'
import {openClusterLens, openLiveDashboard} from '../liveDashboard'
import {experimentDeletionMessage, experimentDisplayStatus, formatBytes, formatDate, isTerminalExperiment} from '../lib'
import type {Artifact, Experiment, ExperimentFigure, ExperimentTrial, Operation, Session, TimeSeriesDataset, Workspace} from '../types'
import {Dialog} from './Dialog'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {LoadProfilePreview, loadProfileSteps, type LoadProfileStep} from './LoadProfileEditor'
import {comparisonMetricCompatible, comparisonWarnings} from '../comparisons'
import {ResourceAttribution} from './ResourceAttribution'
import {trialLabel} from './ExperimentTrialNotes'
import {cleanupWarning, executedPilotCount, isPilot, pilotArtifactIDs, provenanceFor, restingUntil, withoutPilots, type ResolvedProvenance} from './results/runs'
import {useFigureSettings} from './results/figureSettings'
import {fidelityPlot, loadFidelity} from './results/loadFidelity'
import {percentileLadder} from './results/percentiles'
import {locustGroups, type LocustGroup} from './results/locust'
import {comparisonMethodsSentence, concurrentLoadWarning, loadProfileOf, measuredSeconds, methodsParagraph, runContextSummary, type RunContextSummary} from './results/context'
import {AcrossRunsTable, ComparisonTable, ServiceStatisticsTable, StatisticsTabs, StrategyComparison, WithinRunStatistics, type ProvenanceOf, type StatisticsView} from './results/StatisticsTables'
import {SectionNav, scrollToElement, type SectionLink} from './results/SectionNav'
import {hasAutoscaler, resourceCards, resourceSummary, type ResourceSummary} from './results/resources'
import {ShowEverything} from './results/DataTable'
import {cpuSaturationNotices, experimentCleanupNotices, FidelitySummaryTable, LocustSummaryPanel, type QualityNotice} from './results/LoadPanels'
import {MethodsPanel, RunContextPanel} from './results/ContextPanel'
import {CallChainPanel, hasCallChainData} from './results/CallChainPanel'
import {formatDuration, metricTitle} from './results/format'
import {runInsights, type Insight, type ServiceHealth} from './results/insights'
import {comparisonVerdict, kpiCards, overviewGroups, overviewMetrics, paletteColor, primaryPanels, timelineHeadline, timelineSubtitle, rankServiceSummaries, serviceCurve, serviceDetailMetrics, type OverviewGroup} from './results/overview'
import {Disclosure, KpiCards, PrimaryChart, QualityLine, ServicesTable, VerdictStrip, type InsightTarget, type ServiceDetail} from './results/Overview'
import {HeaderMenu} from './results/HeaderMenu'
import {AllMetrics} from './results/AllMetrics'
import {RunFacts} from './results/RunFacts'
import {KubeStudio, type StudioRequest} from './results/kubestudio/KubeStudio'
import type {StudioData} from './results/kubestudio/image'
import {ResultFigures} from './results/kubestudio/ResultFigures'
import {useSavedFigures, type SavedFigure} from './results/kubestudio/savedFigures'
import {KubeStudioMark} from './results/kubestudio/KubeStudioMark'
import {formatSpan} from './results/display'
import type {ServiceRow} from './results/Figures'
import type {LoadFidelity} from './results/loadFidelity'
import {withDisplayColors} from './results/chartColor'
import {useChartMode} from './results/useChartMode'
import {SeriesNotesContext} from './results/TimeChart'
import type {XYSource} from './results/xy'
import {naturalCompare} from '../statistics'
import './results/results.css'

// Metrics that the collector records per service (service filter in All metrics / X–Y).
const perServiceMetrics = ['application.p95_response_time', 'application.request_rps', 'cpu.cores', 'memory.working_set', 'deployment.ready_replicas', 'pod.restarts', 'application.p50_response_time', 'application.p99_response_time', 'application.mean_response_time']

// Legend tooltip notes for variants whose chosen colour is shown adjusted.
const colorNotes = (groups: OverviewGroup[] | undefined) => Object.fromEntries((groups ?? []).filter(group => group.colorNote).map(group => [group.label, group.colorNote!]))
export {formatAxis, compactLabel} from './results/format'

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
  // `notice` renders just below the page header (e.g. comparison integrity).
  comparison?: {sourceConfigurations: Record<string, Record<string, unknown>>; actions?: ReactNode; sources?: ReactNode; notice?: ReactNode}
}

// Secondary content: collapsed by default, opened on demand (or by a verdict link).
type SectionID = 'sources' | 'endpoints' | 'callchain' | 'statistics' | 'environment' | 'placement' | 'metrics'
type Sections = Partial<Record<SectionID, boolean>>

// Load profile shared by every configuration (annotations would mislead otherwise).
function sharedLoadProfile(configurations: Array<Record<string, unknown> | undefined>) {
  const profiles = configurations.map(loadProfileOf)
  return profiles[0] && profiles.every(item => JSON.stringify(item) === JSON.stringify(profiles[0])) ? profiles[0] : undefined
}

export function ResultsView(props: ResultsViewProps) {
  return props.comparison && props.focusedExperiment ? <ComparisonResults {...props} experiment={props.focusedExperiment} /> : <ExperimentResultsView {...props} />
}

function useProvenance(datasets: LoadedDataset[]): ProvenanceOf {
  return useMemo(() => {
    const cache = new Map<string, ResolvedProvenance>()
    return (metric: string) => {
      if (!cache.has(metric)) cache.set(metric, provenanceFor(datasets, metric))
      return cache.get(metric)!
    }
  }, [datasets])
}

// Memoised per-metric curves: the overview, All metrics and KubeStudio share them.
function usePlots(datasets: LoadedDataset[], experiment?: Experiment): (metric: string) => AggregateSeries[] {
  return useMemo(() => {
    const cache = new Map<string, AggregateSeries[]>()
    return (metric: string) => {
      if (!cache.has(metric)) cache.set(metric, aggregatePlotMetric(datasets, metric, experiment))
      return cache.get(metric)!
    }
  }, [datasets, experiment])
}

// Warnings about the validity of the measurement (the verdict's collapsed notes).
function qualityNotices(datasets: LoadedDataset[], groupOf: (item: LoadedDataset) => {id: string; label: string}): QualityNotice[] {
  const notices: QualityNotice[] = []
  for (const item of datasets) for (const warning of loadFidelity(item).warnings) notices.push({kind: 'warning', text: `${item.label}: ${warning}`})
  notices.push(...cpuSaturationNotices(locustGroups(datasets, groupOf)))
  for (const entry of runContextSummary(datasets).concurrentLoad) notices.push({kind: 'warning', text: concurrentLoadWarning(entry)})
  return notices
}

type ComparisonOptions = {baseline: string; mode: 'baseline' | 'all-pairs'}
const comparisonOptionsDefault = (experiment: Experiment | undefined): ComparisonOptions => ({baseline: experiment?.variants[0]?.id ?? '', mode: 'baseline'})

const sectionAnchor: Partial<Record<InsightTarget, {id: string; section?: SectionID}>> = {
  throughput: {id: 'results-timeline'}, latency: {id: 'results-timeline'}, services: {id: 'results-services'},
  endpoints: {id: 'section-endpoints', section: 'endpoints'}, quality: {id: 'section-environment', section: 'environment'}
}

function useSections(initial: Sections = {}) {
  const [sections, setSections] = useState<Sections>(initial)
  const toggle = useCallback((id: SectionID, open: boolean) => setSections(current => ({...current, [id]: open})), [])
  const navigate = useCallback((target: InsightTarget) => {
    const anchor = sectionAnchor[target]
    if (!anchor) return
    if (anchor.section) setSections(current => ({...current, [anchor.section!]: true}))
    requestAnimationFrame(() => {
      const element = document.getElementById(anchor.id)
      if (!element) return
      scrollToElement(element)
      element.querySelector<HTMLElement>('button, h3')?.focus?.({preventScroll: true})
    })
  }, [])
  // Section bar: a collapsed section opens before the jump; focus moves to its heading.
  const jump = useCallback((link: SectionLink) => {
    const section = link.target.startsWith('section-') ? link.target.slice('section-'.length) as SectionID : undefined
    if (section) setSections(current => ({...current, [section]: true}))
    requestAnimationFrame(() => {
      const element = document.getElementById(link.target)
      if (!element) return
      scrollToElement(element)
      element.querySelector<HTMLElement>('.disclosure-heading button, h3')?.focus?.({preventScroll: true})
    })
  }, [])
  return {sections, toggle, navigate, jump}
}

// Links of the section bar, in page order; absent sections are left out.
function sectionLinks({overview, figure, services, endpoints, callchain, statistics, environment, placement, metrics, sources}: Partial<Record<'overview' | 'figure' | 'services' | 'endpoints' | 'callchain' | 'statistics' | 'environment' | 'placement' | 'metrics' | 'sources', boolean>>): SectionLink[] {
  const all: Array<[boolean | undefined, SectionLink]> = [
    [overview, {id: 'overview', label: 'Overview', target: 'results-overview'}],
    [figure, {id: 'figure', label: 'Fig. 1', target: 'results-timeline'}],
    [services, {id: 'services', label: 'Services', target: 'results-services'}],
    [sources, {id: 'sources', label: 'Sources', target: 'section-sources'}],
    [endpoints, {id: 'endpoints', label: 'Endpoints', target: 'section-endpoints'}],
    [callchain, {id: 'callchain', label: 'Call chain', target: 'section-callchain'}],
    [statistics, {id: 'statistics', label: 'Statistics', target: 'section-statistics'}],
    [environment, {id: 'environment', label: 'Environment', target: 'section-environment'}],
    [placement, {id: 'placement', label: 'Placement', target: 'section-placement'}],
    [metrics, {id: 'metrics', label: 'All metrics', target: 'section-metrics'}]
  ]
  return all.filter(([present]) => present).map(([, link]) => link)
}

// "2 variants × 3 repetitions · 5 min measured · 19 Nov 2026".
function ResultsMeta({experiment, datasets, status, run}: {experiment?: Experiment; datasets: LoadedDataset[]; status?: ReactNode; run?: string}) {
  // One execution: its name; the timing facts sit in the facts row below.
  if (run) return <p className="results-meta">{status}<span>{run}</span></p>
  const variants = experiment?.variants.length ?? 0
  const repetitions = experiment ? Math.max(0, ...experiment.variants.map(variant => variant.trials.filter(trial => !isPilot(trial)).length)) : 0
  const measured = datasets.map(measuredSeconds).filter((value): value is number => value !== undefined)
  const span = measured.length ? Math.max(...measured) : undefined
  const started = datasets.map(item => item.dataset.spec.workloadStartedAt ?? item.dataset.spec.start).filter(Boolean).sort()[0] ?? experiment?.createdAt
  const shape = experiment ? experiment.kind === 'comparison' ? `${variants} series` : `${variants} variant${variants === 1 ? '' : 's'} × ${repetitions} repetition${repetitions === 1 ? '' : 's'}` : `${datasets.length} execution${datasets.length === 1 ? '' : 's'}`
  return <p className="results-meta">{status}<span>{shape}</span>{span !== undefined && <span>{formatSpan(span * 1000)} measured</span>}{started && <span>{formatDate(started)}</span>}</p>
}

interface OverviewModel {
  groups: OverviewGroup[]
  insights: Insight[]
  notices: QualityNotice[]
  runLevel: RunLevelMetric[]
  comparisons: VariantComparison[]
  baseline?: string
  slo?: number
  plot: (metric: string) => AggregateSeries[]
  provenance: (metric: string) => ResolvedProvenance | undefined
  services: (group: string | undefined) => ServiceHealth[]
  serviceTrend?: (group: string | undefined, service: string) => AggregateSeries | undefined
  serviceDetail?: (group: string | undefined, service: string) => ServiceDetail[]
  subtitle: string
  servicesNote?: string
  // The verdict already lists the between-variant differences (comparison page).
  comparisonVerdict?: boolean
  // Scaling, CPU, memory and restarts per group (absent in older comparison payloads).
  resources?: ResourceSummary[]
}

// Verdict, KPI cards, the primary chart and the services table: the default view.
// single: one execution — no summary paragraph, only the measurement warnings that matter.
function ResultsOverview({model, onNavigate, single = false}: {model: OverviewModel; onNavigate: (target: InsightTarget) => void; single?: boolean}) {
  const cards = useMemo(() => kpiCards({runLevel: model.runLevel, comparisons: model.comparisons, baseline: model.baseline, slo: model.slo}), [model.runLevel, model.comparisons, model.baseline, model.slo])
  const resources = useMemo(() => resourceCards(model.groups, model.resources), [model.groups, model.resources])
  // Several variants: the strongest significant difference sits right after the first finding.
  const insights = useMemo(() => {
    if (model.comparisonVerdict || model.groups.length < 2) return model.insights
    const differences = comparisonVerdict(cards, []).filter(item => item.id !== 'no-difference')
    return [...model.insights.slice(0, 1), ...differences.slice(0, 1), ...model.insights.slice(1), ...differences.slice(1)]
  }, [model.insights, model.comparisonVerdict, model.groups.length, cards])
  const panels = useMemo(() => primaryPanels({groups: model.groups, plot: model.plot, slo: model.slo}), [model.groups, model.plot, model.slo])
  const headline = useMemo(() => timelineHeadline({groups: model.groups, plot: model.plot, slo: model.slo}), [model.groups, model.plot, model.slo])
  const [group, setGroup] = useState<string | undefined>(model.groups[0]?.id)
  const activeGroup = model.groups.some(item => item.id === group) ? group : model.groups[0]?.id
  const services = useMemo(() => model.services(model.groups.length > 1 ? activeGroup : undefined), [model, activeGroup])
  const scope = model.groups.length > 1 ? activeGroup : undefined
  return <>
    {single ? <QualityLine notices={model.notices} /> : <VerdictStrip insights={insights} notices={model.notices} onNavigate={onNavigate} />}
    <KpiCards cards={cards} provenance={model.provenance} resources={resources} />
    <PrimaryChart panels={panels} subtitle={model.subtitle} title={headline} />
    {(services.length > 0 || model.groups.length > 1) && <ServicesTable services={services} groups={model.groups} group={activeGroup} onGroup={setGroup} note={model.servicesNote}
      trend={model.serviceTrend ? service => model.serviceTrend!(scope, service) : undefined}
      detail={model.serviceDetail ? service => model.serviceDetail!(scope, service) : undefined} />}
  </>
}

function ExperimentResultsView({artifacts, experiments, operations, workspaces, session, changed, openOperation, openPipelineRun, focusedExperiment, onBack, comparison}: ResultsViewProps) {
  const {confirmDelete, confirmAction, confirmationDialog} = useDeleteConfirmation()
  const available = useMemo(() => artifacts.filter(item => !item.sensitive && item.type === 'TimeSeriesDataset' && item.version === 'v1alpha1').sort((left, right) => right.verifiedAt.localeCompare(left.verifiedAt)), [artifacts])
  const datasetCache = useRef(new Map<string, TimeSeriesDataset>())
  const availableByID = useMemo(() => new Map(available.map(item => [item.id, item])), [available])
  const pilots = pilotArtifactIDs(focusedExperiment)
  const focusedArtifactIDs = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(Boolean) ?? []
  // Pilot repetitions stay viewable individually but never enter the aggregate view.
  const analysisArtifactIDs = focusedArtifactIDs.filter(id => !pilots.has(id))
  const selectable = focusedExperiment ? available.filter(item => focusedArtifactIDs.includes(item.id)) : available
  const availableKey = available.map(item => `${item.id}:${item.digest}`).join('|')
  const [selected, setSelected] = useState<string[]>(() => analysisArtifactIDs.filter(id => availableByID.has(id)))
  const [loaded, setLoaded] = useState<LoadedDataset[]>([])
  const [error, setError] = useErrorState()
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [printPending, setPrintPending] = useState(false)
  const [activeExperiment, setActiveExperiment] = useState<string | null>(focusedExperiment?.id ?? null)
  const {sections, toggle: toggleSection, navigate, jump} = useSections()
  const [studio, setStudio] = useState<StudioRequest | null>(null)
  // Figures kept on this page with KubeStudio; a deleted one can be restored once.
  const savedFigures = useSavedFigures(`experiment:${focusedExperiment?.id ?? 'selection'}`)
  const [deletedFigure, setDeletedFigure] = useState<{figure: SavedFigure; index: number}>()
  const keepFigure = (figure: Omit<SavedFigure, 'id'>, id?: string): SavedFigure => {
    if (!id) return savedFigures.add(figure)
    savedFigures.update({...figure, id})
    return {...figure, id}
  }
  const deleteFigure = (figure: SavedFigure) => {
    setDeletedFigure({figure, index: savedFigures.figures.findIndex(item => item.id === figure.id)})
    savedFigures.remove(figure.id)
  }
  const chartMode = useChartMode()
  const [runTab, setRunTab] = useState('summary')
  const [initialized, setInitialized] = useState(Boolean(focusedExperiment))
  const [figureSettings, setFigureSettings] = useFigureSettings(`experiment:${focusedExperiment?.id ?? 'selection'}`)
  useEffect(() => {
    if (!printPending || !sections.statistics) return
    const frame = requestAnimationFrame(() => {window.print(); setPrintPending(false)})
    return () => cancelAnimationFrame(frame)
  }, [printPending, sections.statistics])
  const [deleting, setDeleting] = useState(false)
  const [stopping, setStopping] = useState(false)
  const [stopRequestedId, setStopRequestedId] = useState<string | null>(null)
  const stopRequested = Boolean(focusedExperiment && (focusedExperiment.cancelRequested || stopRequestedId === focusedExperiment.id))
  const [actionError, setActionError] = useErrorState()
  const focusedRuns = focusedExperiment?.variants.flatMap(variant => variant.trials.map(trial => ({variant, trial}))) ?? []
  const activeRun = focusedRuns.find(item => item.trial.id === runTab)
  const liveRun = focusedRuns.find(item => ['running','initializing','initialized'].includes(item.trial.status))
  const viewSelected = activeRun ? activeRun.trial.resultArtifactId && availableByID.has(activeRun.trial.resultArtifactId) ? [activeRun.trial.resultArtifactId] : [] : focusedExperiment ? analysisArtifactIDs.filter(id => availableByID.has(id)) : selected
  const selectedKey = viewSelected.map(id => `${id}:${availableByID.get(id)?.digest ?? ''}:${responseTimeSLO(focusedExperiment, id, comparison?.sourceConfigurations[id]) ?? ''}`).join('|')

  useEffect(() => {setRunTab('summary')}, [focusedExperiment?.id])

  useEffect(() => {
    if (!focusedExperiment || isTerminalExperiment(focusedExperiment.status)) setStopRequestedId(null)
  }, [focusedExperiment?.id, focusedExperiment?.status])

  useEffect(() => {
    setSelected(current => current.filter(id => availableByID.has(id)))
  }, [availableKey])

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
    const loadDataset = async (id: string) => {
      const artifact = availableByID.get(id)
      if (!artifact) throw new Error('A selected run is no longer available.')
      const slo = responseTimeSLO(focusedExperiment, id, comparison?.sourceConfigurations[id])
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
    }
    const loadSelected = async () => {
      const values: LoadedDataset[] = []
      for (let offset = 0; offset < viewSelected.length; offset += 4) {
        if (controller.signal.aborted) throw new DOMException('Aborted', 'AbortError')
        values.push(...await Promise.all(viewSelected.slice(offset, offset + 4).map(loadDataset)))
      }
      return values
    }
    loadSelected().then(values => {if (!controller.signal.aborted) setLoaded(values)}).catch(cause => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the selected executions.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [selectedKey])

  const toggle = (id: string) => {
    if (!focusedExperiment) setActiveExperiment(null)
    setNotice('')
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : (focusedExperiment || activeExperiment || current.length < 8) ? [...current, id] : current)
  }
  const experiment = focusedExperiment ?? experiments.find(item => item.id === activeExperiment)
  // Statistics, aggregates and plots never include pilot (warm-up) repetitions.
  const analysisExperiment = useMemo(() => withoutPilots(experiment), [experiment])
  const statsExperiment = activeRun ? undefined : analysisExperiment
  const experimentPresentation = useMemo(() => {
    const presentation = new Map<string, {label: string; color?: string}>()
    for (const variant of experiment?.variants ?? []) for (const trial of variant.trials) if (trial.resultArtifactId) presentation.set(trial.resultArtifactId, {label: experiment?.kind === 'comparison' ? variant.alias || variant.name : `${variant.alias || variant.name} · ${trialLabel(trial)}`, color: variant.color})
    return presentation
  }, [experiment])
  const displayed = useMemo(() => loaded.filter(item => activeRun || !pilots.has(item.artifact.id)).map(item => ({...item, label: experimentPresentation.get(item.artifact.id)?.label ?? datasetLabel(item.artifact, operations), color: experimentPresentation.get(item.artifact.id)?.color})), [loaded, experimentPresentation, operations, activeRun?.trial.id, [...pilots].join('|')])
  const metrics = useMemo(() => [...new Set(displayed.flatMap(item => [...item.dataset.spec.series.map(series => series.metric), ...(item.dataset.spec.controlMetrics ?? []).filter(summary => summary.status !== 'not_configured').map(summary => summary.metric)]))].filter(metric => !comparison || comparisonMetricCompatible(displayed, metric)).sort(), [displayed, Boolean(comparison)])
  const statisticsMetrics = useMemo(() => [...new Set([...metrics, ...displayed.flatMap(item => item.dataset.spec.controlMetrics?.map(summary => summary.metric) ?? [])])].filter(metric => metric !== 'redis.operation_p95_response_time'), [metrics, displayed])
  const warnings = useMemo(() => comparison ? comparisonWarnings(displayed, comparison.sourceConfigurations) : [], [displayed, comparison?.sourceConfigurations])
  const provenance = useProvenance(displayed)
  const variantOf = (item: LoadedDataset) => {
    const variant = statsExperiment?.variants.find(entry => entry.trials.some(trial => trial.resultArtifactId === item.artifact.id))
    return variant ? {id: variant.id, label: variant.alias || variant.name} : {id: item.artifact.id, label: item.label}
  }
  const notices = useMemo(() => [...qualityNotices(displayed, variantOf), ...experimentCleanupNotices(activeRun ? undefined : experiment)], [displayed, statsExperiment, experiment, activeRun?.trial.id])
  const [comparisonOptions, setComparisonOptions] = useState(() => comparisonOptionsDefault(analysisExperiment))
  useEffect(() => setComparisonOptions(comparisonOptionsDefault(analysisExperiment)), [analysisExperiment?.id])
  const multipleVariants = Boolean(statsExperiment && (statsExperiment.kind === 'comparison' || statsExperiment.variants.length > 1))
  const runLevel = useMemo(() => statsExperiment ? runLevelValues(statsExperiment, displayed, statisticsMetrics) : [], [statsExperiment, displayed, statisticsMetrics])
  const comparisonRows = useMemo(() => multipleVariants ? compareRunValues(runLevel, comparisonOptions) : [], [multipleVariants, runLevel, comparisonOptions])

  // The overview groups runs by variant; a single run (or a free selection) gets one group per dataset.
  const overviewExperiment = useMemo((): Experiment | undefined => {
    if (!displayed.length) return undefined
    if (activeRun) return {...(experiment as Experiment), variants: [{...activeRun.variant, trials: [activeRun.trial]}]}
    if (statsExperiment) return statsExperiment
    return {id: 'selection', kind: 'comparison', name: 'Selection', description: '', status: 'succeeded', resultType: '', resultVersion: '', workspaceId: '', createdAt: '', updatedAt: '', variants: displayed.map(item => ({id: item.artifact.id, name: item.label, configuration: {}, trials: [{id: item.artifact.id, position: 1, status: 'succeeded', resultArtifactId: item.artifact.id}]}))} as unknown as Experiment
  }, [displayed, activeRun?.trial.id, statsExperiment, experiment])
  const plot = usePlots(displayed, overviewExperiment)
  const overview = useMemo((): OverviewModel | undefined => {
    if (!overviewExperiment || !displayed.length) return undefined
    const kpiRunLevel = runLevelValues(overviewExperiment, displayed, overviewMetrics.filter(metric => metrics.includes(metric)))
    const groups = withDisplayColors(overviewExperiment.variants.map((variant, index) => ({id: variant.id, label: variant.alias || variant.name, color: variant.color || paletteColor(index), present: variant.trials.some(trial => displayed.some(item => item.artifact.id === trial.resultArtifactId))})).filter(group => group.present).map(({present: _, ...group}) => group), chartMode)
    const datasetsOf = (group: string | undefined) => group === undefined ? displayed : displayed.filter(item => overviewExperiment.variants.find(variant => variant.id === group)?.trials.some(trial => trial.resultArtifactId === item.artifact.id))
    const safeInsights = (datasets: LoadedDataset[]) => {
      try {return runInsights(datasets, overviewExperiment)} catch (cause) {console.error('Could not compute insights', cause); return {insights: [], services: []}}
    }
    const all = safeInsights(displayed)
    const servicesCache = new Map<string, ServiceHealth[]>()
    const curves = new Map<string, AggregateSeries[]>()
    const curve = (group: string | undefined, metric: string, service: string) => {
      const key = JSON.stringify([group, metric, service])
      if (!curves.has(key)) curves.set(key, serviceCurve(datasetsOf(group), metric, service, overviewExperiment))
      return curves.get(key)!
    }
    const slos = new Set(displayed.map(item => item.dataset.spec.sloMilliseconds))
    const runs = overviewExperiment.variants.reduce((total, variant) => Math.max(total, variant.trials.filter(trial => displayed.some(item => item.artifact.id === trial.resultArtifactId)).length), 0)
    const resources = groups.map(group => resourceSummary(group.id, datasetsOf(group.id), hasAutoscaler(overviewExperiment.variants.find(variant => variant.id === group.id)?.configuration as Record<string, unknown> | undefined)))
    return {
      groups, insights: all.insights, notices, runLevel: kpiRunLevel, resources,
      comparisons: comparisonRows, baseline: comparisonOptions.baseline,
      slo: slos.size === 1 ? [...slos][0] : undefined,
      plot, provenance: metric => metrics.includes(metric) ? provenance(metric) : undefined,
      services: group => {
        if (group === undefined) return all.services
        if (!servicesCache.has(group)) servicesCache.set(group, safeInsights(datasetsOf(group)).services)
        return servicesCache.get(group)!
      },
      serviceTrend: (group, service) => curve(group, 'application.p95_response_time', service)[0],
      serviceDetail: (group, service) => serviceDetailMetrics.map(item => ({title: item.title, unit: item.unit, series: curve(group, item.metric, service)})),
      subtitle: timelineSubtitle(groups.length, runs)
    }
  }, [overviewExperiment, displayed, metrics, notices, comparisonRows, comparisonOptions.baseline, plot, provenance, chartMode])
  // X–Y relationship: one point per sampling window of each run, paired by timestamp.
  const xySource = useMemo((): XYSource | undefined => {
    if (!displayed.length) return undefined
    const services = new Map<string, string[]>()
    const variantOf = (item: LoadedDataset) => overviewExperiment?.variants.find(variant => variant.trials.some(trial => trial.resultArtifactId === item.artifact.id))
    return {
      metrics: metrics.filter(metric => !isControlMetric(metric)), level: 'run', provenance,
      services: metric => {
        if (!perServiceMetrics.includes(metric)) return undefined
        if (!services.has(metric)) services.set(metric, [...new Set(serviceMetricStatistics(displayed, metric, overviewExperiment).rows.map(row => row.service))].sort(naturalCompare))
        return services.get(metric)
      },
      runs: ({metric, service}) => displayed.flatMap(item => {
        const series = service ? serviceCurve([item], metric, service)[0] : aggregateMetric([item], metric)[0]
        if (!series) return []
        const variant = variantOf(item)
        return [{runID: item.artifact.id, runLabel: item.label, seriesID: variant?.id ?? item.artifact.id, seriesLabel: variant ? variant.alias || variant.name : item.label, color: variant?.color ?? item.color, series}]
      })
    }
  }, [displayed, metrics, provenance, overviewExperiment])

  const selectedWorkspaceID = availableByID.get(selected[0])?.workspaceId
  const openExperiment = (item: Experiment) => {
    const artifactIDs = item.variants.flatMap(variant => variant.trials.filter(trial => !isPilot(trial)).map(trial => trial.resultArtifactId)).filter(id => available.some(artifact => artifact.id === id))
    setActiveExperiment(item.id)
    setSelected(artifactIDs)
    setNotice(artifactIDs.length === 0 && item.status === 'succeeded' ? 'This experiment has no plottable metrics.' : '')
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
    if (!focusedExperiment || !['queued', 'running', 'initializing', 'initialized'].includes(focusedExperiment.status)) return
    const label = focusedExperiment.kind === 'suite' ? 'suite' : 'experiment'
    if (!await confirmAction(`Stop ${label} ${focusedExperiment.name}? The current execution will stop and the remaining executions will be canceled.`, {title: 'Stop experiment', label: 'Stop'})) return
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
  const [startingTraffic,setStartingTraffic]=useState(false)
  const startTraffic=async()=>{
    const initializedRun=focusedRuns.find(item=>item.trial.status==='initialized')
    if(!focusedExperiment||!initializedRun?.trial.pipelineRunId)return
    setStartingTraffic(true)
    try{await request(`/experiments/${focusedExperiment.id}/start-traffic`,{method:'POST',body:JSON.stringify({runId:initializedRun.trial.pipelineRunId})},session.csrfToken);await changed()}catch(cause){setActionError(cause instanceof Error?cause.message:"Could not start experiment traffic.")}finally{setStartingTraffic(false)}
  }
  // A run in progress can be followed live in the cluster's Grafana.
  const [openingDashboard, setOpeningDashboard] = useState(false)
  const liveDashboard = Boolean(!comparison && focusedExperiment && ['running', 'initializing', 'initialized'].includes(focusedExperiment.status))
  const openDashboard = async () => {
    if (!focusedExperiment) return
    setOpeningDashboard(true)
    setActionError('')
    try {
      await openLiveDashboard(`/experiments/${focusedExperiment.id}/live-dashboard`, session.csrfToken)
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : 'Could not open the live dashboard.')
    } finally {
      setOpeningDashboard(false)
    }
  }
  const [openingLens, setOpeningLens] = useState(false)
  const configurationId = activeRun?.variant.configurationId ?? focusedExperiment?.configurationId
  const openLens = async () => {
    if (!focusedExperiment) return
    setOpeningLens(true)
    try {
      const query = activeRun ? `?trialId=${encodeURIComponent(activeRun.trial.id)}` : ''
      await openClusterLens(`/experiments/${encodeURIComponent(focusedExperiment.id)}/cluster-lens${query}`)
    } catch (cause) {
      setActionError(cause instanceof Error ? cause.message : 'Could not open ClusterLens.')
    } finally {
      setOpeningLens(false)
    }
  }
  const displayStatus = focusedExperiment ? experimentDisplayStatus({...focusedExperiment, cancelRequested: stopRequested}) : ''
  const canStop = Boolean(focusedExperiment && ['queued', 'running', 'initializing', 'initialized'].includes(focusedExperiment.status))
  const canManage = Boolean(focusedExperiment && canManageResource(session, focusedExperiment.ownerUserId))
  const stopLabel = focusedExperiment?.kind === 'suite' ? 'suite' : 'experiment'
  const runLabels = Object.fromEntries([...experimentPresentation].map(([id, value]) => [id, value.label]))
  const pilotCount = activeRun ? 0 : executedPilotCount(experiment)
  const methods = useMemo(() => sections.environment ? methodsParagraph({experiment: statsExperiment, datasets: displayed, metrics: statisticsMetrics, configurations: comparison?.sourceConfigurations, pilots: pilotCount, comparison: multipleVariants ? {familySize: comparisonRows[0]?.familySize ?? 0, mode: comparisonOptions.mode, baseline: statsExperiment?.variants.find(variant => variant.id === comparisonOptions.baseline)?.alias || statsExperiment?.variants.find(variant => variant.id === comparisonOptions.baseline)?.name} : undefined}) : '', [sections.environment, statsExperiment, displayed, statisticsMetrics, pilotCount, comparisonRows, comparisonOptions])
  const placementDatasets = activeRun ? displayed.filter(item => item.artifact.id === activeRun.trial.resultArtifactId) : displayed
  const heatmap = !comparison && focusedExperiment && (activeRun || focusedExperiment.kind !== 'suite') && placementDatasets.some(item => item.dataset.spec.placement) ? <PlacementHeatmap datasets={placementDatasets.map(item => item.dataset)} canvasHeight={465} /> : undefined
  const locust = useMemo(() => locustGroups(displayed, variantOf), [displayed, statsExperiment])
  const fidelityRows = useMemo(() => displayed.map(item => ({label: item.label, fidelity: loadFidelity(item)})), [displayed])
  // Needed by KubeStudio and by the figures kept on the page.
  const figuresShown = savedFigures.figures.length > 0
  const studioData = useMemo((): StudioData | undefined => {
    if (!(studio || figuresShown) || !displayed.length) return undefined
    const plotted = metrics.filter(metric => !['scheduler.pod_scheduling_latency', 'scheduler.cycle_duration', 'descheduler.cycle_duration', 'descheduler.evictions'].includes(metric))
    const serviceCache = new Map<string, {rows: ServiceRow[]}>()
    return {
      metrics: plotted, plot, provenance, omitSLO: Boolean(comparison) && new Set(displayed.map(item => item.dataset.spec.sloMilliseconds)).size > 1, runLabels,
      runLevel: overviewExperiment ? runLevelValues(overviewExperiment, displayed, plotted) : [],
      ladder: percentileLadder(displayed, overviewExperiment), fidelity: fidelityPlot(displayed, overviewExperiment),
      serviceMetrics: plotted.filter(metric => serviceStatisticsMetrics.includes(metric)),
      services: metric => {if (!serviceCache.has(metric)) serviceCache.set(metric, serviceMetricStatistics(displayed, metric, overviewExperiment)); return serviceCache.get(metric)},
      placement: heatmap, xy: xySource, profile: sharedLoadProfile((overviewExperiment?.variants ?? []).map(variant => variant.configuration as Record<string, unknown> | undefined))
    }
  }, [Boolean(studio), figuresShown, displayed, metrics, plot, provenance, overviewExperiment, xySource])
  const byService = useCallback((metric: string) => {
    if (!perServiceMetrics.includes(metric)) return undefined
    const services = [...new Set(serviceMetricStatistics(displayed, metric, overviewExperiment).rows.map(row => row.service))]
    const mean = (series: AggregateSeries) => series.points.reduce((sum, point) => sum + point.value, 0) / Math.max(1, series.points.length)
    return services.map(service => serviceCurve(displayed, metric, service, overviewExperiment)[0] && {...serviceCurve(displayed, metric, service, overviewExperiment)[0], label: service}).filter((item): item is AggregateSeries => Boolean(item)).sort((a, b) => mean(b) - mean(a))
  }, [displayed, overviewExperiment])
  const callChain = displayed.some(item => hasCallChainData(item.dataset))
  const groupsForStudio = overview?.groups ?? []
  const seriesNotes = useMemo(() => colorNotes(overview?.groups), [overview])
  // A single execution (a run tab, or an experiment with one measured run) gets its key facts on top.
  const singleRun = useMemo(() => {
    if (!focusedExperiment || comparison || displayed.length !== 1) return undefined
    const measuredRuns = focusedRuns.filter(item => !isPilot(item.trial))
    const run = activeRun ?? (measuredRuns.length === 1 ? measuredRuns[0] : undefined)
    return run ? {name: `${run.variant.alias || run.variant.name} · ${trialLabel(run.trial)}`, item: displayed[0]} : undefined
  }, [focusedExperiment, comparison, displayed, activeRun?.trial.id, focusedRuns.length])
  const hasPlacement = Boolean(activeRun && !comparison && placementDatasets.some(item => item.dataset.spec.placement))
  const links = sectionLinks({overview: Boolean(overview), figure: Boolean(overview), services: Boolean(overview), endpoints: locust.length > 0, callchain: callChain, statistics: true, environment: true, placement: hasPlacement, metrics: true})
  return <ShowEverything.Provider value={printPending}><SeriesNotesContext.Provider value={seriesNotes}><div className="results-page">
    {confirmationDialog}
    <header className="page-header" id="results-top">{focusedExperiment && onBack && <DetailBreadcrumb parent={comparison ? 'Comparisons' : 'Experiments'} current={focusedExperiment.name} back={onBack} />}
      <div className={`results-heading ${focusedExperiment ? 'focused' : ''}`}>
        <div className="results-title"><h2 tabIndex={-1}>{focusedExperiment?.name ?? 'Results'}</h2>
          {focusedExperiment && <ResultsMeta run={singleRun?.name} experiment={activeRun ? undefined : withoutPilots(focusedExperiment)} datasets={displayed} status={!comparison ? <span className={`status ${activeRun?.trial.status ?? displayStatus}`}>{activeRun?.trial.status ?? displayStatus}</span> : undefined} />}
        </div>
        <div className="results-detail-actions">
          {comparison?.actions}
          {!comparison && focusedExperiment && configurationId && <a className="button secondary" href={`#experiments/configuration/${encodeURIComponent(configurationId)}`}>Configuration</a>}
          {!comparison && focusedExperiment?.status === 'running' && <button type="button" className="button secondary" disabled={openingLens || !(activeRun ? activeRun.trial.pipelineRunId : focusedRuns.some(item => item.trial.pipelineRunId))} onClick={() => void openLens()}>{openingLens ? 'Opening…' : 'ClusterLens'}</button>}
          {liveDashboard &&<button type="button" className="button secondary live-dashboard-button" disabled={openingDashboard} onClick={() => void openDashboard()}><ActionIcon name="pulse" />{openingDashboard ? 'Opening…' : 'Live dashboard'}</button>}
          {!comparison && focusedExperiment?.status === 'initialized' && canManage && !stopRequested && <button className="button primary" disabled={startingTraffic} onClick={startTraffic}>{startingTraffic ? 'Starting…' : 'Start'}</button>}
          {!comparison && focusedExperiment && canManage && canStop && <button className="button danger" disabled={stopping || stopRequested} onClick={stopExperiment}>{stopping || stopRequested ? `Stopping ${stopLabel}…` : `Stop ${stopLabel}`}</button>}
          {!comparison && focusedExperiment && <ResourceSharing kind="experiments" id={focusedExperiment.id} name={focusedExperiment.name} ownerUserId={focusedExperiment.ownerUserId} session={session} changed={changed} />}
          {displayed.length > 0 && <button type="button" className="button secondary" title="Open KubeStudio to build a publication figure" onClick={() => setStudio({})}><KubeStudioMark size={18} className="kubestudio-entry" />Create figure</button>}
          {displayed.length > 0 && <HeaderMenu label="Export" icon={<ActionIcon name="download" />}>{close => <ExportPanel experiment={statsExperiment} savedFigures={!comparison} datasets={displayed} csv={() => statisticsCSV(displayed, statisticsMetrics, statsExperiment)} comparisonRows={comparisonRows} onPrint={() => {close(); toggleSection('statistics', true); setPrintPending(true)}} />}</HeaderMenu>}
          {!comparison && focusedExperiment && canManage && <HeaderMenu label="" icon={<><svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true"><circle cx="3" cy="8" r="1.4" /><circle cx="8" cy="8" r="1.4" /><circle cx="13" cy="8" r="1.4" /></svg><span className="visually-hidden">More actions</span></>} buttonClass="icon-button more-actions">{close => <div className="menu-list"><button type="button" className="menu-item danger" disabled={deleting || !isTerminalExperiment(focusedExperiment.status)} title={isTerminalExperiment(focusedExperiment.status) ? undefined : `Stop this ${stopLabel} before deleting it`} onClick={() => {close(); void removeExperiment()}}><ActionIcon name="delete" />{deleting ? 'Deleting…' : `Delete ${stopLabel}`}</button></div>}</HeaderMenu>}
          {!focusedExperiment && <div className="results-count" aria-label={`${selectable.length} datasets`}><strong>{selectable.length}</strong><span>datasets</span></div>}
        </div>
      </div>
    </header>
    {comparison?.notice}
    {focusedExperiment && <ResourceAttribution resource={focusedExperiment} editable={Boolean(comparison)} />}
    {comparison?.sources}
    <ErrorFeedback message={actionError} />
    {!focusedExperiment && experiments.length > 0 && <div className="experiment-history"><div className="experiment-history-title"><span><b>1</b><strong>Choose an experiment</strong></span><span>{experiments.length} saved</span></div><div className="experiment-cards">{experiments.map(item => <button key={item.id} className={activeExperiment === item.id ? 'active' : ''} onClick={() => openExperiment(item)}><span className={`status-dot ${experimentDisplayStatus(item)}`} /><span><strong>{item.name}</strong><small>{item.status === 'queued' && item.scheduledFor && new Date(item.scheduledFor).getTime() > Date.now() ? `Scheduled ${formatDate(item.scheduledFor)}` : `${experimentDisplayStatus(item)} · ${item.variants.length} strategies · ${item.variants.reduce((total, variant) => total + variant.trials.filter(trial => !isPilot(trial)).length, 0)} executions`}</small></span><ActionIcon name="right" /></button>)}</div></div>}
    {focusedExperiment && !comparison && focusedRuns.length > 0 && <nav className="experiment-run-tabs" aria-label="Run executions"><button type="button" className="run-tab all-runs" aria-current={runTab === 'summary' ? 'page' : undefined} data-active={runTab === 'summary'} onClick={() => setRunTab('summary')}><span className="run-tab-label">All executions</span></button>{focusedRuns.map(({variant, trial}) => <RunTab key={trial.id} name={`${variant.name} · ${trialLabel(trial)}`} trial={trial} active={runTab === trial.id} select={() => setRunTab(trial.id)} />)}</nav>}
    {experiment && (!activeRun ? <>{experiment.status !== 'succeeded' && <ExperimentProgress experiment={experiment} />}{liveRun && <RunLoadProgress name={`${liveRun.variant.name} · ${trialLabel(liveRun.trial)}`} trial={liveRun.trial} configuration={liveRun.variant.configuration} operation={operations.find(item => item.id === liveRun.trial.operationId)} />}</> : <><RunDiagnostics trial={activeRun.trial} openPipelineRun={openPipelineRun} openOperation={openOperation} /><RunLoadProgress trial={activeRun.trial} configuration={activeRun.variant.configuration} operation={operations.find(item => item.id === activeRun.trial.operationId)} /></>)}
    {!selectable.length || activeRun && !viewSelected.length ? <EmptyResults experiment={experiment} runStatus={activeRun?.trial.status} /> : <div className={`results-layout ${focusedExperiment ? 'focused' : ''}`}>
      {!focusedExperiment ? <aside className="dataset-picker" aria-label="Available datasets">
        <div className="dataset-picker-title"><strong>Select executions</strong><span>{selected.length} selected</span></div>
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
        {activeRun && isPilot(activeRun.trial) && <div className="validation-item note pilot-note">Warm-up (pilot) execution: shown for inspection only and excluded from every statistic, aggregate and plot of this experiment.</div>}
        {comparison && !loading && warnings.map(warning => <div key={warning} className="validation-item warning">{warning}</div>)}
        {!viewSelected.length && <div className="results-placeholder"><strong>Select executions</strong></div>}
        {loading && <div className="results-placeholder" role="status"><span className="results-spinner" /><strong>Loading verified data…</strong></div>}
        {error && <ErrorFeedback message={error} />}
        {!loading && !error && displayed.length > 0 && <>
          {!focusedExperiment && <div className="results-workbench-head">
            <div><h3>{experiment?.name ?? displayed[0].label}</h3><p>{displayed.length} execution{displayed.length === 1 ? '' : 's'} · {metrics.length} metric{metrics.length === 1 ? '' : 's'}</p></div>
            <div className="results-heading-actions"><button className="button primary" disabled={selected.length < 2} onClick={() => setSaving(true)}>Save comparison</button></div>
          </div>}
          <SectionNav links={links} onJump={jump} top="results-top" />
          <div id="results-overview" className="section-anchor" />
          {singleRun && <RunFacts item={singleRun.item} name={singleRun.name} />}
          {overview && <ResultsOverview key={activeRun?.trial.id ?? 'all'} model={overview} onNavigate={navigate} single={Boolean(singleRun)} />}
          {overview && <ResultFigures figures={savedFigures.figures} data={studioData} groups={groupsForStudio} stored={savedFigures.stored}
            onAdd={() => setStudio({})} onEdit={figure => setStudio({figure})} onDelete={deleteFigure}
            deleted={deletedFigure?.figure.name} onUndo={() => {if (deletedFigure) savedFigures.restore(deletedFigure.figure, deletedFigure.index); setDeletedFigure(undefined)}} />}
          <div className="results-sections" role="region" aria-label="Details">
            {locust.length > 0 && <Disclosure id="endpoints" title="Endpoints" summary={`${locust.reduce((total, group) => total + group.rows.filter(row => !row.aggregated).length, 0)} measured by Locust`} open={Boolean(sections.endpoints)} onToggle={open => toggleSection('endpoints', open)}><LocustSummaryPanel groups={locust} /></Disclosure>}
            {callChain && <Disclosure id="callchain" title="Call chain" summary="calls per request, predicted against measured" open={Boolean(sections.callchain)} onToggle={open => toggleSection('callchain', open)}><CallChainPanel datasets={displayed} /></Disclosure>}
            <Disclosure id="statistics" title="Statistics" summary={multipleVariants ? 'comparison tests, effective sample size, CSV' : 'per-execution summaries, effective sample size, CSV'} open={Boolean(sections.statistics)} onToggle={open => toggleSection('statistics', open)}>
              <ExperimentStatistics datasets={displayed} metrics={statisticsMetrics} experiment={statsExperiment} provenance={provenance} comparisonRows={comparisonRows} comparisonOptions={comparisonOptions} setComparisonOptions={setComparisonOptions} fidelityRows={fidelityRows} />
            </Disclosure>
            <Disclosure id="environment" title="Environment & methods" summary="cluster, versions, windows and a methods paragraph" open={Boolean(sections.environment)} onToggle={open => toggleSection('environment', open)}>
              <RunContextPanel context={runContextSummary(displayed)} /><MethodsPanel text={methods} />
            </Disclosure>
            {hasPlacement && <Disclosure id="placement" title="Placement" summary="pod placement replay and zone heatmap" open={Boolean(sections.placement)} onToggle={open => toggleSection('placement', open)}>
              <PlacementReplay datasets={placementDatasets} />
              {heatmap && <div className="chart-grid figure-grid">{heatmap}</div>}
            </Disclosure>}
            <Disclosure id="metrics" title="All metrics" summary={`${metrics.length} recorded, plotted on request`} open={Boolean(sections.metrics)} onToggle={open => toggleSection('metrics', open)}>
              <AllMetrics metrics={metrics} plot={plot} byService={byService} groups={overview?.groups ?? []} provenance={provenance} onFigure={metric => setStudio({kind: 'timeseries', metric})} xy={xySource} onXYFigure={xy => setStudio({kind: 'xy', xy})} />
            </Disclosure>
          </div>
        </>}
      </div>
    </div>}
    {studio && <KubeStudio open onClose={() => setStudio(null)} request={studio} data={studioData} groups={groupsForStudio} settings={figureSettings} onSettings={setFigureSettings} storageKey={`experiment:${focusedExperiment?.id ?? 'selection'}`} onKeep={keepFigure} onSave={focusedExperiment && !comparison && focusedExperiment.kind !== 'comparison' ? async ({metric, format, bytes}) => {
      await request<ExperimentFigure>(`/experiments/${focusedExperiment.id}/figures?metric=${encodeURIComponent(metric)}&format=${format}`, {method: 'POST', headers: {'Content-Type': 'application/pdf', 'X-KubePhos-Source-Artifacts': displayed.map(item => item.artifact.id).slice(0, 20).join(',')}, body: new Blob([bytes.buffer as ArrayBuffer])}, session.csrfToken)
    } : undefined} />}
    {!comparison && <SaveExperimentDialog open={saving} close={() => setSaving(false)} selected={selected.map(id => availableByID.get(id)).filter((item): item is Artifact => Boolean(item))} operations={operations} workspaces={workspaces} session={session} changed={changed} />}
  </div></SeriesNotesContext.Provider></ShowEverything.Provider>
}

const hasFidelity = (rows: Array<{fidelity: LoadFidelity}>) => rows.some(row => row.fidelity.ratio !== undefined || row.fidelity.schedule)

function FidelitySection({rows}: {rows: Array<{label: string; fidelity: LoadFidelity}>}) {
  if (!hasFidelity(rows)) return null
  return <section className="statistics-card" aria-label="Load fidelity">
    <p className="table-lead">Request rate the generator was asked to send vs what it actually sent. A shortfall or a late schedule means the closed-loop generator could not keep the open-loop schedule (coordinated omission), which hides slow responses from client-side latency.</p>
    <FidelitySummaryTable rows={rows} />
  </section>
}

function RunTab({name, trial, active, select}: {name: string; trial: ExperimentTrial; active: boolean; select: () => void}) {
  const resting = restingUntil(trial)
  const cleanup = cleanupWarning(trial)
  return <button type="button" className={`run-tab ${trial.status}`} aria-current={active ? 'page' : undefined} data-active={active} onClick={select} title={cleanup}>
    <span className={`status-dot ${trial.status}`} /><span className="run-tab-label">{name}</span>
    {isPilot(trial) && <span className="run-badge pilot">Pilot</span>}
    {resting && <span className="run-badge resting">{resting}</span>}
    {cleanup && <span className="run-badge warning">Cleanup failed</span>}
  </button>
}

function ExperimentStatistics({datasets, metrics, experiment, provenance, comparisonRows, comparisonOptions, setComparisonOptions, fidelityRows}: {datasets: LoadedDataset[]; metrics: string[]; experiment?: Experiment; provenance: ProvenanceOf; comparisonRows: ReturnType<typeof compareRunValues>; comparisonOptions: {baseline: string; mode: 'baseline' | 'all-pairs'}; setComparisonOptions: (value: {baseline: string; mode: 'baseline' | 'all-pairs'}) => void; fidelityRows: Array<{label: string; fidelity: LoadFidelity}>}) {
  const runPositions = new Map(experiment?.variants.flatMap(variant => variant.trials.map(trial => [trial.resultArtifactId, trial.position] as const)) ?? [])
  const orderedDatasets = [...datasets].sort((left, right) => {
    const position = (dataset: LoadedDataset) => runPositions.get(dataset.artifact.id) ?? Number(dataset.label.match(/(?:Run|Execution) (\d+)/i)?.[1] ?? 0)
    return position(left) - position(right)
  })
  const comparison = Boolean(experiment && (experiment.kind === 'comparison' || experiment.variants.length > 1))
  const acrossServices = !comparison && experiment && datasets.length > 1 && serviceStatisticsRows(datasets, experiment).some(row => row.acrossRuns)
  const runServices = !comparison && datasets.length === 1 && serviceStatisticsRows(datasets).length > 0
  const views: StatisticsView[] = []
  if (experiment && comparison) {
    views.push({id: 'comparison', title: 'Comparison', metrics: true, render: match => <StrategyComparison datasets={datasets} metrics={metrics.filter(match)} experiment={experiment} provenance={provenance} />})
    views.push({id: 'differences', title: 'Differences', metrics: true, render: match => <ComparisonTable rows={comparisonRows} variants={experiment.variants.map(variant => ({id: variant.id, label: variant.alias || variant.name}))} baseline={comparisonOptions.baseline} mode={comparisonOptions.mode} onChange={setComparisonOptions} provenance={provenance} title={experiment.kind === 'comparison' ? 'Differences between experiments' : 'Differences between strategies'} match={match} />})
  }
  if (experiment && !comparison && datasets.length > 1) views.push({id: 'across', title: 'Across executions', metrics: true, render: match => <AcrossRunsTable experiment={experiment} datasets={datasets} metrics={metrics.filter(match)} provenance={provenance} />})
  if (!comparison) views.push({id: 'runs', title: datasets.length > 1 ? 'Per execution' : 'Execution', metrics: true, render: match => <WithinRunStatistics datasets={orderedDatasets} metrics={metrics.filter(match)} />})
  if (acrossServices || runServices) views.push({id: 'services', title: 'By service', metrics: true, render: match => <section className="statistics-card">{acrossServices ? <ServiceStatisticsTable datasets={datasets} experiment={experiment} match={match} /> : <ServiceStatisticsTable datasets={datasets} match={match} />}</section>})
  if (hasFidelity(fidelityRows)) views.push({id: 'fidelity', title: 'Load fidelity', render: () => <FidelitySection rows={fidelityRows} />})
  return <StatisticsTabs views={views} />
}

// Export menu: CSV, print, saved figures and the raw run data.
function ExportPanel({datasets, csv, experiment, comparisonRows, onPrint, savedFigures = true, rawData}: {datasets: LoadedDataset[]; csv: () => string; experiment?: Experiment; comparisonRows: VariantComparison[]; onPrint: () => void; savedFigures?: boolean; rawData?: Array<{id: string; label: string}>}) {
  const [figures, setFigures] = useState<ExperimentFigure[]>([])
  const [error, setError] = useErrorState()
  useEffect(() => {
    setFigures([])
    if (!experiment || !savedFigures) return
    const controller = new AbortController()
    request<{items: ExperimentFigure[]}>(`/experiments/${experiment.id}/figures`, {}, undefined, controller.signal).then(result => {if (!controller.signal.aborted) setFigures(result.items ?? [])}).catch(cause => {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load saved figures.')})
    return () => controller.abort()
  }, [experiment?.id, savedFigures])
  const visibleFigures = figures.filter(figure => figure.sourceArtifactIds.some(id => datasets.some(dataset => dataset.artifact.id === id)))
  const raw = rawData ?? datasets.map(item => ({id: item.artifact.id, label: item.label}))
  return <div className="export-panel">
    <div className="menu-list">
      <span className="menu-label">Tables</span>
      <button type="button" className="menu-item" onClick={() => downloadBlob('kubephos-statistics.csv', new Blob([csv()], {type: 'text/csv;charset=utf-8'}))}>Statistics CSV</button>
      {comparisonRows.length > 0 && <button type="button" className="menu-item" onClick={() => downloadBlob('kubephos-comparisons.csv', new Blob([comparisonCSV(comparisonRows)], {type: 'text/csv;charset=utf-8'}))}>Comparison CSV</button>}
      <button type="button" className="menu-item" onClick={onPrint}>Print statistics</button>
    </div>
    {error && <ErrorFeedback message={error} />}
    {visibleFigures.length > 0 && <div className="menu-list"><span className="menu-label">Saved figures</span>{visibleFigures.map(figure => <a key={figure.id} className="menu-item" href={`/api/v1/figures/${figure.id}/download`}><span><strong>{metricTitle(figure.metric)} · {figure.format.toUpperCase()}</strong><small>{figure.sourceArtifactIds.length} runs · {formatDate(figure.createdAt)}</small></span></a>)}</div>}
    <div className="menu-list"><span className="menu-label">Complete run data (JSON)</span>{raw.map(item => <a key={item.id} className="menu-item" href={`/api/v1/artifacts/${item.id}/download`}><span><strong>{item.label}</strong><small>Metric samples</small></span></a>)}</div>
    <p className="menu-note">Statistics CSV includes source, vantage point, 95% CI and n_eff for every row. Publication figures: Create figure.</p>
  </div>
}

function ExperimentProgress({experiment}: {experiment: Experiment}) {
  const progress = experimentProgress(experiment)
  const trials = experiment.variants.flatMap(variant => variant.trials.map(trial => ({variant: variant.name, trial})))
  const running = trials.find(item => ['running','initializing','initialized'].includes(item.trial.status))
  const failed = trials.filter(item => !isPilot(item.trial) && ['failed', 'canceled'].includes(item.trial.status)).length
  const resting = trials.map(item => restingUntil(item.trial)).find(Boolean)
  const detail = running ? `${running.variant} · ${trialLabel(running.trial)} ${running.trial.status==='initialized'?'ready to start':running.trial.status==='initializing'?'initializing':'in progress'}` : resting ?? (failed ? `${failed} run${failed === 1 ? '' : 's'} did not complete` : progress.completed === progress.total ? 'All executions completed' : 'Waiting for the next execution')
  return <article className="experiment-progress-card">
    <div><strong>{progress.completed} of {progress.total} executions finished</strong><small>{detail}</small></div>
    <div className="experiment-progress" aria-label={`${progress.completed} of ${progress.total} executions finished`}><span style={{width: `${progress.total ? progress.completed / progress.total * 100 : 0}%`}} /></div>
  </article>
}

function RunDiagnostics({trial, openPipelineRun, openOperation}: {trial: ExperimentTrial; openPipelineRun?: (id: string) => void; openOperation?: (id: string) => void}) {
  const {status, error, pipelineRunId, operationId} = trial
  const running = ['running', 'initializing', 'starting', 'prechecking', 'verifying'].includes(status)
  const failed = ['failed', 'canceled'].includes(status)
  const resting = restingUntil(trial)
  const cleanup = cleanupWarning(trial)
  const label = status === 'initialized' ? 'Initialization complete' : status==='initializing'?'Execution initializing':status === 'queued' ? resting ?? 'Execution waiting to start' : status === 'canceled' ? 'Execution canceled' : failed ? 'Execution needs attention' : running ? 'Execution in progress' : status === 'succeeded' ? 'Execution completed' : 'Execution status unavailable'
  if (!error && !cleanup && !resting && !running && !failed && !(pipelineRunId && openPipelineRun)) return null
  return <div className={`run-diagnostics ${failed ? 'failed' : ''}`}><div><strong>{label}</strong>{error && <p>{error}</p>}{cleanup && <p className="run-cleanup-warning">{cleanup}</p>}</div>{pipelineRunId && openPipelineRun ? <button className="button secondary compact" onClick={() => openPipelineRun(pipelineRunId)}>{running ? 'Live process logs' : 'Process logs'}</button> : operationId && openOperation ? <button className="button secondary compact" onClick={() => openOperation(operationId)}>{running ? 'Live logs' : 'Error logs'}</button> : null}</div>
}

function RunLoadProgress({trial, configuration, operation, name}: {trial: ExperimentTrial; configuration: Record<string, unknown>; operation?: Operation; name?: string}) {
  const steps = loadStepsFromConfiguration(configuration)
  const duration = steps.reduce((total, step) => total + step.durationSeconds, 0)
  const lead = loadLeadFromConfiguration(configuration)
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
  let label = trial.status === 'queued' ? restingUntil(trial) ?? 'Waiting to start' : 'Preparing the run'
  if (active && operation) {
    // Call-chain profiling and warmup run before the measured profile's t=0.
    const elapsed = Math.max(0, (now - new Date(operation.createdAt).getTime()) / 1000)
    const profiling = lead.profilingSeconds ?? 0
    progress = Math.floor(Math.min(duration, Math.max(0, elapsed - profiling - lead.warmupSeconds)))
    label = lead.profiling && lead.profilingSeconds === undefined ? 'Call-chain profiling, warmup or load running' : elapsed < profiling ? 'Measuring the call chain' : elapsed < profiling + lead.warmupSeconds ? 'Warmup' : 'Load running'
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
  return <section className="run-load-progress"><div className="run-load-progress-head"><span className={`status-dot ${trial.status}`} /><div><strong>{name ?? 'Input load progress'}</strong><small>{label}{trial.startedAt ? ` · execution elapsed ${formatDuration(runElapsed)}` : ''}</small></div></div><LoadProfilePreview steps={steps} progressSeconds={progress} progressLabel={label} /></section>
}

// Time the load generator spends before the measured profile: call-chain
// profiling (one window per task with a positive weight) and the warmup. The
// number of profiled tasks is known only when the configuration lists the mix.
export function loadLeadFromConfiguration(configuration: Record<string, unknown> | undefined): {warmupSeconds: number; profiling: boolean; profilingSeconds?: number} {
  const components = Array.isArray(configuration?.components) ? configuration.components : []
  const settings = components.find((value): value is {capability?: unknown; configuration: Record<string, unknown>} => Boolean(value) && typeof value === 'object' && String((value as Record<string, unknown>).capability ?? '').startsWith('load.') && typeof (value as Record<string, unknown>).configuration === 'object')?.configuration ?? {}
  const warmupSeconds = typeof settings.warmupSeconds === 'number' && settings.warmupSeconds > 0 ? settings.warmupSeconds : 0
  if (settings.profileCallChain !== true || settings.interactive === true) return {warmupSeconds, profiling: false}
  const perTask = typeof settings.profileSecondsPerTask === 'number' && settings.profileSecondsPerTask > 0 ? settings.profileSecondsPerTask : 60
  const weights = settings.taskWeights && typeof settings.taskWeights === 'object' ? Object.values(settings.taskWeights) : []
  // Listed weights may include TaskSet classes; then the count is only an upper bound.
  const tasks = weights.filter(value => typeof value === 'number' && value > 0).length
  return {warmupSeconds, profiling: true, profilingSeconds: tasks ? tasks * perTask : undefined}
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

// Measured repetitions only: pilot (warm-up) executions are not counted.
export function experimentProgress(experiment: Experiment): {completed: number; total: number} {
  const trials = experiment.variants.flatMap(variant => variant.trials).filter(trial => !isPilot(trial))
  return {completed: trials.filter(trial => ['succeeded', 'failed', 'canceled'].includes(trial.status)).length, total: trials.length}
}

function SaveExperimentDialog({open, close, selected, operations, workspaces, session, changed}: {open: boolean; close: () => void; selected: Artifact[]; operations: Operation[]; workspaces: Workspace[]; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
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
      setError('Enter two distinct approach names.')
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
      <div className="trial-mapping">{selected.map(artifact => <label key={artifact.id}><span>{operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name}</span><input name={`variant-${artifact.id}`} maxLength={80} defaultValue={operations.find(item => item.id === artifact.operationId)?.title ?? artifact.name} required /></label>)}</div>
      <ErrorFeedback message={error} />
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Saving…' : 'Save experiment'}</button></div>
    </form>
  </Dialog>
}

function datasetLabel(artifact: Artifact, operations: Operation[]): string {
  const operation = operations.find(item => item.id === artifact.operationId)
  return operation?.title ?? artifact.name
}

function EmptyResults({experiment, runStatus}: {experiment?: Experiment; runStatus?: string}) {
  const status = runStatus ?? experiment?.status
  if(status==='initialized'||status==='initializing')return <div className="empty-state"><div><strong>{status==='initialized'?'Initialization complete':'Initializing'}</strong><p>{status==='initialized'?'Select Start when ready.':'Preparing the application and experiment components.'}</p></div></div>
  const queued = status === 'queued'
  const running = status && ['running', 'starting', 'prechecking', 'verifying'].includes(status)
  const failed = status === 'failed'
  return <div className="empty-state"><div><strong>{queued ? 'Waiting to start' : running ? 'Runs in progress' : failed ? 'No results from this experiment' : 'No results yet'}</strong>{queued ? 'Results will appear after this run starts and finishes.' : running ? 'Results and plots appear here as runs finish.' : failed ? 'Open the run above to inspect its logs and health checks.' : experiment ? 'Run this experiment to collect metrics and plots.' : 'Choose an experiment or run to explore verified data.'}</div></div>
}

function ComparisonResults({artifacts, experiment, comparison, onBack, session}: ResultsViewProps & {experiment: Experiment}) {
  const [revision, setRevision] = useState(0)
  const initialOptions = useMemo(() => comparisonOptionsDefault(withoutPilots(experiment)), [experiment])
  const job = useMemo(() => ({artifacts, experiment, configurations: comparison?.sourceConfigurations ?? {}, comparison: initialOptions}), [artifacts, experiment, comparison?.sourceConfigurations, session.user?.id, initialOptions])
  const state = usePreparedComparison(job, revision)
  const data = state.result
  const [options, setOptions] = useState(initialOptions)
  const [figureSettings, setFigureSettings] = useFigureSettings(`comparison:${experiment.id}`)
  const {sections, toggle: toggleSection, navigate, jump} = useSections()
  const [studio, setStudio] = useState<StudioRequest | null>(null)
  const chartMode = useChartMode()
  const [printPending, setPrintPending] = useState(false)
  useEffect(() => {
    if (!printPending || !sections.statistics) return
    const frame = requestAnimationFrame(() => {window.print(); setPrintPending(false)})
    return () => cancelAnimationFrame(frame)
  }, [printPending, sections.statistics])
  // The worker computed the initial table; other baselines/modes reuse its run-level values.
  const rows = useMemo(() => !data ? [] : options.baseline === initialOptions.baseline && options.mode === initialOptions.mode ? data.comparisons : compareRunValues(data.runLevel ?? [], options), [data, options, initialOptions])
  const provenance: ProvenanceOf = useCallback(metric => data?.provenance?.[metric] ?? provenanceFor([], metric), [data])
  const analysis = withoutPilots(experiment)
  const baselineName = analysis.variants.find(variant => variant.id === options.baseline)
  const methods = data ? [data.methods, comparisonMethodsSentence(rows.length ? {familySize: rows[0].familySize, mode: options.mode, baseline: baselineName?.alias || baselineName?.name} : undefined)].filter(Boolean).join(' ') : ''
  const notices: QualityNotice[] = data ? [...cpuSaturationNotices(data.locust ?? []), ...(data.context?.concurrentLoad ?? []).map(entry => ({kind: 'warning' as const, text: concurrentLoadWarning(entry)}))] : []
  const plot = useCallback((metric: string): AggregateSeries[] => data?.complete ? data.plots?.[metric] ?? [] : [], [data])
  const overview = useMemo((): OverviewModel | undefined => {
    if (!data) return undefined
    const groups = withDisplayColors(overviewGroups(data.runLevel ?? []), chartMode)
    const fidelityWarnings = (data.fidelityRows ?? []).flatMap(row => row.fidelity.warnings.map(text => ({label: row.label, text})))
    const cards = kpiCards({runLevel: data.runLevel ?? [], comparisons: rows, baseline: options.baseline, slo: undefined})
    const services = data.services as Record<string, {rows: Array<ServiceRow & {kind?: string}>} | undefined>
    const prepared = data.overview
    if (prepared) {
      // Same verdict and services table as the experiment page, from worker-prepared per-series data.
      const seriesOf = (group: string | undefined) => prepared.series.find(entry => entry.id === (group ?? groups[0]?.id)) ?? prepared.series[0]
      const unpacked = new Map<string, AggregateSeries | undefined>()
      const curve = (group: string | undefined, service: string, metric: string) => {
        const key = JSON.stringify([group, service, metric])
        if (!unpacked.has(key)) {const packed = seriesOf(group)?.curves?.[service]?.[metric]; unpacked.set(key, packed && unpackSeries(packed))}
        return unpacked.get(key)
      }
      const curves = prepared.series.some(entry => entry.curves)
      return {
        groups, insights: prepared.insights, notices: [...fidelityWarnings.map(item => ({kind: 'warning' as const, text: `${item.label}: ${item.text}`})), ...notices], resources: data.resources,
        runLevel: data.runLevel ?? [], comparisons: rows, baseline: options.baseline, slo: undefined,
        plot, provenance: metric => data.provenance?.[metric],
        services: group => seriesOf(group)?.services ?? [],
        serviceTrend: curves ? (group, service) => curve(group, service, 'application.p95_response_time') : undefined,
        serviceDetail: curves ? (group, service) => serviceDetailMetrics.map(item => {const series = curve(group, service, item.metric); return {title: item.title, unit: item.unit, series: series ? [series] : []}}) : undefined,
        subtitle: data.complete ? timelineSubtitle(groups.length, 2) : 'Preparing curves…'
      }
    }
    return {
      groups, insights: comparisonVerdict(cards, fidelityWarnings), notices: [...fidelityWarnings.slice(1).map(item => ({kind: 'warning' as const, text: `${item.label}: ${item.text}`})), ...notices], resources: data.resources,
      runLevel: data.runLevel ?? [], comparisons: rows, baseline: options.baseline, slo: undefined,
      plot, provenance: metric => data.provenance?.[metric],
      services: group => rankServiceSummaries(group ?? groups[0]?.id ?? '', services),
      subtitle: data.complete ? timelineSubtitle(groups.length, 2) : 'Preparing curves…',
      comparisonVerdict: true,
      servicesNote: 'Services ranked by mean server P95 across repetitions. A saved comparison keeps no per-service time series.'
    }
  }, [data, rows, options.baseline, plot, chartMode])
  // Saved comparisons keep the mean curve per series only: X–Y points are its windows.
  const xySource = useMemo((): XYSource | undefined => !data?.complete ? undefined : {
    metrics: data.metrics.filter(metric => !isControlMetric(metric)), level: 'mean', provenance, services: () => undefined,
    runs: ({metric}) => plot(metric).map(series => ({runID: series.artifactID, runLabel: series.label, seriesID: series.artifactID, seriesLabel: series.label, color: series.color, series}))
  }, [data, plot, provenance])
  const studioData = useMemo((): StudioData | undefined => !studio || !data?.complete ? undefined : {
    metrics: data.metrics, plot, provenance, omitSLO: data.omitSLO,
    runLevel: (data.runLevel ?? []).filter(entry => data.metrics.includes(entry.metric)), ladder: data.ladder ?? [], fidelity: data.fidelity ?? [],
    serviceMetrics: data.metrics.filter(metric => serviceStatisticsMetrics.includes(metric) && data.services?.[metric]?.rows.length),
    services: metric => data.services?.[metric], xy: xySource, profile: sharedLoadProfile(Object.values(comparison?.sourceConfigurations ?? {}) as Array<Record<string, unknown> | undefined>)
  }, [Boolean(studio), data, plot, provenance, xySource])
  const rawData = artifacts.map(artifact => ({id: artifact.id, label: experiment.variants.find(variant => variant.trials.some(trial => trial.resultArtifactId === artifact.id))?.alias || artifact.name}))
  const links = data ? sectionLinks({overview: true, figure: Boolean(data.complete), services: true, sources: Boolean(comparison?.sources), endpoints: (data.locust ?? []).length > 0, statistics: true, environment: Boolean(data.context), metrics: true}) : []
  const comparisonViews: StatisticsView[] = data ? [
    {id: 'comparison', title: 'Comparison', metrics: true, render: match => <StrategyComparison datasets={[]} metrics={data.statisticsMetrics.filter(match)} experiment={experiment} prepared={data.columns} provenance={provenance} />},
    {id: 'differences', title: 'Differences', metrics: true, render: match => <ComparisonTable rows={rows} variants={analysis.variants.map(variant => ({id: variant.id, label: variant.alias || variant.name}))} baseline={options.baseline} mode={options.mode} onChange={setOptions} provenance={provenance} title="Differences between experiments" match={match} />},
    ...hasFidelity(data.fidelityRows ?? []) ? [{id: 'fidelity', title: 'Load fidelity', render: () => <FidelitySection rows={data.fidelityRows ?? []} />}] : []
  ] : []
  return <ShowEverything.Provider value={printPending}><SeriesNotesContext.Provider value={colorNotes(overview?.groups)}><div className="results-page">
    <header className="page-header" id="results-top">{onBack && <DetailBreadcrumb parent="Comparisons" current={experiment.name} back={onBack} />}
      <div className="results-heading focused">
        <div className="results-title"><h2 tabIndex={-1}>{experiment.name}</h2><ResultsMeta experiment={analysis} datasets={[]} /></div>
        <div className="results-detail-actions">
          {comparison?.actions}
          {data?.complete && <button type="button" className="button secondary" title="Open KubeStudio to build a publication figure" onClick={() => setStudio({})}><KubeStudioMark size={18} className="kubestudio-entry" />Create figure</button>}
          {data && <HeaderMenu label="Export" icon={<ActionIcon name="download" />}>{close => <ExportPanel datasets={[]} csv={() => data.csv} comparisonRows={rows} savedFigures={false} rawData={rawData} onPrint={() => {close(); toggleSection('statistics', true); setPrintPending(true)}} />}</HeaderMenu>}
        </div>
      </div>
    </header>
    {comparison?.notice}
    <ResourceAttribution resource={experiment} editable />
    <ErrorFeedback message={state.error} />
    {data && <SectionNav links={links} onJump={jump} top="results-top" />}
    {state.error ? <div className="results-placeholder"><button className="button secondary" onClick={() => setRevision(value => value + 1)}>Retry loading results</button></div> : !data ? <div className="results-placeholder" role="status"><span className="results-spinner" /><strong>{state.progress}</strong></div> : <div className="results-content">
      <div id="results-overview" className="section-anchor" />
      {data.warnings.map(warning => <div key={warning} className="validation-item warning">{warning}</div>)}
      {overview && <ResultsOverview model={overview} onNavigate={navigate} />}
    </div>}
    <div className="results-sections" role="region" aria-label="Details">
      {comparison?.sources && <Disclosure id="sources" title="Sources" summary={`${experiment.variants.length} source experiment${experiment.variants.length === 1 ? '' : 's'}`} open={Boolean(sections.sources)} onToggle={open => toggleSection('sources', open)}>{comparison.sources}</Disclosure>}
      {data && (data.locust ?? []).length > 0 && <Disclosure id="endpoints" title="Endpoints" summary="per-endpoint statistics from Locust" open={Boolean(sections.endpoints)} onToggle={open => toggleSection('endpoints', open)}><LocustSummaryPanel groups={data.locust as LocustGroup[]} /></Disclosure>}
      {data && <Disclosure id="statistics" title="Statistics" summary="Holm-adjusted tests, effect sizes, CSV" open={Boolean(sections.statistics)} onToggle={open => toggleSection('statistics', open)}>
        <StatisticsTabs views={comparisonViews} />
      </Disclosure>}
      {data?.context && <Disclosure id="environment" title="Environment & methods" summary="cluster, versions, windows and a methods paragraph" open={Boolean(sections.environment)} onToggle={open => toggleSection('environment', open)}><RunContextPanel context={data.context as RunContextSummary} /><MethodsPanel text={methods} /></Disclosure>}
      {data && <Disclosure id="metrics" title="All metrics" summary={`${data.metrics.length} recorded, plotted on request`} open={Boolean(sections.metrics)} onToggle={open => toggleSection('metrics', open)}>
        {data.complete ? <AllMetrics metrics={data.metrics} plot={plot} groups={overview?.groups ?? []} provenance={provenance} onFigure={metric => setStudio({kind: 'timeseries', metric})} xy={xySource} onXYFigure={xy => setStudio({kind: 'xy', xy})} /> : <div className="results-placeholder" role="status"><span className="results-spinner" /><strong>{state.progress ?? 'Preparing plots…'}</strong></div>}
      </Disclosure>}
    </div>
    {studio && <KubeStudio open onClose={() => setStudio(null)} request={studio} data={studioData} groups={overview?.groups ?? []} settings={figureSettings} onSettings={setFigureSettings} storageKey={`comparison:${experiment.id}`} />}
  </div></SeriesNotesContext.Provider></ShowEverything.Provider>
}
