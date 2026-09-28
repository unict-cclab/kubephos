import {useEffect, useMemo, useState} from 'react'
import type {ApplicationComponent} from '../types'
import {NumericInput} from './NumericInput'

export function InitialReplicaEditor({prefix, components, values, resetKey}: {prefix: string; components: ApplicationComponent[]; values: Record<string, unknown>; resetKey: string}) {
  const targets = useMemo(() => components.filter(component => component.traits?.includes('scalable')), [components])
  const [initialReplicas, setInitialReplicas] = useState(number(values.initialReplicas, 1))
  const [enabled, setEnabled] = useState<Record<string, boolean>>({})
  const [overrides, setOverrides] = useState<Record<string, number>>({})
  useEffect(() => {
    const saved = record(values.replicaOverrides)
    setInitialReplicas(number(values.initialReplicas, 1))
    setEnabled(Object.fromEntries(targets.map(target => [target.id, saved[target.id] !== undefined])))
    setOverrides(Object.fromEntries(targets.map(target => [target.id, number(saved[target.id], number(values.initialReplicas, 1))])))
  }, [resetKey, targets.map(target => target.id).join('|')])
  const configured = Object.fromEntries(targets.filter(target => enabled[target.id]).map(target => [target.id, overrides[target.id] ?? initialReplicas]))
  return <section className="initial-replica-editor">
    <label>Initial replicas<NumericInput name={`${prefix}.initialReplicas`} value={initialReplicas} min={1} max={20} required onValueChange={setInitialReplicas} /></label>
    <input type="hidden" name={`${prefix}.replicaOverrides`} value={JSON.stringify(configured)} readOnly />
    <div className="target-editor-heading"><strong>Service overrides</strong><span>{Object.keys(configured).length}</span></div>
    <div className="target-editor-list">{targets.map(target => <article className={enabled[target.id] ? 'enabled' : ''} key={target.id}>
      <div className="target-editor-row"><label className="checkbox-field"><input type="checkbox" checked={Boolean(enabled[target.id])} onChange={event => setEnabled(current => ({...current, [target.id]: event.target.checked}))} /><span>{target.id}</span></label>{enabled[target.id] && <label className="replica-override-value">Replicas<NumericInput value={overrides[target.id] ?? initialReplicas} min={1} max={20} required onValueChange={value => setOverrides(current => ({...current, [target.id]: value}))} /></label>}</div>
    </article>)}</div>
  </section>
}

function record(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function number(value: unknown, fallback: number): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}
