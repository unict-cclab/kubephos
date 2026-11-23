import {useContext, useId, useRef, useState, type KeyboardEvent, type ReactNode} from 'react'
import {aggregateMetric, counterRunTotal, cumulativeCounterMetrics, isControlMetric, podSchedulingStatistics, serviceStatisticsRows, summarizeTimeSeries, variantStatistics, type LoadedDataset, type ScientificSummary, type VariantComparison} from '../../resultsData'
import type {ComparisonColumns} from '../../comparisonPreparation'
import type {Experiment} from '../../types'
import {ResponsiveTable} from '../ResponsiveTable'
import {ProvenanceDisclosure} from './Provenance'
import {provenanceFor, type ResolvedProvenance} from './runs'
import {ShowEverything, useRowLimit} from './DataTable'
import {controlMetricDescription, effectMagnitude, formatDifference, formatInterval, formatMetric, formatNumber, formatPValue, metricTitle, rowScale, scaledInterval, scaledValue, type RowScale} from './format'

export type ProvenanceOf = (metric: string) => ResolvedProvenance

export function statisticsMetricTitle(metric: string, provenance?: ResolvedProvenance): string {
  return metric === 'redis.operation_p95_response_time' ? 'Redis operation P95 per window' : metricTitle(metric, provenance)
}

// 95% CI text with units (CSV-like contexts): never a zero-width interval for n < 2.
export function intervalText(summary: Pick<ScientificSummary, 'count' | 'confidence95Low' | 'confidence95High' | 'nEffective'>, unit: string): string {
  if (!hasInterval(summary)) return 'n/a'
  return `${formatMetric(summary.confidence95Low, unit)}–${formatMetric(summary.confidence95High, unit)}`
}

const hasInterval = (summary: Pick<ScientificSummary, 'count' | 'nEffective'>) => !(summary.count < 2 || (summary.nEffective !== undefined && summary.nEffective < 2))
const scaledCI = (summary: ScientificSummary, scale: RowScale) => hasInterval(summary) ? scaledInterval(summary.confidence95Low, summary.confidence95High, scale) : 'n/a'
const variation = (summary: ScientificSummary) => Number.isFinite(summary.coefficientOfVariation) ? `${(summary.coefficientOfVariation * 100).toFixed(1)}%` : '—'

const neffHelp = 'Effective sample size of the autocorrelated window series: n_eff = n(1 − ρ₁)/(1 + ρ₁), with ρ₁ the lag-1 autocorrelation, clamped to [2, n]. The 95% CI of the time-averaged mean uses n_eff − 1 degrees of freedom.'
const unitHelp = 'Unit of every number in the row (the 95% CI included)'

// Metric name and its unit, written once per row.
function MetricCells({metric, provenance, unit, title}: {metric: string; provenance?: ResolvedProvenance; unit: string; title?: string}) {
  return <><td className="metric-cell" title={title}>{statisticsMetricTitle(metric, provenance)}</td><td className="unit-cell">{unit || '—'}</td></>
}

function SourceCell({metric, provenance}: {metric: string; provenance: ResolvedProvenance}) {
  return <td className="provenance-cell"><ProvenanceDisclosure plain metric={metric} provenance={provenance} /></td>
}

function TableFoot({toggle, children}: {toggle: ReactNode; children?: ReactNode}) {
  if (!toggle && !children) return null
  return <div className="statistics-foot">{toggle}{children}</div>
}

// --- within one execution ---

interface RunRow {metric: string; unit: string; title?: string; n?: number; neff?: string; neffTitle?: string; meanTitle: string; summary?: ScientificSummary; value?: number; exact?: number}

