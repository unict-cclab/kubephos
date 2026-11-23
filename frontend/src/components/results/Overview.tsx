import {Fragment, useEffect, useId, useRef, useState, type ReactNode} from 'react'
import type {AggregateSeries} from '../../resultsData'
import {displayValue, formatChange, formatCount, formatSignificant} from './display'
import {SortHeader, TableScroll, useSort} from './DataTable'
import type {Insight, InsightSeverity, ServiceHealth} from './insights'
import type {QualityNotice} from './LoadPanels'
import {chartSeries, significance, toChartPoints, type KpiCard, type KpiEntry, type OverviewGroup} from './overview'
import {provenanceLabel} from './format'
import type {ResolvedProvenance} from './runs'
import type {ResourceCard} from './resources'
import {LegendKey, Sparkline, TimeChart, type ChartPanel} from './TimeChart'

export type InsightTarget = NonNullable<Insight['target']>

// --- summary (verdict) ---
// Written like the opening of a lab report: the strongest findings as plain sentences, each
// ending with a reference to its evidence (Fig. 1, Table 1, …). Severity is carried by the
// wording and by a thin rule beside the paragraph; the reasoning sits in a quiet disclosure.

const severityLabel: Record<InsightSeverity, string> = {critical: 'Critical', warning: 'Warning', info: 'Note', ok: 'OK'}
const severityRank: Record<InsightSeverity, number> = {critical: 0, warning: 1, info: 2, ok: 3}

const reference: Record<InsightTarget, {text: string; label: string}> = {
  throughput: {text: 'Fig. 1', label: 'Go to figure 1, load and latency over time'},
  latency: {text: 'Fig. 1', label: 'Go to figure 1, load and latency over time'},
  services: {text: 'Table 1', label: 'Go to table 1, services'},
  endpoints: {text: 'Endpoints', label: 'Open the endpoints section'},
  quality: {text: 'Methods', label: 'Open the environment and methods section'}
}

const sentence = (text: string) => /[.!?]$/.test(text) ? text : `${text}.`

function Reference({target, onNavigate}: {target?: InsightTarget; onNavigate: (target: InsightTarget) => void}) {
  if (!target) return null
  return <>{' '}<span className="summary-ref">(<button type="button" className="ref-link" aria-label={reference[target].label} onClick={() => onNavigate(target)}>{reference[target].text}</button>)</span></>
}

export function VerdictStrip({insights, notices, onNavigate, empty}: {insights: Insight[]; notices: QualityNotice[]; onNavigate: (target: InsightTarget) => void; empty?: string}) {
  const shown = insights.slice(0, 3)
  const more = insights.slice(3)
  const worst = shown.reduce<InsightSeverity>((current, item) => severityRank[item.severity] < severityRank[current] ? item.severity : current, 'ok')
  const extra = more.length + notices.length
  const reasoning = shown.filter(item => item.detail)
  return <section className={`summary severity-${shown.length ? worst : 'ok'}`} aria-label="Verdict">
    <h3 className="summary-kicker">Summary</h3>
    {shown.length ? <p className="summary-lede">{shown.map((insight, index) => <span key={insight.id} className={`summary-sentence ${insight.severity}`}>
      {index > 0 && ' '}<span className="visually-hidden">{severityLabel[insight.severity]}: </span>{sentence(insight.title).replace(/\.$/, '')}<Reference target={insight.target} onNavigate={onNavigate} />.
    </span>)}</p> : <p className="summary-lede">{sentence(empty ?? 'No issue detected')} <span className="summary-quiet">The load was sustained and no single service dominates latency.</span></p>}
    {(reasoning.length > 0 || extra > 0) && <details className="summary-notes">
      <summary>{reasoning.length ? 'Reasoning' : 'Notes'}{extra > 0 ? `, and ${extra} more measurement note${extra === 1 ? '' : 's'}` : ''}</summary>
      <ol className="summary-notes-list">
        {reasoning.map(insight => <li key={insight.id}><p>{insight.detail}</p></li>)}
        {more.map(insight => <li key={insight.id} className="summary-note-extra"><p><span className="visually-hidden">{severityLabel[insight.severity]}: </span><em>{sentence(insight.title)}</em> {insight.detail}</p></li>)}
        {notices.map(notice => <li key={notice.text} className="summary-note-extra"><p>{notice.kind === 'warning' && <span className="visually-hidden">Warning: </span>}{notice.text}</p></li>)}
      </ol>
    </details>}
  </section>
}

