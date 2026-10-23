import {FormDisclosure} from './FormDisclosure'
import {ResponsiveTable} from './ResponsiveTable'
import {ResourceAction} from './ResourceAction'
import {ActionIcon} from './ActionIcon'
import {useState, type Dispatch, type SetStateAction} from 'react'

type Override = {id: number; sourceZone: string; targetZone: string; latencyMilliseconds?: number; jitterMilliseconds?: number; correlationPercent?: number; bandwidthBytesPerSecond?: number; packetLossPercent?: number}

export function NetworkInjectionEditor({prefix, zones, values = {}, distribution = 'zone'}: {prefix: string; zones: string[]; values?: Record<string, unknown>; distribution?: 'zone' | 'cluster'}) {
	const initialSource = zones[0] ?? ''
	const initialTarget = zones.find(zone => zone !== initialSource) ?? ''
	const configuredOverrides = parseOverrides(values.linkOverrides)
	const [shared, setShared] = useState(Boolean(values.enableLatency || values.enableBandwidth || values.enablePacketLoss))
  const [latency, setLatency] = useState(Boolean(values.enableLatency))
  const [bandwidth, setBandwidth] = useState(Boolean(values.enableBandwidth))
  const [loss, setLoss] = useState(Boolean(values.enablePacketLoss))
  const [overrides, setOverrides] = useState<Override[]>(configuredOverrides.length || values.linkOverrides !== undefined || shared ? configuredOverrides : [{id: 1, sourceZone: initialSource, targetZone: initialTarget, latencyMilliseconds: 50}])
  const [sequence, setSequence] = useState(Math.max(2, configuredOverrides.length + 1))
  const nextPair = zones.flatMap(sourceZone => zones.map(targetZone => ({sourceZone, targetZone}))).find(pair => pair.sourceZone !== pair.targetZone && !overrides.some(item => item.sourceZone === pair.sourceZone && item.targetZone === pair.targetZone))
  const addOverride = () => {
    if (!nextPair || overrides.length >= 225) return
    setOverrides(items => [...items, {id: sequence, ...nextPair, latencyMilliseconds: 50}])
    setSequence(value => value + 1)
  }
  const targetAvailable = (item: Override, source: string, target: string) => source !== target && !overrides.some(other => other.id !== item.id && other.sourceZone === source && other.targetZone === target)
  return <div className="network-profile-editor form-stack">
    <div className="network-mode-heading"><strong>{distribution === 'cluster' ? 'Cluster links' : 'Zone links'}</strong><label className="checkbox-field"><input type="checkbox" checked={shared} onChange={event => setShared(event.target.checked)} /><span>{distribution === 'cluster' ? 'Use one profile for every inter-cluster link' : 'Use one profile for every inter-zone link'}</span></label></div>
    <input name={`${prefix}.correlationPercent`} type="hidden" value="0" readOnly />
    <div hidden={!shared} data-review-group="Shared profile"><div className="effect-grid">
      <fieldset className={`effect-card ${latency ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableLatency`} type="checkbox" checked={shared && latency} onChange={event => setLatency(event.target.checked)} /><span>Latency and jitter</span></label><div className="effect-fields"><NumberField name={`${prefix}.latencyMilliseconds`} label="Latency (ms)" value={number(values.latencyMilliseconds, 50)} min={1} max={60000} disabled={!shared || !latency} /><NumberField name={`${prefix}.jitterMilliseconds`} label="Jitter (ms)" value={number(values.jitterMilliseconds, 0)} min={0} max={60000} disabled={!shared || !latency} /></div></fieldset>
      <fieldset className={`effect-card ${bandwidth ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableBandwidth`} type="checkbox" checked={shared && bandwidth} onChange={event => setBandwidth(event.target.checked)} /><span>Bandwidth limit</span></label><div className="effect-fields"><NumberField name={`${prefix}.bandwidthBytesPerSecond`} label="Bandwidth (B/s)" value={number(values.bandwidthBytesPerSecond, 12500000)} min={1} max={1000000000000} disabled={!shared || !bandwidth} /></div></fieldset>
      <fieldset className={`effect-card ${loss ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enablePacketLoss`} type="checkbox" checked={shared && loss} onChange={event => setLoss(event.target.checked)} /><span>Packet loss</span></label><div className="effect-fields"><NumberField name={`${prefix}.packetLossPercent`} label="Packet loss (%)" value={number(values.packetLossPercent, 0)} min={0} max={100} step={0.1} disabled={!shared || !loss} /></div></fieldset>
    </div></div>
    {shared && !latency && !bandwidth && !loss && <p className="timeline-error">Enable at least one shared network effect.</p>}
    <section className="form-stack form-stack-compact"><div className="timeline-heading"><ResourceAction action="add" label={distribution === 'cluster' ? 'Add cluster link' : 'Add zone link'} disabled={!nextPair || overrides.length >= 225} onClick={addOverride} /></div>
    <input type="hidden" name={`${prefix}.linkOverrides`} value={JSON.stringify(overrides.map(clean))} data-review-title={distribution === 'cluster' ? 'Cluster links' : 'Zone links'} data-review-table={JSON.stringify({headers: ['Source', 'Destination', 'Latency (ms)', 'Jitter (ms)', 'Bandwidth (B/s)', 'Packet loss (%)'], rows: overrides.map(item => [item.sourceZone, item.targetZone, ...[item.latencyMilliseconds, item.jitterMilliseconds, item.bandwidthBytesPerSecond, item.packetLossPercent].map(value => value === undefined ? '—' : String(value))])})} readOnly />
    {!overrides.length && !shared && <p className="timeline-error">{distribution === 'cluster' ? 'Add at least one directed cluster link.' : 'Add at least one directed zone link.'}</p>}
    {overrides.length > 0 && <div className="network-links-editor-wrap" data-review-skip="true"><ResponsiveTable className="network-links-editor" aria-label={distribution === 'cluster' ? 'Cluster links' : 'Zone links'}><thead><tr><th>Source</th><th>Destination</th><th>Latency (ms)</th><th>Jitter (ms)</th><th>Bandwidth (B/s)</th><th>Packet loss (%)</th><th>Actions</th></tr></thead><tbody>{overrides.map(item => <tr key={item.id}>
      <td><select aria-label={distribution === 'cluster' ? 'Source cluster' : 'Source zone'} value={item.sourceZone} onChange={event => {const sourceZone = event.target.value; const targetZone = targetAvailable(item, sourceZone, item.targetZone) ? item.targetZone : zones.find(zone => targetAvailable(item, sourceZone, zone)) ?? item.targetZone; update(setOverrides, item.id, {sourceZone, targetZone})}}>{zones.map(zone => <option key={zone} disabled={!zones.some(target => targetAvailable(item, zone, target))}>{zone}</option>)}</select></td>
      <td><select aria-label={distribution === 'cluster' ? 'Target cluster' : 'Target zone'} value={item.targetZone} onChange={event => update(setOverrides, item.id, {targetZone: event.target.value})}>{zones.map(zone => <option key={zone} disabled={!targetAvailable(item, item.sourceZone, zone)}>{zone}</option>)}</select></td>
      <td><OptionalNumber label="Latency (ms)" value={item.latencyMilliseconds} min={1} max={60000} set={value => update(setOverrides, item.id, {latencyMilliseconds: value})} /></td>
      <td><OptionalNumber label="Jitter (ms)" value={item.jitterMilliseconds} min={0} max={60000} set={value => update(setOverrides, item.id, {jitterMilliseconds: value})} /></td>
      <td><OptionalNumber label="Bandwidth (B/s)" value={item.bandwidthBytesPerSecond} min={1} max={1000000000000} set={value => update(setOverrides, item.id, {bandwidthBytesPerSecond: value})} /></td>
      <td><OptionalNumber label="Packet loss (%)" step={0.1} value={item.packetLossPercent} min={0} max={100} set={value => update(setOverrides, item.id, {packetLossPercent: value})} /></td>
      <td><button type="button" className="icon-button" aria-label="Remove override" title="Remove override" onClick={() => setOverrides(items => items.filter(candidate => candidate.id !== item.id))}><ActionIcon name="close" /></button></td>
    </tr>)}</tbody></ResponsiveTable></div>}
    </section>
    <FormDisclosure title="Network target"><div className="schema-fields" data-review-group="Network target"><label>Node group label<input name={`${prefix}.nodeGroupLabel`} defaultValue={text(values.nodeGroupLabel, 'topology.kubernetes.io/zone')} required /></label><label>Affected node selector<input name={`${prefix}.nodeSelector`} defaultValue={text(values.nodeSelector, 'kubephos.dev/role=application')} /></label><label>Network interface<input name={`${prefix}.networkInterface`} defaultValue={text(values.networkInterface, 'flannel.1')} required /></label><label className="checkbox-field wide"><input name={`${prefix}.hostNetwork`} type="checkbox" defaultChecked={Boolean(values.hostNetwork)} /><span>Shape host-network traffic instead of Pod CIDRs</span></label></div></FormDisclosure>
  </div>
}

