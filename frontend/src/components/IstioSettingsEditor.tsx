import {useId, useState, type ReactNode} from 'react'
import type {ApplicationComponent, IstioDataPlane} from '../types'
import {FormTabs} from './FormTabs'
import {ResourceAction} from './ResourceAction'
import {ResourceQuantityInput} from './ResourceQuantityInput'
import {ServiceOverridePicker} from './ServiceOverridePicker'

const maxWaypointReplicas = 10
const waypointResources: {field: string; label: string; kind: 'cpu' | 'memory'}[] = [
  {field: 'cpuRequest', label: 'CPU request', kind: 'cpu'},
  {field: 'cpuLimit', label: 'CPU limit', kind: 'cpu'},
  {field: 'memoryRequest', label: 'Memory request', kind: 'memory'},
  {field: 'memoryLimit', label: 'Memory limit', kind: 'memory'},
]
const modes: {value: IstioDataPlane; title: string; description: string}[] = [
  {value: 'sidecar', title: 'Sidecar', description: 'A proxy runs next to each service you choose, with its own resources.'},
  {value: 'ambient', title: 'Ambient', description: 'No sidecars: ztunnel carries traffic and a shared waypoint handles HTTP.'},
]

// The first choice is whether the run enables Istio at all; its options follow only when it does.
// Sidecar mode keeps per-service injection and proxy resources. Ambient mode applies to the
// whole namespace and configures its waypoint. Settings of the mode not chosen stay mounted inside
// disabled fieldsets, so they leave the submitted form but return if that mode is chosen again.
// Experiment configurations and long-lived application deployments offer the same choices.
export function IstioSettingsEditor({dataPlane: initialDataPlane = 'sidecar', enabled: initialEnabled = true, waypoint, services = [], disabled = false, children}: {dataPlane?: IstioDataPlane; enabled?: boolean; waypoint?: Record<string, unknown>; services?: ApplicationComponent[]; disabled?: boolean; children: ReactNode}) {
  const [enabled, setEnabled] = useState(initialEnabled || initialDataPlane === 'ambient')
  const [dataPlane, setDataPlane] = useState<IstioDataPlane>(initialDataPlane)
  const switchId = useId()
  const using = !disabled && enabled
  const ambient = using && dataPlane === 'ambient'
  const summary = disabled ? 'Multi-cluster runs use Cilium networking, which does not run Istio.' : using ? 'The application runs in the Istio service mesh, which reports request metrics between its services.' : 'The application runs without a service mesh: no sidecars, no waypoint and no Istio request metrics.'
  return <div className="istio-settings">
    <input type="hidden" name="istioDataPlane" value={ambient ? 'ambient' : 'sidecar'} />
    <label className="istio-switch">
      <input type="checkbox" checked={using} disabled={disabled} aria-labelledby={`${switchId}-title`} aria-describedby={`${switchId}-summary`} onChange={event => setEnabled(event.target.checked)} />
      <span><strong id={`${switchId}-title`}>Enable Istio</strong><span id={`${switchId}-summary`}>{summary}</span></span>
    </label>
    {using && <fieldset className="istio-mode-picker">
      <legend>Mode</legend>
      <div className="istio-mode-options">
        {modes.map(mode => <label key={mode.value} className="istio-mode-option">
          <input type="radio" name="istioModeChoice" data-review-skip="true" value={mode.value} checked={dataPlane === mode.value} onChange={() => setDataPlane(mode.value)} />
          <strong>{mode.title}</strong>
          <span>{mode.description}</span>
        </label>)}
      </div>
    </fieldset>}
    <fieldset className="istio-mode-settings" disabled={!ambient} hidden={!ambient}>
      <WaypointEditor initial={waypoint} services={services} />
    </fieldset>
    <fieldset className="istio-mode-settings" disabled={!using || ambient} hidden={!using || ambient}>
      <section className="istio-panel">
        <header className="istio-panel-head"><h4>Sidecars</h4><p>Choose which services get a sidecar and how many resources each proxy may use.</p></header>
        {children}
      </section>
    </fieldset>
  </div>
}

