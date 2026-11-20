import {Fragment, useEffect, useId, useMemo, useRef, useState, type ReactNode} from 'react'
import type {AggregateSeries} from '../../resultsData'
import {displayValue, formatChange} from './display'
import type {Insight, InsightSeverity, ServiceHealth} from './insights'
import type {QualityNotice} from './LoadPanels'
import {chartSeries, significance, toChartPoints, type KpiCard, type KpiEntry, type OverviewGroup} from './overview'
import {provenanceLabel} from './format'
import type {ResolvedProvenance} from './runs'
import {Sparkline, TimeChart, type ChartPanel} from './TimeChart'

export type InsightTarget = NonNullable<Insight['target']>

// --- verdict ---

const severityLabel: Record<InsightSeverity, string> = {critical: 'Critical', warning: 'Warning', info: 'Note', ok: 'OK'}

export function SeverityIcon({severity}: {severity: InsightSeverity}) {
  const paths: Record<InsightSeverity, ReactNode> = {
    critical: <><circle cx="8" cy="8" r="7" /><path d="M8 4.5v4.2M8 11.2v.3" /></>,
    warning: <><path d="M8 1.8 15 14H1Z" /><path d="M8 6.2v3.6M8 11.8v.2" /></>,
    info: <><circle cx="8" cy="8" r="7" /><path d="M8 7.2v4.3M8 4.6v.2" /></>,
    ok: <><circle cx="8" cy="8" r="7" /><path d="m5 8.2 2 2 4-4.2" /></>
  }
  return <svg className={`severity-icon ${severity}`} width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" fill="none" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round">{paths[severity]}</svg>
}

const targetLabel: Record<InsightTarget, string> = {throughput: 'Show load chart', latency: 'Show latency chart', services: 'Show services', endpoints: 'Show endpoints', quality: 'Show environment'}

export function VerdictStrip({insights, notices, onNavigate, empty}: {insights: Insight[]; notices: QualityNotice[]; onNavigate: (target: InsightTarget) => void; empty?: string}) {
  const shown = insights.slice(0, 3)
  const more = insights.slice(3)
  const notes = more.length + notices.length
  return <section className="verdict" aria-label="Verdict">
    {shown.length ? <ul className="verdict-list">{shown.map(insight => <li key={insight.id} className={`verdict-item ${insight.severity}`}>
      <SeverityIcon severity={insight.severity} />
      <div className="verdict-text">
        <strong><span className="visually-hidden">{severityLabel[insight.severity]}: </span>{insight.title}</strong>
        <p>{insight.detail}</p>
      </div>
      {insight.target && <button type="button" className="link-button verdict-link" onClick={() => onNavigate(insight.target!)}>{targetLabel[insight.target]}<span aria-hidden="true"> →</span></button>}
    </li>)}</ul> : <div className="verdict-item ok"><SeverityIcon severity="ok" /><div className="verdict-text"><strong>{empty ?? 'No issue detected'}</strong><p>Load was sustained and no service stands out. Details are in the sections below.</p></div></div>}
    {notes > 0 && <details className="verdict-notes">
      <summary>{notes} more measurement note{notes === 1 ? '' : 's'}</summary>
      <ul>{more.map(insight => <li key={insight.id}><SeverityIcon severity={insight.severity} /><span><strong>{insight.title}.</strong> {insight.detail}</span></li>)}
        {notices.map(notice => <li key={notice.text}><SeverityIcon severity={notice.kind === 'warning' ? 'warning' : 'info'} /><span>{notice.text}</span></li>)}</ul>
    </details>}
  </section>
}

// --- KPI cards ---

function Source({provenance}: {provenance?: ResolvedProvenance}) {
  const id = useId()
  if (!provenance) return null
  return <span className="kpi-source" tabIndex={0} aria-describedby={id}>
    {provenanceLabel(provenance)}
    <span role="tooltip" id={id} className="kpi-source-tip">{provenance.description || 'No description recorded.'}{provenance.rateWindowSeconds ? ` Rate window ${provenance.rateWindowSeconds} s.` : ''}{provenance.legacy ? ' Provenance inferred from the metric catalog.' : ''}</span>
  </span>
}

