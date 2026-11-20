import type {ReactNode} from 'react'
import {timeAxis} from '../../charts'
import {summarize, tCritical} from '../../statistics'
import type {AggregateSeries, RunLevelMetric} from '../../resultsData'
import type {MetricPoint} from '../../types'
import {csvWithProvenance, FigureCard, figureMetrics, Legend, legendLayout, legendWidth, leftMargin, Marker, r, seriesStyle, SvgText, useFigureLabels, YAxis, type FigureMetrics, type LegendItem, type SeriesStyle} from './chartKit'
import type {FigureSettings} from './figureSettings'
import {compactLabel, formatAxis, formatMetric, metricAxisLabel, metricTitle, provenanceSentence, toCSV} from './format'
import type {LadderSeries} from './percentiles'
import type {ResolvedProvenance} from './runs'
import {extent, valueScale, type Scale} from './scales'

type Provenanced = {metric: string; provenance: ResolvedProvenance}

const figureID = (name: string) => `figure-${name.replaceAll(/[^a-zA-Z0-9]/g, '-')}`
const uncertaintyText = (settings: FigureSettings, across = 'repetitions') => settings.uncertainty === 'ci' ? `95% confidence interval of the mean across ${across} (exact Student t, n − 1 df)` : settings.uncertainty === 'sd' ? `±1 sample standard deviation across ${across}` : ''
const provenanceText = (items: Provenanced[]) => items.map(item => provenanceSentence(item.metric, item.provenance)).join(' ')
const useLog = (settings: FigureSettings, unit: string) => settings.logY && unit === 'milliseconds'

// reserveRight: room for a legend placed right of the plot.
function plotFrame(metrics: FigureMetrics, scale: Scale, unit: string, reserveRight = 0, bottomExtra = 0) {
  const top = metrics.titleHeight
  const base = metrics.height
  const left = leftMargin(scale, unit, metrics)
  const right = metrics.width - metrics.fontSize * .9 - reserveRight
  const bottom = base - metrics.fontSize * 2.9
  const y = (value: number) => {const position = scale.position(value); return position === undefined ? undefined : bottom - position * (bottom - top)}
  return {top, left, right, bottom, base, bottomExtra, y}
}

function Frame({left, right, top, bottom, metrics}: {left: number; right: number; top: number; bottom: number; metrics: FigureMetrics}) {
  return <rect className="axis" x={r(left)} y={r(top)} width={r(right - left)} height={r(bottom - top)} fill="none" stroke="#111" strokeWidth={r(metrics.line * .6)} />
}

// Half-width of the uncertainty band at each point (undefined: no band).
function bandHalfWidth(series: AggregateSeries, settings: FigureSettings): number[] | undefined {
  if (!series.deviation || settings.uncertainty === 'none') return undefined
  const runs = series.runs ?? 0
  if (settings.uncertainty === 'sd') return series.deviation.map(point => point.value)
  if (runs < 2) return undefined
  const factor = tCritical(runs - 1) / Math.sqrt(runs)
  return series.deviation.map(point => point.value * factor)
}

function segments(points: MetricPoint[], step?: number): number[][] {
  const result: number[][] = []
  points.forEach((point, index) => {
    if (!index || step && Date.parse(point.timestamp) - Date.parse(points[index - 1].timestamp) > step * 1.5) result.push([])
    result.at(-1)!.push(index)
  })
  return result
}

function sparse<T>(items: T[], count = 8): T[] {
  if (items.length <= count) return items
  return Array.from({length: count}, (_, index) => items[Math.round(index * (items.length - 1) / (count - 1))])
}

export interface TimeSeriesFigureProps {
  name: string
  metric: string
  title: string
  axisLabel: string
  series: AggregateSeries[]
  settings: FigureSettings
  provenance: Provenanced[]
  styles?: SeriesStyle[]
  omitSLO?: boolean
  captionLead?: string
  ariaLabel?: string
}

