import {useEffect, useId, useMemo, useState, type Dispatch, type SetStateAction} from 'react'
import {exportChart, niceAxis} from './ResultsView'
import {NumericInput} from './NumericInput'

export type LoadProfileStep = {
  type: 'constant' | 'sinusoidal' | 'exponential'
  durationSeconds: number
  rps: number
  baselineRps: number
  amplitudeRps: number
  periodSeconds: number
  phaseSeconds: number
  startRps: number
  endRps: number
  curve: number
}

type LoadStep = LoadProfileStep & {
  id: number
}

type GeographicStep = {
  id: number
  type: 'constant' | 'linear'
  durationSeconds: number
  weights: Record<string, number>
  startWeights: Record<string, number>
  endWeights: Record<string, number>
}

export function LoadProfileEditor({prefix, zones, values = {}}: {prefix: string; zones: string[]; values?: Record<string, unknown>}) {
  const initialSteps = loadProfileSteps(values.steps)
  const initialGeography = geographicProfileSteps(values.geographicSteps, zones)
  const [sequence, setSequence] = useState(Math.max(3, initialSteps.length + initialGeography.length + 1))
  const [steps, setSteps] = useState<LoadStep[]>(initialSteps.length ? initialSteps.map((step, index) => ({...step, id: index + 1})) : [newLoadStep(1)])
  const initialDuration = (initialSteps.length ? initialSteps : [newLoadStep(1)]).reduce((total, step) => total + step.durationSeconds, 0)
  const [geographicSteps, setGeographicSteps] = useState<GeographicStep[]>(initialGeography.length ? initialGeography.map((step, index) => ({...step, id: initialSteps.length + index + 1})) : [newGeographicStep(2, zones, initialDuration)])
  const duration = useMemo(() => steps.reduce((total, step) => total + step.durationSeconds, 0), [steps])
  const geographicDuration = useMemo(() => geographicSteps.reduce((total, step) => total + step.durationSeconds, 0), [geographicSteps])
  useEffect(() => {
    setGeographicSteps(current => current.length === 1 ? [{...current[0], durationSeconds: duration, weights: completeWeights(current[0].weights, zones), startWeights: completeWeights(current[0].startWeights, zones), endWeights: completeWeights(current[0].endWeights, zones)}] : current.map(step => ({...step, weights: completeWeights(step.weights, zones), startWeights: completeWeights(step.startWeights, zones), endWeights: completeWeights(step.endWeights, zones)})))
  }, [duration, zones.join('|')])
  const addLoadStep = () => {
    setSteps(current => [...current, newLoadStep(sequence)])
    setSequence(value => value + 1)
  }
  const addGeographicStep = () => {
    setGeographicSteps(current => [...current, newGeographicStep(sequence, zones, Math.max(60, duration - geographicDuration))])
    setSequence(value => value + 1)
  }
  const serializedSteps = steps.map(({id: _, ...step}) => step)
  const serializedGeography = zones.length ? geographicSteps.map(({id: _, ...step}) => step.type === 'constant' ? {type: step.type, durationSeconds: step.durationSeconds, weights: step.weights} : {type: step.type, durationSeconds: step.durationSeconds, startWeights: step.startWeights, endWeights: step.endWeights}) : []
  return <div className="load-profile-editor">
    <input type="hidden" name={`${prefix}.steps`} value={JSON.stringify(serializedSteps)} readOnly />
    <input type="hidden" name={`${prefix}.geographicSteps`} value={JSON.stringify(serializedGeography)} readOnly />
    <div className="timeline-heading"><div><strong>Workload timeline</strong><p>Each phase starts after the previous one. Total duration is calculated automatically.</p></div><span className="duration-badge">{formatDuration(duration)}</span></div>
    <LoadProfilePreview steps={serializedSteps} />
    <div className="timeline-list">{steps.map((step, index) => <article className="timeline-card" key={step.id}>
      <div className="timeline-card-head"><span className="timeline-index">{index + 1}</span><select aria-label={`Workload phase ${index + 1} type`} value={step.type} onChange={event => updateLoadStep(setSteps, step.id, {type: event.target.value as LoadStep['type']})}><option value="constant">Constant</option><option value="sinusoidal">Sinusoidal</option><option value="exponential">Exponential</option></select><TimelineActions index={index} count={steps.length} move={direction => setSteps(items => move(items, index, direction))} remove={() => setSteps(items => items.filter(item => item.id !== step.id))} /></div>
      <div className="timeline-fields"><Minutes label="Duration" value={step.durationSeconds} set={value => updateLoadStep(setSteps, step.id, {durationSeconds: value})} />
        {step.type === 'constant' && <NumberField label="Requests / second" value={step.rps} min={0} set={value => updateLoadStep(setSteps, step.id, {rps: value})} />}
        {step.type === 'sinusoidal' && <><NumberField label="Baseline RPS" value={step.baselineRps} min={0} set={value => updateLoadStep(setSteps, step.id, {baselineRps: value})} /><NumberField label="Amplitude RPS" value={step.amplitudeRps} min={0} set={value => updateLoadStep(setSteps, step.id, {amplitudeRps: value})} /><Minutes label="Period" value={step.periodSeconds} set={value => updateLoadStep(setSteps, step.id, {periodSeconds: value})} /><Minutes label="Phase offset" value={step.phaseSeconds} allowZero set={value => updateLoadStep(setSteps, step.id, {phaseSeconds: value})} /></>}
        {step.type === 'exponential' && <><NumberField label="Start RPS" value={step.startRps} min={0} set={value => updateLoadStep(setSteps, step.id, {startRps: value})} /><NumberField label="End RPS" value={step.endRps} min={0} set={value => updateLoadStep(setSteps, step.id, {endRps: value})} /><NumberField label="Curve" value={step.curve} min={-20} max={20} step={0.1} set={value => updateLoadStep(setSteps, step.id, {curve: value})} /></>}
      </div>
    </article>)}</div>
    <button type="button" className="button secondary compact" onClick={addLoadStep}>Add workload phase</button>
    {zones.length > 0 && <><div className="timeline-heading geographic-heading"><div><strong>Geographic timeline</strong><p>Keep traffic fixed in the zones or move it progressively between them.</p></div><span className={`duration-badge ${duration === geographicDuration ? '' : 'mismatch'}`}>{formatDuration(geographicDuration)} / {formatDuration(duration)}</span></div>
      <div className="timeline-list">{geographicSteps.map((step, index) => <article className="timeline-card geographic-card" key={step.id}>
        <div className="timeline-card-head"><span className="timeline-index">{index + 1}</span><select aria-label={`Geographic phase ${index + 1} type`} value={step.type} onChange={event => updateGeographicStep(setGeographicSteps, step.id, {type: event.target.value as GeographicStep['type']})}><option value="constant">Fixed distribution</option><option value="linear">Linear transition</option></select><TimelineActions index={index} count={geographicSteps.length} move={direction => setGeographicSteps(items => move(items, index, direction))} remove={() => setGeographicSteps(items => items.filter(item => item.id !== step.id))} /></div>
        <div className="timeline-fields"><Minutes label="Duration" value={step.durationSeconds} set={value => updateGeographicStep(setGeographicSteps, step.id, {durationSeconds: value})} /></div>
        {step.type === 'constant' ? <WeightGrid title="Traffic weight" zones={zones} values={step.weights} set={(zone, value) => updateGeographicStep(setGeographicSteps, step.id, {weights: {...step.weights, [zone]: value}})} /> : <div className="weight-transition"><WeightGrid title="Start weights" zones={zones} values={step.startWeights} set={(zone, value) => updateGeographicStep(setGeographicSteps, step.id, {startWeights: {...step.startWeights, [zone]: value}})} /><span>→</span><WeightGrid title="End weights" zones={zones} values={step.endWeights} set={(zone, value) => updateGeographicStep(setGeographicSteps, step.id, {endWeights: {...step.endWeights, [zone]: value}})} /></div>}
      </article>)}</div>
      <button type="button" className="button secondary compact" onClick={addGeographicStep}>Add geographic phase</button>
      {duration !== geographicDuration && <p className="timeline-error">Geographic phases must cover exactly {formatDuration(duration)} before this configuration can be saved.</p>}
    </>}
  </div>
}

