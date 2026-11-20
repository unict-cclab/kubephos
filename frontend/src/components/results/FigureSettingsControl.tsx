import {useId, type ReactNode} from 'react'
import type {FigureLabelOverrides} from './chartKit'
import {customLimits, defaultFigureSettings, figureSizes, type FigureLegend, type FigureSettings, type FigureSize, type FigureUncertainty} from './figureSettings'

// Style panel of the Figure studio: page size, type, uncertainty, axes, legend, colour and
// labels. Every group is visible (nothing collapsed); chips are native radio groups, so arrow
// keys move between them.

interface Choice<T extends string> {value: T; label: string; hint?: string; icon?: ReactNode}

export function Chips<T extends string>({legend, name, value, choices, onChange, className = '', disabled = false, hideLegend = false}: {legend: string; name: string; value: T; choices: Array<Choice<T>>; onChange: (value: T) => void; className?: string; disabled?: boolean; hideLegend?: boolean}) {
  return <fieldset className={`studio-chips ${className}`} disabled={disabled}>
    <legend className={hideLegend ? 'visually-hidden' : 'studio-field-label'}>{legend}</legend>
    <div className="studio-chip-row">
      {choices.map(choice => <label key={choice.value} className="studio-chip" data-checked={choice.value === value}>
        <input type="radio" className="visually-hidden" name={name} value={choice.value} checked={choice.value === value} onChange={() => onChange(choice.value)} />
        {choice.icon}
        <span className="studio-chip-text"><span>{choice.label}</span>{choice.hint && <small>{choice.hint}</small>}</span>
      </label>)}
    </div>
  </fieldset>
}

export function Section({title, children, action}: {title: string; children: ReactNode; action?: ReactNode}) {
  return <section className="studio-section" aria-label={title}>
    <div className="studio-section-head"><h3>{title}</h3>{action}</div>
    {children}
  </section>
}

// Page outline drawn to scale (width and plot height) inside a 28×20 box.
function PageIcon({width, height}: {width: number; height: number}) {
  const scale = Math.min(26 / width, 18 / height)
  const w = Math.max(6, width * scale)
  const h = Math.max(4, height * scale)
  return <svg className="studio-chip-icon" width="28" height="20" viewBox="0 0 28 20" aria-hidden="true" focusable="false"><rect x={(28 - w) / 2} y={(20 - h) / 2} width={w} height={h} rx="1.5" fill="none" stroke="currentColor" strokeWidth="1.4" /></svg>
}

const sizeChoices: Array<Choice<FigureSize>> = [
  {value: 'single', label: 'Single column', hint: '3.5 in · IEEE/ACM'},
  {value: 'onehalf', label: '1.5 column', hint: '5.5 in'},
  {value: 'double', label: 'Double column', hint: '7.16 in · IEEE/ACM'},
  {value: 'slide', label: 'Slide 16:9', hint: '10 × 5.63 in'},
  {value: 'custom', label: 'Custom', hint: 'W × H'}
].map(choice => ({...choice, value: choice.value as FigureSize, icon: <PageIcon width={choice.value === 'custom' ? 300 : figureSizes[choice.value as FigureSize].width} height={choice.value === 'custom' ? 220 : figureSizes[choice.value as FigureSize].height} />}))

const legendChoices: Array<Choice<FigureLegend>> = [
  {value: 'top', label: 'Top'}, {value: 'right', label: 'Right'}, {value: 'bottom', label: 'Bottom'}, {value: 'inside', label: 'Inside'}, {value: 'none', label: 'None'}
]

export interface StyleLabels {title: string; x?: string; y?: string; series: string[]}

function LabelField({label, value, fallback, onChange}: {label: string; value: string | undefined; fallback: string; onChange: (value: string | undefined) => void}) {
  const id = useId()
  const edited = value !== undefined && value !== fallback
  return <div className="studio-text-field">
    <label htmlFor={id}>{label}</label>
    <div className="studio-input-row">
      <input id={id} type="text" value={value ?? fallback} onChange={event => onChange(event.target.value === fallback ? undefined : event.target.value)} />
      <button type="button" className="icon-button studio-reset" aria-label={`Reset ${label.toLowerCase()}`} title="Reset to the default" disabled={!edited} onClick={() => onChange(undefined)}>
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6" /></svg>
      </button>
    </div>
  </div>
}