function toneText(tone: KpiEntry['tone']) {
  return tone === 'critical' ? 'critical' : tone === 'warning' ? 'needs attention' : tone === 'ok' ? 'ok' : ''
}

function ChangeBadge({entry, better}: {entry: KpiEntry; better: 'higher' | 'lower'}) {
  if (entry.baseline) return <span className="kpi-baseline">baseline</span>
  if (entry.change === undefined) return <span className="kpi-baseline">no change computable</span>
  const significant = significance(entry.comparison)
  const improved = better === 'higher' ? entry.change > 0 : entry.change < 0
  return <span className="kpi-change-group">
    <span className={`kpi-change ${Math.abs(entry.change) < .5 ? 'flat' : improved ? 'better' : 'worse'}`}>{formatChange(entry.change)}</span>
    {significant && <span className={`kpi-significance ${significant.tone}`} title={significant.title}>{significant.label}</span>}
  </span>
}

export function KpiCards({cards, provenance, sparklines}: {cards: KpiCard[]; provenance: (metric: string) => ResolvedProvenance | undefined; sparklines: Record<string, AggregateSeries | undefined>}) {
  if (!cards.length) return null
  return <div className="kpi-grid" role="list" aria-label="Key results">{cards.map(card => {
    const single = card.entries.length === 1
    const entry = card.entries[0]
    const shown = displayValue(entry.value, card.unit)
    const trend = single ? sparklines[card.id] : undefined
    return <article key={card.id} className={`kpi-card ${single && entry.tone ? `tone-${entry.tone}` : ''}`} role="listitem" aria-label={card.title} id={`kpi-${card.id}`}>
      <div className="kpi-head"><h3>{card.title}</h3>{trend && <Sparkline points={toChartPoints(trend)} color={entry.tone === 'critical' || entry.tone === 'warning' ? 'var(--kpi-trend-alert)' : 'var(--chart-1)'} />}</div>
      {single ? <>
        <p className="kpi-value"><span className="kpi-number">{shown.value}</span>{shown.unit && <>{' '}<span className="kpi-unit">{shown.unit}</span></>}{entry.tone && entry.tone !== 'ok' && <span className="visually-hidden"> ({toneText(entry.tone)})</span>}</p>
        <p className="kpi-detail">{entry.tone && entry.tone !== 'ok' && <span className={`kpi-tone ${entry.tone}`} aria-hidden="true" />}{card.detail(entry) ?? ' '}</p>
      </> : <ul className="kpi-series">{card.entries.map(item => {
        const value = displayValue(item.value, card.unit)
        return <li key={item.group.id}>
          <span className="kpi-swatch" style={{background: item.group.color}} aria-hidden="true" />
          <span className="kpi-series-label" title={card.detail(item)}>{item.group.label}</span>
          <span className="kpi-series-value">{value.text}{item.tone && item.tone !== 'ok' && <span className={`kpi-tone ${item.tone}`} title={toneText(item.tone)} />}</span>
          <ChangeBadge entry={item} better={card.better} />
        </li>
      })}</ul>}
      <p className="kpi-caption"><Source provenance={provenance(card.metric)} />{entry.n > 1 && single ? <span> · mean of {entry.n} repetitions</span> : null}</p>
    </article>
  })}</div>
}

// --- primary chart ---

export function PrimaryChart({panels, subtitle}: {panels: ChartPanel[]; subtitle: string}) {
  if (!panels.length) return null
  return <section className="results-card primary-chart" id="results-timeline" aria-labelledby="results-timeline-title">
    <div className="results-card-head"><div><h3 id="results-timeline-title">Load and latency over time</h3><p>{subtitle}</p></div></div>
    <TimeChart panels={panels} ariaLabel={`Request rate and client latency over the measured window: ${panels.map(panel => panel.series.map(series => series.label).join(', ')).join('; ')}`} />
  </section>
}

// --- services table ---

const serviceColumns = [
  {key: 'rps', label: 'Req/s', title: 'Mean request rate reaching the service (server side)'},
  {key: 'p95', label: 'Server P95', title: 'Mean of the window P95 latency at the service (mesh proxy, server side)'},
  {key: 'cpu', label: 'CPU', title: 'Mean CPU usage of all pods (cores); share of the CPU limit when recorded'},
  {key: 'replicas', label: 'Replicas', title: 'Mean ready replicas'},
  {key: 'restarts', label: 'Restarts', title: 'Container restarts during the run'}
] as const

