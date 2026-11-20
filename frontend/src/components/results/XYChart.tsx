import {useContext, useId, useMemo, useState, type KeyboardEvent, type PointerEvent} from 'react'
import {ResponsiveTable} from '../ResponsiveTable'
import {FigureCard, figureMetrics, Legend, legendLayout, legendWidth, Marker, r, seriesStyle, SvgText, useFigureLabels, type LegendItem} from './chartKit'
import {displayValue, formatClock} from './display'
import type {FigureSettings} from './figureSettings'
import {formatAxisValue, metricTitle, provenanceSentence} from './format'
import {paletteColor, type OverviewGroup} from './overview'
import {valueScale, type Scale} from './scales'
import {SeriesNotesContext, useWidth} from './TimeChart'
import {fitAt, fitSummary, fitXY, pathSegments, plottable, xyCSV, type XYAxisSelection, type XYData, type XYFit, type XYOptions, type XYPoint, type XYSource, type XYState} from './xy'

export type {XYState}


// ---------------------------------------------------------------------------------------
// Controls: X metric (+ service), Y metric (+ service), path / fit / log toggles.

function AxisPicker({id, axis, source, value, onChange}: {id: string; axis: 'X' | 'Y'; source: XYSource; value: XYAxisSelection; onChange: (next: XYAxisSelection) => void}) {
  const services = value.metric ? source.services(value.metric) : undefined
  return <div className="xy-axis-picker">
    <label htmlFor={`${id}-metric`}>{axis} axis<select id={`${id}-metric`} value={value.metric} onChange={event => onChange({metric: event.target.value})}>
      {source.metrics.map(metric => <option key={metric} value={metric}>{metricTitle(metric, source.provenance(metric))}</option>)}
    </select></label>
    {services && services.length > 0 && <label htmlFor={`${id}-service`}>{axis} service<select id={`${id}-service`} value={value.service ?? ''} onChange={event => onChange({metric: value.metric, service: event.target.value || undefined})}>
      <option value="">All services (total)</option>
      {services.map(service => <option key={service} value={service}>{service}</option>)}
    </select></label>}
  </div>
}

const allOptions: Array<keyof XYOptions> = ['fit', 'path', 'logX', 'logY']

export function XYControls({source, value, onChange, options = allOptions}: {source: XYSource; value: XYState; onChange: (next: XYState) => void; options?: Array<keyof XYOptions>}) {
  const id = useId()
  const option = (key: keyof XYOptions, label: string) => options.includes(key) && <label className="checkbox-row"><input type="checkbox" checked={value.options[key]} onChange={event => onChange({...value, options: {...value.options, [key]: event.target.checked}})} />{label}</label>
  return <div className="xy-controls" role="group" aria-label="X–Y relationship settings">
    <AxisPicker id={`${id}-x`} axis="X" source={source} value={value.x} onChange={x => onChange({...value, x})} />
    <AxisPicker id={`${id}-y`} axis="Y" source={source} value={value.y} onChange={y => onChange({...value, y})} />
    {options.length > 0 && <div className="xy-options">
      {option('fit', 'Fit line and correlation')}
      {option('path', 'Connect each run in time order')}
      {option('logX', 'Log X axis')}
      {option('logY', 'Log Y axis')}
    </div>}
  </div>
}

// ---------------------------------------------------------------------------------------
// Shared geometry.

const shapes = ['circle', 'square', 'triangle', 'diamond'] as const

function ShapeMark({shape, x, y, size, color, className, ring = true}: {shape: number; x: number; y: number; size: number; color: string; className?: string; ring?: boolean}) {
  const style = {fill: color}
  const props = {className, style, stroke: ring ? 'var(--card)' : undefined, strokeWidth: ring ? 1.5 : undefined}
  const kind = shapes[shape % shapes.length]
  if (kind === 'square') return <rect x={x - size * .9} y={y - size * .9} width={size * 1.8} height={size * 1.8} {...props} />
  if (kind === 'triangle') return <polygon points={`${x},${y - size * 1.15} ${x - size * 1.1},${y + size * .8} ${x + size * 1.1},${y + size * .8}`} {...props} />
  if (kind === 'diamond') return <polygon points={`${x},${y - size * 1.2} ${x - size * 1.2},${y} ${x},${y + size * 1.2} ${x + size * 1.2},${y}`} {...props} />
  return <circle cx={x} cy={y} r={size} {...props} />
}

