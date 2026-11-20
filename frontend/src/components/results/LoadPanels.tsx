import type {LoadedDataset} from '../../resultsData'
import type {Experiment} from '../../types'
import {ResponsiveTable} from '../ResponsiveTable'
import {formatNumber} from './format'
import {fidelityThresholds, loadFidelity, type LoadFidelity} from './loadFidelity'
import {locustColumns, type AcrossRuns, type LocustGroup} from './locust'

const cpuWarning = 'Load generator saturated — latency may be inflated'

function cell(value: AcrossRuns | undefined, digits = 1, suffix = '') {
  if (!value) return '—'
  if (value.n < 2 || value.low === undefined || value.high === undefined) return `${formatNumber(value.mean, digits)}${suffix}`
  return `${formatNumber(value.mean, digits)} ± ${formatNumber((value.high - value.low) / 2, digits)}${suffix}`
}

function cellTitle(value: AcrossRuns | undefined) {
  if (!value || value.n < 2 || value.low === undefined || value.high === undefined) return undefined
  return `Mean across ${value.n} runs; 95% CI ${formatNumber(value.low, 2)} to ${formatNumber(value.high, 2)} (exact Student t, n − 1 df)`
}

export function LocustSummaryTable({group}: {group: LocustGroup}) {
  const repeated = group.runs > 1
  return <section className="statistics-card locust-summary" aria-label={`Locust summary for ${group.label}`}>
    <div className="locust-summary-head"><h3>{group.label}</h3><p className="table-lead">{repeated ? `${group.runs} executions: mean ± half-width of the 95% CI across executions (exact t).` : 'Exact statistics over the measured window, merged from all Locust workers.'}{group.workers ? ` ${group.workers} Locust worker${group.workers === 1 ? '' : 's'}.` : ''}</p>
      {group.cpuWarnings.length > 0 && <span className="quality-badge warning" title={group.cpuWarnings.map(item => `${item.run}: ${item.worker} reached ${item.maxPercent.toFixed(0)}% CPU (${item.warnings} warning${item.warnings === 1 ? '' : 's'})`).join('\n')}>{cpuWarning}</span>}
    </div>
    <div className="statistics-table-wrap"><ResponsiveTable className="locust-table" aria-label={`Locust endpoints for ${group.label}`}>
      <thead><tr><th>Endpoint</th><th>Requests</th><th>Failures</th><th>Failure %</th>{locustColumns.map(([, label]) => <th key={label}>{label} (ms)</th>)}</tr></thead>
      <tbody>{group.rows.map(row => <tr key={row.key} className={row.aggregated ? 'aggregated-row' : undefined}>
        <td>{row.aggregated ? <strong>Aggregated</strong> : <><span className="http-method">{row.method}</span> {row.name}</>}</td>
        <td title={cellTitle(row.requests)}>{cell(row.requests, repeated ? 1 : 0)}</td>
        <td title={cellTitle(row.failures)}>{cell(row.failures, repeated ? 1 : 0)}</td>
        <td title={cellTitle(row.failurePercent) ?? 'Exact: failures / requests'}>{cell(row.failurePercent, 3, '%')}</td>
        {locustColumns.map(([key]) => <td key={key} title={cellTitle(row.latency[key])}>{cell(row.latency[key])}</td>)}
      </tr>)}</tbody>
    </ResponsiveTable></div>
    {group.errors.length > 0 && <div className="statistics-table-wrap"><ResponsiveTable className="locust-errors" aria-label={`Locust errors for ${group.label}`}>
      <thead><tr><th>Endpoint</th><th>Error</th><th>Occurrences</th>{repeated && <th>Executions</th>}</tr></thead>
      <tbody>{group.errors.map(error => <tr key={`${error.method} ${error.name} ${error.error}`}><td><span className="http-method">{error.method}</span> {error.name}</td><td><code>{error.error}</code></td><td>{error.occurrences.toLocaleString('en-US')}</td>{repeated && <td>{error.runs}</td>}</tr>)}</tbody>
    </ResponsiveTable></div>}
  </section>
}

export function LocustSummaryPanel({groups}: {groups: LocustGroup[]}) {
  if (!groups.length) return <section className="statistics-card"><h3>Locust summary</h3><p className="empty-inline">These runs have no Locust summary (collected before exact load-generator statistics were recorded). Windowed client-side metrics are still available under Statistics and Plots.</p></section>
  return <section aria-label="Locust summary" className="locust-panel"><h3>Locust summary</h3><p className="table-lead">Percentiles come from the Locust response-time histograms of all workers, merged over the measured window (never averaged across workers). Failure % is exact: failures / requests.</p>{groups.map(group => <LocustSummaryTable key={group.id} group={group} />)}</section>
}