function runRow(dataset: LoadedDataset, metric: string): RunRow | undefined {
  const series = aggregateMetric([dataset], metric)[0]
  const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
  if (!series && !control) return undefined
  const unit = series?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
  const title = control ? controlMetricDescription(metric, control.scope, control.reason, control.timestampResolutionSeconds) : undefined
  const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
  if (podSummary) return {metric, unit, title, n: podSummary.count, neffTitle: 'Independent pods', meanTitle: 'Mean per pod', summary: podSummary}
  if (isControlMetric(metric)) return {metric, unit, title, n: control?.count, meanTitle: control?.total !== undefined || metric === 'descheduler.evictions' ? 'Run total' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : 'Mean per cycle', value: control?.total ?? control?.mean}
  if (cumulativeCounterMetrics.includes(metric)) return {metric, unit, title, n: series!.points.length, meanTitle: 'Increase during the run', value: counterRunTotal(dataset.dataset, metric)}
  const summary = summarizeTimeSeries(series!.points.map(point => point.value))
  const locust = dataset.dataset.spec.locustSummary
  const exact = metric === 'load.failure_percentage' && locust && locust.requests > 0 ? 100 * locust.failures / locust.requests : undefined
  return {metric, unit, title, n: summary.count, neff: summary.nEffective === undefined ? '—' : formatNumber(summary.nEffective, 1), neffTitle: summary.lag1Autocorrelation === undefined ? 'Fewer than 3 samples or constant series: n_eff = n' : `ρ₁ = ${summary.lag1Autocorrelation.toFixed(3)}`, meanTitle: exact !== undefined ? 'Mean of window percentages' : 'Mean over time', summary, exact}
}

function RunStatisticsRow({row, provenance}: {row: RunRow; provenance: ResolvedProvenance}) {
  const {summary} = row
  const scale = rowScale(row.unit, summary?.mean ?? row.value)
  const dashes = (count: number) => Array.from({length: count}, (_, index) => <td key={index}>—</td>)
  return <tr>
    <MetricCells metric={row.metric} provenance={provenance} unit={scale.unit} title={row.title} />
    <SourceCell metric={row.metric} provenance={provenance} />
    <td>{row.n ?? '—'}</td>
    <td title={row.neffTitle}>{row.neff ?? '—'}</td>
    <td title={row.meanTitle}>{scaledValue(summary?.mean ?? row.value, scale)}{row.exact !== undefined && <small className="exact-value" title="Exact failures / requests over the measured window (Locust summary); used for run-level comparisons"> · exact {scaledValue(row.exact, scale)}</small>}</td>
    {summary ? <>{[summary.standardDeviation, summary.min, summary.median, summary.max].map((value, index) => <td key={index}>{scaledValue(value, scale)}</td>)}<td>{variation(summary)}</td><td className="ci-cell">{scaledCI(summary, scale)}</td></> : dashes(6)}
  </tr>
}

export function RunStatisticsTable({dataset, metrics}: {dataset: LoadedDataset; metrics: string[]}) {
  const rows = metrics.map(metric => runRow(dataset, metric)).filter((row): row is RunRow => Boolean(row))
  const {shown, toggle} = useRowLimit(rows, 10, 'metrics')
  return <><div className="statistics-table-wrap"><ResponsiveTable className="runs-table dense" aria-label={`Statistics for ${dataset.label}`}>
    <thead><tr><th className="text-col">Metric</th><th className="text-col" title={unitHelp}>Unit</th><th className="text-col">Source</th><th title="Window samples">n</th><th title={neffHelp}>n<sub>eff</sub></th><th>Mean / total</th><th title="Standard deviation">SD</th><th>Min</th><th>Median</th><th>Max</th><th title="Coefficient of variation">CV</th><th>95% CI</th></tr></thead>
    <tbody>{shown.map(row => <RunStatisticsRow key={row.metric} row={row} provenance={provenanceFor([dataset], row.metric)} />)}</tbody>
  </ResponsiveTable></div>
  <TableFoot toggle={toggle} />
  <p className="table-footnote">n: window samples in the measured window. n<sub>eff</sub>: {neffHelp} 95% CI of the mean, shown as n/a when fewer than two samples are available.</p></>
}

// --- across executions ---

function SummaryCells({summary, scale}: {summary: ScientificSummary; scale: RowScale}) {
  return <>{[summary.mean, summary.standardDeviation, summary.min, summary.median, summary.max].map((value, index) => <td key={index}>{scaledValue(value, scale)}</td>)}</>
}