export function TimeSeriesFigure({name, metric, title, axisLabel, series, settings, provenance, styles, omitSLO = false, captionLead, ariaLabel}: TimeSeriesFigureProps) {
  const labels = useFigureLabels()
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  const unit = series[0]?.unit ?? ''
  const log = useLog(settings, unit)
  const bands = series.map(item => bandHalfWidth(item, settings))
  const slo = !omitSLO && metric === 'load.p95_response_time' ? series.find(item => item.sloMilliseconds)?.sloMilliseconds : undefined
  const values = series.flatMap((item, index) => item.points.flatMap((point, pointIndex) => bands[index] ? [point.value + bands[index]![pointIndex], point.value - bands[index]![pointIndex]] : [point.value]))
  const scale = valueScale([...values, ...(slo ? [slo] : [])], {zero: settings.zeroBased, log, integer: unit === 'replicas' || unit === 'count'})
  const seriesStyles = styles ?? series.map((item, index) => seriesStyle(index, item.color, settings))
  const multiple = series.length > 1 || bands.some(Boolean)
  const seriesNames = series.map(item => compactLabel(item.label))
  const items: LegendItem[] = multiple ? series.map((_, index) => ({label: labels.series(seriesNames[index]), style: seriesStyles[index]})) : []
  const frame = plotFrame(metrics, scale, unit, legendWidth(items, metrics))
  const {left, right, top, bottom} = frame
  const maxX = Math.max(0, extent(series.flatMap(item => item.points.map(point => Date.parse(point.timestamp)))).max) / 60000
  const xAxis = timeAxis(maxX, true)
  const x = (minutes: number) => left + minutes / xAxis.upper * (right - left)
  const legend = legendLayout(items, left, right, metrics)
  const legendTop = frame.base + fs * .2
  const height = frame.base + legend.height
  const clamp = (value: number) => Math.min(scale.upper, Math.max(scale.lower, value))
  const runs = [...new Set(series.map(item => item.runs ?? 1))]
  const caption = [
    `${captionLead ?? title} over the measured window (time 0 = start of the measured window).`,
    series.some(item => (item.runs ?? 1) > 1) ? `Lines: mean across repetitions at each sampling window (n = ${runs.join('/')} per series)${bands.some(Boolean) ? `; shaded band: ${uncertaintyText(settings)}` : ''}.` : '',
    slo ? `Dashed red line: SLO of ${formatMetric(slo, 'milliseconds')}.` : '',
    log ? 'Logarithmic y axis.' : ''
  ].filter(Boolean).join(' ')
  const methods = provenanceText(provenance)
  const csv = () => csvWithProvenance(['series', 'minute', 'value', 'sd_across_runs', 'band_low', 'band_high', 'n_runs'], series.flatMap((item, index) => item.points.map((point, pointIndex) => {
    const half = bands[index]?.[pointIndex]
    return [item.label, Date.parse(point.timestamp) / 60000, point.value, item.deviation?.[pointIndex]?.value, half === undefined ? undefined : point.value - half, half === undefined ? undefined : point.value + half, item.runs ?? 1]
  })), provenance[0])
  return <FigureCard id={figureID(name)} name={name} title={title} caption={caption} methods={methods} ariaLabel={ariaLabel ?? `${title} comparison chart`} width={metrics.width} height={height} settings={settings} provenance={provenance} csv={csv} labels={{x: 'Time (min)', y: axisLabel, series: multiple ? seriesNames : []}} legendHeight={legend.height}>
    <YAxis scale={scale} unit={unit} left={left} right={right} top={top} bottom={bottom} metrics={metrics} label={axisLabel} />
    {xAxis.ticks.map(tick => <g key={`x-${tick}`}>
      <line x1={r(x(tick))} x2={r(x(tick))} y1={r(bottom)} y2={r(bottom + fs * .35)} stroke="#111" strokeWidth={r(metrics.line * .6)} />
      <SvgText x={x(tick)} y={bottom + fs * 1.3} anchor="middle" size={fs} family={metrics.family} className="tick">{formatAxis(tick)}</SvgText>
    </g>)}
    <SvgText x={(left + right) / 2} y={bottom + fs * 2.55} anchor="middle" size={fs} family={metrics.family} className="axis-label">{labels.x('Time (min)')}</SvgText>
    <Frame left={left} right={right} top={top} bottom={bottom} metrics={metrics} />
    {slo !== undefined && frame.y(slo) !== undefined && <g><line x1={r(left)} x2={r(right)} y1={r(frame.y(slo)!)} y2={r(frame.y(slo)!)} stroke="#b42318" strokeWidth={r(metrics.line)} strokeDasharray="4 3" /><SvgText x={right - fs * .3} y={frame.y(slo)! - fs * .35} anchor="end" size={fs * .9} family={metrics.family} fill="#b42318" className="legend-label slo-label">SLO {formatMetric(slo, 'milliseconds')}</SvgText></g>}
    {series.map((item, index) => {
      const style = seriesStyles[index]
      const half = bands[index]
      const parts = segments(item.points, item.stepMilliseconds)
      const valid = (value: number) => frame.y(value) !== undefined
      const path = parts.map(indices => indices.filter(pointIndex => valid(item.points[pointIndex].value)).map((pointIndex, position) => `${position ? 'L' : 'M'}${r(x(Date.parse(item.points[pointIndex].timestamp) / 60000))},${r(frame.y(item.points[pointIndex].value)!)}`).join(' ')).join(' ')
      return <g key={`${item.artifactID}-${index}`}>
        {half && <g className="deviation-band" fill={style.color} fillOpacity="0.16" stroke="none">{parts.filter(indices => indices.length > 1).map((indices, segment) => {
          const edge = (pointIndex: number, sign: number) => `${r(x(Date.parse(item.points[pointIndex].timestamp) / 60000))},${r(frame.y(clamp(Math.max(log ? scale.lower : -Infinity, item.points[pointIndex].value + sign * half[pointIndex])))!)}`
          return <path key={segment} d={`M${indices.map(pointIndex => edge(pointIndex, 1)).join(' L')} L${[...indices].reverse().map(pointIndex => edge(pointIndex, -1)).join(' L')} Z`} />
        })}</g>}
        {path && <path className="series-line" d={path} fill="none" stroke={style.color} strokeWidth={r(metrics.line * 1.2)} strokeDasharray={style.dash || undefined} strokeLinejoin="round" />}
        {sparse(item.points.filter(point => valid(point.value))).map((point, pointIndex) => <Marker key={pointIndex} shape={style.marker} x={x(Date.parse(point.timestamp) / 60000)} y={frame.y(point.value)!} color={style.color} size={fs * .24} />)}
      </g>
    })}
    {multiple && <Legend items={items} layout={legend} top={legendTop} metrics={metrics} plot={frame} />}
  </FigureCard>
}