export function LoadProfilePreview({steps, progressSeconds, progressLabel}: {steps: LoadProfileStep[]; progressSeconds?: number; progressLabel?: string}) {
  const svgID = `input-load-${useId()}`
  const [exportError, setExportError] = useState('')
  const width = 700
  const height = 525
  const plot = {left: 112, right: 28, top: 28, bottom: 82}
  const duration = steps.reduce((total, step) => total + validNumber(step.durationSeconds), 0)
  const samples = loadSamples(steps)
  const maximum = Math.max(1, ...samples.map(point => point.rps))
  const xAxis = niceAxis(duration / 60)
  const yAxis = niceAxis(maximum)
  const x = (seconds: number) => plot.left + (seconds / 60 / xAxis.upper) * (width - plot.left - plot.right)
  const y = (rps: number) => height - plot.bottom - (rps / yAxis.upper) * (height - plot.bottom - plot.top)
  const points = samples.map((point, index) => `${index ? 'L' : 'M'}${x(point.seconds).toFixed(2)} ${y(point.rps).toFixed(2)}`).join(' ')
  const progress = progressSeconds === undefined ? undefined : Math.min(duration, Math.max(0, progressSeconds))
  const progressRps = progress === undefined ? 0 : rpsAt(steps, progress)
  let elapsed = 0
  const boundaries = steps.slice(0, -1).map(step => {
    elapsed += validNumber(step.durationSeconds)
    return elapsed
  })
  const output = (format: 'pdf' | 'png') => exportChart(svgID, 'input-load-profile', format).then(() => setExportError('')).catch(cause => setExportError(cause instanceof Error ? cause.message : 'Could not export input load profile.'))
  return <figure className="metric-card input-load-card" aria-label="Input load preview">
    <div className="metric-card-header"><div><h3>Input load profile</h3><span>{steps.length} phases · {formatDuration(duration)}</span></div><div className="chart-export-actions"><button type="button" onClick={() => void output('pdf')}>PDF</button><button type="button" onClick={() => void output('png')}>PNG 600 DPI</button></div></div>
    {exportError && <p className="form-error">{exportError}</p>}
    <div className="chart-wrap"><svg id={svgID} viewBox={`0 0 ${width} ${height}`} xmlns="http://www.w3.org/2000/svg" role="img" aria-label={`Load profile lasting ${formatDuration(duration)} with a peak of ${formatRps(maximum)} requests per second`}>
      <style>{`text{fill:#111;font-family:Arial,Helvetica,"DejaVu Sans",sans-serif}.tick{font-size:17px}.axis-label{font-size:20px}.legend-label{font-size:15px}.axis{stroke:#111;stroke-width:2;fill:none}.tick-line{stroke:#111;stroke-width:1.5}`}</style>
      <rect x={plot.left} y={plot.top} width={width - plot.left - plot.right} height={height - plot.top - plot.bottom} className="axis" />
      {xAxis.ticks.map(tick => <g key={`x-${tick}`}><line x1={x(tick * 60)} y1={height - plot.bottom} x2={x(tick * 60)} y2={height - plot.bottom + 7} className="tick-line" /><text x={x(tick * 60)} y={height - plot.bottom + 28} textAnchor="middle" className="tick">{tick.toLocaleString('en-US', {maximumFractionDigits: 2})}</text></g>)}
      {yAxis.ticks.map(tick => <g key={`y-${tick}`}><line x1={plot.left - 7} y1={y(tick)} x2={plot.left} y2={y(tick)} className="tick-line" /><text x={plot.left - 13} y={y(tick) + 6} textAnchor="end" className="tick">{tick.toLocaleString('en-US', {maximumFractionDigits: 2})}</text></g>)}
      <text x={(plot.left + width - plot.right) / 2} y={height - plot.bottom + 58} textAnchor="middle" className="axis-label">Time (min)</text>
      <text x="27" y={(plot.top + height - plot.bottom) / 2} textAnchor="middle" className="axis-label" transform={`rotate(-90 27 ${(plot.top + height - plot.bottom) / 2})`}>Requests / second</text>
      {boundaries.map((seconds, index) => <g key={`phase-${seconds}`}><line x1={x(seconds)} x2={x(seconds)} y1={plot.top} y2={height - plot.bottom} stroke="#777" strokeWidth="1.5" strokeDasharray="5 5" /><text x={x(seconds) + 5} y={plot.top + 18} className="legend-label">{index + 2}</text></g>)}
      {points && <path d={points} fill="none" stroke="#1f77b4" strokeWidth="2.5" strokeLinejoin="round" />}
      {samples.filter((_, index) => index % Math.max(1, Math.floor(samples.length / 12)) === 0).map((point, index) => <circle key={index} cx={x(point.seconds)} cy={y(point.rps)} r="4" fill="#1f77b4" />)}
      {progress !== undefined && <g className="load-progress-marker"><line x1={x(progress)} x2={x(progress)} y1={plot.top} y2={height - plot.bottom} stroke="#d55e00" strokeWidth="2" strokeDasharray="6 5" /><circle cx={x(progress)} cy={y(progressRps)} r="8" fill="#d55e00" /></g>}
    </svg></div>
    <figcaption className="chart-legend"><div><span style={{background: '#1f77b4'}} /><strong>{progressLabel ?? 'Configured input'}</strong><small>{progress === undefined ? `peak ${formatRps(maximum)} RPS · ${formatDuration(duration)}` : `${formatDuration(progress)} of ${formatDuration(duration)} · ${formatRps(progressRps)} RPS`}</small></div></figcaption>
  </figure>
}

