import {useState, type ReactNode} from 'react'
import type {IstioDataPlane} from '../types'
import {ResourceQuantityInput} from './ResourceQuantityInput'

const maxWaypointReplicas = 10
const waypointResources: {field: string; label: string; kind: 'cpu' | 'memory'}[] = [
  {field: 'cpuRequest', label: 'CPU request', kind: 'cpu'},
  {field: 'cpuLimit', label: 'CPU limit', kind: 'cpu'},
  {field: 'memoryRequest', label: 'Memory request', kind: 'memory'},
  {field: 'memoryLimit', label: 'Memory limit', kind: 'memory'},
]

// The first choice is whether the run uses Istio at all; its options follow only when it does.
// Sidecar mode keeps per-service injection and proxy resources. Ambient mode applies to the
// whole namespace and sizes its waypoint. Settings of the modes not chosen stay mounted inside
// disabled fieldsets, so they leave the submitted form but return if that mode is chosen again.
// Long-lived application deployments run sidecars only, so they omit the data plane choice.
export function IstioSettingsEditor({dataPlane: initialDataPlane = 'sidecar', enabled: initialEnabled = true, waypoint, disabled = false, ambientAvailable = true, children}: {dataPlane?: IstioDataPlane; enabled?: boolean; waypoint?: Record<string, unknown>; disabled?: boolean; ambientAvailable?: boolean; children: ReactNode}) {
  const [enabled, setEnabled] = useState(initialEnabled || initialDataPlane === 'ambient')
  const [dataPlane, setDataPlane] = useState<IstioDataPlane>(initialDataPlane)
  const using = !disabled && enabled
  const ambient = using && ambientAvailable && dataPlane === 'ambient'
  return <div className="form-stack istio-settings">
    {ambientAvailable && <input type="hidden" name="istioDataPlane" value={ambient ? 'ambient' : 'sidecar'} />}
    <label>Use Istio<select value={using ? 'yes' : 'no'} disabled={disabled} onChange={event => setEnabled(event.target.value === 'yes')}>
      <option value="yes">Yes</option>
      <option value="no">No</option>
    </select></label>
    {disabled && <p className="details-copy">Multi-cluster runs use Cilium networking, which does not run Istio.</p>}
    {!disabled && !using && <p className="details-copy">The application runs without a service mesh: no sidecars, no waypoint and no Istio request metrics.</p>}
    {using && ambientAvailable && <label>Data plane<select value={dataPlane} onChange={event => setDataPlane(event.target.value as IstioDataPlane)}>
      <option value="sidecar">Sidecar</option>
      <option value="ambient">Ambient</option>
    </select></label>}
    <fieldset className="istio-ambient-settings" disabled={!ambient} hidden={!ambient}>
      <p className="details-copy">The whole namespace joins the ambient mesh: ztunnel carries its traffic and the waypoint reports request metrics. KubePhos installs ambient mode on the cluster the first time it is used.</p>
      <WaypointEditor initial={waypoint} />
    </fieldset>
    <fieldset className="istio-sidecar-settings" disabled={!using || ambient} hidden={!using || ambient}>{children}</fieldset>
  </div>
}

// Replicas and per-replica proxy resources of the ambient waypoint; empty fields keep Istio's defaults.
function WaypointEditor({initial = {}}: {initial?: Record<string, unknown>}) {
  const [replicas, setReplicas] = useState(initial.replicas === undefined ? '' : String(initial.replicas))
  const [resources, setResources] = useState<Record<string, string>>(Object.fromEntries(waypointResources.map(({field}) => [field, typeof initial[field] === 'string' ? initial[field] as string : ''])))
  const settings = {...(replicas !== '' ? {replicas: Number(replicas)} : {}), ...Object.fromEntries(Object.entries(resources).filter(([, value]) => value !== ''))}
  const summary = [['Replicas', replicas || '1 (Istio default)'], ...waypointResources.map(({field, label}) => [label, resources[field] || 'Istio default'])]
  return <section className="waypoint-settings" data-review-group="Istio waypoint">
    <input type="hidden" name="istioWaypoint" value={JSON.stringify(Object.keys(settings).length ? settings : null)} data-review-title="Istio waypoint" data-review-table={JSON.stringify({headers: ['Field', 'Value'], rows: summary})} />
    <h4 className="waypoint-settings-title">Waypoint</h4>
    <label>Replicas<input type="number" data-review-skip="true" min={1} max={maxWaypointReplicas} step={1} inputMode="numeric" placeholder="1" value={replicas} onChange={event => setReplicas(event.target.value)} /></label>
    <p className="waypoint-hint">Waypoint pods that share the namespace's request traffic, from 1 to {maxWaypointReplicas}.</p>
    <div className="waypoint-resource-grid" data-review-skip="true">
      {waypointResources.map(({field, label, kind}) => <div key={field} className="waypoint-resource">
        <span className="waypoint-resource-label">{label} per replica</span>
        <ResourceQuantityInput label={`Waypoint ${label.toLowerCase()} per replica`} kind={kind} placeholder="Istio default" value={resources[field]} onChange={value => setResources(current => ({...current, [field]: value}))} />
      </div>)}
    </div>
  </section>
}