// The ambient waypoint: its replicas and per-replica proxy resources, and which services use it.
// Empty sizes keep Istio's defaults; services follow the namespace default unless overridden.
function WaypointEditor({initial = {}, services}: {initial?: Record<string, unknown>; services: ApplicationComponent[]}) {
  const [replicas, setReplicas] = useState(initial.replicas === undefined ? '' : String(initial.replicas))
  const [resources, setResources] = useState<Record<string, string>>(Object.fromEntries(waypointResources.map(({field}) => [field, typeof initial[field] === 'string' ? initial[field] as string : ''])))
  const [allServices, setAllServices] = useState(initial.allServices !== false)
  const [overrides, setOverrides] = useState<Record<string, boolean>>({...(initial.serviceOverrides as Record<string, boolean> | undefined)})
  const [selected, setSelected] = useState<string>()
  const settings = {
    ...(replicas !== '' ? {replicas: Number(replicas)} : {}),
    ...Object.fromEntries(Object.entries(resources).filter(([, value]) => value !== '')),
    ...(!allServices ? {allServices: false} : {}),
    ...(Object.keys(overrides).length ? {serviceOverrides: overrides} : {}),
  }
  const summary = [
    ['Replicas', replicas || '1 (Istio default)'],
    ...waypointResources.map(({field, label}) => [`${label} per replica`, resources[field] || 'Istio default']),
    ['Every service uses the waypoint', allServices ? 'Yes' : 'No'],
    ...Object.entries(overrides).map(([id, use]) => [id, use ? 'Uses the waypoint' : 'Bypasses the waypoint']),
  ]
  const size = <div className="form-stack">
    <label className="waypoint-replicas">Replicas<input type="number" data-review-skip="true" min={1} max={maxWaypointReplicas} step={1} inputMode="numeric" placeholder="1" value={replicas} onChange={event => setReplicas(event.target.value)} /></label>
    <p className="istio-hint">Waypoint pods that share the namespace's request traffic, from 1 to {maxWaypointReplicas}.</p>
    <div className="waypoint-resource-grid" data-review-skip="true">
      {waypointResources.map(({field, label, kind}) => <div key={field} className="waypoint-resource">
        <span className="waypoint-resource-label">{label} per replica</span>
        <ResourceQuantityInput label={`Waypoint ${label.toLowerCase()} per replica`} kind={kind} placeholder="Istio default" value={resources[field]} onChange={value => setResources(current => ({...current, [field]: value}))} />
      </div>)}
    </div>
  </div>
  const routing = <div className="form-stack">
    <div>
      <label className="checkbox-field"><input data-review-skip="true" type="checkbox" checked={allServices} onChange={event => setAllServices(event.target.checked)} /><span>Route every service through the waypoint</span></label>
      <p className="istio-hint">Turn this off to use the waypoint only for some services, then add them below. Services that bypass it stay in the ambient mesh but lose HTTP processing and request metrics.</p>
    </div>
    <FormTabs label="Waypoint service overrides" selectedId={selected} onSelect={setSelected} actions={<ServiceOverridePicker services={services} taken={id => overrides[id] !== undefined} onAdd={id => {setOverrides(current => ({...current, [id]: !allServices})); setSelected(id)}} />} items={Object.entries(overrides).map(([id, use]) => ({id, title: id, content: <div className="istio-service-heading">
      <label>Service<select data-review-skip="true" aria-label={`Waypoint service ${id}`} value={id} onChange={event => {const target = event.target.value; setOverrides(current => {const copy = {...current}; delete copy[id]; copy[target] = use; return copy}); setSelected(target)}}>
        {!services.some(service => service.id === id) && <option value={id}>{id} (unavailable)</option>}
        {services.map(service => <option key={service.id} value={service.id} disabled={service.id !== id && overrides[service.id] !== undefined}>{service.id}</option>)}
      </select></label>
      <label>Waypoint<select data-review-skip="true" aria-label={`Waypoint use for ${id}`} value={String(use)} onChange={event => setOverrides(current => ({...current, [id]: event.target.value === 'true'}))}>
        <option value="true">Uses the waypoint</option>
        <option value="false">Bypasses the waypoint</option>
      </select></label>
      <ResourceAction action="delete" label={`Remove waypoint override for ${id}`} onClick={() => setOverrides(current => {const copy = {...current}; delete copy[id]; return copy})} />
    </div>}))} />
  </div>
  return <section className="istio-panel" data-review-group="Istio waypoint">
    <input type="hidden" name="istioWaypoint" value={JSON.stringify(Object.keys(settings).length ? settings : null)} data-review-title="Istio waypoint" data-review-table={JSON.stringify({headers: ['Field', 'Value'], rows: summary})} />
    <header className="istio-panel-head"><h4>Waypoint</h4><p>The whole namespace joins the ambient mesh. KubePhos installs ambient mode on the cluster the first time it is used.</p></header>
    <FormTabs label="Waypoint settings" items={[{id: 'size', title: 'Size', content: size}, {id: 'services', title: 'Service overrides', content: routing}]} />
  </section>
}
