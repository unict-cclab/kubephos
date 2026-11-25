import {useMemo} from 'react'
import type {LoadedDataset} from '../../resultsData'
import type {Experiment} from '../../types'
import {formatNumber} from './format'
import {fidelityThresholds, loadFidelity, type LoadFidelity} from './loadFidelity'
import {locustColumns, type AcrossRuns, type LocustGroup, type LocustRow} from './locust'
import {formatCount, formatSignificant} from './display'
import {SortHeader, TableScroll, useSort, worstRows} from './DataTable'

const cpuWarning = 'Load generator saturated — latency may be inflated'

function cellTitle(value: AcrossRuns | undefined) {
  if (!value || value.n < 2 || value.low === undefined || value.high === undefined) return undefined
  return `Mean across ${value.n} runs; 95% CI ${formatNumber(value.low, 2)} to ${formatNumber(value.high, 2)} (exact Student t, n − 1 df)`
}

const locustSortKeys = (): Record<string, (row: LocustRow) => number | string | undefined> => ({
  endpoint: row => row.name, requests: row => row.requests.mean, failures: row => row.failures.mean, failurePercent: row => row.failurePercent?.mean,
  ...Object.fromEntries(locustColumns.map(([key]) => [key, (row: LocustRow) => row.latency[key]?.mean]))
})

// One run: exact value. Repetitions: mean ± half-width of the 95% CI, both at the same precision.
function measure(value: AcrossRuns | undefined, format: (value: number) => string) {
  if (!value) return '—'
  if (value.n < 2 || value.low === undefined || value.high === undefined) return format(value.mean)
  return `${format(value.mean)} ± ${format((value.high - value.low) / 2)}`
}
const percent = (value: number) => value === 0 ? '0' : value >= 1 ? formatSignificant(value, 3) : formatSignificant(value, 2)

function EndpointCells({row, repeated, worst}: {row: LocustRow; repeated: boolean; worst: (key: string) => boolean}) {
  const cellOf = (key: string, value: AcrossRuns | undefined, format: (value: number) => string, title?: string) => {
    const text = measure(value, format)
    return <td key={key} className={`numeric ${worst(key) ? 'worst' : ''}`.trim()} title={cellTitle(value) ?? title}>{worst(key) ? <strong>{text}</strong> : text}</td>
  }
  return <>
    {cellOf('requests', row.requests, repeated ? value => formatSignificant(value) : formatCount)}
    {cellOf('failures', row.failures, repeated ? value => formatSignificant(value) : formatCount)}
    {cellOf('failurePercent', row.failurePercent, percent, 'Exact: failures / requests')}
    {locustColumns.map(([key]) => cellOf(key, row.latency[key], value => formatSignificant(value)))}
  </>
}

function EndpointName({row}: {row: LocustRow}) {
  return row.aggregated ? <>Aggregated</> : <span className="endpoint-name"><span className="http-method">{row.method}</span> <span className="endpoint-path">{row.name}</span></span>
}

export function LocustSummaryTable({group}: {group: LocustGroup}) {
  const repeated = group.runs > 1
  const endpoints = useMemo(() => group.rows.filter(row => !row.aggregated), [group.rows])
  const total = group.rows.find(row => row.aggregated)
  const accessors = useMemo(locustSortKeys, [])
  const {sorted, sort, toggle} = useSort(endpoints, accessors)
  // The slowest / most failing endpoint per column, set in bold (only when it stands out).
  const worst = useMemo(() => {
    const keys = ['failurePercent', ...locustColumns.map(([key]) => key)]
    return new Map(keys.map(key => [key, new Set([...worstRows(endpoints.map(row => accessors[key](row) as number | undefined))].map(index => endpoints[index].key))]))
  }, [endpoints, accessors])
  const isWorst = (row: LocustRow) => (key: string) => !row.aggregated && Boolean(worst.get(key)?.has(row.key))
  const anyWorst = [...worst.values()].some(set => set.size > 0)
  return <section className="statistics-card locust-summary" aria-label={`Locust summary for ${group.label}`}>
    <div className="locust-summary-head"><h3>{group.label}</h3><p className="table-lead">{repeated ? `${group.runs} executions: mean ± half-width of the 95% CI across executions (exact t).` : 'Exact statistics over the measured window, merged from all Locust workers.'}{group.workers ? ` ${group.workers} Locust worker${group.workers === 1 ? '' : 's'}.` : ''}</p>
      {group.cpuWarnings.length > 0 && <span className="quality-badge warning" title={group.cpuWarnings.map(item => `${item.run}: ${item.worker} reached ${item.maxPercent.toFixed(0)}% CPU (${item.warnings} warning${item.warnings === 1 ? '' : 's'})`).join('\n')}>{cpuWarning}</span>}
    </div>
    <TableScroll label={`Endpoints of ${group.label}, scrollable`}><table className="report-table locust-table sticky-first" aria-label={`Locust endpoints for ${group.label}`}>
      <thead><tr>
        <SortHeader id="endpoint" label="Endpoint" sort={sort} onSort={toggle} />
        <SortHeader id="requests" label="Requests" numeric sort={sort} onSort={toggle} />
        <SortHeader id="failures" label="Failures" numeric sort={sort} onSort={toggle} />
        <SortHeader id="failurePercent" label="Failure %" numeric sort={sort} onSort={toggle} title="Exact: failures / requests" />
        {locustColumns.map(([key, label]) => <SortHeader key={key} id={key} label={`${label} (ms)`} numeric sort={sort} onSort={toggle} />)}
      </tr></thead>
      <tbody>{sorted.map(row => <tr key={row.key}><th scope="row"><EndpointName row={row} /></th><EndpointCells row={row} repeated={repeated} worst={isWorst(row)} /></tr>)}</tbody>
      {total && <tfoot><tr className="aggregated-row"><th scope="row"><EndpointName row={total} /></th><EndpointCells row={total} repeated={repeated} worst={() => false} /></tr></tfoot>}
    </table></TableScroll>
    <p className="table-footnote">Latencies in milliseconds, three significant digits.{anyWorst ? ' Bold: the highest value of a column, when one or two endpoints stand out.' : ''} — not recorded.</p>
    {group.errors.length > 0 && <TableScroll label={`Errors of ${group.label}, scrollable`}><table className="report-table locust-errors" aria-label={`Locust errors for ${group.label}`}>
      <thead><tr><th scope="col">Endpoint</th><th scope="col">Error</th><th scope="col" className="numeric">Occurrences</th>{repeated && <th scope="col" className="numeric">Executions</th>}</tr></thead>
      <tbody>{group.errors.map(error => <tr key={`${error.method} ${error.name} ${error.error}`}><td><span className="endpoint-name"><span className="http-method">{error.method}</span> <span className="endpoint-path">{error.name}</span></span></td><td className="error-text"><code>{error.error}</code></td><td className="numeric">{formatCount(error.occurrences)}</td>{repeated && <td className="numeric">{error.runs}</td>}</tr>)}</tbody>
    </table></TableScroll>}
  </section>
}

