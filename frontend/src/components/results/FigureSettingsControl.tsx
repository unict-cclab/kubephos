import {useId, useState, type ReactNode} from 'react'

// KubeStudio property controls: one visual system for every inspector field. Labels sit
// above their control, every control is 32 px tall, groups are separated by 16 px with a
// quiet 12 px heading. Choice groups are native radios (arrow keys move between them).

export interface Choice<T extends string> {value: T; label: string; hint?: string; icon?: ReactNode}

// Segmented control (radio group). With icons, the label becomes the accessible name and tooltip.
export function Chips<T extends string>({legend, name, value, choices, onChange, className = '', disabled = false, hideLegend = false}: {legend: string; name: string; value: T; choices: Array<Choice<T>>; onChange: (value: T) => void; className?: string; disabled?: boolean; hideLegend?: boolean}) {
  return <fieldset className={`ks-field ks-segmented ${className}`} disabled={disabled}>
    <legend className={hideLegend ? 'visually-hidden' : 'ks-label'}>{legend}</legend>
    <div className="ks-segments">
      {choices.map(choice => <label key={choice.value} className="ks-segment" data-checked={choice.value === value} title={choice.hint ?? (choice.icon ? choice.label : undefined)}>
        <input type="radio" className="visually-hidden" name={name} value={choice.value} checked={choice.value === value} onChange={() => onChange(choice.value)} aria-label={choice.icon ? choice.label : undefined} />
        {choice.icon ?? <span>{choice.label}</span>}
      </label>)}
    </div>
  </fieldset>
}

// A titled group of properties.
export function Group({title, children, action}: {title: string; children: ReactNode; action?: ReactNode}) {
  return <section className="ks-group" aria-label={title}>
    <div className="ks-group-head"><h3>{title}</h3>{action}</div>
    <div className="ks-group-body">{children}</div>
  </section>
}

// Collapsed-by-default group for rarely used settings.
export function MoreGroup({title, children}: {title: string; children: ReactNode}) {
  const id = useId()
  const [open, setOpen] = useState(false)
  return <section className="ks-group" aria-label={title}>
    <div className="ks-group-head"><h3><button type="button" className="ks-disclosure" aria-expanded={open} aria-controls={id} onClick={() => setOpen(!open)}>
      <svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M4 2.5 7.5 6 4 9.5" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" /></svg>{title}
    </button></h3></div>
    <div id={id} className="ks-group-body" hidden={!open}>{children}</div>
  </section>
}

export const ResetIcon = () => <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6" /></svg>

// Text field with a reset-to-automatic button.
export function LabelField({label, value, fallback, onChange}: {label: string; value: string | undefined; fallback: string; onChange: (value: string | undefined) => void}) {
  const id = useId()
  const edited = value !== undefined && value !== fallback
  return <div className="ks-field">
    <label className="ks-label" htmlFor={id}>{label}</label>
    <div className="ks-input-row">
      <input id={id} type="text" value={value ?? fallback} onChange={event => onChange(event.target.value === fallback ? undefined : event.target.value)} />
      <button type="button" className="ks-icon-button" aria-label={`Reset ${label.toLowerCase()}`} title="Back to the automatic text" disabled={!edited} onClick={() => onChange(undefined)}><ResetIcon /></button>
    </div>
  </div>
}

// Labelled select.
export function SelectField({label, value, onChange, children, hint}: {label: string; value: string; onChange: (value: string) => void; children: ReactNode; hint?: string}) {
  const id = useId()
  return <div className="ks-field">
    <label className="ks-label" htmlFor={id}>{label}</label>
    <select id={id} value={value} onChange={event => onChange(event.target.value)}>{children}</select>
    {hint && <p className="ks-hint">{hint}</p>}
  </div>
}

// Switch-style checkbox row.
export function Toggle({label, checked, onChange, disabled, hint}: {label: string; checked: boolean; onChange: (checked: boolean) => void; disabled?: boolean; hint?: string}) {
  return <label className="ks-toggle" title={hint}>
    <input type="checkbox" checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} />
    <span>{label}</span>
  </label>
}

const snap = (value: number, step: number, direction: 1 | -1) => {
  const index = value / step
  const next = direction > 0 ? Math.floor(index + 1e-9) + 1 : Math.ceil(index - 1e-9) - 1
  return Math.round(next * step * 1000) / 1000
}
export const shortNumber = (value: number) => String(Number(value.toFixed(2)))

// Numeric input with − / + buttons that snap to the step grid; shows at most 2 decimals.
export function Stepper({label, value, min, max, step, unit, onChange}: {label: string; value: number; min: number; max: number; step: number; unit?: string; onChange: (value: number) => void}) {
  const id = useId()
  const [text, setText] = useState<string>()
  const clamp = (next: number) => Math.min(max, Math.max(min, next))
  const fullLabel = unit ? `${label} (${unit})` : label
  return <div className="ks-field">
    <label className="ks-label" htmlFor={id}>{fullLabel}</label>
    <div className="ks-stepper">
      <button type="button" aria-label={`Decrease ${label.toLowerCase()}`} disabled={value <= min} onClick={() => {setText(undefined); onChange(clamp(snap(value, step, -1)))}}>−</button>
      <input id={id} type="number" inputMode="decimal" min={min} max={max} step={step} value={text ?? shortNumber(value)} onBlur={() => setText(undefined)}
        onChange={event => {setText(event.target.value); const next = Number(event.target.value); if (event.target.value.trim() !== '' && Number.isFinite(next) && next >= min && next <= max) onChange(next)}} />
      <button type="button" aria-label={`Increase ${label.toLowerCase()}`} disabled={value >= max} onClick={() => {setText(undefined); onChange(clamp(snap(value, step, 1)))}}>+</button>
    </div>
  </div>
}
