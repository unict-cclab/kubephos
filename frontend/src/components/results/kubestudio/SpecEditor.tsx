import {useId, useState} from 'react'
import {Chips, ResetIcon, SelectField, Stepper, Toggle} from './controls'
import {builtInPresets, defaultPanel, lineStyles, markerShapes, paletteColors, palettes, paperContrast, unitChoices, type Annotation, type AxisSide, type AxisSpec, type FigureSpec, type LineStyle, type MarkerShape, type PaletteId, type PanelSpec, type RunMode, type SeriesStyleSpec, type StylePreset, type TimeFormat, type XAxisSpec} from './figureSpec'
import type {ModelSeries} from './figureModel'
import {metricTitle} from '../format'
import type {ResolvedProvenance} from '../runs'
import {ShapeMarker} from './SpecFigure'
import type {XYAxisSelection, XYSource} from '../xy'

// KubeStudio property editors for a FigureSpec: series style, axis metrics and scales,
// annotations, palette, presets and the spec as JSON. Each setting lives in exactly one of
// them; the studio shows the one that matches the selected layer.

type SetSpec = (update: (spec: FigureSpec) => FigureSpec) => void
const markerNames: Record<MarkerShape, string> = {circle: 'Circle', square: 'Square', triangle: 'Triangle', diamond: 'Diamond', cross: 'Cross', x: 'X', none: 'No marker'}
const lineNames: Record<LineStyle, string> = {solid: 'Solid', dashed: 'Dashed', dotted: 'Dotted', dashdot: 'Dash-dot', none: 'No line'}
export const runNames: Record<RunMode, {label: string; hint: string}> = {ci: {label: 'Mean + 95 % CI', hint: 'Band: CI of the mean'}, sd: {label: 'Mean ± SD', hint: 'Band: ±1 SD'}, iqr: {label: 'Median + IQR', hint: 'Band: 25th–75th percentile'}, spaghetti: {label: 'All runs + mean', hint: 'Thin runs, bold mean'}, runs: {label: 'Runs only', hint: 'One line per run'}, mean: {label: 'Mean only', hint: 'No band'}}

