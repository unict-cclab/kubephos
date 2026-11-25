import {useMemo, useState} from 'react'
import {isControlMetric, type AggregateSeries} from '../../resultsData'
import {groupMetrics, metricTitle, provenanceLabel, unitLabel} from './format'
import {chartSeries, colorOf, type OverviewGroup} from './overview'
import type {ResolvedProvenance} from './runs'
import {TimeChart} from './TimeChart'
import type {XYSource, XYState} from './xy'
import XYRelationship from './XYRelationship'

// "All metrics": every recorded metric on demand. Nothing is plotted until it is picked.
export function AllMetrics({metrics, plot, byService, groups, provenance, onFigure, initial = [], xy, onXYFigure}: {metrics: string[]; plot: (metric: string) => AggregateSeries[]; byService?: (metric: string) => AggregateSeries[] | undefined; groups: OverviewGroup[]; provenance: (metric: string) => ResolvedProvenance; onFigure?: (metric: string) => void; initial?: string[]; xy?: XYSource; onXYFigure?: (state: XYState) => void}) {
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<string[]>(initial)
  const [split, setSplit] = useState<Record<string, boolean>>({})
  const titled = useMemo(() => metrics.map(metric => ({metric, title: metricTitle(metric, provenance(metric))})), [metrics, provenance])
  const needle = query.trim().toLowerCase()
  const matching = titled.filter(item => !needle || item.title.toLowerCase().includes(needle) || item.metric.toLowerCase().includes(needle))
  const grouped = groupMetrics(matching.map(item => item.metric))
  const toggle = (metric: string) => setSelected(current => current.includes(metric) ? current.filter(item => item !== metric) : [...current, metric])
  return <div className="all-metrics">
    {xy && xy.metrics.length > 1 && <XYEntry source={xy} groups={groups} onFigure={onXYFigure} />}
    <div className="all-metrics-picker">
      <input type="search" className="all-metrics-search" aria-label="Search metrics" placeholder={`Search ${metrics.length} metrics…`} value={query} onChange={event => setQuery(event.target.value)} />
      {!matching.length && <p className="empty-inline">No metric matches “{query}”.</p>}
      {[...grouped].map(([group, items]) => <div key={group} className="metric-chip-group" role="group" aria-label={group}>
        <span className="metric-chip-group-label">{group}</span>
        <div className="metric-chips">{items.map(metric => <button key={metric} type="button" className="metric-chip" aria-pressed={selected.includes(metric)} title={metric} onClick={() => toggle(metric)}>{titled.find(item => item.metric === metric)?.title}</button>)}</div>
      </div>)}
    </div>
    {selected.length === 0 ? <p className="all-metrics-hint">Pick a metric to plot it.</p> : <div className="all-metrics-charts">{selected.map(metric => {
      const curves = isControlMetric(metric) ? [] : split[metric] ? byService?.(metric) ?? [] : plot(metric)
      const unit = curves[0]?.unit ?? ''
      const title = metricTitle(metric, provenance(metric))
      const serviceable = Boolean(byService) && groups.length <= 1 && !isControlMetric(metric) && byService!(metric) !== undefined
      return <section key={metric} className="metric-figure" aria-label={title}>
        <div className="metric-figure-head">
          <div><h4>{title}</h4><p>{provenanceLabel(provenance(metric)).replace(/ · /g, ', ')}{unit ? `; ${unitLabel(unit)}` : ''}</p></div>
          <div className="results-card-actions">
            {serviceable && <button type="button" className="link-button" aria-pressed={Boolean(split[metric])} onClick={() => setSplit(current => ({...current, [metric]: !current[metric]}))}>{split[metric] ? 'Total' : 'By service'}</button>}
            {onFigure && <button type="button" className="link-button" aria-label={`Create figure: ${title}`} onClick={() => onFigure(metric)}>Create figure</button>}
            <button type="button" className="link-button" aria-label={`Remove ${title}`} onClick={() => toggle(metric)}>Remove</button>
          </div>
        </div>
        {curves.length ? <TimeChart panelTitles={false} panelHeight={150} ariaLabel={`${title} over time`} panels={[{id: metric, title, unit, series: curves.slice(0, 8).map((series, index) => chartSeries(series, `${index}`, series.label, split[metric] ? `var(--chart-${index % 8 + 1})` : colorOf(groups, series, index)))}]} /> : <p className="empty-inline">This metric has no time series (statistics only).</p>}
        {split[metric] && curves.length > 8 && <p className="table-footnote">The 8 services with the highest mean; the rest are in Statistics.</p>}
      </section>
    })}</div>}
  </div>
}

function XYEntry({source, groups, onFigure}: {source: XYSource; groups: OverviewGroup[]; onFigure?: (state: XYState) => void}) {
  const [open, setOpen] = useState(false)
  if (!open) return <div className="xy-entry"><button type="button" className="button secondary compact" aria-expanded={false} onClick={() => setOpen(true)}>Plot one metric against another (X–Y)</button></div>
  return <XYRelationship source={source} groups={groups} onFigure={onFigure} onClose={() => setOpen(false)} />
}