export function ServiceStatisticsTable({datasets, experiment, match = () => true}: {datasets: LoadedDataset[]; experiment?: Experiment; match?: (metric: string) => boolean}) {
  const [query, setQuery] = useState('')
  const rows = serviceStatisticsRows(datasets, experiment).filter(row => (!experiment || row.acrossRuns) && match(row.metric))
  const allServices = [...new Set(rows.map(row => row.service))]
  const services = allServices.filter(service => service.toLowerCase().includes(query.trim().toLowerCase())).sort((left, right) => left.localeCompare(right, undefined, {numeric: true}))
  const {shown, toggle} = useRowLimit(services, 10, 'services')
  const strategies = new Set(rows.map(row => row.group)).size > 1
  const columns = 9 + (strategies ? 1 : 0)
  if (!rows.length) return <p className="empty-inline">No per-service statistics{experiment ? ' across executions' : ''} for the selected metrics.</p>
  return <div className="service-statistics"><div className="list-toolbar"><input type="search" aria-label={`Search services for ${experiment ? 'all executions' : datasets[0].label}`} placeholder="Search services…" value={query} onChange={event => setQuery(event.target.value)} /><span>{services.length} of {allServices.length} services</span></div>
    {!services.length && <p className="empty-inline">No services match your search.</p>}
    <div className="statistics-table-wrap"><ResponsiveTable className="runs-table dense" aria-label={experiment ? 'Service statistics across executions' : `Service statistics for ${datasets[0].label}`}>
      <thead><tr><th className="text-col">Metric</th><th className="text-col" title={unitHelp}>Unit</th>{strategies && <th className="text-col">Strategy</th>}<th title={experiment ? 'Executions' : 'Window samples'}>n</th><th>Mean</th><th title="Standard deviation">SD</th><th>Min</th><th>Median</th><th>Max</th><th>95% CI</th></tr></thead>
      {shown.map(service => <tbody key={service}><tr className="service-statistics-group"><th scope="rowgroup" colSpan={columns}>{service}</th></tr>{rows.filter(row => row.service === service).map(row => {
        const scale = rowScale(row.unit, row.summary.mean)
        return <tr key={`${row.metric}-${row.group}`}><MetricCells metric={row.metric} unit={scale.unit} />{strategies && <td>{row.group}</td>}<td>{row.summary.count}</td><SummaryCells summary={row.summary} scale={scale} /><td className="ci-cell">{scaledCI(row.summary, scale)}</td></tr>
      })}</tbody>)}
    </ResponsiveTable></div>
    <TableFoot toggle={toggle} />
  </div>
}

export function AcrossRunsTable({experiment, datasets, metrics, provenance}: {experiment: Experiment; datasets: LoadedDataset[]; metrics: string[]; provenance: ProvenanceOf}) {
  const rows = variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2)
  const {shown, toggle} = useRowLimit(rows, 10, 'metrics')
  if (!rows.length) return <p className="empty-inline">No metric has values in at least two executions{metrics.length ? '' : ' for this filter'}.</p>
  const strategies = new Set(rows.map(row => row.variant)).size > 1
  return <section className="statistics-card"><p className="table-lead">One value per execution (its mean over the measured window); interval: 95% CI of the mean across executions, exact Student t with n − 1 df.</p><div className="statistics-table-wrap"><ResponsiveTable className="aggregate-table dense" aria-label="Statistics across executions">
    <thead><tr>{strategies && <th className="text-col">Strategy</th>}<th className="text-col">Metric</th><th className="text-col" title={unitHelp}>Unit</th><th className="text-col">Source</th><th title="Executions with a value (of all executions)">n</th><th title="Mean across executions">Mean</th><th title="Standard deviation">SD</th><th>Min</th><th>Median</th><th>Max</th><th title="Coefficient of variation">CV</th><th>95% CI</th></tr></thead>
    <tbody>{shown.map(row => {
      const scale = rowScale(row.unit, row.summary.mean)
      return <tr key={`${row.variant}-${row.metric}`}>{strategies && <td>{row.variant}</td>}<MetricCells metric={row.metric} provenance={provenance(row.metric)} unit={scale.unit} /><SourceCell metric={row.metric} provenance={provenance(row.metric)} /><td title={row.missing ? `${row.missing} execution(s) without this metric` : undefined}>{row.missing ? `${row.summary.count}/${row.summary.count + row.missing}` : row.summary.count}</td><SummaryCells summary={row.summary} scale={scale} /><td>{variation(row.summary)}</td><td className="ci-cell">{scaledCI(row.summary, scale)}</td></tr>
    })}</tbody></ResponsiveTable></div><TableFoot toggle={toggle} /></section>
}