function categoryLabels(labels: string[], centers: number[], band: number, bottom: number, metrics: FigureMetrics) {
  const fs = metrics.fontSize
  const widest = Math.max(0, ...labels.map(label => metrics.text(label)))
  const angle = widest <= band * .92 ? 0 : labels.length > 8 ? -90 : -40
  const extra = angle ? widest * Math.abs(Math.sin(angle * Math.PI / 180)) - fs * .4 : 0
  const elements = labels.map((label, index) => angle
    ? <SvgText key={`${label}-${index}`} x={centers[index] + (angle === -90 ? fs * .35 : 0)} y={bottom + fs * .9} anchor="end" rotate={angle} size={fs} family={metrics.family} className="tick">{label}</SvgText>
    : <SvgText key={`${label}-${index}`} x={centers[index]} y={bottom + fs * 1.3} anchor="middle" size={fs} family={metrics.family} className="tick">{label}</SvgText>)
  return {elements, extra: Math.max(0, extra)}
}

function Whisker({x, low, high, cap, color, width}: {x: number; low?: number; high?: number; cap: number; color: string; width: number}) {
  if (low === undefined || high === undefined) return null
  return <path d={`M${r(x)},${r(low)}V${r(high)}M${r(x - cap)},${r(low)}H${r(x + cap)}M${r(x - cap)},${r(high)}H${r(x + cap)}`} fill="none" stroke={color} strokeWidth={r(width)} />
}