export function FidelitySummaryTable({rows}: {rows: Array<{label: string; fidelity: LoadFidelity}>}) {
  const shown = rows.filter(row => row.fidelity.ratio !== undefined || row.fidelity.schedule)
  if (!shown.length) return null
  return <div className="statistics-table-wrap"><ResponsiveTable aria-label="Load fidelity per execution">
    <thead><tr><th>Execution</th><th>Offered rate</th><th>Achieved rate</th><th>Achieved / offered</th><th>Late requests</th><th>Mean lag</th><th>Max lag</th></tr></thead>
    <tbody>{shown.map(({label, fidelity}) => <tr key={label} className={fidelity.warnings.length ? 'fidelity-warning' : undefined}>
      <td>{label}</td>
      <td title={fidelity.offeredSource === 'configured' ? 'Configured rate (offered rate not recorded)' : 'Sum of generator target rates'}>{fidelity.offeredMean === undefined ? '—' : `${formatNumber(fidelity.offeredMean, 2)} req/s${fidelity.offeredSource === 'configured' ? ' (configured)' : ''}`}</td>
      <td title={fidelity.achievedSource === 'successful+failed' ? 'Successful + failed request rate (achieved rate not recorded)' : undefined}>{fidelity.achievedMean === undefined ? '—' : `${formatNumber(fidelity.achievedMean, 2)} req/s`}</td>
      <td>{fidelity.ratio === undefined ? '—' : `${(fidelity.ratio * 100).toFixed(1)}%`}</td>
      <td>{fidelity.schedule?.source === 'locust-summary' ? `${fidelity.schedule.lateRequests.toLocaleString('en-US')}${fidelity.schedule.lateShare !== undefined ? ` (${(fidelity.schedule.lateShare * 100).toFixed(2)}%)` : ''}` : '—'}</td>
      <td>{fidelity.schedule?.meanLagSeconds === undefined ? '—' : `${(fidelity.schedule.meanLagSeconds * 1000).toFixed(1)} ms`}</td>
      <td>{fidelity.schedule?.maxLagSeconds === undefined ? '—' : `${(fidelity.schedule.maxLagSeconds * 1000).toFixed(0)} ms`}</td>
    </tr>)}</tbody>
  </ResponsiveTable><p className="table-footnote">Warning thresholds: achieved rate below {fidelityThresholds.achievedRatio * 100}% of the offered rate over the measured window; ≥ {fidelityThresholds.lateShare * 100}% of requests issued late; or mean schedule lag ≥ {fidelityThresholds.meanLagSeconds * 1000} ms. A request is late when it is issued more than max(50 ms, 0.5/target rate) after its scheduled slot.</p></div>
}

export function fidelityRows(datasets: LoadedDataset[]) {
  return datasets.map(item => ({label: item.label, fidelity: loadFidelity(item)}))
}

export interface QualityNotice {kind: 'warning' | 'note'; text: string}

export function cpuSaturationNotices(groups: LocustGroup[]): QualityNotice[] {
  return groups.filter(group => group.cpuWarnings.length).map(group => {
    const workers = new Set(group.cpuWarnings.map(item => item.worker)).size
    return {kind: 'warning' as const, text: `${cpuWarning} (${group.label}: Locust reported CPU usage above 90% on ${workers} worker${workers === 1 ? '' : 's'}).`}
  })
}

export function QualityNotices({notices}: {notices: QualityNotice[]}) {
  if (!notices.length) return null
  return <div className="quality-notices" role="region" aria-label="Measurement quality">{notices.map(notice => <div key={notice.text} className={`validation-item ${notice.kind === 'warning' ? 'warning' : 'note'}`}>{notice.text}</div>)}</div>
}

export function experimentCleanupNotices(experiment: Experiment | undefined): QualityNotice[] {
  return (experiment?.variants ?? []).flatMap(variant => variant.trials.filter(trial => trial.cleanupError).map(trial => ({kind: 'note' as const, text: `${variant.alias || variant.name} · ${trial.pilot ? 'Warm-up' : `Execution ${trial.position}`}: result kept — environment cleanup failed: ${trial.cleanupError}`})))
}
