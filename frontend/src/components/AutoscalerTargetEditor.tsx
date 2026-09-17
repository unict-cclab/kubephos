import {useEffect, useMemo, useState, type Dispatch, type SetStateAction} from 'react'
import type {ApplicationComponent} from '../types'
import {NumericInput} from './NumericInput'

type Settings = {intervalSeconds: number; minReplicas: number; maxReplicas: number; parameters: string}

export function AutoscalerTargetEditor({prefix, components, defaults, resetKey, excluded = [], targetOverrides = {}}: {prefix: string; components: ApplicationComponent[]; defaults: Record<string, unknown>; resetKey: string; excluded?: string[]; targetOverrides?: Record<string, unknown>}) {
  const targets = useMemo(() => components.filter(component => component.traits?.includes('scalable')), [components])
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [custom, setCustom] = useState<Record<string, boolean>>({})
  const [overrides, setOverrides] = useState<Record<string, Settings>>({})
  useEffect(() => {
    const initial = defaultSettings(defaults)
    setSelected(Object.fromEntries(targets.map(target => [target.id, !excluded.includes(target.id)])))
    setCustom(Object.fromEntries(targets.map(target => [target.id, Boolean(targetOverrides[target.id])])))
    setOverrides(Object.fromEntries(targets.map(target => [target.id, {...initial, ...settings(targetOverrides[target.id])}])))
  }, [resetKey, targets.map(target => target.id).join('|')])
  const excludedTargets = targets.filter(target => selected[target.id] === false).map(target => target.id)
  const configured = Object.fromEntries(targets.filter(target => selected[target.id] !== false && custom[target.id]).map(target => [target.id, overrides[target.id] ?? defaultSettings(defaults)]))
  return <div className="autoscaler-target-editor">
    <input type="hidden" name={`exclude-${prefix.replace('component-', '')}`} value={excludedTargets.join(',')} readOnly />
    <input type="hidden" name={`${prefix}.targetOverrides`} value={JSON.stringify(configured)} readOnly />
    <div className="target-editor-heading"><div><strong>Scalable microservices</strong><p>Defaults apply to every selected service. Enable an override only where values differ.</p></div><span>{targets.filter(target => selected[target.id] !== false).length} / {targets.length}</span></div>
    <div className="target-editor-list">{targets.map(target => {
      const values = overrides[target.id] ?? defaultSettings(defaults)
      const enabled = selected[target.id] !== false
      return <article className={enabled ? 'enabled' : ''} key={target.id}>
        <div className="target-editor-row"><label className="checkbox-field"><input type="checkbox" checked={enabled} onChange={event => setSelected(current => ({...current, [target.id]: event.target.checked}))} /><span>{target.id}</span></label><label className="checkbox-field target-override-toggle"><input type="checkbox" checked={Boolean(custom[target.id])} disabled={!enabled} onChange={event => setCustom(current => ({...current, [target.id]: event.target.checked}))} /><span>Custom values</span></label></div>
        {enabled && custom[target.id] && <div className="target-override-fields"><NumberField label="Interval (s)" value={values.intervalSeconds} min={5} max={3600} set={value => update(setOverrides, target.id, values, {intervalSeconds: value})} /><NumberField label="Min replicas" value={values.minReplicas} min={1} max={1000} set={value => update(setOverrides, target.id, values, {minReplicas: value})} /><NumberField label="Max replicas" value={values.maxReplicas} min={values.minReplicas} max={1000} set={value => update(setOverrides, target.id, values, {maxReplicas: value})} /><label>Parameters<textarea value={values.parameters} rows={3} spellCheck={false} onChange={event => update(setOverrides, target.id, values, {parameters: event.target.value})} /></label></div>}
      </article>
    })}</div>
    {!targets.length && <p className="details-copy">The application contract does not declare scalable microservices.</p>}
  </div>
}

function NumberField({label, value, min, max, set}: {label: string; value: number; min: number; max: number; set: (value: number) => void}) {
  return <label>{label}<NumericInput value={value} min={min} max={max} required onValueChange={set} /></label>
}

function defaultSettings(values: Record<string, unknown>): Settings {
  return {intervalSeconds: number(values.intervalSeconds, 15), minReplicas: number(values.minReplicas, 1), maxReplicas: number(values.maxReplicas, 10), parameters: typeof values.parameters === 'string' ? values.parameters : '{}'}
}

function settings(value: unknown): Partial<Settings> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  const item = value as Record<string, unknown>
  return {intervalSeconds: number(item.intervalSeconds, 15), minReplicas: number(item.minReplicas, 1), maxReplicas: number(item.maxReplicas, 10), parameters: typeof item.parameters === 'string' ? item.parameters : '{}'}
}

function number(value: unknown, fallback: number): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

function update(set: Dispatch<SetStateAction<Record<string, Settings>>>, id: string, current: Settings, patch: Partial<Settings>) {
  set(values => ({...values, [id]: {...current, ...patch}}))
}