export function StrategyComparison({datasets, metrics, experiment, prepared, provenance}: {datasets: LoadedDataset[]; metrics: string[]; experiment: Experiment; prepared?: ComparisonColumns; provenance: ProvenanceOf}) {
  const columns = prepared ?? experiment.variants.map(variant => ({
    variant,
    rows: variantStatistics({...experiment, variants: [variant]}, datasets, metrics),
    available: variant.trials.filter(trial => datasets.some(dataset => dataset.artifact.id === trial.resultArtifactId)).length,
  }))
  const visibleMetrics = metrics.filter(metric => columns.some(column => column.rows.some(row => row.metric === metric)))
  const {shown, toggle} = useRowLimit(visibleMetrics, 10, 'metrics')
  const label = experiment.kind === 'comparison' ? 'Experiment comparison' : 'Strategy comparison'
  if (!visibleMetrics.length) return <p className="empty-inline">No metric matches the filter.</p>
  return <section className="statistics-card">
    <p className="table-lead">Mean ± sample standard deviation across repetitions, in the row's unit.</p>
    <div className="statistics-table-wrap"><ResponsiveTable className="dense" aria-label={label}>
      <thead><tr><th className="text-col">Metric</th><th className="text-col" title={unitHelp}>Unit</th><th className="text-col">Source</th>{columns.map(({variant, available}) => <th key={variant.id}>{variant.alias || variant.name}{experiment.kind !== 'comparison' && <small className="muted"> · {available}/{variant.trials.length} executions</small>}</th>)}</tr></thead>
      <tbody>{shown.map(metric => {
        const first = columns.flatMap(column => column.rows.filter(row => row.metric === metric))[0]
        const scale = rowScale(first?.unit ?? '', first?.summary.mean)
        return <tr key={metric}>
          <MetricCells metric={metric} provenance={provenance(metric)} unit={scale.unit} />
          <SourceCell metric={metric} provenance={provenance(metric)} />
          {columns.map(({variant, rows}) => {
            const row = rows.find(item => item.metric === metric)
            const aggregation = metric === 'descheduler.evictions' ? 'Total evictions per run' : cumulativeCounterMetrics.includes(metric) ? 'Total increase per run' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : isControlMetric(metric) ? 'Mean per cycle' : metric === 'load.failure_percentage' ? 'Exact failures/requests when a Locust summary exists, else mean over time windows' : 'Mean over time windows'
            return <td key={variant.id} title={row ? experiment.kind === 'comparison' ? aggregation : `${aggregation}; ${row.summary.count} execution(s) with this metric${row.summary.count > 1 ? '; mean ± sample standard deviation across executions' : ''}` : 'No collected data'}>{row ? <>{scaledValue(row.summary.mean, scale)}{row.summary.count > 1 && <> ± {scaledValue(row.summary.standardDeviation, scale)}</>}</> : '—'}</td>
          })}
        </tr>
      })}</tbody>
    </ResponsiveTable></div>
    <TableFoot toggle={toggle} />
  </section>
}

// One execution at a time: a selector when the view holds several.
export function WithinRunStatistics({datasets, metrics}: {datasets: LoadedDataset[]; metrics: string[]}) {
  const [selected, setSelected] = useState(datasets[0]?.artifact.id)
  const everything = useContext(ShowEverything)
  const id = useId()
  const dataset = datasets.find(item => item.artifact.id === selected) ?? datasets[0]
  if (!dataset) return null
  const shown = everything ? datasets : [dataset]
  return <>
    {datasets.length > 1 && !everything && <div className="statistics-toolbar-row"><label htmlFor={id}>Execution</label><select id={id} value={dataset.artifact.id} onChange={event => setSelected(event.target.value)}>{datasets.map(item => <option key={item.artifact.id} value={item.artifact.id}>{item.label}</option>)}</select><span className="muted">{datasets.length} executions</span></div>}
    {shown.map(item => <section className="statistics-card" key={item.artifact.id} aria-label={`Execution ${item.label}`}>{(datasets.length === 1 || everything) && <h4>{item.label}</h4>}<RunStatisticsTable dataset={item} metrics={metrics} /></section>)}
  </>
}