// Run-level strip plot: every repetition visible, mean with 95% CI (or ±SD) whiskers.
export function RunDotPlot({data, settings, provenance, runLabels = {}}: {data: RunLevelMetric; settings: FigureSettings; provenance: Provenanced; runLabels?: Record<string, string>}) {
  const names = useFigureLabels()
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  const groups = data.groups.filter(group => group.values.length)
  const unit = data.unit
  const title = `${metricTitle(data.metric, provenance.provenance)} per repetition`
  const summaries = groups.map(group => summarize(group.values))
  const whisker = summaries.map(summary => settings.uncertainty === 'ci' && summary.count >= 2 ? [summary.confidence95Low, summary.confidence95High] : settings.uncertainty === 'sd' && summary.count >= 2 ? [summary.mean - summary.standardDeviation, summary.mean + summary.standardDeviation] : undefined)
  const scale = valueScale([...groups.flatMap(group => group.values), ...whisker.flatMap(item => item ?? [])], {zero: settings.zeroBased, log: useLog(settings, unit), integer: unit === 'count'})
  const base = plotFrame(metrics, scale, unit)
  const band = (base.right - base.left) / Math.max(1, groups.length)
  const centers = groups.map((_, index) => base.left + band * (index + .5))
  const groupNames = groups.map(group => compactLabel(group.label))
  const labels = categoryLabels(groupNames.map(names.series), centers, band, base.bottom, metrics)
  const height = base.base + labels.extra
  const name = `${data.metric}-per-repetition`
  const caption = [
    `${metricTitle(data.metric, provenance.provenance)}: each marker is one repetition (its mean over the measured window)${data.metric === 'load.failure_percentage' ? '; with a Locust summary the exact failures/requests ratio is used' : ''}.`,
    `Horizontal bar: mean across repetitions${settings.uncertainty === 'none' ? '' : `; whiskers: ${uncertaintyText(settings)}, shown when n ≥ 2`}.`,
    `n per configuration: ${groups.map(group => `${group.label} = ${group.values.length}`).join(', ')}.`
  ].join(' ')
  const methods = provenanceText([provenance])
  const csv = () => csvWithProvenance(['configuration', 'run', 'value', 'unit', 'configuration_mean', 'configuration_sd', 'ci95_low', 'ci95_high', 'n'], groups.flatMap((group, index) => group.values.map((value, run) => {
    const summary = summaries[index]
    return [group.label, runLabels[group.artifactIds[run]] ?? group.artifactIds[run], value, unit, summary.mean, summary.count > 1 ? summary.standardDeviation : undefined, summary.count > 1 ? summary.confidence95Low : undefined, summary.count > 1 ? summary.confidence95High : undefined, summary.count]
  })), provenance)
  const yLabel = metricAxisLabel(data.metric, unit, provenance.provenance)
  return <FigureCard id={figureID(name)} name={name} title={title} caption={caption} methods={methods} labels={{y: yLabel, series: groupNames}} ariaLabel={`${title}: individual repetitions with mean${settings.uncertainty === 'ci' ? ' and 95% confidence interval' : settings.uncertainty === 'sd' ? ' and standard deviation' : ''}`} width={metrics.width} height={height} settings={settings} provenance={[provenance]} csv={csv}>
    <YAxis scale={scale} unit={unit} left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} label={yLabel} />
    {labels.elements}
    <Frame left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} />
    {groups.map((group, index) => {
      const style = seriesStyle(data.groups.indexOf(group), group.color, settings)
      const center = centers[index]
      const summary = summaries[index]
      const spread = Math.min(band * .3, fs * 2.2)
      const order = group.values.map((value, run) => ({value, run})).sort((a, b) => a.value - b.value)
      const meanY = base.y(summary.mean)
      const range = whisker[index]
      return <g key={group.id} className="run-dots">
        {order.map(({value, run}, rank) => {
          const y = base.y(value)
          if (y === undefined) return null
          // Deterministic jitter (0, −, +, −−, ++) so tied repetitions stay visible.
          const offset = group.values.length === 1 ? 0 : [0, -1, 1, -2, 2][rank % 5] * spread / 4
          return <g key={run}><title>{`${group.label} · ${runLabels[group.artifactIds[run]] ?? `run ${run + 1}`}: ${formatMetric(value, unit)}`}</title><Marker shape={style.marker} x={center + offset} y={y} color={style.color} size={fs * .26} hollow /></g>
        })}
        {range && <Whisker x={center + spread * .9} low={base.y(range[0]) ?? base.bottom} high={base.y(range[1]) ?? base.top} cap={fs * .35} color="#111111" width={metrics.line * .8} />}
        {meanY !== undefined && <line x1={r(center - spread * .9)} x2={r(center + spread * 1.2)} y1={r(meanY)} y2={r(meanY)} stroke="#111111" strokeWidth={r(metrics.line * 1.4)} />}
      </g>
    })}
  </FigureCard>
}

