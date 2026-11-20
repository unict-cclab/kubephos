import {useId, useState} from 'react'
import {Chips} from './FigureSettingsControl'
import {builtInPresets, defaultPanel, lineStyles, markerShapes, palettes, paletteColors, panelCount, paperContrast, runModes, unitChoices, type Annotation, type AxisSide, type AxisSpec, type FigureSpec, type GridMode, type LineStyle, type MarkerShape, type PaletteId, type PanelGrid, type PanelSpec, type RunMode, type SeriesStyleSpec, type StylePreset, type TimeFormat, type XAxisSpec} from './figureSpec'
import type {ModelSeries} from './figureModel'
import {metricTitle} from './format'
import type {ResolvedProvenance} from './runs'
import {ShapeMarker} from './SpecFigure'
import type {XYSource} from './xy'

// Studio panels that edit a FigureSpec: data bindings (metrics per axis and panel), series
// styles, repeated-run display, annotations, axes, palette and style presets.

type SetSpec = (update: (spec: FigureSpec) => FigureSpec) => void
const markerNames: Record<MarkerShape, string> = {circle: 'Circle', square: 'Square', triangle: 'Triangle', diamond: 'Diamond', cross: 'Cross', x: 'X', none: 'None'}
const lineNames: Record<LineStyle, string> = {solid: 'Solid', dashed: 'Dashed', dotted: 'Dotted', dashdot: 'Dash-dot', none: 'No line'}
const runNames: Record<RunMode, {label: string; hint: string}> = {ci: {label: 'Mean + 95 % CI', hint: 'Band: CI of the mean'}, sd: {label: 'Mean ± SD', hint: 'Band: ±1 SD'}, iqr: {label: 'Median + IQR', hint: 'Band: 25th–75th pct'}, spaghetti: {label: 'All runs + mean', hint: 'Thin runs, bold mean'}, runs: {label: 'Runs only', hint: 'One line per run'}, mean: {label: 'Mean only', hint: 'No band'}}

function MarkerIcon({shape}: {shape: MarkerShape}) {
  return <svg width="14" height="14" viewBox="0 0 14 14" aria-hidden="true" focusable="false" className="spec-marker-icon">{shape === 'none' ? <path d="M3 11 11 3" stroke="currentColor" strokeWidth="1.2" /> : <ShapeMarker shape={shape} x={7} y={7} size={3.6} color="currentColor" />}</svg>
}

function LineIcon({line}: {line: LineStyle}) {
  const dash = {solid: undefined, dashed: '4 2.5', dotted: '1 2', dashdot: '4 2 1 2', none: undefined}[line]
  return <svg width="24" height="10" viewBox="0 0 24 10" aria-hidden="true" focusable="false">{line !== 'none' && <line x1="1" x2="23" y1="5" y2="5" stroke="currentColor" strokeWidth="1.6" strokeDasharray={dash} />}</svg>
}

export function SeriesSwatch({series}: {series: ModelSeries}) {
  const color = series.style.color
  return <svg className="spec-swatch" width="28" height="14" viewBox="0 0 28 14" aria-hidden="true" focusable="false">
    {series.style.line !== 'none' && <line x1="1" x2="27" y1="7" y2="7" stroke={color} strokeWidth="1.6" strokeDasharray={series.style.line === 'dashed' ? '4 2.5' : series.style.line === 'dotted' ? '1 2' : series.style.line === 'dashdot' ? '4 2 1 2' : undefined} />}
    <ShapeMarker shape={series.style.marker} x={14} y={7} size={3.4} color={color} hollow={series.style.hollow} width={1.2} />
  </svg>
}

// ---------------------------------------------------------------------------------------
// Data: panels and metric bindings.