export function loadProfileSteps(value: unknown): LoadProfileStep[] {
  if (!Array.isArray(value)) return []
  return value.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const step = item as Record<string, unknown>
    const type = step.type
    const durationSeconds = validNumber(step.durationSeconds)
    if (!['constant', 'sinusoidal', 'exponential'].includes(String(type)) || durationSeconds <= 0) return []
    return [{
      type: type as LoadProfileStep['type'], durationSeconds,
      rps: validNumber(step.rps), baselineRps: validNumber(step.baselineRps), amplitudeRps: validNumber(step.amplitudeRps),
      periodSeconds: validNumber(step.periodSeconds), phaseSeconds: validNumber(step.phaseSeconds), startRps: validNumber(step.startRps),
      endRps: validNumber(step.endRps), curve: validNumber(step.curve)
    }]
  })
}

function geographicProfileSteps(value: unknown, zones: string[]): Omit<GeographicStep, 'id'>[] {
  if (!Array.isArray(value)) return []
  return value.flatMap(item => {
    if (!item || typeof item !== 'object') return []
    const step = item as Record<string, unknown>
    const type = step.type
    const durationSeconds = validNumber(step.durationSeconds)
    if (!['constant', 'linear'].includes(String(type)) || durationSeconds <= 0) return []
    const weights = completeWeights(numericRecord(step.weights), zones)
    const startWeights = completeWeights(numericRecord(step.startWeights), zones)
    const endWeights = completeWeights(numericRecord(step.endWeights), zones)
    return [{type: type as GeographicStep['type'], durationSeconds, weights, startWeights, endWeights}]
  })
}

