import {FormDisclosure} from './FormDisclosure'
import type {ApplicationComponent} from '../types'

export const serviceResourceFields = ['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit'] as const
const rows = [{name: 'CPU', request: 'cpuRequest', limit: 'cpuLimit'}, {name: 'Memory', request: 'memoryRequest', limit: 'memoryLimit'}] as const

export function ServiceResourceEditor({prefix, values, targets, onValue}: {prefix: string; values: Record<string, unknown>; targets?: ApplicationComponent[]; onValue: (changes: Record<string, unknown>) => void}) {
  const services = targets?.filter(target => target.traits?.some(trait => trait === 'scalable' || trait === 'schedulable'))
  const overrides = record(values.resourceOverrides)
  const configured = services ? Object.fromEntries(services.filter(service => service.id in overrides).map(service => [service.id, overrides[service.id]])) : overrides
  const override = (name: string, changes: Record<string, unknown> | undefined) => {
    const next = {...overrides}
    if (changes === undefined) delete next[name]
    else next[name] = changes
    onValue({resourceOverrides: next})
  }
  return <section className="service-resources wide">
    <strong>Service resources</strong>
    <div className="service-resource-grid">
      <span /><strong>Request</strong><strong>Limit</strong>
      {rows.map(row => <ResourceRow key={row.name} row={row} prefix={prefix} values={values} onValue={onValue} />)}
    </div>
    {services && <>
      <input type="hidden" name={`${prefix}.resourceOverrides`} value={JSON.stringify(configured)} readOnly />
      <FormDisclosure title="Resource overrides by service" className="service-overrides service-resource-overrides" defaultOpen={Object.keys(configured).length > 0} count={Object.keys(configured).length}>
      <div className="target-editor-list">{services.map(service => {
        const enabled = service.id in overrides
        const saved = record(overrides[service.id])
        return <article data-review-group={service.id} key={service.id} className={enabled ? 'enabled' : ''}>
          <label className="checkbox-field"><input type="checkbox" checked={enabled} aria-label={`Override resources for ${service.id}`} onChange={event => override(service.id, event.target.checked ? {} : undefined)} /><span>{service.id}</span></label>
          {enabled && <div className="service-resource-grid">
            <span /><strong>Request</strong><strong>Limit</strong>
            {rows.map(row => <ResourceRow key={row.name} row={row} service={service.id} prefix={prefix} values={saved} inherited={values} onValue={changes => {
              const next = {...saved}
              for (const [field, value] of Object.entries(changes)) { if (value === '') delete next[field]; else next[field] = value }
              override(service.id, next)
            }} />)}
          </div>}
        </article>
      })}</div></FormDisclosure>
    </>}
  </section>
}

function ResourceRow({row, prefix, values, service, inherited, onValue}: {row: typeof rows[number]; prefix: string; values: Record<string, unknown>; service?: string; inherited?: Record<string, unknown>; onValue: (changes: Record<string, unknown>) => void}) {
  return <>
    <strong>{row.name}</strong>
    {[row.request, row.limit].map((field, index) => <label className="service-resource-input" key={field}>
      <input name={service ? undefined : `${prefix}.${field}`} aria-label={`${row.name} ${index === 0 ? 'request' : 'limit'} ${service ? `for ${service}` : 'per service (optional)'}`} value={String(values[field] ?? '')} placeholder={service && inherited?.[field] ? `Inherited: ${inherited[field]}` : 'Not set'} onChange={event => onValue({[field]: event.target.value})} />
    </label>)}
  </>
}
function record(value: unknown): Record<string, unknown> { return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {} }