function ShapeKey({shape, color}: {shape: number; color: string}) {
  return <svg className="legend-key" width="12" height="12" aria-hidden="true"><ShapeMark shape={shape} x={6} y={6} size={4} color={color} ring={false} /></svg>
}

function axisScale(values: number[], log: boolean, unit: string): Scale {
  return valueScale(values, {zero: false, log, integer: unit === 'replicas' || unit === 'count'})
}

// Fitted line over the series' own x range (never extrapolated), sampled so it stays
// straight on the axes it was fitted on.
function fitSamples(fit: XYFit, points: XYPoint[], xScale: Scale, yScale: Scale, count = 48): Array<[number, number]> {
  if (!points.length) return []
  let low = Infinity
  let high = -Infinity
  for (const point of points) {low = Math.min(low, point.x); high = Math.max(high, point.x)}
  const samples: Array<[number, number]> = []
  for (let index = 0; index <= count; index++) {
    const position = index / count
    const x = xScale.log ? 10 ** (Math.log10(low) + position * (Math.log10(high) - Math.log10(low))) : low + position * (high - low)
    const y = fitAt(fit, x)
    if (y === undefined || !Number.isFinite(y)) continue
    const yPosition = yScale.position(y)
    if (yPosition === undefined || yPosition < 0 || yPosition > 1) continue
    samples.push([x, y])
  }
  return samples
}

interface Prepared {
  data: XYData
  fits: Map<string, XYFit | undefined>
  xScale: Scale
  yScale: Scale
  hidden: number
}

function prepare(data: XYData, options: XYOptions): Prepared {
  const all = data.series.flatMap(series => series.runs.flatMap(run => run.points))
  const visible = plottable(all, options)
  const fits = new Map(data.series.map(series => [series.id, options.fit ? fitXY(series.runs.flatMap(run => run.points), options) : undefined]))
  return {data, fits, hidden: all.length - visible.length, xScale: axisScale(visible.map(point => point.x), options.logX, data.x.unit), yScale: axisScale(visible.map(point => point.y), options.logY, data.y.unit)}
}


// ---------------------------------------------------------------------------------------
// On-screen chart: scatter, optional run paths and fit lines, nearest-point tooltip,
// keyboard stepping, legend with fit statistics and a table view.

// Compact tick labels; falls back to 3 significant digits when they would repeat.
function tickLabels(ticks: number[], unit: string): Map<number, string> {
  const compact = ticks.map(tick => displayValue(tick, unit, true).text)
  const unique = new Set(compact).size === compact.length
  return new Map(ticks.map((tick, index) => [tick, unique ? compact[index] : formatAxisValue(tick, unit)]))
}

const axisShort = (axis: XYData['x']) => `${metricTitle(axis.metric, axis.provenance)}${axis.service ? ` (${axis.service})` : ''}`

interface FlatPoint {series: number; run: number; point: XYPoint; px: number; py: number}