export function LocustSummaryPanel({groups}: {groups: LocustGroup[]}) {
  if (!groups.length) return <section className="statistics-card"><h3>Locust summary</h3><p className="empty-inline">These runs have no Locust summary (collected before exact load-generator statistics were recorded). Windowed client-side metrics are still available under Statistics and Plots.</p></section>
  return <section aria-label="Locust summary" className="locust-panel"><h3>Locust summary</h3><p className="table-lead">Percentiles come from the Locust response-time histograms of all workers, merged over the measured window (never averaged across workers). Failure % is exact: failures / requests.</p>{groups.map(group => <LocustSummaryTable key={group.id} group={group} />)}</section>
}

export function FidelitySummaryTable({rows}: {rows: Array<{label: string; fidelity: LoadFidelity}>}) {
  const shown = rows.filter(row => row.fidelity.ratio !== undefined || row.fidelity.schedule)
  if (!shown.length) return null
  return <><TableScroll label="Load fidelity, scrollable"><table className="report-table sticky-first" aria-label="Load fidelity per execution">
    <thead><tr><th scope="col">Execution</th><th scope="col" className="numeric">Offered (req/s)</th><th scope="col" className="numeric">Achieved (req/s)</th><th scope="col" className="numeric">Achieved / offered (%)</th><th scope="col" className="numeric">Late requests</th><th scope="col" className="numeric">Mean lag (ms)</th><th scope="col" className="numeric">Max lag (ms)</th></tr></thead>
    <tbody>{shown.map(({label, fidelity}) => <tr key={label} className={fidelity.warnings.length ? 'fidelity-warning' : undefined}>
      <th scope="row">{label}</th>
      <td className="numeric" title={fidelity.offeredSource === 'configured' ? 'Configured rate (offered rate not recorded)' : 'Sum of generator target rates'}>{formatSignificant(fidelity.offeredMean)}{fidelity.offeredMean !== undefined && fidelity.offeredSource === 'configured' && <small className="cell-note"> configured</small>}</td>
      <td className="numeric" title={fidelity.achievedSource === 'successful+failed' ? 'Successful + failed request rate (achieved rate not recorded)' : undefined}>{formatSignificant(fidelity.achievedMean)}</td>
      <td className={`numeric ${fidelity.ratio !== undefined && fidelity.ratio < fidelityThresholds.achievedRatio ? 'worst' : ''}`.trim()}>{fidelity.ratio === undefined ? '—' : fidelity.ratio < fidelityThresholds.achievedRatio ? <strong>{formatSignificant(fidelity.ratio * 100)}</strong> : formatSignificant(fidelity.ratio * 100)}</td>
      <td className="numeric">{fidelity.schedule?.source === 'locust-summary' ? `${formatCount(fidelity.schedule.lateRequests)}${fidelity.schedule.lateShare !== undefined ? ` (${formatSignificant(fidelity.schedule.lateShare * 100)} %)` : ''}` : '—'}</td>
      <td className="numeric">{fidelity.schedule?.meanLagSeconds === undefined ? '—' : formatSignificant(fidelity.schedule.meanLagSeconds * 1000)}</td>
      <td className="numeric">{fidelity.schedule?.maxLagSeconds === undefined ? '—' : formatSignificant(fidelity.schedule.maxLagSeconds * 1000)}</td>
    </tr>)}</tbody>
  </table></TableScroll><p className="table-footnote">Bold: achieved rate below the threshold. — not recorded. Warning thresholds: achieved rate below {fidelityThresholds.achievedRatio * 100}% of the offered rate over the measured window; ≥ {fidelityThresholds.lateShare * 100}% of requests issued late; or mean schedule lag ≥ {fidelityThresholds.meanLagSeconds * 1000} ms. A request is late when it is issued more than max(50 ms, 0.5/target rate) after its scheduled slot.</p></>
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