export interface ComparisonTableProps {
  rows: VariantComparison[]
  variants: Array<{id: string; label: string}>
  baseline: string
  mode: 'baseline' | 'all-pairs'
  onChange: (next: {baseline: string; mode: 'baseline' | 'all-pairs'}) => void
  provenance: ProvenanceOf
  title?: string
  match?: (metric: string) => boolean
}

// Between-configuration inference on run-level values (variantComparisons).
export function ComparisonTable({rows: allRows, variants, baseline, mode, onChange, provenance, title = 'Differences between strategies', match = () => true}: ComparisonTableProps) {
  const rows = allRows.filter(row => match(row.metric))
  const {shown, toggle} = useRowLimit(rows, 10, 'rows')
  if (variants.length < 2) return null
  const familySize = allRows[0]?.familySize ?? 0
  const metrics = [...new Set(allRows.map(row => row.metric))]
  const baselineLabel = variants.find(variant => variant.id === baseline)?.label ?? variants[0].label
  return <section className="statistics-card comparison-table-card">
    <div className="comparison-table-head">
      <div className="comparison-controls" role="group" aria-label="Comparison options">
        <label>Baseline<select value={baseline} disabled={mode === 'all-pairs'} onChange={event => onChange({baseline: event.target.value, mode})}>{variants.map(variant => <option key={variant.id} value={variant.id}>{variant.label}</option>)}</select></label>
        <label>Comparisons<select value={mode} onChange={event => onChange({baseline, mode: event.target.value as 'baseline' | 'all-pairs'})}><option value="baseline">Each vs baseline</option><option value="all-pairs">All pairs</option></select></label>
      </div>
    </div>
    {!rows.length ? <p className="empty-inline">{allRows.length ? 'No metric matches the filter.' : 'No metric has values in both groups.'}</p> : <div className="statistics-table-wrap"><ResponsiveTable className="effects-table dense" aria-label={title}>
      <thead><tr><th className="text-col">Metric</th><th className="text-col">Comparison</th><th>Δ mean</th><th>Δ %</th><th>95% CI of Δ</th><th>Hedges' g [95% CI]</th><th>Magnitude</th><th>Welch p</th><th>Holm p</th><th>Mann–Whitney p</th><th>n</th></tr></thead>
      <tbody>{shown.map(row => <tr key={`${row.metric}-${row.baselineId}-${row.groupId}`} className={row.insufficient ? 'insufficient' : undefined}>
        <td className="metric-cell">{statisticsMetricTitle(row.metric, provenance(row.metric))}</td>
        <td>{row.group} vs {row.baseline}{row.insufficient && <span className="stat-flag" title="Fewer than 3 executions in at least one group: inference is not reliable">n &lt; 3</span>}{row.zeroVariance && <span className="stat-flag" title="At least one group has identical values in every execution: Welch's test and Hedges' g may be undefined">zero variance</span>}</td>
        <td>{formatDifference(row.difference, row.unit)}</td>
        <td>{row.relativeDifferencePercent === undefined ? 'n/a' : `${row.relativeDifferencePercent > 0 ? '+' : row.relativeDifferencePercent < 0 ? '−' : ''}${Math.abs(row.relativeDifferencePercent).toFixed(1)}%`}</td>
        <td>{formatInterval(row.differenceLow, row.differenceHigh, row.unit)}</td>
        <td>{row.hedgesG === undefined ? 'n/a' : `${formatNumber(row.hedgesG)} [${formatNumber(row.hedgesGLow)}, ${formatNumber(row.hedgesGHigh)}]`}</td>
        <td>{effectMagnitude(row.hedgesG)}</td>
        <td>{formatPValue(row.pValue)}</td>
        <td>{formatPValue(row.adjustedPValue)}</td>
        <td>{formatPValue(row.mannWhitneyPValue)}{row.mannWhitneyMethod && <small className="muted"> ({row.mannWhitneyMethod})</small>}</td>
        <td>{row.baselineCount} / {row.groupCount}</td>
      </tr>)}</tbody>
    </ResponsiveTable></div>}
    <TableFoot toggle={toggle} />
    <p className="table-footnote">Δ = comparison − reference ({mode === 'all-pairs' ? 'the earlier configuration of each pair' : baselineLabel}), computed on run-level values (one mean per execution). 95% CI of Δ and p from Welch's two-sided t-test (Welch–Satterthwaite df). Holm–Bonferroni adjustment over the family of {familySize} Welch tests ({mode === 'all-pairs' ? 'all pairs' : 'each configuration vs the baseline'} × {metrics.length} metric{metrics.length === 1 ? '' : 's'}; the metric filter does not change it). Hedges' g: bias-corrected standardized difference with an approximate normal 95% CI; magnitude per Cohen's thresholds (0.2/0.5/0.8). Mann–Whitney U: two-sided, exact without ties for n ≤ 50, otherwise normal approximation with tie correction. Rows flagged n &lt; 3 have too few executions for reliable inference.</p>
  </section>
}