export function XYChart({data, options, groups = []}: {data: XYData; options: XYOptions; groups?: OverviewGroup[]}) {
  const [container, width] = useWidth()
  const [active, setActive] = useState<number | undefined>()
  const [table, setTable] = useState(false)
  const notes = useContext(SeriesNotesContext)
  const id = useId()
  const prepared = useMemo(() => prepare(data, options), [data, options])
  const colors = data.series.map((series, index) => groups.find(group => group.label === series.label)?.color ?? paletteColor(index))
  const height = width < 480 ? 260 : 320
  const margin = {left: 56, right: 16, top: 12, bottom: 30}
  const plotWidth = Math.max(80, width - margin.left - margin.right)
  const plotHeight = height - margin.top - margin.bottom
  const {xScale, yScale, fits} = prepared
  const xLabels = tickLabels(xScale.ticks, data.x.unit)
  const yLabels = tickLabels(yScale.ticks, data.y.unit)
  const px = (value: number) => margin.left + (xScale.position(value) ?? 0) * plotWidth
  const py = (value: number) => margin.top + (1 - (yScale.position(value) ?? 0)) * plotHeight
  const flat = useMemo(() => {
    const list: FlatPoint[] = []
    data.series.forEach((series, seriesIndex) => series.runs.forEach((run, runIndex) => plottable(run.points, options).forEach(point => list.push({series: seriesIndex, run: runIndex, point, px: px(point.x), py: py(point.y)}))))
    return list
  }, [prepared, width, height])
  if (!flat.length) return <p className="empty-inline">No sampling window has both metrics{options.logX || options.logY ? ' with positive values for the log axes' : ''}. Pick other metrics or services.</p>
  const onPointer = (event: PointerEvent<SVGSVGElement>) => {
    const box = event.currentTarget.getBoundingClientRect()
    const scale = box.width ? width / box.width : 1
    const x = (event.clientX - box.left) * scale
    const y = (event.clientY - box.top) * scale
    let best = -1
    let distance = 24 ** 2
    flat.forEach((item, index) => {const d = (item.px - x) ** 2 + (item.py - y) ** 2; if (d < distance) {distance = d; best = index}})
    setActive(best >= 0 ? best : undefined)
  }
  const onKey = (event: KeyboardEvent<HTMLDivElement>) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End', 'Escape'].includes(event.key)) return
    event.preventDefault()
    if (event.key === 'Escape') return setActive(undefined)
    const current = active ?? (event.key === 'ArrowLeft' ? flat.length : -1)
    setActive(event.key === 'Home' ? 0 : event.key === 'End' ? flat.length - 1 : Math.max(0, Math.min(flat.length - 1, current + (event.key === 'ArrowLeft' ? -1 : 1))))
  }
  const focus = active === undefined ? undefined : flat[active]
  const focusSeries = focus ? data.series[focus.series] : undefined
  const focusRun = focus && focusSeries ? focusSeries.runs[focus.run] : undefined
  const runName = focusSeries && focusRun && focusRun.label !== focusSeries.label ? focusRun.label : undefined
  const readout = focus && focusSeries && focusRun ? `${runName ?? focusSeries.label} at ${formatClock(focus.point.t / 1000)}: X ${displayValue(focus.point.x, data.x.unit).text}, Y ${displayValue(focus.point.y, data.y.unit).text}` : ''
  const summary = `Scatter of ${data.y.title} against ${data.x.title}: ${flat.length} sampling windows in ${data.series.length} series.${data.series.map(series => {const fit = fits.get(series.id); return fit ? ` ${series.label}: r ${fit.r.toFixed(2)}, Spearman ${fit.spearman?.toFixed(2) ?? '—'}.` : ''}).join('')}`
  return <div className="xy-chart" ref={container}>
    <div className="xy-axis-title y">{data.y.title}</div>
    <div className="time-chart-plot" tabIndex={0} role="img" aria-label={summary} aria-describedby={`${id}-readout`} onKeyDown={onKey} onBlur={() => setActive(undefined)}>
      <svg width="100%" height={height} viewBox={`0 0 ${width} ${height}`} onPointerMove={onPointer} onPointerLeave={() => setActive(undefined)} aria-hidden="true" focusable="false">
        {yScale.ticks.map(tick => <g key={`y-${tick}`}>
          <line className="chart-grid-line" x1={margin.left} x2={width - margin.right} y1={py(tick)} y2={py(tick)} />
          <text className="chart-tick" x={margin.left - 8} y={py(tick) + 4} textAnchor="end">{yLabels.get(tick)}</text>
        </g>)}
        {xScale.ticks.map(tick => <g key={`x-${tick}`}>
          <line className="chart-grid-line" x1={px(tick)} x2={px(tick)} y1={margin.top} y2={margin.top + plotHeight} />
          <text className="chart-tick" x={px(tick)} y={height - 10} textAnchor="middle">{xLabels.get(tick)}</text>
        </g>)}
        {options.path && data.series.map((series, seriesIndex) => series.runs.map(run => pathSegments(run, plottable(run.points, options)).filter(segment => segment.length > 1).map((segment, index) => <path key={`${series.id}-${run.id}-${index}`} className="xy-path" d={segment.map((point, position) => `${position ? 'L' : 'M'}${px(point.x).toFixed(1)},${py(point.y).toFixed(1)}`).join('')} style={{stroke: colors[seriesIndex]}} />)))}
        {flat.map((item, index) => <ShapeMark key={index} shape={item.series} x={item.px} y={item.py} size={3.5} color={colors[item.series]} className="xy-dot" />)}
        {data.series.map((series, seriesIndex) => {
          const fit = fits.get(series.id)
          const samples = fit ? fitSamples(fit, plottable(series.runs.flatMap(run => run.points), options), xScale, yScale) : []
          return samples.length > 1 && <path key={`fit-${series.id}`} className="xy-fit" d={samples.map(([x, y], index) => `${index ? 'L' : 'M'}${px(x).toFixed(1)},${py(y).toFixed(1)}`).join('')} style={{stroke: colors[seriesIndex]}} />
        })}
        {focus && <g className="chart-crosshair"><ShapeMark shape={focus.series} x={focus.px} y={focus.py} size={6} color={colors[focus.series]} /></g>}
      </svg>
      {focus && focusSeries && focusRun && <div className={`chart-tooltip xy-tooltip ${focus.px > width * .6 ? 'flip' : ''}`} style={{left: `${focus.px / width * 100}%`, top: `${Math.max(0, focus.py - 40)}px`}}>
        <div className="chart-tooltip-time">{formatClock(focus.point.t / 1000)} into the measured window</div>
        <div className="xy-tooltip-series"><ShapeKey shape={focus.series} color={colors[focus.series]} /><span>{runName ?? focusSeries.label}</span></div>
        <dl className="xy-tooltip-values">
          <dt>X · {axisShort(data.x)}</dt><dd>{displayValue(focus.point.x, data.x.unit).text}</dd>
          <dt>Y · {axisShort(data.y)}</dt><dd>{displayValue(focus.point.y, data.y.unit).text}</dd>
        </dl>
      </div>}
    </div>
    <div className="xy-axis-title x">{data.x.title}</div>
    <p id={`${id}-readout`} className="visually-hidden" aria-live="polite">{readout || 'Use the left and right arrow keys to step through the points.'}</p>
    <ul className="xy-legend" aria-label="Series">
      {data.series.map((series, index) => {const fit = fits.get(series.id); const note = notes[series.label]; return <li key={series.id} title={note}>
        <ShapeKey shape={index} color={colors[index]} /><strong>{series.label}{note && <sup aria-hidden="true">*</sup>}</strong>
        <span>{series.runs.length > 1 ? `${series.runs.length} runs · ` : ''}{options.fit ? fit ? fitSummary(fit) : 'too few windows or no spread for a fit' : `${series.runs.reduce((total, run) => total + run.points.length, 0)} windows`}</span>
      </li>})}
    </ul>
    <div className="time-chart-foot">
      <span className="time-chart-axis-label">One point per sampling window{data.level === 'mean' ? ' of the mean across repetitions' : ' of each run'}; X and Y paired by timestamp, never interpolated.{prepared.hidden ? ` ${prepared.hidden} windows with non-positive values are not shown on the log axes.` : ''}{options.fit ? ' Slope CI assumes independent windows (adjacent windows are autocorrelated, so it is optimistic).' : ''}</span>
      <button type="button" className="link-button" aria-expanded={table} onClick={() => setTable(value => !value)}>{table ? 'Hide data table' : 'View as table'}</button>
    </div>
    {table && <div className="chart-table-wrap"><ResponsiveTable aria-label={`${summary} Data table`}>
      <thead><tr><th>Series</th><th>Run</th><th>Time</th><th>{data.x.title}</th><th>{data.y.title}</th></tr></thead>
      <tbody>{data.series.flatMap(series => series.runs.flatMap(run => run.points.map(point => <tr key={`${series.id}-${run.id}-${point.t}`}><td>{series.label}</td><td>{run.label}</td><td>{formatClock(point.t / 1000)}</td><td>{displayValue(point.x, data.x.unit).text}</td><td>{displayValue(point.y, data.y.unit).text}</td></tr>)))}</tbody>
    </ResponsiveTable></div>}
  </div>
}