function MarkerIcon({shape}: {shape: MarkerShape}) {
  return <svg width="16" height="16" viewBox="0 0 16 16" aria-hidden="true" focusable="false">{shape === 'none' ? <path d="M4 12 12 4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /> : <ShapeMarker shape={shape} x={8} y={8} size={3.6} color="currentColor" />}</svg>
}

function LineIcon({line}: {line: LineStyle}) {
  const dash = {solid: undefined, dashed: '4 2.5', dotted: '1 2', dashdot: '4 2 1 2', none: undefined}[line]
  return <svg width="24" height="16" viewBox="0 0 24 16" aria-hidden="true" focusable="false">{line === 'none' ? <path d="M8 12 16 4" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" /> : <line x1="2" x2="22" y1="8" y2="8" stroke="currentColor" strokeWidth="1.6" strokeDasharray={dash} />}</svg>
}

export function SeriesSwatch({series}: {series: ModelSeries}) {
  const color = series.style.color
  return <svg className="ks-swatch" width="24" height="12" viewBox="0 0 24 12" aria-hidden="true" focusable="false">
    {series.style.line !== 'none' && <line x1="1" x2="23" y1="6" y2="6" stroke={color} strokeWidth="1.6" strokeDasharray={series.style.line === 'dashed' ? '4 2.5' : series.style.line === 'dotted' ? '1 2' : series.style.line === 'dashdot' ? '4 2 1 2' : undefined} />}
    {series.kind === 'scatter' && <ShapeMarker shape={series.style.marker} x={12} y={6} size={3} color={color} hollow={series.style.hollow} width={1.2} />}
  </svg>
}

// Number input that keeps partial text ("-", "1.") while typing and follows outside changes
// (undo, dragging on the canvas).
// A number typed freely, with a comma or a dot. What is typed is never rewritten while typing:
// a number applies at once (the parent may refuse it, e.g. an SLO of 0 on the way to 0.2), and
// the text follows the value only when it changes from elsewhere (undo, a unit switch). Leaving
// the field shows the value in use; leaving it empty clears it.
const numberText = (number: number | undefined) => number === undefined ? '' : String(Number(number.toPrecision(10)))
const parseNumber = (raw: string) => {const clean = raw.trim().replace(',', '.'); return /^[-+]?(\d+(\.\d+)?|\.\d+)(e[-+]?\d+)?$/i.test(clean) ? Number(clean) : undefined}

export function NumberInput({value, onChange, id, min, ...rest}: {value: number | undefined; onChange: (value: number | undefined) => void; id?: string; min?: number; 'aria-label'?: string}) {
  // value: the last value received, to tell a change from elsewhere.
  const [state, setState] = useState({text: numberText(value), value})
  let text = state.text
  if (state.value !== value) {
    if (parseNumber(state.text) !== value) text = numberText(value)
    setState({text, value})
  }
  return <input id={id} type="text" inputMode="decimal" autoComplete="off" spellCheck={false} {...rest} value={text} onChange={event => {
    const raw = event.target.value
    setState(current => ({...current, text: raw}))
    const number = parseNumber(raw)
    if (number !== undefined && Number.isFinite(number) && (min === undefined || number >= min)) onChange(number)
  }} onBlur={() => {
    if (text.trim() === '') {if (value !== undefined) onChange(undefined); return}
    if (parseNumber(text) !== value) setState({text: numberText(value), value})
  }} />
}

function NumberField({label, value, onChange}: {label: string; value: number | undefined; onChange: (value: number | undefined) => void}) {
  const id = useId()
  return <div className="ks-field"><label className="ks-label" htmlFor={id}>{label}</label><NumberInput id={id} value={value} onChange={onChange} /></div>
}

// ---------------------------------------------------------------------------------------
// Series.

export function SeriesProperties({series, spec, setSpec, label, onLabel, axis, onAxis, onMove, first, last}: {series: ModelSeries; spec: FigureSpec; setSpec: SetSpec; label: string; onLabel: (value: string | undefined) => void; axis?: AxisSide; onAxis?: (side: AxisSide) => void; onMove: (by: -1 | 1) => void; first: boolean; last: boolean}) {
  const id = useId()
  const style = series.style
  const [hex, setHex] = useState(style.color)
  const [shown, setShown] = useState(style.color)
  if (shown !== style.color) {setShown(style.color); setHex(style.color)}
  const set = (patch: SeriesStyleSpec) => setSpec(current => ({...current, series: {...current.series, [series.key]: {...current.series[series.key], ...patch}}}))
  const check = paperContrast(/^#[0-9a-f]{6}$/i.test(hex) ? hex : style.color)
  const customised = Boolean(spec.series[series.key] && Object.keys(spec.series[series.key]).length) || label !== series.defaultLabel
  return <div className="ks-props" role="group" aria-label={`Style of ${series.label}`}>
    <div className="ks-field">
      <label className="ks-label" htmlFor={`${id}-label`}>Label</label>
      <input id={`${id}-label`} type="text" value={label} onChange={event => onLabel(event.target.value === series.defaultLabel ? undefined : event.target.value)} />
    </div>
    {axis && onAxis && <Chips legend="Y axis" name={`${id}-axis`} value={axis} onChange={onAxis} choices={[{value: 'left', label: 'Left'}, {value: 'right', label: 'Right'}]} />}
    <fieldset className="ks-field">
      <legend className="ks-label">Colour</legend>
      <div className="ks-hex">
        <input type="color" aria-label="Pick colour" value={/^#[0-9a-f]{6}$/i.test(hex) ? hex : style.color} onChange={event => {setHex(event.target.value); set({color: event.target.value})}} />
        <label className="visually-hidden" htmlFor={`${id}-hex`}>Hex</label>
        <input id={`${id}-hex`} type="text" value={hex} maxLength={7} spellCheck={false} onChange={event => {setHex(event.target.value); if (/^#[0-9a-fA-F]{6}$/.test(event.target.value)) set({color: event.target.value.toLowerCase()})}} />
      </div>
      {!check.ok && <p className="ks-hint ks-warning" role="status">Hard to see on white paper ({check.ratio.toFixed(1)}:1, below 3:1).</p>}
    </fieldset>
    <Chips legend="Line" name={`${id}-line`} value={style.line} onChange={line => set({line})} className="ks-icons" choices={lineStyles.map(line => ({value: line, label: lineNames[line], icon: <LineIcon line={line} />}))} />
    <div className="ks-two">
      <Stepper label="Line width" unit="pt" value={style.lineWidth} min={.25} max={6} step={.25} onChange={lineWidth => set({lineWidth})} />
      <div className="ks-field">
        <label className="ks-label" htmlFor={`${id}-opacity`}>Opacity {Math.round(style.opacity * 100)} %</label>
        <input id={`${id}-opacity`} className="ks-range" type="range" min={10} max={100} step={5} value={Math.round(style.opacity * 100)} onChange={event => set({opacity: Number(event.target.value) / 100})} />
      </div>
    </div>
    {series.kind === 'scatter' && <>
      <Chips legend="Point" name={`${id}-marker`} value={style.marker} onChange={marker => set({marker})} className="ks-icons" choices={markerShapes.map(shape => ({value: shape, label: markerNames[shape], icon: <MarkerIcon shape={shape} />}))} />
      <div className="ks-two ks-align-end">
        <Stepper label="Point size" unit="pt" value={style.markerSize} min={.5} max={8} step={.25} onChange={markerSize => set({markerSize})} />
        <Toggle label="Hollow" checked={style.hollow} onChange={hollow => set({hollow})} hint="White-filled point with a coloured outline" />
      </div>
    </>}
    <div className="ks-actions">
      <button type="button" className="ks-button" disabled={first} onClick={() => onMove(-1)} title="Earlier in the legend; drawn below the others">Move up</button>
      <button type="button" className="ks-button" disabled={last} onClick={() => onMove(1)} title="Later in the legend; drawn on top">Move down</button>
      <button type="button" className="ks-button ks-push" disabled={!customised} onClick={() => {setSpec(current => {const {[series.key]: _, ...rest} = current.series; void _; return {...current, series: rest}}); onLabel(undefined)}}><ResetIcon />Reset</button>
    </div>
  </div>
}

// ---------------------------------------------------------------------------------------
// Axes: metrics bound to an axis (time series), X–Y metric pickers and the scale settings.

const withPrimary = (panel: PanelSpec, index: number, primary: string) => index === 0 && !panel.metrics.length ? [{metric: primary, axis: 'left' as const}] : panel.metrics

export function AxisMetrics({spec, setSpec, panel, side, metrics, provenance, primary, onPrimary, label = 'Metrics on this axis'}: {spec: FigureSpec; setSpec: SetSpec; panel: number; side: AxisSide; metrics: string[]; provenance: (metric: string) => ResolvedProvenance; primary: string; onPrimary: (metric: string) => void; label?: string}) {
  const id = useId()
  const current = spec.panels[panel] ?? defaultPanel()
  const bindings = withPrimary(current, panel, primary).map((binding, index) => ({...binding, index})).filter(binding => binding.axis === side)
  const title = (metric: string) => metricTitle(metric, provenance(metric))
  const update = (change: (list: PanelSpec['metrics']) => PanelSpec['metrics']) => setSpec(spec => ({...spec, panels: spec.panels.map((item, position) => position === panel ? {...item, metrics: change(withPrimary(item, position, primary))} : item)}))
  const unused = metrics.filter(metric => !withPrimary(current, panel, primary).some(binding => binding.metric === metric))
  return <div className="ks-field">
    <span className="ks-label" id={`${id}-label`}>{label}</span>
    <ul className="ks-list" aria-labelledby={`${id}-label`}>
      {bindings.map(binding => {
        const primaryRow = panel === 0 && binding.index === 0
        return <li key={`${binding.metric}-${binding.index}`} className="ks-input-row">
          <select aria-label={primaryRow ? 'Metric' : `Metric ${binding.index + 1}`} value={primaryRow ? primary : binding.metric} onChange={event => primaryRow ? onPrimary(event.target.value) : update(list => list.map((entry, position) => position === binding.index ? {...entry, metric: event.target.value} : entry))}>
            {metrics.map(metric => <option key={metric} value={metric}>{title(metric)}</option>)}
          </select>
          {!primaryRow && <button type="button" className="ks-icon-button" aria-label={`Remove ${title(binding.metric)}`} title="Remove from the figure" onClick={() => update(list => list.filter((_, position) => position !== binding.index))}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>}
        </li>
      })}
    </ul>
    {unused.length > 0 && <select aria-label={`Add a metric to the ${side} axis`} className="ks-add-select" value="" onChange={event => {const metric = event.target.value; if (metric) update(list => [...list, {metric, axis: side}])}}>
      <option value="">+ Add a metric…</option>
      {unused.map(metric => <option key={metric} value={metric}>{title(metric)}</option>)}
    </select>}
  </div>
}

export function XYAxisPicker({label, source, value, onChange, optional = false}: {label: string; source: XYSource; value: XYAxisSelection | undefined; onChange: (next: XYAxisSelection | undefined) => void; optional?: boolean}) {
  const services = value?.metric ? source.services(value.metric) : undefined
  return <>
    <SelectField label={label} value={value?.metric ?? ''} onChange={metric => onChange(metric ? {metric} : undefined)}>
      {optional && <option value="">None</option>}
      {source.metrics.map(metric => <option key={metric} value={metric}>{metricTitle(metric, source.provenance(metric))}</option>)}
    </SelectField>
    {value && services && services.length > 0 && <SelectField label="Service" value={value.service ?? ''} onChange={service => onChange({metric: value.metric, service: service || undefined})}>
      <option value="">All services (total)</option>
      {services.map(service => <option key={service} value={service}>{service}</option>)}
    </SelectField>}
  </>
}

export function AxisFields({axis, onChange, unit, time}: {axis: AxisSpec | XAxisSpec; onChange: (next: AxisSpec | XAxisSpec) => void; unit?: string; time?: boolean}) {
  const id = useId()
  const set = (patch: Partial<XAxisSpec>) => onChange({...axis, ...patch})
  const choices = unit ? unitChoices[unit] : undefined
  return <>
    <Chips legend="Range" name={`${id}-range`} value={axis.range} onChange={range => set({range})} choices={[{value: 'auto', label: 'Auto', hint: 'Fit the data'}, {value: 'zero', label: 'From 0', hint: 'Start at zero'}, {value: 'custom', label: 'Custom', hint: 'Set min and max'}]} />
    {axis.range === 'custom' && <div className="ks-two">
      <NumberField label="Min" value={axis.min} onChange={min => set({min})} />
      <NumberField label="Max" value={axis.max} onChange={max => set({max})} />
    </div>}
    <div className="ks-two">
      {time ? <SelectField label="Time as" value={(axis as XAxisSpec).time} onChange={value => set({time: value as TimeFormat})}><option value="min">Minutes</option><option value="s">Seconds</option><option value="mmss">mm:ss</option></SelectField>
        : choices && choices.length > 1 ? <SelectField label="Unit" value={axis.unit ?? choices[0].id} onChange={value => set({unit: value === choices[0].id ? undefined : value})}>{choices.map(choice => <option key={choice.id} value={choice.id}>{choice.label}</option>)}</SelectField> : null}
      {!time && <SelectField label="Decimals" value={axis.decimals === undefined ? '' : String(axis.decimals)} onChange={value => set({decimals: value === '' ? undefined : Number(value)})}><option value="">Auto</option>{[0, 1, 2, 3, 4].map(value => <option key={value} value={value}>{value}</option>)}</SelectField>}
    </div>
    <div className="ks-two">
      <SelectField label="Ticks" value={axis.ticks} onChange={value => set({ticks: value as AxisSpec['ticks']})}><option value="auto">Automatic</option><option value="count">Count</option><option value="step">Step</option></SelectField>
      {axis.ticks !== 'auto' && <NumberField label={axis.ticks === 'count' ? 'Number of ticks' : 'Tick step'} value={axis.tickValue} onChange={tickValue => set({tickValue})} />}
    </div>
    <div className="ks-toggles">
      <Toggle label="Log scale" checked={axis.log} onChange={log => set({log})} />
      {!time && <Toggle label="Thousands separator" checked={axis.thousands} onChange={thousands => set({thousands})} />}
    </div>
  </>
}

// X–Y: optional second Y metric on the right axis.
export function SecondY({spec, setSpec, source}: {spec: FigureSpec; setSpec: SetSpec; source: XYSource}) {
  const set = (y2: PanelSpec['y2']) => setSpec(current => ({...current, panels: current.panels.map((panel, index) => index ? panel : {...panel, y2})}))
  return <XYAxisPicker label="Right Y axis metric" source={source} value={spec.panels[0].y2} onChange={set} optional />
}

// ---------------------------------------------------------------------------------------
// Annotations.

let annotationCounter = 0
const newID = () => `a${Date.now().toString(36)}${(annotationCounter++).toString(36)}`
export const annotationNames: Record<Annotation['kind'], string> = {hline: 'Threshold line', vline: 'Vertical line', xrange: 'Shaded range', text: 'Text label'}
// What each kind is for, with an example, in the "Add a line in the graph" menu.
export const annotationExamples: Record<Annotation['kind'], string> = {hline: 'A limit across the chart, e.g. the 350 ms SLO', xrange: 'Highlights a period, e.g. a load spike from 5 to 8 min', vline: 'Marks a moment, e.g. when a pod was rescheduled', text: 'A short note written on the chart'}

// A new annotation placed in the middle of what is visible.
export function newAnnotation(kind: Annotation['kind'], at: {x: number; x2: number; y: number; axis?: AxisSide}): Annotation {
  const round = (value: number) => Number(value.toPrecision(3))
  if (kind === 'hline') return {id: newID(), kind, axis: at.axis ?? 'left', value: round(at.y), label: 'Threshold {value}'}
  if (kind === 'vline') return {id: newID(), kind, at: round(at.x), label: ''}
  if (kind === 'xrange') return {id: newID(), kind, from: round(at.x), to: round(at.x2), label: 'Event'}
  return {id: newID(), kind, axis: 'left', at: round(at.x), value: round(at.y), text: 'Note'}
}

export function AnnotationFields({item, onChange, onRemove, xUnit, yUnit}: {item: Annotation; onChange: (patch: Partial<Annotation>) => void; onRemove: () => void; xUnit: string; yUnit?: (side: AxisSide) => string}) {
  const id = useId()
  return <div className="ks-props" role="group" aria-label={annotationNames[item.kind]}>
    <div className="ks-field">
      <label className="ks-label" htmlFor={`${id}-text`}>{item.kind === 'text' ? 'Text' : 'Label'}</label>
      <input id={`${id}-text`} type="text" value={item.kind === 'text' ? item.text : item.label ?? ''} onChange={event => onChange(item.kind === 'text' ? {text: event.target.value} : {label: event.target.value || undefined})} />
    </div>
    {item.kind === 'hline' && <div className="ks-two">
      <NumberField label={`Value (${yUnit?.(item.axis) || 'axis units'})`} value={item.value} onChange={value => {if (value !== undefined) onChange({value})}} />
      <SelectField label="Axis" value={item.axis} onChange={axis => onChange({axis: axis as AxisSide})}><option value="left">Left</option><option value="right">Right</option></SelectField>
    </div>}
    {item.kind === 'vline' && <NumberField label={`At (${xUnit})`} value={item.at} onChange={at => {if (at !== undefined) onChange({at})}} />}
    {item.kind === 'xrange' && <div className="ks-two">
      <NumberField label={`From (${xUnit})`} value={item.from} onChange={from => {if (from !== undefined) onChange({from})}} />
      <NumberField label={`To (${xUnit})`} value={item.to} onChange={to => {if (to !== undefined) onChange({to})}} />
    </div>}
    {item.kind === 'text' && <div className="ks-two">
      <NumberField label={`X (${xUnit})`} value={item.at} onChange={at => {if (at !== undefined) onChange({at})}} />
      <NumberField label="Y (axis units)" value={item.value} onChange={value => {if (value !== undefined) onChange({value})}} />
    </div>}
    <p className="ks-hint">{item.kind === 'hline' ? '{value} in the label shows the value with its unit. Or drag the line on the figure.' : 'Or drag it on the figure.'}</p>
    <div className="ks-actions"><button type="button" className="ks-button ks-danger" onClick={onRemove}>Delete {annotationNames[item.kind].toLowerCase()}</button></div>
  </div>
}

// ---------------------------------------------------------------------------------------
// Palette, style presets and the spec as JSON.

// Each palette with its colours, in the order series take them.
export function PaletteEditor({spec, setSpec}: {spec: FigureSpec; setSpec: SetSpec}) {
  const id = useId()
  return <fieldset className="ks-field ks-palettes">
    <legend className="ks-label">Palette</legend>
    {(Object.keys(palettes) as PaletteId[]).map(key => <label key={key} className="ks-palette" data-checked={key === spec.palette || undefined}>
      <input type="radio" className="visually-hidden" name={`${id}-palette`} value={key} checked={key === spec.palette} onChange={() => setSpec(current => ({...current, palette: key}))} />
      <span className="ks-palette-name"><strong>{palettes[key].label}</strong><small>{palettes[key].hint}</small></span>
      <span className="ks-palette-colors" aria-hidden="true">{[...new Set(paletteColors(key))].slice(0, 6).map(color => <i key={color} style={{background: color}} />)}</span>
    </label>)}
  </fieldset>
}

export function PresetsEditor({presets, onApply, onSave, onDelete}: {presets: StylePreset[]; onApply: (preset: StylePreset) => void; onSave: (name: string) => void; onDelete: (name: string) => void}) {
  const id = useId()
  const all = [...builtInPresets, ...presets]
  const [selected, setSelected] = useState(all[0].name)
  const [name, setName] = useState('')
  const preset = all.find(item => item.name === selected) ?? all[0]
  return <>
    <div className="ks-field">
      <label className="ks-label" htmlFor={`${id}-preset`}>Style preset</label>
      <div className="ks-input-row">
        <select id={`${id}-preset`} value={preset.name} onChange={event => setSelected(event.target.value)}>
          <optgroup label="Built-in">{builtInPresets.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</optgroup>
          {presets.length > 0 && <optgroup label="Saved">{presets.map(item => <option key={item.name} value={item.name}>{item.name}</option>)}</optgroup>}
        </select>
        <button type="button" className="ks-button" onClick={() => onApply(preset)}>Apply</button>
        {!preset.builtIn && <button type="button" className="ks-icon-button" aria-label={`Delete preset ${preset.name}`} onClick={() => {onDelete(preset.name); setSelected(all[0].name)}}><svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" aria-hidden="true"><path d="M6 6l12 12M18 6 6 18" /></svg></button>}
      </div>
    </div>
    <form className="ks-field" onSubmit={event => {event.preventDefault(); if (name.trim()) {onSave(name.trim()); setSelected(name.trim()); setName('')}}}>
      <label className="ks-label" htmlFor={`${id}-name`}>Preset name</label>
      <div className="ks-input-row">
        <input id={`${id}-name`} type="text" placeholder="e.g. My talk" value={name} maxLength={60} onChange={event => setName(event.target.value)} />
        <button type="submit" className="ks-button" disabled={!name.trim()} aria-label="Save current style as preset">Save</button>
      </div>
    </form>
  </>
}

export function SpecActions({onCopy, onPaste, onReset}: {onCopy: () => void; onPaste: (json?: string) => void; onReset: () => void}) {
  const id = useId()
  const [manual, setManual] = useState(false)
  const [text, setText] = useState('')
  return <>
    <p className="ks-hint">The whole figure as JSON (data, styles, axes, annotations, layout). Paste it into another experiment to reuse it.</p>
    <div className="ks-actions">
      <button type="button" className="ks-button" onClick={onCopy}>Copy spec</button>
      <button type="button" className="ks-button" onClick={() => onPaste()}>Paste spec</button>
      <button type="button" className="ks-button" aria-expanded={manual} onClick={() => setManual(value => !value)}>Paste as text…</button>
      <button type="button" className="ks-button ks-danger" onClick={onReset}>Reset figure</button>
    </div>
    {manual && <div className="ks-field">
      <label className="ks-label" htmlFor={`${id}-json`}>Figure spec (JSON)</label>
      <textarea id={`${id}-json`} rows={5} value={text} spellCheck={false} onChange={event => setText(event.target.value)} />
      <div className="ks-actions"><button type="button" className="ks-button" disabled={!text.trim()} onClick={() => {onPaste(text); setManual(false); setText('')}}>Apply spec</button></div>
    </div>}
  </>
}
