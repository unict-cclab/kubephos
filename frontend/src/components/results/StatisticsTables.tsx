import {useState} from 'react'
import {aggregateMetric, counterRunTotal, cumulativeCounterMetrics, isControlMetric, podSchedulingStatistics, serviceStatisticsRows, summarizeTimeSeries, variantStatistics, type LoadedDataset, type ScientificSummary, type VariantComparison} from '../../resultsData'
import type {ComparisonColumns} from '../../comparisonPreparation'
import type {Experiment} from '../../types'
import {ResponsiveTable} from '../ResponsiveTable'
import {FormTabs} from '../FormTabs'
import {ProvenanceDisclosure} from './Provenance'
import {provenanceFor, type ResolvedProvenance} from './runs'
import {controlMetricDescription, effectMagnitude, formatDifference, formatInterval, formatMetric, formatNumber, formatPValue, metricTitle} from './format'

export type ProvenanceOf = (metric: string) => ResolvedProvenance

export function statisticsMetricTitle(metric: string, provenance?: ResolvedProvenance): string {
  return metric === 'redis.operation_p95_response_time' ? 'Redis operation P95 per window' : metricTitle(metric, provenance)
}

// 95% CI cell: never a zero-width interval for n < 2.
export function intervalText(summary: Pick<ScientificSummary, 'count' | 'confidence95Low' | 'confidence95High' | 'nEffective'>, unit: string): string {
  if (summary.count < 2 || (summary.nEffective !== undefined && summary.nEffective < 2)) return 'n/a'
  return `${formatMetric(summary.confidence95Low, unit)}–${formatMetric(summary.confidence95High, unit)}`
}

const neffHelp = 'Effective sample size of the autocorrelated window series: n_eff = n(1 − ρ₁)/(1 + ρ₁), with ρ₁ the lag-1 autocorrelation, clamped to [2, n]. The 95% CI of the time-averaged mean uses n_eff − 1 degrees of freedom.'

