import {useState, type ReactNode} from 'react'
import type {IstioDataPlane} from '../types'

// Sidecar mode keeps per-service injection and proxy resources. Ambient mode applies to
// the whole namespace: the sidecar settings stay mounted inside a disabled fieldset, so
// they leave the submitted form and the review but return if sidecar mode is chosen again.
// Long-lived application deployments run sidecars only, so they omit the choice.
export function IstioSettingsEditor({dataPlane: initial = 'sidecar', disabled = false, ambientAvailable = true, children}: {dataPlane?: IstioDataPlane; disabled?: boolean; ambientAvailable?: boolean; children: ReactNode}) {
  const [dataPlane, setDataPlane] = useState<IstioDataPlane>(initial)
  const ambient = !disabled && dataPlane === 'ambient'
  if (!ambientAvailable) return <>{children}</>
  return <div className="form-stack istio-settings">
    <input type="hidden" name="istioDataPlane" value={ambient ? 'ambient' : 'sidecar'} />
    <label>Data plane<select value={ambient ? 'ambient' : 'sidecar'} disabled={disabled} onChange={event => setDataPlane(event.target.value as IstioDataPlane)}>
      <option value="sidecar">Sidecar</option>
      <option value="ambient">Ambient</option>
    </select></label>
    {ambient && <p className="details-copy">The whole namespace joins the ambient mesh: ztunnel carries its traffic and one waypoint reports request metrics. KubePhos installs ambient mode on the cluster the first time it is used.</p>}
    <fieldset className="istio-sidecar-settings" disabled={ambient} hidden={ambient}>{children}</fieldset>
  </div>
}