// ---------------------------------------------------------------------------------------
// Publication figure (Figure studio): same data, exported as SVG/PDF/PNG/CSV.

export function XYFigure({data, options, settings}: {data: XYData; options: XYOptions; settings: FigureSettings}) {
  const labels = useFigureLabels()
  const metrics = figureMetrics(settings)
  const fs = metrics.fontSize
  const prepared = prepare(data, options)
  const {xScale, yScale, fits} = prepared
  const styles = data.series.map((series, index) => seriesStyle(index, series.color, settings))
  const top = metrics.titleHeight
  const base = metrics.height
  const widest = Math.max(fs, ...yScale.ticks.map(tick => metrics.text(formatAxisValue(tick, data.y.unit))))
  const left = widest + fs * 2.1
  const items: LegendItem[] = data.series.map((series, index) => ({label: labels.series(series.label), style: styles[index], kind: 'marker'}))
  const shownItems = data.series.length > 1 ? items : []
  const right = metrics.width - fs * .9 - legendWidth(shownItems, metrics)
  const bottom = base - fs * 2.9
  const x = (value: number) => left + (xScale.position(value) ?? 0) * (right - left)
  const y = (value: number) => bottom - (yScale.position(value) ?? 0) * (bottom - top)
  const legend = legendLayout(shownItems, left, right, metrics)
  const height = base + legend.height
  const fitText = data.series.map(series => {const fit = fits.get(series.id); return fit ? `${data.series.length > 1 ? `${series.label}: ` : ''}${fitSummary(fit)}` : ''}).filter(Boolean)
  const caption = [
    `${data.y.title} against ${data.x.title}. Each marker is one sampling window${data.level === 'mean' ? ' of the mean across repetitions' : ' of one run'}; X and Y are paired by timestamp within the measured window and never interpolated.`,
    options.path ? 'Thin lines connect the windows of each run in time order (broken at missing windows).' : '',
    fitText.length ? `Lines: ordinary least-squares fit per series${options.logX || options.logY ? ' on the log-scaled axes' : ''}.` : '',
    options.logX || options.logY ? `Logarithmic ${[options.logX ? 'x' : '', options.logY ? 'y' : ''].filter(Boolean).join(' and ')} axis${prepared.hidden ? `; ${prepared.hidden} windows with non-positive values omitted` : ''}.` : ''
  ].filter(Boolean).join(' ')
  const fitMethods = fitText.length ? `Fit: ordinary least squares; slope with 95% CI (Student t, n − 2 df, assuming independent windows — adjacent windows are autocorrelated, so the interval is optimistic). ${fitText.join('; ')}.` : ''
  const methods = [fitMethods, ...[data.x, data.y].map(axis => axis.provenance ? provenanceSentence(axis.metric, axis.provenance) : '').filter((text, index, list) => text && list.indexOf(text) === index)].filter(Boolean).join(' ')
  const csv = () => xyCSV(data, options)
  const name = `xy-${data.x.metric}${data.x.service ? `-${data.x.service}` : ''}-vs-${data.y.metric}${data.y.service ? `-${data.y.service}` : ''}`
  return <FigureCard id={`figure-${name.replaceAll(/[^a-zA-Z0-9]/g, '-')}`} name={name} title={`${metricTitle(data.y.metric, data.y.provenance)} vs ${metricTitle(data.x.metric, data.x.provenance)}`} caption={caption} methods={methods} labels={{x: data.x.title, y: data.y.title, series: data.series.length > 1 ? data.series.map(series => series.label) : []}} legendHeight={legend.height} ariaLabel={`Scatter of ${data.y.title} against ${data.x.title}`} width={metrics.width} height={height} settings={settings} provenance={[data.x, data.y].filter(axis => axis.provenance).map(axis => ({metric: axis.metric, provenance: axis.provenance!}))} csv={csv}>
    {yScale.ticks.map(tick => <g key={`y-${tick}`}>
      <line x1={r(left)} x2={r(right)} y1={r(y(tick))} y2={r(y(tick))} stroke="#e3e3e3" strokeWidth={r(metrics.line * .5)} />
      <line x1={r(left - fs * .35)} x2={r(left)} y1={r(y(tick))} y2={r(y(tick))} stroke="#111111" strokeWidth={r(metrics.line * .6)} />
      <SvgText x={left - fs * .55} y={y(tick) + fs * .35} anchor="end" size={fs} family={metrics.family} className="tick">{formatAxisValue(tick, data.y.unit)}</SvgText>
    </g>)}
    {xScale.ticks.map(tick => <g key={`x-${tick}`}>
      <line x1={r(x(tick))} x2={r(x(tick))} y1={r(bottom)} y2={r(bottom + fs * .35)} stroke="#111111" strokeWidth={r(metrics.line * .6)} />
      <SvgText x={x(tick)} y={bottom + fs * 1.3} anchor="middle" size={fs} family={metrics.family} className="tick">{formatAxisValue(tick, data.x.unit)}</SvgText>
    </g>)}
    <SvgText x={fs * .95} y={(top + bottom) / 2} anchor="middle" size={fs} family={metrics.family} rotate={-90} className="axis-label">{labels.y(data.y.title)}</SvgText>
    <SvgText x={(left + right) / 2} y={bottom + fs * 2.55} anchor="middle" size={fs} family={metrics.family} className="axis-label">{labels.x(data.x.title)}</SvgText>
    <rect className="axis" x={r(left)} y={r(top)} width={r(right - left)} height={r(bottom - top)} fill="none" stroke="#111" strokeWidth={r(metrics.line * .6)} />
    {options.path && data.series.map((series, index) => series.runs.flatMap(run => pathSegments(run, plottable(run.points, options)).filter(segment => segment.length > 1).map((segment, part) => <path key={`${series.id}-${run.id}-${part}`} d={segment.map((point, position) => `${position ? 'L' : 'M'}${r(x(point.x))},${r(y(point.y))}`).join(' ')} fill="none" stroke={styles[index].color} strokeOpacity="0.45" strokeWidth={r(metrics.line * .5)} />)))}
    {data.series.map((series, index) => <g key={series.id}>{series.runs.flatMap(run => plottable(run.points, options).map(point => <Marker key={`${run.id}-${point.t}`} shape={styles[index].marker} x={x(point.x)} y={y(point.y)} color={styles[index].color} size={fs * .2} />))}</g>)}
    {data.series.map((series, index) => {
      const fit = fits.get(series.id)
      const samples = fit ? fitSamples(fit, plottable(series.runs.flatMap(run => run.points), options), xScale, yScale) : []
      return samples.length > 1 && <path key={`fit-${series.id}`} d={samples.map(([a, b], position) => `${position ? 'L' : 'M'}${r(x(a))},${r(y(b))}`).join(' ')} fill="none" stroke={styles[index].color} strokeWidth={r(metrics.line * 1.2)} strokeDasharray={styles[index].dash || undefined} />
    })}
    {data.series.length > 1 && <Legend items={items} layout={legend} top={base + fs * .2} metrics={metrics} plot={{left, right, top, bottom}} />}
  </FigureCard>
}