function RunStatisticsRow({dataset, metric, provenance}: {dataset: LoadedDataset; metric: string; provenance: ResolvedProvenance}) {
  const series = aggregateMetric([dataset], metric)[0]
  const control = dataset.dataset.spec.controlMetrics?.find(item => item.metric === metric)
  if (!series && !control) return null
  const unit = series?.unit ?? (metric === 'descheduler.evictions' ? 'count' : 'milliseconds')
  const name = <td title={control ? controlMetricDescription(metric, control.scope, control.reason, control.timestampResolutionSeconds) : undefined}>{statisticsMetricTitle(metric, provenance)}</td>
  const source = <td className="provenance-cell"><ProvenanceDisclosure metric={metric} provenance={provenance} /></td>
  const dashes = (count: number) => Array.from({length: count}, (_, index) => <td key={index}>—</td>)
  const podSummary = metric === 'scheduler.pod_scheduling_latency' ? podSchedulingStatistics(dataset) : undefined
  if (podSummary) return <tr>{name}{source}<td>{podSummary.count}</td><td title="Independent pods">—</td><td title="Mean per pod">{formatMetric(podSummary.mean, unit)}</td><td>{formatMetric(podSummary.standardDeviation, unit)}</td><td>{formatMetric(podSummary.min, unit)}</td><td>{formatMetric(podSummary.median, unit)}</td><td>{formatMetric(podSummary.max, unit)}</td><td>{(podSummary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{intervalText(podSummary, unit)}</td></tr>
  if (isControlMetric(metric)) {
    const value = control?.total ?? control?.mean
    return <tr>{name}{source}<td>{control?.count ?? '—'}</td><td>—</td><td title={control?.total !== undefined || metric === 'descheduler.evictions' ? 'Run total' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : 'Mean per cycle'}>{value === undefined ? '—' : formatMetric(value, unit)}</td>{dashes(6)}</tr>
  }
  if (cumulativeCounterMetrics.includes(metric)) {
    const total = counterRunTotal(dataset.dataset, metric)
    return <tr>{name}{source}<td>{series!.points.length}</td><td>—</td><td title="Increase during the run">{total === undefined ? '—' : formatMetric(total, unit)}</td>{dashes(6)}</tr>
  }
  const summary = summarizeTimeSeries(series!.points.map(point => point.value))
  const locust = dataset.dataset.spec.locustSummary
  const exactFailure = metric === 'load.failure_percentage' && locust && locust.requests > 0 ? 100 * locust.failures / locust.requests : undefined
  return <tr>{name}{source}<td>{summary.count}</td><td title={summary.lag1Autocorrelation === undefined ? 'Fewer than 3 samples or constant series: n_eff = n' : `ρ₁ = ${summary.lag1Autocorrelation.toFixed(3)}`}>{summary.nEffective === undefined ? '—' : formatNumber(summary.nEffective, 1)}</td><td title={exactFailure !== undefined ? 'Mean of window percentages' : 'Mean over time'}>{formatMetric(summary.mean, unit)}{exactFailure !== undefined && <small className="exact-value" title="Exact failures / requests over the measured window (Locust summary); used for run-level comparisons"> · exact {formatMetric(exactFailure, 'percent')}</small>}</td><td>{formatMetric(summary.standardDeviation, unit)}</td><td>{formatMetric(summary.min, unit)}</td><td>{formatMetric(summary.median, unit)}</td><td>{formatMetric(summary.max, unit)}</td><td>{(summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{intervalText(summary, unit)}</td></tr>
}

export function RunStatisticsTable({dataset, metrics}: {dataset: LoadedDataset; metrics: string[]}) {
  return <><div className="statistics-table-wrap"><ResponsiveTable className="runs-table" aria-label={`Statistics for ${dataset.label}`}>
    <thead><tr><th>Metric</th><th>Source</th><th>n</th><th title={neffHelp}>n<sub>eff</sub></th><th>Mean / total</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>Variation</th><th>95% CI of mean</th></tr></thead>
    <tbody>{metrics.map(metric => <RunStatisticsRow key={metric} dataset={dataset} metric={metric} provenance={provenanceFor([dataset], metric)} />)}</tbody>
  </ResponsiveTable></div>
  <p className="table-footnote">n: window samples in the measured window. n<sub>eff</sub>: {neffHelp} CI shown as n/a when fewer than two samples are available.</p></>
}

function SummaryCells({summary, unit}: {summary: ScientificSummary; unit: string}) {
  return <>{[summary.mean, summary.standardDeviation, summary.min, summary.median, summary.max].map((value, index) => <td key={index}>{formatMetric(value, unit)}</td>)}</>
}

export function ServiceStatisticsTable({datasets, experiment}: {datasets: LoadedDataset[]; experiment?: Experiment}) {
  const [query, setQuery] = useState('')
  const rows = serviceStatisticsRows(datasets, experiment).filter(row => !experiment || row.acrossRuns)
  if (!rows.length) return null
  const allServices = [...new Set(rows.map(row => row.service))]
  const services = allServices.filter(service => service.toLowerCase().includes(query.trim().toLowerCase())).sort((left, right) => left.localeCompare(right, undefined, {numeric: true}))
  return <div className="service-statistics"><h4>{experiment ? 'By service · Across executions' : 'By service'}</h4><div className="list-toolbar"><input type="search" aria-label={`Search services for ${experiment ? 'all executions' : datasets[0].label}`} placeholder="Search services…" value={query} onChange={event => setQuery(event.target.value)} /><span>{services.length} of {allServices.length}</span></div>{!services.length && <p className="empty-inline">No services match your search.</p>}<div className="statistics-table-wrap"><ResponsiveTable className="runs-table" aria-label={experiment ? 'Service statistics across executions' : `Service statistics for ${datasets[0].label}`}><thead><tr><th>Metric</th>{experiment && <th>Strategy</th>}<th>{experiment ? 'Executions' : 'Samples'}</th><th>Mean</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>95% CI of mean</th></tr></thead>{services.map(service => <tbody key={service}><tr className="service-statistics-group"><th scope="rowgroup" colSpan={experiment ? 9 : 8}>{service}</th></tr>{rows.filter(row => row.service === service).map(row => <tr key={`${row.metric}-${row.group}`}><td>{statisticsMetricTitle(row.metric)}</td>{experiment && <td>{row.group}</td>}<td>{row.summary.count}</td><SummaryCells summary={row.summary} unit={row.unit} /><td>{intervalText(row.summary, row.unit)}</td></tr>)}</tbody>)}</ResponsiveTable></div></div>
}

export function AcrossRunsTable({experiment, datasets, metrics, provenance}: {experiment: Experiment; datasets: LoadedDataset[]; metrics: string[]; provenance: ProvenanceOf}) {
  const rows = variantStatistics(experiment, datasets, metrics).filter(row => row.summary.count >= 2)
  if (!rows.length) return null
  return <section className="statistics-card"><div><h3>Across executions</h3><p className="table-lead">One value per execution (its mean over the measured window); interval: 95% CI of the mean across executions, exact Student t with n − 1 df.</p></div><div className="statistics-table-wrap"><ResponsiveTable className="aggregate-table" aria-label="Statistics across executions"><thead><tr><th>Strategy</th><th>Metric</th><th>Source</th><th>Executions</th><th>Executions missing</th><th>Mean across executions</th><th>Std. dev.</th><th>Min</th><th>Median</th><th>Max</th><th>Variation</th><th>95% CI of mean</th></tr></thead><tbody>{rows.map(row => <tr key={`${row.variant}-${row.metric}`}><td>{row.variant}</td><td>{statisticsMetricTitle(row.metric, provenance(row.metric))}</td><td className="provenance-cell"><ProvenanceDisclosure metric={row.metric} provenance={provenance(row.metric)} /></td><td>{row.summary.count}</td><td>{row.missing}</td><SummaryCells summary={row.summary} unit={row.unit} /><td>{(row.summary.coefficientOfVariation * 100).toFixed(1)}%</td><td>{intervalText(row.summary, row.unit)}</td></tr>)}</tbody></ResponsiveTable></div></section>
}

export function StrategyComparison({datasets, metrics, experiment, prepared, provenance}: {datasets: LoadedDataset[]; metrics: string[]; experiment: Experiment; prepared?: ComparisonColumns; provenance: ProvenanceOf}) {
  const columns = prepared ?? experiment.variants.map(variant => ({
    variant,
    rows: variantStatistics({...experiment, variants: [variant]}, datasets, metrics),
    available: variant.trials.filter(trial => datasets.some(dataset => dataset.artifact.id === trial.resultArtifactId)).length,
  }))
  const visibleMetrics = metrics.filter(metric => columns.some(column => column.rows.some(row => row.metric === metric)))
  if (!visibleMetrics.length) return null
  return <section className="statistics-card">
    <div><h3>{experiment.kind === 'comparison' ? 'Experiment comparison' : 'Strategy comparison'}</h3></div>
    <div className="statistics-table-wrap"><ResponsiveTable aria-label={experiment.kind === 'comparison' ? 'Experiment comparison' : 'Strategy comparison'}>
      <thead><tr><th>Metric</th><th>Source</th>{columns.map(({variant, available}) => <th key={variant.id}>{variant.alias || variant.name}{experiment.kind !== 'comparison' && <small className="muted"> · {available}/{variant.trials.length} executions</small>}</th>)}</tr></thead>
      <tbody>{visibleMetrics.map(metric => <tr key={metric}>
        <td>{statisticsMetricTitle(metric, provenance(metric))}</td>
        <td className="provenance-cell"><ProvenanceDisclosure metric={metric} provenance={provenance(metric)} /></td>
        {columns.map(({variant, rows}) => {
          const row = rows.find(item => item.metric === metric)
          const aggregation = metric === 'descheduler.evictions' ? 'Total evictions per run' : cumulativeCounterMetrics.includes(metric) ? 'Total increase per run' : metric === 'scheduler.pod_scheduling_latency' ? 'Mean per pod' : isControlMetric(metric) ? 'Mean per cycle' : metric === 'load.failure_percentage' ? 'Exact failures/requests when a Locust summary exists, else mean over time windows' : 'Mean over time windows'
          return <td key={variant.id} title={row ? experiment.kind === 'comparison' ? aggregation : `${aggregation}; ${row.summary.count} execution(s) with this metric${row.summary.count > 1 ? '; mean ± sample standard deviation across executions' : ''}` : 'No collected data'}>{row ? <>{formatMetric(row.summary.mean, row.unit)}{row.summary.count > 1 && <> ± {formatMetric(row.summary.standardDeviation, row.unit)}</>}</> : '—'}</td>
        })}
      </tr>)}</tbody>
    </ResponsiveTable></div>
  </section>
}

export function WithinRunStatistics({datasets, metrics}: {datasets: LoadedDataset[]; metrics: string[]}) {
  return <><h3>Within each execution</h3>
    {datasets.map(dataset => <section className="statistics-card" key={dataset.artifact.id}><div><h3>{dataset.label}</h3></div><FormTabs label={`Statistics for ${dataset.label}`} items={[{id: 'overall', title: 'Overall', content: <RunStatisticsTable dataset={dataset} metrics={metrics} />}, ...(serviceStatisticsRows([dataset]).length ? [{id: 'services', title: 'By service', content: <ServiceStatisticsTable datasets={[dataset]} />}] : [])]} /></section>)}
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
}

// Between-configuration inference on run-level values (variantComparisons).
export function ComparisonTable({rows, variants, baseline, mode, onChange, provenance, title = 'Differences between strategies'}: ComparisonTableProps) {
  if (variants.length < 2) return null
  const familySize = rows[0]?.familySize ?? 0
  const metrics = [...new Set(rows.map(row => row.metric))]
  const baselineLabel = variants.find(variant => variant.id === baseline)?.label ?? variants[0].label
  return <section className="statistics-card comparison-table-card">
    <div className="comparison-table-head"><h3>{title}</h3>
      <div className="comparison-controls" role="group" aria-label="Comparison options">
        <label>Baseline<select value={baseline} disabled={mode === 'all-pairs'} onChange={event => onChange({baseline: event.target.value, mode})}>{variants.map(variant => <option key={variant.id} value={variant.id}>{variant.label}</option>)}</select></label>
        <label>Comparisons<select value={mode} onChange={event => onChange({baseline, mode: event.target.value as 'baseline' | 'all-pairs'})}><option value="baseline">Each vs baseline</option><option value="all-pairs">All pairs</option></select></label>
      </div>
    </div>
    {!rows.length ? <p className="empty-inline">No metric has values in both groups.</p> : <div className="statistics-table-wrap"><ResponsiveTable className="effects-table" aria-label={title}>
      <thead><tr><th>Metric</th><th>Comparison</th><th>Δ mean</th><th>Δ %</th><th>95% CI of Δ</th><th>Hedges' g [95% CI]</th><th>Magnitude</th><th>Welch p</th><th>Holm p</th><th>Mann–Whitney p</th><th>n</th></tr></thead>
      <tbody>{rows.map(row => <tr key={`${row.metric}-${row.baselineId}-${row.groupId}`} className={row.insufficient ? 'insufficient' : undefined}>
        <td>{statisticsMetricTitle(row.metric, provenance(row.metric))}</td>
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
    <p className="table-footnote">Δ = comparison − reference ({mode === 'all-pairs' ? 'the earlier configuration of each pair' : baselineLabel}), computed on run-level values (one mean per execution). 95% CI of Δ and p from Welch's two-sided t-test (Welch–Satterthwaite df). Holm–Bonferroni adjustment over the family of {familySize} Welch tests shown in this table ({mode === 'all-pairs' ? 'all pairs' : 'each configuration vs the baseline'} × {metrics.length} metric{metrics.length === 1 ? '' : 's'}). Hedges' g: bias-corrected standardized difference with an approximate normal 95% CI; magnitude per Cohen's thresholds (0.2/0.5/0.8). Mann–Whitney U: two-sided, exact without ties for n ≤ 50, otherwise normal approximation with tie correction. Rows flagged n &lt; 3 have too few executions for reliable inference.</p>
  </section>
}