export function DataBindings({spec, setSpec, metrics, provenance, primary, onPrimary, panel, onPanel}: {spec: FigureSpec; setSpec: SetSpec; metrics: string[]; provenance: (metric: string) => ResolvedProvenance; primary: string; onPrimary: (metric: string) => void; panel: number; onPanel: (index: number) => void}) {
  const id = useId()
  const count = panelCount(spec.layout.grid)
  const active = Math.min(panel, count - 1)
  const bindings = spec.panels[active]?.metrics ?? []
  const [adding, setAdding] = useState('')
  const title = (metric: string) => metricTitle(metric, provenance(metric))
  const setPanel = (index: number, update: (panel: PanelSpec) => PanelSpec) => setSpec(current => ({...current, panels: current.panels.map((item, position) => position === index ? update(item) : item)}))
  const setGrid = (grid: PanelGrid) => setSpec(current => {
    const panels = [...current.panels]
    while (panels.length < panelCount(grid)) {
      // New panels start with the next metric not yet shown.
      const used = new Set(panels.flatMap(item => item.metrics.map(binding => binding.metric)))
      const next = metrics.find(metric => !used.has(metric) && metric !== primary) ?? primary
      panels.push({...defaultPanel([{metric: next, axis: 'left'}]), x: {...panels[0].x}})
    }
    return {...current, layout: {...current.layout, grid}, panels}
  })
  const candidate = adding && metrics.includes(adding) ? adding : metrics.find(metric => !bindings.some(item => item.metric === metric)) ?? ''
  const add = (axis: AxisSide) => {if (candidate) setPanel(active, item => ({...item, metrics: [...(active === 0 && !item.metrics.length ? [{metric: primary, axis: 'left' as const}] : item.metrics), {metric: candidate, axis}]}))}
  return <div className="spec-bindings">
    <Chips legend="Panels" name={`${id}-grid`} value={spec.layout.grid} onChange={setGrid} className="studio-segmented" choices={[{value: '1x1', label: '1'}, {value: '1x2', label: '1 × 2'}, {value: '2x1', label: '2 × 1'}, {value: '2x2', label: '2 × 2'}]} />
    {count > 1 && <>
      <div className="studio-checks spec-inline-checks" role="group" aria-label="Panel layout">
        <label className="checkbox-row"><input type="checkbox" checked={spec.layout.sharedX} onChange={event => setSpec(current => ({...current, layout: {...current.layout, sharedX: event.target.checked}}))} />Shared X axis</label>
        <label className="checkbox-row"><input type="checkbox" checked={spec.layout.sharedY} onChange={event => setSpec(current => ({...current, layout: {...current.layout, sharedY: event.target.checked}}))} />Shared left Y axis</label>
        <label className="checkbox-row"><input type="checkbox" checked={spec.layout.letters} onChange={event => setSpec(current => ({...current, layout: {...current.layout, letters: event.target.checked}}))} />Panel letters (a), (b)…</label>
      </div>
      <Chips legend="Edit panel" name={`${id}-panel`} value={String(active)} onChange={value => onPanel(Number(value))} className="studio-segmented" choices={Array.from({length: count}, (_, index) => ({value: String(index), label: `(${'abcd'[index]})`}))} />
    </>}
    <ul className="spec-binding-list" aria-label={count > 1 ? `Metrics in panel (${'abcd'[active]})` : 'Metrics'}>
      {(active === 0 && !bindings.length ? [{metric: primary, axis: 'left' as const}] : bindings).map((binding, index) => <li key={`${binding.metric}-${index}`} className="spec-binding">
        {index === 0 && active === 0 ? <label className="visually-hidden" htmlFor={`${id}-primary`}>Metric</label> : null}
        {index === 0 && active === 0
          ? <select id={`${id}-primary`} value={primary} onChange={event => onPrimary(event.target.value)}>{metrics.map(metric => <option key={metric} value={metric}>{title(metric)}</option>)}</select>
          : <select aria-label={`Metric ${index + 1}`} value={binding.metric} onChange={event => setPanel(active, item => ({...item, metrics: item.metrics.map((entry, position) => position === index ? {...entry, metric: event.target.value} : entry)}))}>{metrics.map(metric => <option key={metric} value={metric}>{title(metric)}</option>)}</select>}
        <div className="spec-axis-toggle" role="group" aria-label={`Axis of ${title(index === 0 && active === 0 ? primary : binding.metric)}`}>
          {(['left', 'right'] as const).map(axis => <button key={axis} type="button" className="spec-toggle" aria-pressed={binding.axis === axis} onClick={() => setPanel(active, item => {const list = active === 0 && !item.metrics.length ? [{metric: primary, axis: 'left' as const}] : item.metrics; return {...item, metrics: list.map((entry, position) => position === index ? {...entry, axis} : entry)}})}>{axis === 'left' ? 'Left' : 'Right'}</button>)}
        </div>
        {(index > 0 || active > 0) && <button type="button" className="icon-button spec-remove" aria-label={`Remove ${title(binding.metric)}`} onClick={() => setPanel(active, item => ({...item, metrics: item.metrics.filter((_, position) => position !== index)}))}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>}
      </li>)}
    </ul>
    {metrics.length > 1 && <div className="spec-add">
      <label className="studio-field" htmlFor={`${id}-add`}>Add metric<select id={`${id}-add`} value={candidate} onChange={event => setAdding(event.target.value)}>{metrics.map(metric => <option key={metric} value={metric}>{title(metric)}</option>)}</select></label>
      <div className="spec-add-buttons">
        <button type="button" className="button secondary compact" onClick={() => add('left')}>Add to left axis</button>
        <button type="button" className="button secondary compact" onClick={() => add('right')}>Add to right axis</button>
      </div>
    </div>}
  </div>
}

