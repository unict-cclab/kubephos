import {useEffect, useMemo, useState} from 'react'
import type {ApplicationComponent, JsonSchema, SchemaProperty} from '../types'
import {NumericInput} from './NumericInput'

type Settings = Record<string, number | string>
type Field = [name: string, property: SchemaProperty]

const shortLabels: Record<string, string> = {intervalSeconds: 'Interval (s)', minReplicas: 'Min replicas', maxReplicas: 'Max replicas', parameters: 'Parameters'}

// Selects the scalable microservices of an autoscaler and their per-microservice
// overrides. The override fields are the autoscaler's own settings, read from its
// plugin schema: integer bounds, plus the JSON parameters of image-based autoscalers.
// When the settings come from a catalog entry, only the microservices are chosen here.
export function AutoscalerTargetEditor({prefix, schema, components, defaults, resetKey, excluded = [], targetOverrides = {}, overridable = true}: {prefix: string; schema: JsonSchema; components: ApplicationComponent[]; defaults: Record<string, unknown>; resetKey: string; excluded?: string[]; targetOverrides?: Record<string, unknown>; overridable?: boolean}) {
  const targets = useMemo(() => components.filter(component => component.traits?.includes('scalable')), [components])
  const fields = useMemo(() => overrideFields(schema), [schema])
  const [selected, setSelected] = useState<Record<string, boolean>>({})
  const [custom, setCustom] = useState<Record<string, boolean>>({})
  const [overrides, setOverrides] = useState<Record<string, Settings>>({})
  useEffect(() => {
    const initial = settings(fields, defaults)
    setSelected(Object.fromEntries(targets.map(target => [target.id, !excluded.includes(target.id)])))
    setCustom(Object.fromEntries(targets.map(target => [target.id, Boolean(targetOverrides[target.id])])))
    setOverrides(Object.fromEntries(targets.map(target => [target.id, settings(fields, record(targetOverrides[target.id]), initial)])))
  }, [resetKey, targets.map(target => target.id).join('|')])
  const excludedTargets = targets.filter(target => selected[target.id] === false).map(target => target.id)
  const configured = !overridable ? {} : Object.fromEntries(targets.filter(target => selected[target.id] !== false && custom[target.id]).map(target => [target.id, overrides[target.id] ?? settings(fields, defaults)]))
  const update = (id: string, current: Settings, name: string, value: number | string) => setOverrides(values => ({...values, [id]: {...current, [name]: value}}))
  return <div className="autoscaler-target-editor">
    <input type="hidden" name={`exclude-${prefix.replace('component-', '')}`} value={excludedTargets.join(',')} readOnly />
    <input type="hidden" name={`${prefix}.targetOverrides`} value={JSON.stringify(configured)} readOnly />
    <div className="target-editor-heading"><strong>Scalable microservices</strong><span>{targets.filter(target => selected[target.id] !== false).length} / {targets.length}</span></div>
    <div className="target-editor-list">{targets.map(target => {
      const values = overrides[target.id] ?? settings(fields, defaults)
      const enabled = selected[target.id] !== false
      return <article className={enabled ? 'enabled' : ''} key={target.id}>
        <div className="target-editor-row"><label className="checkbox-field"><input type="checkbox" data-review-skip="true" checked={enabled} onChange={event => setSelected(current => ({...current, [target.id]: event.target.checked}))} /><span>{target.id}</span></label>{overridable && <label className="checkbox-field target-override-toggle"><input type="checkbox" data-review-skip="true" checked={Boolean(custom[target.id])} disabled={!enabled} onChange={event => setCustom(current => ({...current, [target.id]: event.target.checked}))} /><span>Custom values</span></label>}</div>
        {overridable && enabled && custom[target.id] && <div className="target-override-fields" data-review-group={`Override · ${target.id}`}>{fields.map(([name, property]) => {
          const label = shortLabels[name] ?? property.title ?? name
          if (property.type !== 'integer') return <label key={name}>{label}<textarea value={String(values[name])} rows={3} spellCheck={false} onChange={event => update(target.id, values, name, event.target.value)} /></label>
          const minimum = name === 'maxReplicas' ? Number(values.minReplicas ?? property.minimum) : property.minimum
          return <label key={name}>{label}<NumericInput value={Number(values[name])} min={minimum} max={property.maximum} required onValueChange={value => update(target.id, values, name, value)} /></label>
        })}</div>}
      </article>
    })}</div>
    {!targets.length && <p className="details-copy">The application contract does not declare scalable microservices.</p>}
  </div>
}

function overrideFields(schema: JsonSchema): Field[] {
  return Object.entries(schema.properties ?? {}).filter(([name, property]) => property.type === 'integer' || name === 'parameters').sort(([, left], [, right]) => (left['x-kubephos-order'] ?? Infinity) - (right['x-kubephos-order'] ?? Infinity))
}

// settings reads each field from values, then from fallback, then from the schema default.
function settings(fields: Field[], values: Record<string, unknown>, fallback: Settings = {}): Settings {
  return Object.fromEntries(fields.map(([name, property]) => {
    const value = values[name] ?? fallback[name] ?? property.default
    if (property.type !== 'integer') return [name, typeof value === 'string' ? value : JSON.stringify(value ?? {})]
    const parsed = Number(value)
    return [name, Number.isFinite(parsed) ? parsed : Number(property.minimum ?? 0)]
  }))
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}