// Single execution: no summary paragraph, only the measurement warnings that matter
// (target not sustained, concurrent load, generator saturated) as one quiet line.
export function QualityLine({notices}: {notices: QualityNotice[]}) {
  const warnings = notices.filter(notice => notice.kind === 'warning').map(notice => notice.text.replace(/^[^:]*·[^:]*:\s/, '').replace(/\.$/, ''))
  if (!warnings.length) return null
  // The line names each issue briefly; the full sentences sit one click away.
  const short = warnings.map(text => text.split(/\s[(—]|[:;]/)[0].trim())
  return <details className="quality-line" aria-label="Measurement quality">
    <summary>
      <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.8 15 14H1L8 1.8Z" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinejoin="round" /><path d="M8 6.2v3.6M8 11.6v.2" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></svg>
      <span><span className="quality-line-label">Measurement {warnings.length === 1 ? 'note' : 'notes'}:</span> {short.join(' · ')}</span>
    </summary>
    <ul>{warnings.map(text => <li key={text}>{text}.</li>)}</ul>
  </details>
}

// --- key figures ---
// A typographic row: large tabular numbers, small labels underneath, hairlines between.
// Sources go into one footnote line below the row instead of a caption under every figure.

const plainLabel = (text: string) => text.replace(/ · /g, ', ')

function Source({provenance, mark}: {provenance: ResolvedProvenance; mark?: number}) {
  const id = useId()
  return <span className="kpi-source" tabIndex={0} aria-describedby={id}>
    {mark !== undefined && <sup aria-hidden="true">{mark}</sup>}{plainLabel(provenanceLabel(provenance))}
    <span role="tooltip" id={id} className="kpi-source-tip">{provenance.description || 'No description recorded.'}{provenance.rateWindowSeconds ? ` Rate window ${provenance.rateWindowSeconds} s.` : ''}{provenance.legacy ? ' Provenance inferred from the metric catalog.' : ''}</span>
  </span>
}

function toneText(tone: KpiEntry['tone']) {
  return tone === 'critical' ? 'critical' : tone === 'warning' ? 'needs attention' : tone === 'ok' ? 'ok' : ''
}

function Change({entry, better}: {entry: KpiEntry; better: 'higher' | 'lower'}) {
  if (entry.baseline) return <span className="kpi-baseline">baseline</span>
  if (entry.change === undefined) return <span className="kpi-baseline">no change computable</span>
  const significant = significance(entry.comparison)
  const improved = better === 'higher' ? entry.change > 0 : entry.change < 0
  return <span className="kpi-change-group">
    <span className={`kpi-change ${Math.abs(entry.change) < .5 ? 'flat' : improved ? 'better' : 'worse'}`}>{formatChange(entry.change)}</span>
    {significant && <span className={`kpi-significance ${significant.tone}`} title={significant.title}>{significant.label}</span>}
  </span>
}

function ResourceRow({cards}: {cards: ResourceCard[]}) {
  return <div className="kpi-row kpi-row-resources" role="list" aria-label="Resource usage">{cards.map(card => {
    const single = card.entries.length === 1
    const entry = card.entries[0]
    return <div key={card.id} className={`kpi ${single && entry.tone ? `tone-${entry.tone}` : ''}`} role="listitem" aria-label={card.title} id={`kpi-${card.id}`}>
      {single ? <>
        <p className="kpi-value"><span className="kpi-number">{entry.value}</span>{entry.unit && <span className="kpi-unit">{` ${entry.unit}`}</span>}</p>
        <p className="kpi-label">{card.title}</p>
        {entry.detail && <p className="kpi-detail">{entry.detail}</p>}
      </> : <>
        <p className="kpi-label kpi-label-top">{card.title}</p>
        <ul className="kpi-series">{card.entries.map(item => <li key={item.group.id}>
          <LegendKey color={item.group.color} />
          <span className="kpi-series-label" title={item.detail}>{item.group.label}</span>
          <span className={`kpi-series-value ${item.tone ? `tone-${item.tone}` : ''}`}>{item.value}{item.unit && item.value !== '—' ? ` ${item.unit}` : ''}</span>
          {item.detail && <span className="kpi-series-detail">{item.detail}</span>}
        </li>)}</ul>
      </>}
    </div>
  })}</div>
}

export function KpiCards({cards, provenance, resources = []}: {cards: KpiCard[]; provenance: (metric: string) => ResolvedProvenance | undefined; resources?: ResourceCard[]}) {
  if (!cards.length && !resources.length) return null
  const sources = cards.map(card => provenance(card.metric))
  const labels = [...new Set(sources.filter((item): item is ResolvedProvenance => Boolean(item)).map(item => provenanceLabel(item)))]
  const marked = labels.length > 1
  const repetitions = Math.max(0, ...cards.flatMap(card => card.entries.map(entry => entry.n)))
  return <div className="kpi-block">
    {cards.length > 0 && <div className="kpi-row" role="list" aria-label="Key results">{cards.map((card, index) => {
      const single = card.entries.length === 1
      const entry = card.entries[0]
      const shown = displayValue(entry.value, card.unit)
      const source = sources[index]
      const mark = marked && source ? labels.indexOf(provenanceLabel(source)) + 1 : undefined
      return <div key={card.id} className={`kpi ${single && entry.tone ? `tone-${entry.tone}` : ''}`} role="listitem" aria-label={card.title} id={`kpi-${card.id}`}>
        {single ? <>
          <p className="kpi-value"><span className="kpi-number">{shown.value}</span>{shown.unit && <span className="kpi-unit">{` ${shown.unit}`}</span>}{entry.tone && entry.tone !== 'ok' && <span className="visually-hidden"> ({toneText(entry.tone)})</span>}</p>
          <p className="kpi-label">{card.title}{mark !== undefined && <sup aria-hidden="true">{mark}</sup>}</p>
          {card.detail(entry) && <p className="kpi-detail">{card.detail(entry)}</p>}
        </> : <>
          <p className="kpi-label kpi-label-top">{card.title}{mark !== undefined && <sup aria-hidden="true">{mark}</sup>}</p>
          <ul className="kpi-series">{card.entries.map(item => {
            const value = displayValue(item.value, card.unit)
            return <li key={item.group.id}>
              <LegendKey color={item.group.color} />
              <span className="kpi-series-label" title={card.detail(item)}>{item.group.label}</span>
              <span className={`kpi-series-value ${item.tone && item.tone !== 'ok' ? `tone-${item.tone}` : ''}`}>{value.text}{item.tone && item.tone !== 'ok' && <span className="visually-hidden"> ({toneText(item.tone)})</span>}</span>
              <Change entry={item} better={card.better} />
            </li>
          })}</ul>
        </>}
      </div>
    })}</div>}
    {resources.length > 0 && <ResourceRow cards={resources} />}
    {labels.length > 0 && <p className="kpi-footnote">
      {marked ? 'Sources: ' : 'Source: '}{labels.map((label, index) => {
        const item = sources.find(source => source && provenanceLabel(source) === label)!
        return <Fragment key={label}>{index > 0 && '; '}<Source provenance={item} mark={marked ? index + 1 : undefined} /></Fragment>
      })}{repetitions > 1 ? `. Means of ${repetitions} repetitions over the measured window.` : '. Means over the measured window.'}
      {resources.length > 0 && ' Resources: application workloads only (load generator excluded), from cAdvisor and kube-state-metrics.'}
    </p>}
  </div>
}

// --- primary chart ---

export function PrimaryChart({panels, subtitle, title = 'Load and latency over time'}: {panels: ChartPanel[]; subtitle: string; title?: string}) {
  if (!panels.length) return null
  return <section className="report-figure primary-chart" id="results-timeline" aria-labelledby="results-timeline-title">
    <header className="report-head">
      <p className="report-label">Figure 1</p>
      <h3 id="results-timeline-title">{title}</h3>
      <p className="report-sub">{subtitle}</p>
    </header>
    <TimeChart panels={panels} ariaLabel={`Request rate and client latency over the measured window: ${panels.map(panel => panel.series.map(series => series.label).join(', ')).join('; ')}`} />
  </section>
}

// --- services table ---

const serviceColumns = [
  {key: 'rps', label: 'Req/s', title: 'Mean request rate reaching the service (server side)', value: (row: ServiceHealth) => row.rps},
  {key: 'p95', label: 'Server P95 (ms)', title: 'Mean of the window P95 latency at the service (mesh proxy, server side)', value: (row: ServiceHealth) => row.p95Ms},
  {key: 'cpu', label: 'CPU (cores)', title: 'Mean CPU usage of all pods (cores); share of the CPU limit when recorded', value: (row: ServiceHealth) => row.cpuCores},
  {key: 'replicas', label: 'Replicas', title: 'Mean ready replicas', value: (row: ServiceHealth) => row.replicas},
  {key: 'restarts', label: 'Restarts', title: 'Container restarts during the run', value: (row: ServiceHealth) => row.restarts}
] as const
const serviceAccessors: Record<string, (row: ServiceHealth) => number | string | undefined> = {service: row => row.service, ...Object.fromEntries(serviceColumns.map(column => [column.key, column.value]))}

export interface ServiceDetail {title: string; unit: string; series: AggregateSeries[]}

export function ServicesTable({services, groups, group, onGroup, trend, detail, note}: {services: ServiceHealth[]; groups?: OverviewGroup[]; group?: string; onGroup?: (id: string) => void; trend?: (service: string) => AggregateSeries | undefined; detail?: (service: string) => ServiceDetail[]; note?: string}) {
  const [open, setOpen] = useState<string | undefined>()
  const [all, setAll] = useState(false)
  const limit = 8
  const ranked = services
  const {sorted, sort, toggle} = useSort(ranked, serviceAccessors)
  const shown = all ? sorted : sorted.slice(0, limit)
  const columns = 2 + serviceColumns.length + (trend ? 1 : 0)
  const bottleneck = ranked.find(row => row.bottleneck)
  const title = bottleneck ? `${bottleneck.service} carries most of the server-side latency` : ranked.length ? 'No single service dominates latency' : 'Services'
  return <section className="report-figure services-card" id="results-services" aria-labelledby="results-services-title">
    <header className="report-head">
      <p className="report-label">Table 1</p>
      <h3 id="results-services-title">{title}</h3>
      <p className="report-sub">{note ?? 'Services ranked by their contribution to client latency: server P95 weighted by the share of requests that reach them.'}</p>
      {groups && groups.length > 1 && onGroup && <div className="variant-tabs" role="group" aria-label="Variant">{groups.map(item => <button key={item.id} type="button" aria-pressed={item.id === group} onClick={() => onGroup(item.id)}><LegendKey color={item.color} />{item.label}</button>)}</div>}
    </header>
    {!ranked.length ? <p className="empty-inline">No per-service metrics were collected for these runs.</p> : <TableScroll label="Services, scrollable" className="services-table-wrap"><table className="services-table sticky-first" aria-label="Services ranked by bottleneck score">
      <thead><tr><SortHeader id="service" label="Service" sort={sort} onSort={toggle} />{serviceColumns.map(column => <SortHeader key={column.key} id={column.key} label={column.label} title={column.title} numeric className={`col-${column.key}`} sort={sort} onSort={toggle} />)}{trend && <th scope="col" className="trend-cell">P95 over time</th>}</tr></thead>
      <tbody>{shown.map(row => {
        const expanded = open === row.service
        const cpu = row.cpuCores === undefined ? '—' : `${row.cpuCores.toFixed(2)}${row.cpuLimitCores ? ` (${Math.round(row.cpuCores / row.cpuLimitCores * 100)} %)` : ''}`
        const points = trend?.(row.service)
        return <Fragment key={row.service}>
          <tr className={`${row.bottleneck ? 'bottleneck' : ''} ${expanded ? 'expanded' : ''}`}>
            <th scope="row">
              {detail ? <button type="button" className="service-toggle" aria-expanded={expanded} onClick={() => setOpen(expanded ? undefined : row.service)}><svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="m3.5 2 3 3-3 3" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /></svg>{row.service}</button> : <span className="service-name">{row.service}</span>}
              {row.bottleneck && <span className="margin-note">Bottleneck<sup aria-hidden="true">†</sup></span>}
            </th>
            <td className="numeric">{formatSignificant(row.rps)}</td>
            <td className="numeric">{formatSignificant(row.p95Ms)}</td>
            <td className="numeric" title={row.cpuLimitCores ? `${row.cpuCores?.toFixed(3)} of ${row.cpuLimitCores} cores limit` : 'cores; no CPU limit recorded'}>{cpu}</td>
            <td className="numeric col-replicas">{formatSignificant(row.replicas)}</td>
            <td className="numeric col-restarts">{formatCount(row.restarts)}</td>
            {trend && <td className="trend-cell">{points ? <Sparkline points={toChartPoints(points)} color={row.bottleneck ? 'var(--chart-1)' : 'var(--chart-quiet)'} width={88} height={20} /> : null}</td>}
          </tr>
          {expanded && detail && <tr className="service-detail-row"><td colSpan={columns}><ServiceDetailCharts service={row.service} reasons={row.bottleneck ? [] : row.reasons} charts={detail(row.service)} /></td></tr>}
        </Fragment>
      })}</tbody>
    </table></TableScroll>}
    {ranked.length > 0 && <div className="services-foot">
      {bottleneck && <p className="table-note"><sup aria-hidden="true">†</sup> {sentence(bottleneck.reasons.map((reason, index) => index ? reason : reason.charAt(0).toUpperCase() + reason.slice(1)).join('; '))}</p>}
      <p className="table-note">— not recorded.{sort ? ' Sorted by the selected column; select it until the arrows reset to restore the ranking.' : ''}</p>
      {ranked.length > limit && <button type="button" className="link-button services-more" aria-expanded={all} onClick={() => setAll(value => !value)}>{all ? `Top ${limit} only` : `All ${ranked.length} services`}</button>}
    </div>}
  </section>
}

function ServiceDetailCharts({service, reasons, charts}: {service: string; reasons: string[]; charts: ServiceDetail[]}) {
  const visible = charts.filter(chart => chart.series.length)
  return <div className="service-detail">
    {reasons.length > 0 && <p className="service-detail-reasons">{sentence(reasons.join('; '))}</p>}
    {visible.length ? <div className="service-detail-grid">{visible.map(chart => <div key={chart.title} className="service-detail-chart">
      <span className="time-chart-panel-title">{chart.title}</span>
      <TimeChart compact panelHeight={88} ariaLabel={`${service}: ${chart.title} over time`} panels={[{id: chart.title, title: chart.title, unit: chart.unit, series: chart.series.map((series, index) => chartSeries(series, `${index}`, series.label, index ? `var(--chart-${index % 8 + 1})` : 'var(--chart-1)'))}]} />
    </div>)}</div> : <p className="empty-inline">No time series recorded for {service}.</p>}
  </div>
}

// --- disclosure sections ---

export function Disclosure({id, title, summary, open, onToggle, children}: {id: string; title: string; summary?: string; open: boolean; onToggle: (open: boolean) => void; children: ReactNode}) {
  return <section className={`disclosure ${open ? 'open' : ''}`} id={`section-${id}`}>
    <h3 className="disclosure-heading"><button type="button" aria-expanded={open} aria-controls={open ? `section-${id}-body` : undefined} onClick={() => onToggle(!open)}>
      <svg className="disclosure-chevron" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="m3.5 2 3 3-3 3" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" /></svg>
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