// X–Y: optional second Y metric on the right axis.
export function SecondY({spec, setSpec, source}: {spec: FigureSpec; setSpec: SetSpec; source: XYSource}) {
  const id = useId()
  const value = spec.panels[0].y2
  const services = value?.metric ? source.services(value.metric) : undefined
  const set = (y2: PanelSpec['y2']) => setSpec(current => ({...current, panels: current.panels.map((panel, index) => index ? panel : {...panel, y2})}))
  return <div className="xy-axis-picker">
    <label htmlFor={`${id}-y2`}>Right Y axis (optional)<select id={`${id}-y2`} value={value?.metric ?? ''} onChange={event => set(event.target.value ? {metric: event.target.value} : undefined)}>
      <option value="">None</option>
      {source.metrics.map(metric => <option key={metric} value={metric}>{metricTitle(metric, source.provenance(metric))}</option>)}
    </select></label>
    {value && services && services.length > 0 && <label htmlFor={`${id}-y2s`}>Right Y service<select id={`${id}-y2s`} value={value.service ?? ''} onChange={event => set({metric: value.metric, service: event.target.value || undefined})}><option value="">All services (total)</option>{services.map(service => <option key={service} value={service}>{service}</option>)}</select></label>}
  </div>
}

// ---------------------------------------------------------------------------------------
// Series list and per-series style editor.

function NumberField({label, value, min, max, step, onChange, suffix}: {label: string; value: number; min: number; max: number; step: number; onChange: (value: number) => void; suffix?: string}) {
  const id = useId()
  return <label className="spec-number" htmlFor={id}><span>{label}{suffix ? ` (${suffix})` : ''}</span><input id={id} type="number" min={min} max={max} step={step} value={Number(value.toFixed(2))} onChange={event => {const next = Number(event.target.value); if (Number.isFinite(next) && next >= min && next <= max) onChange(next)}} /></label>
}