function NumberField({name, label, value, min, max, step = 1, disabled}: {name: string; label: string; value: number; min: number; max: number; step?: number; disabled?: boolean}) {
  return <label>{label}<input name={name} type="number" defaultValue={value} min={min} max={max} step={step} required readOnly={disabled} data-review-skip={disabled || undefined} /></label>
}

function OptionalNumber({label, value, min, max, step = 1, set}: {label: string; value?: number; min: number; max: number; step?: number; set: (value?: number) => void}) {
  return <input aria-label={label} type="number" value={value ?? ''} min={min} max={max} step={step} placeholder="Not set" onChange={event => set(event.target.value === '' ? undefined : Number(event.target.value))} />
}

function update(set: Dispatch<SetStateAction<Override[]>>, id: number, values: Partial<Override>) {
  set(items => items.map(item => item.id === id ? {...item, ...values} : item))
}

function clean(value: Override): Omit<Override, 'id'> {
  return Object.fromEntries(Object.entries(value).filter(([key, item]) => key !== 'id' && key !== 'correlationPercent' && item !== undefined)) as Omit<Override, 'id'>
}

function parseOverrides(value: unknown): Override[] {
  if (!Array.isArray(value)) return []
  return value.flatMap((item, index) => item && typeof item === 'object' ? [{id: index + 1, ...(item as Omit<Override, 'id'>)}] : [])
}

function number(value: unknown, fallback: number): number {
  const parsed = Number(value)
  return Number.isFinite(parsed) ? parsed : fallback
}

function text(value: unknown, fallback: string): string {
  return typeof value === 'string' ? value : fallback
}