// Latency percentile ladder: P50/P90/P95/P99 per configuration, client vs server.
export function PercentileLadder({series, settings, provenance, name = 'latency-percentile-ladder'}: {series: LadderSeries[]; settings: FigureSettings; provenance: Provenanced[]; name?: string}) {
  const labels = useFigureLabels()
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  const levels = (['p50', 'p90', 'p95', 'p99'] as const).filter(level => series.some(item => item.points.some(point => point.level === level)))
  const groupNames = [...new Set(series.map(item => item.group))]
  const interval = (point: LadderSeries['points'][number]): [number, number] | undefined => settings.uncertainty === 'ci' && point.low !== undefined && point.high !== undefined ? [point.low, point.high] : settings.uncertainty === 'sd' && point.standardDeviation !== undefined ? [point.mean - point.standardDeviation, point.mean + point.standardDeviation] : undefined
  const scale = valueScale(series.flatMap(item => item.points.flatMap(point => [point.mean, ...(interval(point) ?? [])])), {zero: settings.zeroBased && !settings.logY, log: settings.logY})
  const describe = (item: LadderSeries) => `${compactLabel(item.group)} · ${item.vantage === 'client' ? `client${item.estimator === 'exact' ? ' (Locust, exact)' : ' (Locust)'}` : 'server (Istio)'}`
  const styleOf = (item: LadderSeries) => {
    const style = seriesStyle(groupNames.indexOf(item.group), item.color, settings)
    return {...style, dash: item.vantage === 'server' ? '4 2' : ''}
  }
  const items: LegendItem[] = series.map(item => ({label: labels.series(describe(item)), style: styleOf(item)}))
  const base = plotFrame(metrics, scale, 'milliseconds', legendWidth(items, metrics))
  const band = (base.right - base.left) / Math.max(1, levels.length)
  const center = (level: string) => base.left + band * (levels.indexOf(level as typeof levels[number]) + .5)
  const dodge = (index: number) => (index - (series.length - 1) / 2) * Math.min(band * .5 / Math.max(1, series.length), fs * .9)
  const legend = legendLayout(items, base.left, base.right, metrics)
  const height = base.base + legend.height
  const title = 'Latency percentiles'
  const caption = [
    'Latency percentiles per configuration; each repetition contributes one value per percentile and markers show the mean across repetitions',
    settings.uncertainty === 'none' ? '.' : ` with ${uncertaintyText(settings)} (shown when n ≥ 2).`,
    series.some(item => item.vantage === 'client' && item.estimator === 'exact') ? ' Client side (solid, filled markers), exact: percentiles over the whole measured window from the merged Locust response-time histograms.' : '',
    series.some(item => item.vantage === 'client' && item.estimator === 'window') ? ' Client side (solid), windowed: mean over the measured window of per-window percentiles estimated from Locust histogram buckets.' : '',
    series.some(item => item.vantage === 'server') ? ' Server side (dashed, hollow markers): mean over the measured window of per-window percentiles estimated from Istio histogram buckets, averaged over destination workloads.' : '',
    settings.logY ? ' Logarithmic y axis.' : '',
    ` n: ${[...new Set(series.map(item => `${item.group} = ${Math.max(...item.points.map(point => point.n))}`))].join(', ')}.`
  ].join('')
  const methods = provenanceText(provenance)
  const csv = () => toCSV([['configuration', 'vantage', 'estimator', 'percentile', 'mean_ms', 'ci95_low_ms', 'ci95_high_ms', 'sd_ms', 'n'], ...series.flatMap(item => item.points.map(point => [item.group, item.vantage, item.estimator, point.level.toUpperCase(), point.mean, point.low, point.high, point.standardDeviation, point.n]))])
  return <FigureCard id={figureID(name)} name={name} title={title} caption={caption} methods={methods} labels={{x: 'Percentile', y: 'Latency (ms)', series: series.map(describe)}} legendHeight={legend.height} ariaLabel="Latency percentile ladder: client and server percentiles per configuration" width={metrics.width} height={height} settings={settings} provenance={provenance} csv={csv}>
    <YAxis scale={scale} unit="milliseconds" left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} label="Latency (ms)" />
    {levels.map(level => <SvgText key={level} x={center(level)} y={base.bottom + fs * 1.3} anchor="middle" size={fs} family={metrics.family} className="tick">{level.toUpperCase()}</SvgText>)}
    <SvgText x={(base.left + base.right) / 2} y={base.bottom + fs * 2.55} anchor="middle" size={fs} family={metrics.family} className="axis-label">{labels.x('Percentile')}</SvgText>
    <Frame left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} />
    {series.map((item, index) => {
      const style = styleOf(item)
      const points = item.points.filter(point => base.y(point.mean) !== undefined)
      return <g key={item.id}>
        <path d={points.map((point, position) => `${position ? 'L' : 'M'}${r(center(point.level) + dodge(index))},${r(base.y(point.mean)!)}`).join(' ')} fill="none" stroke={style.color} strokeWidth={r(metrics.line)} strokeDasharray={style.dash || undefined} />
        {points.map(point => {
          const range = interval(point)
          return <g key={point.level}>
            <title>{`${describe(item)} ${point.level.toUpperCase()}: ${formatMetric(point.mean, 'milliseconds')} (n = ${point.n})`}</title>
            {range && <Whisker x={center(point.level) + dodge(index)} low={base.y(Math.max(range[0], scale.lower)) ?? base.bottom} high={base.y(range[1]) ?? base.top} cap={fs * .25} color={style.color} width={metrics.line * .7} />}
            <Marker shape={style.marker} x={center(point.level) + dodge(index)} y={base.y(point.mean)!} color={style.color} size={fs * .27} hollow={item.vantage === 'server'} />
          </g>
        })}
      </g>
    })}
    <Legend items={items} layout={legend} top={base.base + fs * .2} metrics={metrics} plot={base} />
  </FigureCard>
}