export function SeriesStyleEditor({series, spec, setSpec, onLabel, label}: {series: ModelSeries; spec: FigureSpec; setSpec: SetSpec; onLabel: (value: string | undefined) => void; label: string}) {
  const id = useId()
  const style = series.style
  const [hex, setHex] = useState(style.color)
  const set = (patch: SeriesStyleSpec) => setSpec(current => ({...current, series: {...current.series, [series.key]: {...current.series[series.key], ...patch}}}))
  const check = paperContrast(/^#[0-9a-f]{6}$/i.test(hex) ? hex : style.color)
  const swatches = [...new Set([...paletteColors(spec.palette), ...paletteColors('okabe-ito'), '#000000', '#555555'])]
  return <div className="spec-style-editor" role="group" aria-label={`Style of ${series.label}`}>
    <div className="studio-text-field">
      <label htmlFor={`${id}-label`}>Label</label>
      <input id={`${id}-label`} type="text" value={label} onChange={event => onLabel(event.target.value === series.defaultLabel ? undefined : event.target.value)} />
    </div>
    <fieldset className="spec-colors">
      <legend className="studio-field-label">Colour</legend>
      <div className="spec-swatch-grid">
        {swatches.map(color => <button key={color} type="button" className="spec-color" style={{background: color}} aria-label={`Colour ${color}`} aria-pressed={style.color === color} onClick={() => {setHex(color); set({color})}} />)}
      </div>
      <div className="spec-hex">
        <label htmlFor={`${id}-hex`}>Hex</label>
        <input id={`${id}-hex`} type="text" value={hex} maxLength={7} spellCheck={false} onChange={event => {setHex(event.target.value); if (/^#[0-9a-fA-F]{6}$/.test(event.target.value)) set({color: event.target.value.toLowerCase()})}} />
        <input type="color" aria-label="Pick colour" value={/^#[0-9a-f]{6}$/i.test(hex) ? hex : style.color} onChange={event => {setHex(event.target.value); set({color: event.target.value})}} />
        <span className={`spec-contrast ${check.ok ? 'ok' : 'low'}`} role="status">{check.ratio.toFixed(1)}:1 on white{check.ok ? '' : ' — below 3:1, hard to see'}</span>
      </div>
    </fieldset>
    <fieldset className="spec-icon-chips">
      <legend className="studio-field-label">Marker</legend>
      <div className="spec-chip-row">{markerShapes.map(shape => <label key={shape} className="spec-icon-chip" data-checked={style.marker === shape} title={markerNames[shape]}>
        <input type="radio" className="visually-hidden" name={`${id}-marker`} checked={style.marker === shape} onChange={() => set({marker: shape})} aria-label={markerNames[shape]} /><MarkerIcon shape={shape} />
      </label>)}</div>
    </fieldset>
    <div className="studio-two">
      <NumberField label="Marker size" suffix="pt" value={style.markerSize} min={.5} max={8} step={.1} onChange={markerSize => set({markerSize})} />
      <label className="checkbox-row spec-hollow"><input type="checkbox" checked={style.hollow} onChange={event => set({hollow: event.target.checked})} />Hollow marker</label>
    </div>
    <fieldset className="spec-icon-chips">
      <legend className="studio-field-label">Line</legend>
      <div className="spec-chip-row">{lineStyles.map(line => <label key={line} className="spec-icon-chip wide" data-checked={style.line === line} title={lineNames[line]}>
        <input type="radio" className="visually-hidden" name={`${id}-line`} checked={style.line === line} onChange={() => set({line})} aria-label={lineNames[line]} /><LineIcon line={line} />
      </label>)}</div>
    </fieldset>
    <div className="studio-two">
      <NumberField label="Line width" suffix="pt" value={style.lineWidth} min={.25} max={6} step={.05} onChange={lineWidth => set({lineWidth})} />
      <label className="spec-number" htmlFor={`${id}-opacity`}><span>Opacity {Math.round(style.opacity * 100)} %</span><input id={`${id}-opacity`} type="range" min={10} max={100} step={5} value={Math.round(style.opacity * 100)} onChange={event => set({opacity: Number(event.target.value) / 100})} /></label>
    </div>
    <button type="button" className="button ghost compact" onClick={() => {setSpec(current => {const {[series.key]: _, ...rest} = current.series; void _; return {...current, series: rest}}); setHex(style.color); onLabel(undefined)}}>Reset this series</button>
  </div>
}

export function SeriesList({series, spec, setSpec, editing, onEdit, labels, onLabel, idPrefix}: {series: ModelSeries[]; spec: FigureSpec; setSpec: SetSpec; editing?: string; onEdit: (key: string | undefined) => void; labels: Record<string, string>; onLabel: (defaultLabel: string, value: string | undefined) => void; idPrefix: string}) {
  const keys = series.map(item => item.key)
  const move = (index: number, by: number) => {
    const next = [...keys]
    const [item] = next.splice(index, 1)
    next.splice(Math.max(0, Math.min(keys.length, index + by)), 0, item)
    setSpec(current => ({...current, order: next}))
  }
  return <ul className="spec-series-list" aria-label="Series styles (legend and drawing order)">
    {series.map((item, index) => <li key={item.key} id={`${idPrefix}${item.key}`} className="spec-series" data-open={editing === item.key || undefined}>
      <div className="spec-series-row">
        <button type="button" className="spec-series-button" aria-expanded={editing === item.key} onClick={() => onEdit(editing === item.key ? undefined : item.key)}>
          <SeriesSwatch series={item} />
          <span className="spec-series-text"><span>{item.label}</span><small>{item.axis === 'right' ? 'Right axis' : 'Left axis'}</small></span>
          <span className="visually-hidden">Edit style</span>
        </button>
        <button type="button" className="icon-button spec-move" aria-label={`Move ${item.label} up`} disabled={!index} onClick={() => move(index, -1)}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 15 6-6 6 6" /></svg></button>
        <button type="button" className="icon-button spec-move" aria-label={`Move ${item.label} down`} disabled={index === series.length - 1} onClick={() => move(index, 1)}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="m6 9 6 6 6-6" /></svg></button>
      </div>
      {editing === item.key && <SeriesStyleEditor key={item.key} series={item} spec={spec} setSpec={setSpec} label={labels[item.defaultLabel] ?? item.defaultLabel} onLabel={value => onLabel(item.defaultLabel, value)} />}
    </li>)}
  </ul>
}

export function RunModePicker({value, onChange, fallback, disabled}: {value: RunMode; onChange: (mode: RunMode) => void; fallback: boolean; disabled: boolean}) {
  const id = useId()
  return <div className="spec-runmode">
    <Chips legend="Repeated runs" hideLegend name={`${id}-runs`} value={value} onChange={onChange} disabled={disabled} className="spec-run-chips" choices={runModes.map(mode => ({value: mode, label: runNames[mode].label, hint: runNames[mode].hint}))} />
    {fallback && <p className="studio-hint">Individual repetitions are not available here (saved comparisons keep only the mean); showing the mean with its 95 % CI.</p>}
    {disabled && <p className="studio-hint">One run per series: nothing to summarise.</p>}
  </div>
}

// ---------------------------------------------------------------------------------------
// Annotations.

let annotationCounter = 0
const newID = () => `a${Date.now().toString(36)}${(annotationCounter++).toString(36)}`

export function AnnotationsEditor({spec, setSpec, panel, xUnit, hasProfile, hasSLO, timeSeries}: {spec: FigureSpec; setSpec: SetSpec; panel: number; xUnit: string; hasProfile: boolean; hasSLO: boolean; timeSeries: boolean}) {
  const id = useId()
  const items = spec.panels[panel]?.annotations ?? []
  const setItems = (update: (items: Annotation[]) => Annotation[]) => setSpec(current => ({...current, panels: current.panels.map((item, index) => index === panel ? {...item, annotations: update(item.annotations)} : item)}))
  const patch = (index: number, value: Partial<Annotation>) => setItems(list => list.map((item, position) => position === index ? {...item, ...value} as Annotation : item))
  const number = (value: string) => {const parsed = Number(value); return Number.isFinite(parsed) ? parsed : undefined}
  const kindName: Record<Annotation['kind'], string> = {hline: 'Reference line', vline: 'Vertical line', xrange: 'Shaded range', text: 'Text label'}
  const field = (label: string, value: number, onChange: (value: number) => void) => <label className="spec-number"><span>{label}</span><input type="number" step="any" defaultValue={value} onChange={event => {const parsed = number(event.target.value); if (parsed !== undefined) onChange(parsed)}} /></label>
  return <div className="spec-annotations">
    {timeSeries && <div className="studio-checks" role="group" aria-label="Automatic annotations">
      <label className="checkbox-row"><input type="checkbox" checked={spec.auto.phases} disabled={!hasProfile} onChange={event => setSpec(current => ({...current, auto: {...current.auto, phases: event.target.checked}}))} />Load-phase boundaries{hasProfile ? '' : ' (no load profile)'}</label>
      <label className="checkbox-row"><input type="checkbox" checked={spec.auto.warmup} disabled={!hasProfile} onChange={event => setSpec(current => ({...current, auto: {...current.auto, warmup: event.target.checked}}))} />Warm-up end</label>
      <label className="checkbox-row"><input type="checkbox" checked={spec.auto.slo} disabled={!hasSLO} onChange={event => setSpec(current => ({...current, auto: {...current.auto, slo: event.target.checked}}))} />SLO line{hasSLO ? '' : ' (no SLO set)'}</label>
    </div>}
    <ul className="spec-annotation-list" aria-label="Annotations">
      {items.map((item, index) => <li key={item.id} className="spec-annotation">
        <div className="spec-annotation-head"><strong>{kindName[item.kind]}</strong><button type="button" className="icon-button spec-remove" aria-label={`Remove ${kindName[item.kind].toLowerCase()} ${index + 1}`} onClick={() => setItems(list => list.filter((_, position) => position !== index))}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button></div>
        <div className="spec-annotation-fields">
          {item.kind === 'hline' && <>{field('Value (axis units)', item.value, value => patch(index, {value}))}<label className="spec-number"><span>Axis</span><select value={item.axis} onChange={event => patch(index, {axis: event.target.value as AxisSide})}><option value="left">Left</option><option value="right">Right</option></select></label></>}
          {item.kind === 'vline' && field(`At (${xUnit})`, item.at, at => patch(index, {at}))}
          {item.kind === 'xrange' && <>{field(`From (${xUnit})`, item.from, from => patch(index, {from}))}{field(`To (${xUnit})`, item.to, to => patch(index, {to}))}</>}
          {item.kind === 'text' && <>{field(`X (${xUnit})`, item.at, at => patch(index, {at}))}{field('Y (axis units)', item.value, value => patch(index, {value}))}</>}
          <label className="spec-text"><span>{item.kind === 'text' ? 'Text' : 'Label'}</span><input type="text" value={item.kind === 'text' ? item.text : item.label ?? ''} onChange={event => patch(index, item.kind === 'text' ? {text: event.target.value} : {label: event.target.value || undefined})} /></label>
        </div>
      </li>)}
    </ul>
    <div className="spec-add-buttons" role="group" aria-label="Add annotation">
      <button type="button" className="button secondary compact" onClick={() => setItems(list => [...list, {id: newID(), kind: 'hline', axis: 'left', value: 0, label: 'Reference'}])}>+ Reference line</button>
      <button type="button" className="button secondary compact" onClick={() => setItems(list => [...list, {id: newID(), kind: 'xrange', from: 0, to: 60, label: 'Event'}])}>+ Shaded range</button>
      <button type="button" className="button secondary compact" onClick={() => setItems(list => [...list, {id: newID(), kind: 'vline', at: 60, label: ''}])}>+ Vertical line</button>
      <button type="button" className="button secondary compact" onClick={() => setItems(list => [...list, {id: newID(), kind: 'text', axis: 'left', at: 30, value: 0, text: 'Note'}])}>+ Text</button>
    </div>
    <p className="studio-hint" id={`${id}-units`}>{timeSeries ? 'X positions are seconds since the start of the measured window; values are in the axis display unit.' : 'Positions are in the axis display units.'}</p>
  </div>
}

// ---------------------------------------------------------------------------------------
// Axes.

function AxisFields({axis, onChange, unit, time, labelField = true}: {axis: AxisSpec | XAxisSpec; onChange: (next: AxisSpec | XAxisSpec) => void; unit?: string; time?: boolean; labelField?: boolean}) {
  const id = useId()
  const set = (patch: Partial<XAxisSpec>) => onChange({...axis, ...patch})
  const choices = unit ? unitChoices[unit] : undefined
  const number = (value: string) => value.trim() === '' ? undefined : Number.isFinite(Number(value)) ? Number(value) : undefined
  return <div className="spec-axis-fields">
    {labelField && <div className="studio-text-field"><label htmlFor={`${id}-label`}>Label (blank = automatic)</label><input id={`${id}-label`} type="text" value={axis.label ?? ''} onChange={event => set({label: event.target.value || undefined})} /></div>}
    <Chips legend="Range" name={`${id}-range`} value={axis.range} onChange={range => set({range})} className="studio-segmented" choices={[{value: 'auto', label: 'Auto'}, {value: 'zero', label: 'From 0'}, {value: 'custom', label: 'Custom'}]} />
    {axis.range === 'custom' && <div className="studio-two">
      <label className="spec-number" htmlFor={`${id}-min`}><span>Min</span><input id={`${id}-min`} type="number" step="any" defaultValue={axis.min} onChange={event => set({min: number(event.target.value)})} /></label>
      <label className="spec-number" htmlFor={`${id}-max`}><span>Max</span><input id={`${id}-max`} type="number" step="any" defaultValue={axis.max} onChange={event => set({max: number(event.target.value)})} /></label>
    </div>}
    <div className="studio-two">
      <label className="spec-number" htmlFor={`${id}-ticks`}><span>Ticks</span><select id={`${id}-ticks`} value={axis.ticks} onChange={event => set({ticks: event.target.value as AxisSpec['ticks']})}><option value="auto">Automatic</option><option value="count">Count</option><option value="step">Step</option></select></label>
      {axis.ticks !== 'auto' && <label className="spec-number" htmlFor={`${id}-tick-value`}><span>{axis.ticks === 'count' ? 'Number of ticks' : 'Tick step'}</span><input id={`${id}-tick-value`} type="number" min={axis.ticks === 'count' ? 2 : 0} step="any" defaultValue={axis.tickValue} onChange={event => set({tickValue: number(event.target.value)})} /></label>}
    </div>
    <div className="studio-two">
      {time ? <label className="spec-number" htmlFor={`${id}-time`}><span>Time as</span><select id={`${id}-time`} value={(axis as XAxisSpec).time} onChange={event => set({time: event.target.value as TimeFormat})}><option value="min">Minutes</option><option value="s">Seconds</option><option value="mmss">mm:ss</option></select></label>
        : choices && choices.length > 1 ? <label className="spec-number" htmlFor={`${id}-unit`}><span>Unit</span><select id={`${id}-unit`} value={axis.unit ?? choices[0].id} onChange={event => set({unit: event.target.value === choices[0].id ? undefined : event.target.value})}>{choices.map(choice => <option key={choice.id} value={choice.id}>{choice.label}</option>)}</select></label> : null}
      {!time && <label className="spec-number" htmlFor={`${id}-decimals`}><span>Decimals</span><select id={`${id}-decimals`} value={axis.decimals ?? ''} onChange={event => set({decimals: event.target.value === '' ? undefined : Number(event.target.value)})}><option value="">Auto</option>{[0, 1, 2, 3, 4].map(value => <option key={value} value={value}>{value}</option>)}</select></label>}
    </div>
    <div className="studio-checks spec-inline-checks">
      {!time && <label className="checkbox-row"><input type="checkbox" checked={axis.thousands} onChange={event => set({thousands: event.target.checked})} />Thousands separator</label>}
      <label className="checkbox-row"><input type="checkbox" checked={axis.log} onChange={event => set({log: event.target.checked})} />Log scale</label>
    </div>
  </div>
}

export function AxesEditor({spec, setSpec, panel, units, timeSeries, hasRight, onXLog, onLeftLog}: {spec: FigureSpec; setSpec: SetSpec; panel: number; units: {x?: string; left?: string; right?: string}; timeSeries: boolean; hasRight: boolean; onXLog?: (log: boolean) => void; onLeftLog?: (log: boolean) => void}) {
  const id = useId()
  const [which, setWhich] = useState<'x' | 'left' | 'right'>('left')
  const tab = which === 'right' && !hasRight ? 'left' : which
  const current = spec.panels[panel] ?? spec.panels[0]
  const setAxis = (key: 'x' | 'left' | 'right', next: AxisSpec | XAxisSpec) => {
    if (key === 'x' && onXLog && next.log !== current.x.log) onXLog(next.log)
    if (key === 'left' && onLeftLog && next.log !== current.left.log) onLeftLog(next.log)
    // The time format is shared by every panel (one X axis family).
    setSpec(spec => ({...spec, panels: spec.panels.map((item, index) => index === panel ? {...item, [key]: next} : key === 'x' && timeSeries ? {...item, x: {...item.x, time: (next as XAxisSpec).time}} : item)}))
  }
  return <div className="spec-axes">
    <Chips legend="Axis" name={`${id}-axis`} value={tab} onChange={setWhich} className="studio-segmented" hideLegend choices={[{value: 'x', label: 'X'}, {value: 'left', label: 'Left Y'}, ...(hasRight ? [{value: 'right' as const, label: 'Right Y'}] : [])]} />
    <AxisFields key={`${panel}-${tab}`} axis={current[tab]} onChange={next => setAxis(tab, next)} unit={units[tab]} time={tab === 'x' && timeSeries} />
    <Chips legend="Grid" name={`${id}-grid`} value={spec.grid} onChange={(grid: GridMode) => setSpec(spec => ({...spec, grid}))} className="studio-segmented" choices={[{value: 'none', label: 'None'}, {value: 'major', label: 'Major'}, {value: 'minor', label: 'Major + minor'}]} />
  </div>
}

// ---------------------------------------------------------------------------------------
// Palette, style presets and the spec as JSON.

export function PaletteEditor({spec, setSpec}: {spec: FigureSpec; setSpec: SetSpec}) {
  const id = useId()
  return <div className="spec-palette">
    <label className="studio-field" htmlFor={`${id}-palette`}>Palette<select id={`${id}-palette`} value={spec.palette} onChange={event => setSpec(current => ({...current, palette: event.target.value as PaletteId}))}>{(Object.keys(palettes) as PaletteId[]).map(key => <option key={key} value={key}>{palettes[key].label} — {palettes[key].hint}</option>)}</select></label>
    <div className="spec-palette-strip" aria-hidden="true">{paletteColors(spec.palette).map((color, index) => <span key={`${color}-${index}`} style={{background: color}} />)}</div>
    <button type="button" className="button secondary compact" onClick={() => setSpec(current => ({...current, series: Object.fromEntries(Object.entries(current.series).flatMap(([key, style]) => {const {color: _, ...rest} = style; void _; return Object.keys(rest).length ? [[key, rest]] : []}))}))}>Apply palette to all series</button>
  </div>
}

export function PresetsEditor({presets, onApply, onSave, onDelete}: {presets: StylePreset[]; onApply: (preset: StylePreset) => void; onSave: (name: string) => void; onDelete: (name: string) => void}) {
  const id = useId()
  const all = [...builtInPresets, ...presets]
  const [selected, setSelected] = useState(all[0].name)
  const [name, setName] = useState('')
  const preset = all.find(item => item.name === selected) ?? all[0]
  return <div className="spec-presets">
    <div className="spec-preset-row">
      <label className="visually-hidden" htmlFor={`${id}-preset`}>Style preset</label>
      <select id={`${id}-preset`} value={preset.name} onChange={event => setSelected(event.target.value)}>
        <optgroup label="Built-in">{builtInPresets.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</optgroup>
        {presets.length > 0 && <optgroup label="Saved">{presets.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</optgroup>}
      </select>
      <button type="button" className="button secondary compact" onClick={() => onApply(preset)}>Apply</button>
      {!preset.builtIn && <button type="button" className="icon-button spec-remove" aria-label={`Delete preset ${preset.name}`} onClick={() => {onDelete(preset.name); setSelected(all[0].name)}}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>}
    </div>
    <form className="spec-preset-row" onSubmit={event => {event.preventDefault(); if (name.trim()) {onSave(name.trim()); setSelected(name.trim()); setName('')}}}>
      <label className="visually-hidden" htmlFor={`${id}-name`}>Preset name</label>
      <input id={`${id}-name`} type="text" placeholder="Preset name" value={name} maxLength={60} onChange={event => setName(event.target.value)} />
      <button type="submit" className="button ghost compact" disabled={!name.trim()}>Save current style as preset</button>
    </form>
  </div>
}

export function SpecActions({onCopy, onPaste, onReset}: {onCopy: () => void; onPaste: (json?: string) => void; onReset: () => void}) {
  const id = useId()
  const [manual, setManual] = useState(false)
  const [text, setText] = useState('')
  return <div className="spec-actions">
    <div className="spec-add-buttons">
      <button type="button" className="button secondary compact" onClick={onCopy}>Copy spec</button>
      <button type="button" className="button secondary compact" onClick={() => onPaste()}>Paste spec</button>
      <button type="button" className="button ghost compact" aria-expanded={manual} onClick={() => setManual(value => !value)}>Paste as text…</button>
      <button type="button" className="button ghost compact" onClick={onReset}>Reset figure</button>
    </div>
    {manual && <div className="spec-manual">
      <label className="studio-field" htmlFor={`${id}-json`}>Figure spec (JSON)<textarea id={`${id}-json`} rows={5} value={text} spellCheck={false} onChange={event => setText(event.target.value)} /></label>
      <button type="button" className="button secondary compact" disabled={!text.trim()} onClick={() => {onPaste(text); setManual(false); setText('')}}>Apply spec</button>
    </div>}
  </div>
}