function numericRecord(value: unknown): Record<string, number> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).flatMap(([key, item]) => Number.isFinite(Number(item)) ? [[key, Number(item)]] : []))
}

function loadSamples(steps: LoadProfileStep[]): Array<{seconds: number; rps: number}> {
  const result: Array<{seconds: number; rps: number}> = []
  let elapsed = 0
  steps.forEach(step => {
    const count = Math.max(2, Math.min(100, Math.ceil(step.durationSeconds / 10)))
    for (let index = 0; index <= count; index += 1) {
      const local = step.durationSeconds * index / count
      result.push({seconds: elapsed + local, rps: Math.max(0, stepRps(step, local))})
    }
    elapsed += step.durationSeconds
  })
  return result
}

function rpsAt(steps: LoadProfileStep[], seconds: number): number {
  let elapsed = 0
  for (const step of steps) {
    if (seconds <= elapsed + step.durationSeconds) return Math.max(0, stepRps(step, seconds - elapsed))
    elapsed += step.durationSeconds
  }
  return 0
}

function stepRps(step: LoadProfileStep, localSeconds: number): number {
  if (step.type === 'constant') return step.rps
  if (step.type === 'sinusoidal') return step.baselineRps + step.amplitudeRps * Math.sin(2 * Math.PI * (localSeconds + step.phaseSeconds) / Math.max(1, step.periodSeconds))
  const progress = Math.min(1, localSeconds / Math.max(1, step.durationSeconds))
  if (Math.abs(step.curve) < .0001) return step.startRps + (step.endRps - step.startRps) * progress
  const curved = (Math.exp(step.curve * progress) - 1) / (Math.exp(step.curve) - 1)
  return step.startRps + (step.endRps - step.startRps) * curved
}

