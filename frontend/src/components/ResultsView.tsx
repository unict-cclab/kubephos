import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatBytes, formatDate} from '../lib'
import type {Artifact, Experiment, MetricPoint, Operation, Session, TimeSeriesDataset, Workspace} from '../types'
import {Dialog} from './Dialog'

interface Props {
  artifacts: Artifact[]
  experiments: Experiment[]
  operations: Operation[]
  workspaces: Workspace[]
  session: Session
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

export interface LoadedDataset {
  artifact: Artifact
  dataset: TimeSeriesDataset
  label: string
}

export interface AggregateSeries {
  artifactID: string
  label: string
  unit: string
  points: MetricPoint[]
}

const colors = ['#177554', '#3169a8', '#a9660c', '#8b4fa3', '#b43f5e', '#3f8994', '#7356a6', '#8a6f22']

export function ResultsView({artifacts, experiments, operations, workspaces, session, changed, openOperation}: Props) {
  const available = useMemo(() => artifacts.filter(item => !item.sensitive && item.type === 'TimeSeriesDataset' && item.version === 'v1alpha1').sort((left, right) => right.verifiedAt.localeCompare(left.verifiedAt)), [artifacts])
  const availableKey = available.map(item => `${item.id}:${item.digest}`).join('|')
  const [selected, setSelected] = useState<string[]>([])
  const [loaded, setLoaded] = useState<LoadedDataset[]>([])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [activeExperiment, setActiveExperiment] = useState<string | null>(null)
  const selectedKey = selected.join('|')

  useEffect(() => {
    setSelected(current => current.filter(id => available.some(item => item.id === id)))
  }, [availableKey])

  useEffect(() => {
    if (!selected.length) {
      setLoaded([])
      setError('')
      return
    }
    const controller = new AbortController()
    setLoading(true)
    setError('')
    Promise.all(selected.map(async id => {
      const artifact = available.find(item => item.id === id)
      if (!artifact) throw new Error('A selected dataset is no longer available.')
      const dataset = await request<TimeSeriesDataset>(`/artifacts/${id}/download`, {}, undefined, controller.signal)
      validateDataset(dataset)
      return {artifact, dataset, label: ''}
    })).then(values => setLoaded(values)).catch(cause => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the selected datasets.')
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [availableKey, selectedKey])

  const toggle = (id: string) => {
    setActiveExperiment(null)
    setNotice('')
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : current.length < 8 ? [...current, id] : current)
  }
  const experiment = experiments.find(item => item.id === activeExperiment)
  const experimentLabels = new Map<string, string>()
  for (const variant of experiment?.variants ?? []) for (const trial of variant.trials) if (trial.resultArtifactId) experimentLabels.set(trial.resultArtifactId, `${variant.name} · Trial ${trial.position}`)
  const displayed = loaded.map(item => ({...item, label: experimentLabels.get(item.artifact.id) ?? datasetLabel(item.artifact, operations, workspaces)}))
  const metrics = [...new Set(displayed.flatMap(item => item.dataset.spec.series.map(series => series.metric)))].sort()
  const samples = displayed.reduce((total, item) => total + item.dataset.spec.summary.samples, 0)
  const selectedWorkspaceID = available.find(item => item.id === selected[0])?.workspaceId
  const openExperiment = (item: Experiment) => {
    const artifactIDs = item.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(id => available.some(artifact => artifact.id === id))
    setActiveExperiment(item.id)
    setSelected(artifactIDs.slice(0, 20))
    setNotice(artifactIDs.length > 20 ? 'This experiment has more than twenty datasets. Showing the first twenty.' : artifactIDs.length === 0 && item.status === 'succeeded' ? `${item.resultType}/${item.resultVersion} is stored, but has no chart renderer.` : '')
  }
  return <>
    <div className="results-heading"><div><p className="eyebrow">REPEATABLE EVIDENCE</p><h2>Compare collected results</h2><p>Explore verified runs, compare normalized timelines and export publication-ready evidence.</p></div><div className="results-heading-actions"><button className="button secondary" disabled={!displayed.length} onClick={() => window.print()}>Print / PDF</button><button className="button secondary" disabled={!displayed.length} onClick={() => exportStatisticsCSV(displayed, metrics)}>Export CSV</button><button className="button primary" disabled={selected.length < 2} onClick={() => setSaving(true)}>Save comparison</button><div className="results-count"><strong>{available.length}</strong><span>datasets</span></div></div></div>
    {experiments.length > 0 && <div className="experiment-history"><div className="experiment-history-title"><strong>Experiment history</strong><span>{experiments.length} repeatable comparisons</span></div><div className="experiment-cards">{experiments.map(item => <button key={item.id} className={activeExperiment === item.id ? 'active' : ''} onClick={() => openExperiment(item)}><span className={`status-dot ${item.status}`} /><span><strong>{item.name}</strong><small>{item.status === 'queued' && item.scheduledFor && new Date(item.scheduledFor).getTime() > Date.now() ? `scheduled ${formatDate(item.scheduledFor)}` : item.status} · {item.variants.length} variants · {item.variants.reduce((total, variant) => total + variant.trials.length, 0)} trials · {formatDate(item.createdAt)}</small></span></button>)}</div></div>}
    {experiment && <ExperimentDetail experiment={experiment} openOperation={openOperation} />}
    {!available.length ? <EmptyResults /> : <div className="results-layout">
      <aside className="dataset-picker" aria-label="Available datasets">
        <div className="dataset-picker-title"><strong>Dataset history</strong><span>{selected.length} selected</span></div>
        {available.map(artifact => {
          const operation = operations.find(item => item.id === artifact.operationId)
          const workspace = workspaces.find(item => item.id === artifact.workspaceId)
          const active = selected.includes(artifact.id)
          const incompatibleWorkspace = Boolean(selectedWorkspaceID && artifact.workspaceId !== selectedWorkspaceID)
          return <button className={`dataset-option ${active ? 'active' : ''}`} key={artifact.id} onClick={() => toggle(artifact.id)} disabled={!active && (selected.length >= 8 || incompatibleWorkspace)} aria-pressed={active}>
            <span className="dataset-check">{active ? '✓' : ''}</span><span><strong>{operation?.title ?? artifact.name}</strong><small>{workspace?.name ?? 'Workspace'} · {formatDate(artifact.verifiedAt)}</small><small>{formatBytes(artifact.sizeBytes)} · {artifact.digest.slice(0, 14)}…</small></span>
          </button>
        })}
      </aside>
      <div className="results-content">
        {notice && <div className="validation-item warning">{notice}</div>}
        {!selected.length && <div className="results-placeholder"><span>⌁</span><strong>Select a dataset</strong><p>Choose one result to explore it or multiple results to compare them.</p></div>}
        {loading && <div className="results-placeholder"><span className="results-spinner" /><strong>Loading verified data…</strong></div>}
        {error && <div className="validation-item error">{error}</div>}
        {!loading && !error && loaded.length > 0 && <>
          <div className="results-stats">
            <ResultStat label="Compared" value={displayed.length} />
            <ResultStat label="Metrics" value={metrics.length} />
            <ResultStat label="Samples" value={samples.toLocaleString()} />
            <ResultStat label="Resolution" value={`${loaded[0].dataset.spec.stepSeconds}s`} />
          </div>
          <div className="chart-grid">{metrics.map(metric => {
            const series = aggregateMetric(displayed, metric)
            return <MetricChart key={metric} metric={metric} series={series} />
          })}</div>
          <StatisticsTable datasets={displayed} metrics={metrics} experiment={experiment} />
          <div className="dataset-downloads">{displayed.map(item => <a key={item.artifact.id} href={`/api/v1/artifacts/${item.artifact.id}/download`}><span>↓</span><span><strong>{item.label}</strong><small>Download verified JSON</small></span></a>)}</div>
        </>}
      </div>
    </div>}
    <SaveExperimentDialog open={saving} close={() => setSaving(false)} selected={selected.map(id => available.find(item => item.id === id)).filter((item): item is Artifact => Boolean(item))} operations={operations} workspaces={workspaces} session={session} changed={changed} />
  </>
}

function ExperimentDetail({experiment, openOperation}: {experiment: Experiment; openOperation: (id: string) => void}) {
  const progress = experimentProgress(experiment)
  return <article className="experiment-detail">
    <div className="experiment-detail-head"><div><span className={`status-dot ${experiment.status}`} /><span><strong>{experiment.name}</strong><small>{experiment.resultType}/{experiment.resultVersion}{experiment.scheduledFor && experiment.status === 'queued' ? ` · ${formatDate(experiment.scheduledFor)}` : ''}</small></span></div><div><strong>{progress.completed}/{progress.total}</strong><small>trials complete</small></div></div>
    <div className="experiment-progress"><span style={{width: `${progress.total ? progress.completed / progress.total * 100 : 0}%`}} /></div>
    <div className="experiment-variants">{experiment.variants.map(variant => <section key={variant.id}><div><strong>{variant.name}</strong><small>{variant.trials.filter(trial => trial.status === 'succeeded').length}/{variant.trials.length} succeeded</small></div>{variant.trials.map(trial => <button type="button" key={trial.id} disabled={!trial.operationId} onClick={() => trial.operationId && openOperation(trial.operationId)}><span className={`status-dot ${trial.status}`} /><span><strong>Trial {trial.position}</strong><small>{trial.error || (trial.operationId ? 'Open logs and health gates' : trial.status === 'queued' ? 'Waiting to start' : trial.status)}</small></span><b>›</b></button>)}</section>)}</div>
  </article>
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

function MetricChart({metric, series}: {metric: string; series: AggregateSeries[]}) {
  const width = 720
  const height = 230
  const padding = {top: 18, right: 18, bottom: 30, left: 58}
  const allPoints = series.flatMap(item => item.points)
  const timestamps = allPoints.map(point => new Date(point.timestamp).getTime())
  const values = allPoints.map(point => point.value)
  const minX = Math.min(...timestamps)
  const maxX = Math.max(...timestamps)
  const minY = Math.min(0, ...values)
  const maxY = Math.max(...values)
  const x = (value: number) => padding.left + ((value - minX) / Math.max(1, maxX - minX)) * (width - padding.left - padding.right)
  const y = (value: number) => height - padding.bottom - ((value - minY) / Math.max(Number.EPSILON, maxY - minY)) * (height - padding.top - padding.bottom)
  const unit = series[0]?.unit ?? ''
  const chartID = `chart-${metric.replaceAll(/[^a-zA-Z0-9]/g, '-')}`
  return <article className="metric-card">
    <div className="metric-card-header"><div><p className="eyebrow">{unit.toUpperCase()}</p><h3>{metricTitle(metric)}</h3></div><div className="chart-export-actions"><strong>{formatMetric(maxY, unit)} peak</strong><button type="button" onClick={() => exportChart(chartID, metric, 'svg')}>SVG</button><button type="button" onClick={() => exportChart(chartID, metric, 'png')}>PNG 600 DPI</button></div></div>
    <div className="chart-wrap"><svg id={chartID} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={`${metricTitle(metric)} comparison chart`}>
      <style>{`.chart-grid-line{stroke:#dfe6e1;stroke-width:1}text{fill:#647168;font:10px Inter,Arial,sans-serif}`}</style>
      {[0, .25, .5, .75, 1].map(tick => {
        const value = minY + (maxY - minY) * tick
        const py = y(value)
        return <g key={tick}><line x1={padding.left} y1={py} x2={width - padding.right} y2={py} className="chart-grid-line" /><text x={padding.left - 8} y={py + 4} textAnchor="end">{formatMetric(value, unit)}</text></g>
      })}
      {series.map((item, index) => <path key={item.artifactID} d={chartPath(item.points, x, y)} fill="none" stroke={colors[index % colors.length]} strokeWidth="2.4" vectorEffect="non-scaling-stroke" />)}
      <text x={padding.left} y={height - 8}>0m</text>
      <text x={width - padding.right} y={height - 8} textAnchor="end">{formatDuration(maxX - minX)}</text>
    </svg></div>
    <div className="chart-legend">{series.map((item, index) => {
      const values = item.points.map(point => point.value)
      const mean = values.reduce((sum, value) => sum + value, 0) / Math.max(1, values.length)
      return <div key={item.artifactID}><span style={{background: colors[index % colors.length]}} /><strong>{item.label}</strong><small>avg {formatMetric(mean, item.unit)} · last {formatMetric(values.at(-1) ?? 0, item.unit)}</small></div>
    })}</div>
  </article>
}

export function aggregateMetric(datasets: LoadedDataset[], metric: string): AggregateSeries[] {
  return datasets.map(item => {
    const matching = item.dataset.spec.series.filter(series => series.metric === metric)
    const values = new Map<number, number>()
    for (const series of matching) {
      for (const point of series.points) {
        const timestamp = new Date(point.timestamp).getTime()
        values.set(timestamp, (values.get(timestamp) ?? 0) + point.value)
      }
    }
    return {
      artifactID: item.artifact.id,
      label: item.label,
      unit: matching[0]?.unit ?? '',
      points: [...values.entries()].sort((left, right) => left[0] - right[0]).map(([, value], index) => ({timestamp: new Date(index * item.dataset.spec.stepSeconds * 1000).toISOString(), value}))
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
  const margin = count > 1 ? 1.96 * standardDeviation / Math.sqrt(count) : 0
  return {count, mean, standardDeviation, min: sorted[0], max: sorted[count - 1], median: percentile(sorted, .5), p05: percentile(sorted, .05), p25: percentile(sorted, .25), p75: percentile(sorted, .75), p95: percentile(sorted, .95), p99: percentile(sorted, .99), coefficientOfVariation: mean === 0 ? 0 : standardDeviation / Math.abs(mean), confidence95Low: mean - margin, confidence95High: mean + margin}
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
    {!!effects.length && <section className="statistics-card"><div><p className="eyebrow">COMPARATIVE EFFECT</p><h3>Standardized difference between strategies</h3></div><div className="statistics-table-wrap"><table><thead><tr><th>Comparison</th><th>Metric</th><th>Cohen's d</th><th>Magnitude</th><th>Runs</th></tr></thead><tbody>{effects.map(row => <tr key={row.metric}><td>{row.left} vs {row.right}</td><td>{metricTitle(row.metric)}</td><td>{row.value.toFixed(3)}</td><td>{effectMagnitude(row.value)}</td><td>{row.leftCount} / {row.rightCount}</td></tr>)}</tbody></table></div></section>}
    {!!aggregates.length && <section className="statistics-card"><div><p className="eyebrow">STRATEGY AGGREGATES</p><h3>Distribution across independent sequential runs</h3></div><div className="statistics-table-wrap"><table><thead><tr><th>Strategy</th><th>Metric</th><th>Runs</th><th>Missing</th><th>Mean</th><th>Std</th><th>Min</th><th>Median</th><th>Max</th><th>P95</th><th>CV</th><th>95% CI</th></tr></thead><tbody>{aggregates.map(row => <tr key={`${row.variant}-${row.metric}`}><td>{row.variant}</td><td>{metricTitle(row.metric)}</td><td>{row.summary.count}</td><td>{row.missing}</td><td>{formatMetric(row.summary.mean, row.unit)}</td><td>{formatMetric(row.summary.standardDeviation, row.unit)}</td><td>{formatMetric(row.summary.min, row.unit)}</td><td>{formatMetric(row.summary.median, row.unit)}</td><td>{formatMetric(row.summary.max, row.unit)}</td><td>{formatMetric(row.summary.p95, row.unit)}</td><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(row.summary.confidence95Low, row.unit)}–{formatMetric(row.summary.confidence95High, row.unit)}</td></tr>)}</tbody></table></div></section>}
    <section className="statistics-card"><div><p className="eyebrow">RUN STATISTICS</p><h3>Samples within each collected run</h3></div><div className="statistics-table-wrap"><table><thead><tr><th>Dataset</th><th>Metric</th><th>N</th><th>Mean</th><th>Std</th><th>Min</th><th>Median</th><th>Max</th><th>P95</th><th>P99</th><th>CV</th><th>95% CI</th></tr></thead><tbody>{rows.map((row, index) => <tr key={`${row.dataset}-${row.metric}-${index}`}><td>{row.dataset}</td><td>{metricTitle(row.metric)}</td><td>{row.summary.count}</td><td>{formatMetric(row.summary.mean, row.unit)}</td><td>{formatMetric(row.summary.standardDeviation, row.unit)}</td><td>{formatMetric(row.summary.min, row.unit)}</td><td>{formatMetric(row.summary.median, row.unit)}</td><td>{formatMetric(row.summary.max, row.unit)}</td><td>{formatMetric(row.summary.p95, row.unit)}</td><td>{formatMetric(row.summary.p99, row.unit)}</td><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{formatMetric(row.summary.confidence95Low, row.unit)}–{formatMetric(row.summary.confidence95High, row.unit)}</td></tr>)}</tbody></table></div></section>
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
    return values.length ? {variant: variant.name, metric, unit, missing: variant.trials.length - values.length, summary: summarize(values)} : null
  }).filter((item): item is {variant: string; metric: string; unit: string; missing: number; summary: ScientificSummary} => Boolean(item)))
}

export function variantEffectSizes(experiment: Experiment, datasets: LoadedDataset[], metrics: string[]) {
  if (experiment.variants.length !== 2) return []
  const byArtifact = new Map(datasets.map(item => [item.artifact.id, item]))
  return metrics.map(metric => {
    const left = variantRunMeans(experiment.variants[0], byArtifact, metric)
    const right = variantRunMeans(experiment.variants[1], byArtifact, metric)
    if (left.length < 2 || right.length < 2) return null
    return {metric, left: experiment.variants[0].name, right: experiment.variants[1].name, value: cohenD(left, right), leftCount: left.length, rightCount: right.length}
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

function exportStatisticsCSV(datasets: LoadedDataset[], metrics: string[]) {
  const header = ['dataset', 'metric', 'unit', 'count', 'mean', 'std', 'min', 'median', 'max', 'p05', 'p25', 'p75', 'p95', 'p99', 'cv', 'ci95_low', 'ci95_high']
  const rows: Array<Array<string | number>> = [header]
  for (const dataset of datasets) for (const metric of metrics) {
    const series = aggregateMetric([dataset], metric)[0]
    if (!series) continue
    const value = summarize(series.points.map(point => point.value))
    rows.push([dataset.label, metric, series.unit, value.count, value.mean, value.standardDeviation, value.min, value.median, value.max, value.p05, value.p25, value.p75, value.p95, value.p99, value.coefficientOfVariation, value.confidence95Low, value.confidence95High])
  }
  downloadBlob('kubephos-statistics.csv', new Blob([rows.map(row => row.map(csvValue).join(',')).join('\n')], {type: 'text/csv;charset=utf-8'}))
}

function csvValue(value: string | number): string {
  const text = String(value)
  return /[",\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text
}

async function exportChart(id: string, metric: string, format: 'svg' | 'png') {
  const source = document.getElementById(id) as SVGSVGElement | null
  if (!source) return
  const serialized = new XMLSerializer().serializeToString(source)
  const name = `kubephos-${metric.replaceAll(/[^a-zA-Z0-9]+/g, '-').toLowerCase()}`
  if (format === 'svg') {
    downloadBlob(`${name}.svg`, new Blob([serialized], {type: 'image/svg+xml;charset=utf-8'}))
    return
  }
  const image = new Image()
  const url = URL.createObjectURL(new Blob([serialized], {type: 'image/svg+xml'}))
  try {
    await new Promise<void>((resolve, reject) => { image.onload = () => resolve(); image.onerror = () => reject(new Error('Could not render chart')); image.src = url })
    const scale = 4
    const canvas = document.createElement('canvas')
    canvas.width = 720 * scale
    canvas.height = 230 * scale
    const context = canvas.getContext('2d')
    if (!context) return
    context.fillStyle = '#ffffff'
    context.fillRect(0, 0, canvas.width, canvas.height)
    context.drawImage(image, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/png', 1))
    if (blob) downloadBlob(`${name}-600dpi.png`, blob)
  } finally {
    URL.revokeObjectURL(url)
  }
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

function chartPath(points: MetricPoint[], x: (value: number) => number, y: (value: number) => number): string {
  return points.map((point, index) => `${index ? 'L' : 'M'}${x(new Date(point.timestamp).getTime()).toFixed(2)},${y(point.value).toFixed(2)}`).join(' ')
}

function validateDataset(dataset: TimeSeriesDataset) {
  if (dataset.apiVersion !== 'artifacts.kubephos.dev/v1alpha1' || dataset.kind !== 'TimeSeriesDataset' || dataset.metadata?.version !== 'v1alpha1' || !dataset.spec?.applicationRef || !dataset.spec?.namespace || !Array.isArray(dataset.spec?.series) || !dataset.spec.series.length) throw new Error('The artifact does not implement TimeSeriesDataset/v1alpha1.')
  for (const series of dataset.spec.series) {
    if (!series.metric || !series.unit || !Array.isArray(series.points) || !series.points.length || series.points.some(point => !Number.isFinite(point.value) || !Number.isFinite(new Date(point.timestamp).getTime()))) throw new Error('The dataset contains an invalid metric series.')
  }
}

function datasetLabel(artifact: Artifact, operations: Operation[], workspaces: Workspace[]): string {
  const operation = operations.find(item => item.id === artifact.operationId)
  const workspace = workspaces.find(item => item.id === artifact.workspaceId)
  return `${workspace?.name ?? 'Workspace'} · ${operation?.title ?? artifact.name}`
}

function metricTitle(metric: string): string {
  return metric.split('.').map(value => value.replaceAll('_', ' ')).map(value => value.slice(0, 1).toUpperCase() + value.slice(1)).join(' · ')
}

function formatMetric(value: number, unit: string): string {
  if (!Number.isFinite(value)) return '—'
  if (unit === 'bytes') {
    const divisor = value >= 1024 ** 3 ? 1024 ** 3 : value >= 1024 ** 2 ? 1024 ** 2 : value >= 1024 ? 1024 : 1
    const suffix = divisor === 1024 ** 3 ? 'GiB' : divisor === 1024 ** 2 ? 'MiB' : divisor === 1024 ? 'KiB' : 'B'
    return `${(value / divisor).toFixed(divisor === 1 ? 0 : 1)} ${suffix}`
  }
  if (unit === 'cores') return value < 1 ? `${(value * 1000).toFixed(0)}m` : value.toFixed(2)
  return value.toLocaleString(undefined, {maximumFractionDigits: 2})
}

function ResultStat({label, value}: {label: string; value: string | number}) {
  return <div><span>{label}</span><strong>{value}</strong></div>
}

function EmptyResults() {
  return <div className="empty-state"><div><strong>No metrics collected</strong>Run the metrics collector from a workspace to create the first verified dataset.</div></div>
}