export interface ServiceDetail {title: string; unit: string; series: AggregateSeries[]}

export function ServicesTable({services, groups, group, onGroup, trend, detail, note}: {services: ServiceHealth[]; groups?: OverviewGroup[]; group?: string; onGroup?: (id: string) => void; trend?: (service: string) => AggregateSeries | undefined; detail?: (service: string) => ServiceDetail[]; note?: string}) {
  const [open, setOpen] = useState<string | undefined>()
  const [all, setAll] = useState(false)
  const limit = 8
  const ranked = services
  const shown = all ? ranked : ranked.slice(0, limit)
  const columns = 2 + serviceColumns.length + (trend ? 1 : 0)
  return <section className="results-card services-card" id="results-services" aria-labelledby="results-services-title">
    <div className="results-card-head">
      <div><h3 id="results-services-title">Services</h3><p>{note ?? 'Ranked by bottleneck score: server latency weighted by the share of requests that reach the service.'}</p></div>
      {groups && groups.length > 1 && onGroup && <div className="segmented" role="group" aria-label="Variant">{groups.map(item => <button key={item.id} type="button" aria-pressed={item.id === group} onClick={() => onGroup(item.id)}><span className="kpi-swatch" style={{background: item.color}} aria-hidden="true" />{item.label}</button>)}</div>}
    </div>
    {!ranked.length ? <p className="empty-inline">No per-service metrics were collected for these runs.</p> : <div className="services-table-wrap"><table className="services-table" aria-label="Services ranked by bottleneck score">
      <thead><tr><th scope="col">Service</th>{serviceColumns.map(column => <th key={column.key} scope="col" title={column.title} className={`numeric col-${column.key}`}>{column.label}</th>)}{trend && <th scope="col" className="trend-cell">P95 trend</th>}</tr></thead>
      <tbody>{shown.map(row => {
        const expanded = open === row.service
        const cpu = row.cpuCores === undefined ? '—' : `${row.cpuCores.toFixed(2)}${row.cpuLimitCores ? ` · ${Math.round(row.cpuCores / row.cpuLimitCores * 100)} %` : ''}`
        const points = trend?.(row.service)
        return <Fragment key={row.service}>
          <tr className={`${row.bottleneck ? 'bottleneck' : ''} ${expanded ? 'expanded' : ''}`}>
            <th scope="row">
              {detail ? <button type="button" className="service-toggle" aria-expanded={expanded} onClick={() => setOpen(expanded ? undefined : row.service)}><svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="m4 2.5 3.5 3.5L4 9.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>{row.service}</button> : <span className="service-name">{row.service}</span>}
              {row.bottleneck && <span className="bottleneck-badge" title={row.reasons.join('; ')}>Bottleneck</span>}
              {row.bottleneck && <span className="service-reason">{row.reasons.slice(0, 2).join(' · ')}</span>}
            </th>
            <td className="numeric">{row.rps === undefined ? '—' : displayValue(row.rps, 'requests_per_second', true).text}</td>
            <td className="numeric">{row.p95Ms === undefined ? '—' : displayValue(row.p95Ms, 'milliseconds').text}</td>
            <td className="numeric" title={row.cpuLimitCores ? `${row.cpuCores?.toFixed(3)} of ${row.cpuLimitCores} cores limit` : 'cores; no CPU limit recorded'}>{cpu}</td>
            <td className="numeric col-replicas">{row.replicas === undefined ? '—' : displayValue(row.replicas, 'replicas').text}</td>
            <td className="numeric col-restarts">{row.restarts === undefined ? '—' : Math.round(row.restarts)}</td>
            {trend && <td className="trend-cell">{points ? <Sparkline points={toChartPoints(points)} color={row.bottleneck ? 'var(--kpi-trend-alert)' : 'var(--chart-1)'} width={88} height={22} /> : null}</td>}
          </tr>
          {expanded && detail && <tr className="service-detail-row"><td colSpan={columns}><ServiceDetailCharts service={row.service} reasons={row.bottleneck ? row.reasons.slice(2) : row.reasons} charts={detail(row.service)} /></td></tr>}
        </Fragment>
      })}</tbody>
    </table></div>}
    {ranked.length > limit && <button type="button" className="link-button services-more" aria-expanded={all} onClick={() => setAll(value => !value)}>{all ? 'Show top services only' : `Show all ${ranked.length} services`}</button>}
  </section>
}

function ServiceDetailCharts({service, reasons, charts}: {service: string; reasons: string[]; charts: ServiceDetail[]}) {
  const visible = charts.filter(chart => chart.series.length)
  return <div className="service-detail">
    {reasons.length > 0 && <p className="service-detail-reasons">{reasons.join(' · ')}</p>}
    {visible.length ? <div className="service-detail-grid">{visible.map(chart => <div key={chart.title} className="service-detail-chart">
      <span className="time-chart-panel-title">{chart.title}</span>
      <TimeChart compact panelHeight={96} ariaLabel={`${service}: ${chart.title} over time`} panels={[{id: chart.title, title: chart.title, unit: chart.unit, series: chart.series.map((series, index) => chartSeries(series, `${index}`, series.label, `var(--chart-${index % 8 + 1})`))}]} />
    </div>)}</div> : <p className="empty-inline">No time series recorded for {service}.</p>}
  </div>
}

// --- disclosure sections ---

export function Disclosure({id, title, summary, open, onToggle, children}: {id: string; title: string; summary?: string; open: boolean; onToggle: (open: boolean) => void; children: ReactNode}) {
  return <section className={`disclosure ${open ? 'open' : ''}`} id={`section-${id}`}>
    <h3 className="disclosure-heading"><button type="button" aria-expanded={open} aria-controls={open ? `section-${id}-body` : undefined} onClick={() => onToggle(!open)}>
      <svg className="disclosure-chevron" width="14" height="14" viewBox="0 0 14 14" aria-hidden="true"><path d="m5 3 4 4-4 4" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" /></svg>
      <span className="disclosure-title">{title}</span>
      {summary && <>{' '}<span className="disclosure-summary">{summary}</span></>}
    </button></h3>
    {open && <div className="disclosure-body" id={`section-${id}-body`}>{children}</div>}
  </section>
}

// --- header menu ---

export function HeaderMenu({label, icon, children, align = 'end', buttonClass = 'button secondary'}: {label: string; icon?: ReactNode; children: (close: () => void) => ReactNode; align?: 'start' | 'end'; buttonClass?: string}) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const id = useId()
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {if (!root.current?.contains(event.target as Node)) setOpen(false)}
    const escape = (event: globalThis.KeyboardEvent) => {if (event.key === 'Escape') {setOpen(false); root.current?.querySelector<HTMLButtonElement>('button')?.focus()}}
    document.addEventListener('mousedown', outside)
    document.addEventListener('keydown', escape)
    return () => {document.removeEventListener('mousedown', outside); document.removeEventListener('keydown', escape)}
  }, [open])
  return <div className="header-menu" ref={root}>
    <button type="button" className={buttonClass} aria-expanded={open} aria-controls={open ? id : undefined} onClick={() => setOpen(value => !value)}>{icon}{label}</button>
    {open && <div className={`header-menu-panel ${align}`} id={id}>{children(() => setOpen(false))}</div>}
  </div>
}

// Memoised map from metric to its first aggregated curve (sparklines).
export function useSparklines(plot: (metric: string) => AggregateSeries[], groups: OverviewGroup[]): Record<string, AggregateSeries | undefined> {
  return useMemo(() => {
    if (groups.length !== 1) return {}
    const first = (metric: string) => plot(metric)[0]
    return {throughput: first('load.achieved_rps') ?? first('load.successful_rps'), latency: first('load.p95_response_time'), errors: first('load.failure_percentage'), slo: first('load.slo_violation_percentage') && {...first('load.slo_violation_percentage')!, points: first('load.slo_violation_percentage')!.points.map(point => ({...point, value: 100 - point.value}))}}
  }, [plot, groups])
}