function validNumber(value: unknown): number {
  const number = Number(value)
  return Number.isFinite(number) ? number : 0
}

function formatRps(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k`
  return value >= 10 ? value.toFixed(0) : value.toFixed(1)
}

function TimelineActions({index, count, move: moveItem, remove}: {index: number; count: number; move: (direction: -1 | 1) => void; remove: () => void}) {
  return <div className="timeline-actions"><button type="button" className="icon-button" aria-label="Move up" disabled={index === 0} onClick={() => moveItem(-1)}>↑</button><button type="button" className="icon-button" aria-label="Move down" disabled={index === count - 1} onClick={() => moveItem(1)}>↓</button><button type="button" className="icon-button" aria-label="Remove phase" disabled={count === 1} onClick={remove}>×</button></div>
}

function NumberField({label, value, set, min, max = 100000, step = 1}: {label: string; value: number; set: (value: number) => void; min: number; max?: number; step?: number}) {
  return <label>{label}<NumericInput value={value} min={min} max={max} step={step} required onValueChange={set} /></label>
}

function Minutes({label, value, set, allowZero = false}: {label: string; value: number; set: (value: number) => void; allowZero?: boolean}) {
  return <label>{label} (minutes)<NumericInput value={value / 60} min={allowZero ? 0 : 0.1} max={1440} step={0.1} required onValueChange={next => set(Math.round(next * 60))} /></label>
}

function WeightGrid({title, zones, values, set}: {title: string; zones: string[]; values: Record<string, number>; set: (zone: string, value: number) => void}) {
  return <fieldset className="weight-grid"><legend>{title}</legend><div>{zones.map(zone => <label key={zone}>{zone}<NumericInput min={0} max={1000} step={0.1} value={values[zone] ?? 0} required onValueChange={value => set(zone, value)} /></label>)}</div></fieldset>
}

function newLoadStep(id: number): LoadStep {
  return {id, type: 'constant', durationSeconds: 900, rps: 30, baselineRps: 100, amplitudeRps: 50, periodSeconds: 300, phaseSeconds: 0, startRps: 30, endRps: 300, curve: 3}
}

function newGeographicStep(id: number, zones: string[], durationSeconds: number): GeographicStep {
  const weights = Object.fromEntries(zones.map(zone => [zone, 1]))
  return {id, type: 'constant', durationSeconds, weights, startWeights: weights, endWeights: weights}
}

function completeWeights(values: Record<string, number>, zones: string[]): Record<string, number> {
  return Object.fromEntries(zones.map(zone => [zone, values[zone] ?? 1]))
}

function updateLoadStep(set: Dispatch<SetStateAction<LoadStep[]>>, id: number, values: Partial<LoadStep>) {
  set(items => items.map(item => item.id === id ? {...item, ...values} : item))
}

function updateGeographicStep(set: Dispatch<SetStateAction<GeographicStep[]>>, id: number, values: Partial<GeographicStep>) {
  set(items => items.map(item => item.id === id ? {...item, ...values} : item))
}

function move<T>(items: T[], index: number, direction: -1 | 1): T[] {
  const target = index + direction
  if (target < 0 || target >= items.length) return items
  const next = [...items]
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

function formatDuration(seconds: number): string {
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const rest = seconds % 60
  return [hours ? `${hours}h` : '', minutes ? `${minutes}m` : '', rest ? `${rest}s` : ''].filter(Boolean).join(' ') || '0s'
}