export function FigureStylePanel({settings, onChange, labels, overrides, onOverrides, logX, onLogX, uncertainty = true, publication = true, spec = false}: {
  settings: FigureSettings
  onChange: (next: FigureSettings) => void
  labels?: StyleLabels
  overrides: FigureLabelOverrides
  onOverrides: (next: FigureLabelOverrides) => void
  // X–Y figures: their own log axes replace the global log y setting.
  logX?: {x: boolean; y: boolean}
  onLogX?: (next: {x: boolean; y: boolean}) => void
  uncertainty?: boolean
  publication?: boolean
  // Spec-driven figures (time series, X–Y) edit runs, axes and colour in their own sections.
  spec?: boolean
}) {
  const id = useId()
  const set = <K extends keyof FigureSettings>(key: K, value: FigureSettings[K]) => onChange({...settings, [key]: value})
  const inches = (key: 'customWidth' | 'customHeight', text: string) => {const value = Number(text); if (Number.isFinite(value) && value >= customLimits.min && value <= customLimits.max) set(key, value)}
  const series = (label: string, value: string | undefined) => {
    const next = {...overrides.series}
    if (value === undefined) delete next[label]
    else next[label] = value
    onOverrides({...overrides, series: next})
  }
  if (!publication) return <div className="studio-style"><p className="studio-hint">This figure keeps its on-screen layout. Size, type and label settings apply to publication figures (time series, X–Y, run-level, percentiles, services, load fidelity).</p></div>
  return <div className="studio-style">
    <Section title="Size">
      <Chips legend="Page size" name={`${id}-size`} value={settings.size} choices={sizeChoices} onChange={value => set('size', value)} className="studio-size-chips" hideLegend />
      {settings.size === 'custom' && <div className="studio-two">
        <label>Width (in)<input type="number" min={customLimits.min} max={customLimits.max} step="0.05" defaultValue={settings.customWidth} onChange={event => inches('customWidth', event.target.value)} /></label>
        <label>Plot height (in)<input type="number" min={customLimits.min} max={customLimits.max} step="0.05" defaultValue={settings.customHeight} onChange={event => inches('customHeight', event.target.value)} /></label>
      </div>}
    </Section>
    <Section title="Type">
      <Chips legend="Font family" name={`${id}-font`} value={settings.font} choices={[{value: 'serif', label: 'Serif', hint: 'Times'}, {value: 'sans', label: 'Sans', hint: 'Helvetica'}]} onChange={value => set('font', value)} className="studio-segmented" />
      <label className="studio-inline-field">Font size<select value={settings.fontSize} onChange={event => set('fontSize', Number(event.target.value))}>{[7, 8, 9, 10, 11, 12].map(size => <option key={size} value={size}>{size} pt</option>)}</select></label>
    </Section>
    {!spec && <Section title="Uncertainty & axes">
      <Chips legend="Uncertainty" name={`${id}-uncertainty`} value={settings.uncertainty} choices={[{value: 'none', label: 'None'}, {value: 'sd', label: '±1 SD'}, {value: 'ci', label: '95 % CI'}] as Array<Choice<FigureUncertainty>>} onChange={value => set('uncertainty', value)} className="studio-segmented" disabled={!uncertainty} />
      <div className="studio-checks" role="group" aria-label="Axes">
        <label className="checkbox-row"><input type="checkbox" checked={settings.zeroBased} disabled={Boolean(logX)} onChange={event => set('zeroBased', event.target.checked)} />Y axis from zero</label>
        {logX && onLogX && <label className="checkbox-row"><input type="checkbox" checked={logX.x} onChange={event => onLogX({...logX, x: event.target.checked})} />Log X axis</label>}
        <label className="checkbox-row"><input type="checkbox" checked={logX ? logX.y : settings.logY} onChange={event => logX && onLogX ? onLogX({...logX, y: event.target.checked}) : set('logY', event.target.checked)} />Log Y axis{logX ? '' : ' (latency)'}</label>
      </div>
    </Section>}
    <Section title={spec ? 'Legend' : 'Legend & colour'}>
      <Chips legend="Legend position" name={`${id}-legend`} value={settings.legend} choices={legendChoices} onChange={value => set('legend', value)} className="studio-segmented" />
      {!spec && <label className="checkbox-row"><input type="checkbox" checked={settings.grayscale} onChange={event => set('grayscale', event.target.checked)} />Grayscale-safe (Okabe–Ito, dashes, markers)</label>}
    </Section>
    {labels && <Section title="Labels">
      <label className="checkbox-row"><input type="checkbox" checked={settings.showTitle} onChange={event => set('showTitle', event.target.checked)} />Show title in figure</label>
      <LabelField label="Title" value={overrides.title} fallback={labels.title} onChange={value => onOverrides({...overrides, title: value})} />
      {labels.x !== undefined && <LabelField label="X axis label" value={overrides.x} fallback={labels.x} onChange={value => onOverrides({...overrides, x: value})} />}
      {labels.y !== undefined && <LabelField label="Y axis label" value={overrides.y} fallback={labels.y} onChange={value => onOverrides({...overrides, y: value})} />}
      {labels.series.map((label, index) => <LabelField key={`${label}-${index}`} label={`Series ${index + 1} label`} value={overrides.series?.[label]} fallback={label} onChange={value => series(label, value)} />)}
    </Section>}
    <button type="button" className="button ghost compact studio-reset-all" onClick={() => onChange({...defaultFigureSettings, customWidth: settings.customWidth, customHeight: settings.customHeight})}>Reset style to defaults</button>
  </div>
}