export interface ServiceRow {service: string; group: string; groupID: string; color?: string; unit: string; summary: ReturnType<typeof summarize>; acrossRuns: boolean}

// Per-service values: dot + 95% CI (default) or bar ± SD.
export function ServiceFigure({metric, rows, settings, provenance, title}: {metric: string; rows: ServiceRow[]; settings: FigureSettings; provenance: Provenanced; title: string}) {
  const names = useFigureLabels()
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  if (!rows.length) return null
  const unit = rows[0].unit
  const services = [...new Set(rows.map(row => row.service))]
  const groupIDs = [...new Set(rows.map(row => row.groupID))]
  const bars = settings.uncertainty === 'sd'
  const range = (row: ServiceRow): [number, number] | undefined => row.summary.count < 2 ? undefined : bars ? [row.summary.mean - row.summary.standardDeviation, row.summary.mean + row.summary.standardDeviation] : settings.uncertainty === 'ci' ? [row.summary.confidence95Low, row.summary.confidence95High] : undefined
  const scale = valueScale([...rows.flatMap(row => [row.summary.mean, ...(range(row) ?? [])]), ...(bars ? [0] : [])], {zero: settings.zeroBased || bars, log: !bars && useLog(settings, unit)})
  const groupLabels = groupIDs.map(id => rows.find(row => row.groupID === id)!.group)
  const items: LegendItem[] = groupIDs.length > 1 ? groupLabels.map((label, index) => ({label: names.series(compactLabel(label)), style: seriesStyle(index, rows.find(row => row.groupID === groupIDs[index])?.color, settings), kind: 'marker'})) : []
  const base = plotFrame(metrics, scale, unit, legendWidth(items, metrics))
  const band = (base.right - base.left) / services.length
  const slot = Math.max(1, band * .8) / groupIDs.length
  const centers = services.map((_, index) => base.left + band * (index + .5))
  const labels = categoryLabels(services, centers, band, base.bottom, metrics)
  const legend = legendLayout(items, base.left, base.right, metrics)
  const height = base.base + labels.extra + legend.height + (items.length ? fs * .4 : 0)
  const across = rows.some(row => row.acrossRuns)
  const statistic = bars ? 'mean and standard deviation' : settings.uncertainty === 'ci' ? 'mean and 95% confidence interval' : 'mean'
  const caption = [
    `${title}.`,
    across ? `Markers: mean across repetitions of each repetition's time-averaged value${bars ? '; bars ±1 SD across repetitions' : settings.uncertainty === 'ci' ? `; whiskers: ${uncertaintyText(settings)}` : ''}.` : `Markers: time-averaged value over the measured window${bars ? '; bars ±1 SD of the window samples' : settings.uncertainty === 'ci' ? '; whiskers: 95% CI of the time average, adjusted for autocorrelation (AR(1) effective sample size)' : ''}.`
  ].join(' ')
  const methods = provenanceText([provenance])
  const yLabel = metricAxisLabel(metric, unit, provenance.provenance)
  const name = `${metric}-by-service`
  const csv = () => csvWithProvenance(['service', 'configuration', 'n', 'mean', 'sd', 'ci95_low', 'ci95_high', 'n_effective', 'scope', 'unit'], rows.map(row => [row.service, row.group, row.summary.count, row.summary.mean, row.summary.standardDeviation, row.summary.count > 1 ? row.summary.confidence95Low : undefined, row.summary.count > 1 ? row.summary.confidence95High : undefined, row.summary.nEffective, row.acrossRuns ? 'across_runs' : 'within_run', unit]), provenance)
  return <FigureCard id={figureID(name)} name={name} title={title} caption={caption} methods={methods} labels={{y: yLabel, series: items.length ? groupLabels.map(label => compactLabel(label)) : []}} legendHeight={legend.height} ariaLabel={`${title}: ${statistic}`} width={metrics.width} height={height} settings={settings} provenance={[provenance]} csv={csv} className="service-figure">
    <YAxis scale={scale} unit={unit} left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} label={yLabel} />
    {labels.elements}
    <Frame left={base.left} right={base.right} top={base.top} bottom={base.bottom} metrics={metrics} />
    {rows.map((row, index) => {
      const groupIndex = groupIDs.indexOf(row.groupID)
      const style = seriesStyle(groupIndex, row.color, settings)
      const center = centers[services.indexOf(row.service)] + (groupIndex - (groupIDs.length - 1) / 2) * slot
      const meanY = base.y(row.summary.mean)
      const interval = range(row)
      const zeroY = base.y(Math.max(0, scale.lower)) ?? base.bottom
      if (meanY === undefined) return null
      return <g key={`${row.service}-${row.groupID}-${index}`}>
        <title>{`${row.service} · ${row.group}: mean ${formatMetric(row.summary.mean, unit)}${interval ? `, ${bars ? 'SD' : '95% CI'} ${formatMetric(interval[0], unit)}–${formatMetric(interval[1], unit)}` : ''}`}</title>
        {bars && <rect x={r(center - slot * .35)} y={r(Math.min(meanY, zeroY))} width={r(slot * .7)} height={r(Math.abs(zeroY - meanY))} fill={style.color} fillOpacity="0.8" />}
        {interval && <Whisker x={center} low={base.y(Math.max(interval[0], scale.lower)) ?? base.bottom} high={base.y(interval[1]) ?? base.top} cap={Math.min(fs * .35, slot * .3)} color={bars ? '#111111' : style.color} width={metrics.line * .8} />}
        {!bars && <Marker shape={style.marker} x={center} y={meanY} color={style.color} size={fs * .27} />}
      </g>
    })}
    {items.length > 0 && <Legend items={items} layout={legend} top={base.base + labels.extra + fs * .4} metrics={metrics} plot={base} />}
  </FigureCard>
}

export function FigureSection({title, children, note}: {title: string; children: ReactNode; note?: string}) {
  return <section className="figure-section" aria-label={title}><div className="figure-section-head"><h3>{title}</h3>{note && <p>{note}</p>}</div><div className="chart-grid figure-grid">{children}</div></section>
}
