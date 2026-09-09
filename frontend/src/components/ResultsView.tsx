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

const colors = ['#177554', '#3169a8', '#a9660c', '#8b4fa3']

export function ResultsView({artifacts, experiments, operations, workspaces, session, changed}: Props) {
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
    setSelected(current => current.includes(id) ? current.filter(item => item !== id) : current.length < 4 ? [...current, id] : current)
  }
  const experiment = experiments.find(item => item.id === activeExperiment)
  const experimentLabels = new Map<string, string>()
  for (const variant of experiment?.variants ?? []) for (const trial of variant.trials) experimentLabels.set(trial.resultArtifactId, `${variant.name} · Trial ${trial.position}`)
  const displayed = loaded.map(item => ({...item, label: experimentLabels.get(item.artifact.id) ?? datasetLabel(item.artifact, operations, workspaces)}))
  const metrics = [...new Set(displayed.flatMap(item => item.dataset.spec.series.map(series => series.metric)))].sort()
  const samples = displayed.reduce((total, item) => total + item.dataset.spec.summary.samples, 0)
  const selectedWorkspaceID = available.find(item => item.id === selected[0])?.workspaceId
  const compatibleExperiments = experiments.filter(item => item.resultType === 'TimeSeriesDataset' && item.resultVersion === 'v1alpha1')
  const openExperiment = (item: Experiment) => {
    const artifactIDs = item.variants.flatMap(variant => variant.trials.map(trial => trial.resultArtifactId)).filter(id => available.some(artifact => artifact.id === id))
    setActiveExperiment(item.id)
    setSelected(artifactIDs.slice(0, 4))
    setNotice(artifactIDs.length > 4 ? 'This experiment has more than four trials. Showing the first four.' : '')
  }
  return <>
    <div className="results-heading"><div><p className="eyebrow">REPEATABLE EVIDENCE</p><h2>Compare collected results</h2><p>Select up to four verified datasets. Use the same variant name for multiple trials, then save the immutable comparison.</p></div><div className="results-heading-actions"><button className="button primary" disabled={selected.length < 2} onClick={() => setSaving(true)}>Save experiment</button><div className="results-count"><strong>{available.length}</strong><span>datasets</span></div></div></div>
    {compatibleExperiments.length > 0 && <div className="experiment-history"><div className="experiment-history-title"><strong>Saved experiments</strong><span>{compatibleExperiments.length} immutable comparisons</span></div><div className="experiment-cards">{compatibleExperiments.map(item => <button key={item.id} className={activeExperiment === item.id ? 'active' : ''} onClick={() => openExperiment(item)}><span className="status-dot succeeded" /><span><strong>{item.name}</strong><small>{item.variants.length} variants · {item.variants.reduce((total, variant) => total + variant.trials.length, 0)} trials · {formatDate(item.createdAt)}</small></span></button>)}</div></div>}
    {!available.length ? <EmptyResults /> : <div className="results-layout">
      <aside className="dataset-picker" aria-label="Available datasets">
        <div className="dataset-picker-title"><strong>Dataset history</strong><span>{selected.length}/4 selected</span></div>
        {available.map(artifact => {
          const operation = operations.find(item => item.id === artifact.operationId)
          const workspace = workspaces.find(item => item.id === artifact.workspaceId)
          const active = selected.includes(artifact.id)
          const incompatibleWorkspace = Boolean(selectedWorkspaceID && artifact.workspaceId !== selectedWorkspaceID)
          return <button className={`dataset-option ${active ? 'active' : ''}`} key={artifact.id} onClick={() => toggle(artifact.id)} disabled={!active && (selected.length === 4 || incompatibleWorkspace)} aria-pressed={active}>
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
          <div className="dataset-downloads">{displayed.map(item => <a key={item.artifact.id} href={`/api/v1/artifacts/${item.artifact.id}/download`}><span>↓</span><span><strong>{item.label}</strong><small>Download verified JSON</small></span></a>)}</div>
        </>}
      </div>
    </div>}
    <SaveExperimentDialog open={saving} close={() => setSaving(false)} selected={selected.map(id => available.find(item => item.id === id)).filter((item): item is Artifact => Boolean(item))} operations={operations} workspaces={workspaces} session={session} changed={changed} />
  </>
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
  return <article className="metric-card">
    <div className="metric-card-header"><div><p className="eyebrow">{unit.toUpperCase()}</p><h3>{metricTitle(metric)}</h3></div><strong>{formatMetric(maxY, unit)} peak</strong></div>
    <div className="chart-wrap"><svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${metricTitle(metric)} comparison chart`}>
      {[0, .25, .5, .75, 1].map(tick => {
        const value = minY + (maxY - minY) * tick
        const py = y(value)
        return <g key={tick}><line x1={padding.left} y1={py} x2={width - padding.right} y2={py} className="chart-grid-line" /><text x={padding.left - 8} y={py + 4} textAnchor="end">{formatMetric(value, unit)}</text></g>
      })}
      {series.map((item, index) => <path key={item.artifactID} d={chartPath(item.points, x, y)} fill="none" stroke={colors[index % colors.length]} strokeWidth="2.4" vectorEffect="non-scaling-stroke" />)}
      <text x={padding.left} y={height - 8}>{new Date(minX).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'})}</text>
      <text x={width - padding.right} y={height - 8} textAnchor="end">{new Date(maxX).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit'})}</text>
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
      points: [...values.entries()].sort((left, right) => left[0] - right[0]).map(([timestamp, value]) => ({timestamp: new Date(timestamp).toISOString(), value}))
    }
  }).filter(item => item.points.length > 0)
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