// --- one table at a time ---

export interface StatisticsView {
  id: string
  title: string
  // Views listing metrics react to the metric filter.
  metrics?: boolean
  render: (match: (metric: string) => boolean) => ReactNode
}

// Tabs over the statistics tables plus a metric filter; only the selected table is rendered
// (every table when printing).
export function StatisticsTabs({views, label = 'Statistics tables'}: {views: StatisticsView[]; label?: string}) {
  const [active, setActive] = useState(views[0]?.id)
  const [query, setQuery] = useState('')
  // Views opened once stay mounted (hidden), so their own state (searches, selections) survives.
  const [visited, setVisited] = useState<string[]>([])
  const everything = useContext(ShowEverything)
  const id = useId()
  const list = useRef<HTMLDivElement>(null)
  if (!views.length) return null
  const current = views.find(view => view.id === active) ?? views[0]
  if (!visited.includes(current.id)) setVisited(list => list.includes(current.id) ? list : [...list, current.id])
  const needle = query.trim().toLowerCase()
  const match = (metric: string) => !needle || metric.toLowerCase().includes(needle) || statisticsMetricTitle(metric).toLowerCase().includes(needle)
  const keyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    const offset = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0
    const next = event.key === 'Home' ? 0 : event.key === 'End' ? views.length - 1 : offset ? (index + offset + views.length) % views.length : -1
    if (next < 0) return
    event.preventDefault()
    setActive(views[next].id)
    list.current?.querySelectorAll<HTMLButtonElement>('[role="tab"]')[next]?.focus()
  }
  if (everything) return <div className="statistics-views">{views.map(view => <section key={view.id} className="statistics-view-print" aria-label={view.title}><h3>{view.title}</h3>{view.render(() => true)}</section>)}</div>
  return <div className="statistics-views">
    <div className="statistics-toolbar">
      <div className="statistics-tabs" role="tablist" aria-label={label} ref={list}>{views.map((view, index) => <button key={view.id} type="button" role="tab" id={`${id}-${view.id}`} aria-controls={view.id === current.id || visited.includes(view.id) ? `${id}-${view.id}-panel` : undefined} aria-selected={view.id === current.id} tabIndex={view.id === current.id ? 0 : -1} onClick={() => setActive(view.id)} onKeyDown={event => keyDown(event, index)}>{view.title}</button>)}</div>
      {current.metrics && <input type="search" className="statistics-filter" aria-label="Filter metrics" placeholder="Filter metrics…" value={query} onChange={event => setQuery(event.target.value)} />}
    </div>
    {views.filter(view => view.id === current.id || visited.includes(view.id)).map(view => <div key={view.id} role="tabpanel" id={`${id}-${view.id}-panel`} aria-labelledby={`${id}-${view.id}`} className="statistics-panel" hidden={view.id !== current.id}>{view.render(view.metrics ? match : () => true)}</div>)}
  </div>
}
