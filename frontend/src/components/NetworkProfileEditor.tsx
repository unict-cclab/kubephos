import {ValidationMessage} from './ValidationMessage'
import {FormDisclosure} from './FormDisclosure'
import {ResponsiveTable} from './ResponsiveTable'
import {ResourceAction} from './ResourceAction'
import {ActionIcon} from './ActionIcon'
import {useEffect, useState, type Dispatch, type SetStateAction} from 'react'

type Override = {id: number; bidirectional?: boolean; sourceZone: string; targetZone: string; latencyMilliseconds?: number; jitterMilliseconds?: number; correlationPercent?: number; bandwidthBytesPerSecond?: number; packetLossPercent?: number}

export function NetworkProfileEditor({prefix, zones, values = {}, distribution = 'zone', showTarget = true, onProfileChange, allowNeutral = false}: {prefix: string; zones: string[]; values?: Record<string, unknown>; distribution?: 'zone' | 'cluster'; showTarget?: boolean; onProfileChange?: () => void; allowNeutral?: boolean}) {
	const initialSource = zones[0] ?? ''
	const initialTarget = zones.find(zone => zone !== initialSource) ?? ''
	const configuredOverrides = parseOverrides(values.linkOverrides)
	const [shared, setShared] = useState(Boolean(values.enableLatency || values.enableBandwidth || values.enablePacketLoss))
  const [customLinks,setCustomLinks]=useState(Boolean(configuredOverrides.length))
  const [latency, setLatency] = useState(Boolean(values.enableLatency))
  const [bandwidth, setBandwidth] = useState(Boolean(values.enableBandwidth))
  const [loss, setLoss] = useState(Boolean(values.enablePacketLoss))
  const [overrides, setOverrides] = useState<Override[]>(configuredOverrides.length || values.linkOverrides !== undefined || shared ? configuredOverrides : [{id: 1, sourceZone: initialSource, targetZone: initialTarget, bidirectional: true, latencyMilliseconds: 50}])
  const [sequence, setSequence] = useState(Math.max(2, configuredOverrides.length + 1))
  useEffect(()=>{onProfileChange?.()},[shared,latency,bandwidth,loss,overrides])
  const nextPair = zones.flatMap(sourceZone => zones.map(targetZone => ({sourceZone, targetZone}))).find(pair => pair.sourceZone !== pair.targetZone && !overrides.some(item => covers(item,pair.sourceZone,pair.targetZone)))
  const addOverride = () => {
    if (!nextPair || overrides.length >= 225) return
    setOverrides(items => [...items, {id: sequence, ...nextPair, bidirectional: !items.some(item=>covers(item,nextPair.targetZone,nextPair.sourceZone)), latencyMilliseconds: 50}])
    setSequence(value => value + 1)
  }
  const targetAvailable = (item: Override, source: string, target: string, both = item.bidirectional === true) => source !== target && !overrides.some(other => other.id !== item.id && (covers(other,source,target) || both && covers(other,target,source)))
  return <div className="network-profile-editor form-stack">
    <div className="network-mode-heading"><strong>{distribution === 'cluster' ? 'Cluster links' : 'Zone links'}</strong><label className="checkbox-field"><input type="checkbox" checked={shared} onChange={event => {setShared(event.target.checked);setCustomLinks(false)}} /><span>{distribution === 'cluster' ? 'Use one profile for every inter-cluster link' : 'Use one profile for every inter-zone link'}</span></label></div>
    <input name={`${prefix}.correlationPercent`} type="hidden" value="0" readOnly />
    <div hidden={!shared} data-review-group="Shared profile"><div className="effect-grid">
      <fieldset className={`effect-card ${latency ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableLatency`} type="checkbox" checked={shared && latency} onChange={event => setLatency(event.target.checked)} /><span>Latency and jitter</span></label><div className="effect-fields"><NumberField name={`${prefix}.latencyMilliseconds`} label="One-way latency (ms)" value={number(values.latencyMilliseconds, 50)} min={1} max={60000} disabled={!shared || !latency} /><NumberField name={`${prefix}.jitterMilliseconds`} label="Jitter (ms)" value={number(values.jitterMilliseconds, 0)} min={0} max={60000} disabled={!shared || !latency} /></div></fieldset>
      <fieldset className={`effect-card ${bandwidth ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableBandwidth`} type="checkbox" checked={shared && bandwidth} onChange={event => setBandwidth(event.target.checked)} /><span>Bandwidth limit</span></label><div className="effect-fields"><NumberField name={`${prefix}.bandwidthBytesPerSecond`} label="Bandwidth (B/s)" value={number(values.bandwidthBytesPerSecond, 12500000)} min={1} max={1000000000000} disabled={!shared || !bandwidth} /></div></fieldset>
      <fieldset className={`effect-card ${loss ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enablePacketLoss`} type="checkbox" checked={shared && loss} onChange={event => setLoss(event.target.checked)} /><span>Packet loss</span></label><div className="effect-fields"><NumberField name={`${prefix}.packetLossPercent`} label="Packet loss (%)" value={number(values.packetLossPercent, 0)} min={0} max={100} step={0.1} disabled={!shared || !loss} /></div></fieldset>
    </div></div>
    {!allowNeutral && shared && !latency && !bandwidth && !loss && <ValidationMessage message="Select a network effect."/>}
    <input type="hidden" name={`${prefix}.linkOverrides`} value={JSON.stringify(shared&&!customLinks?[]:overrides.map(clean))} data-review-title={distribution === 'cluster' ? 'Cluster links' : 'Zone links'} data-review-table={JSON.stringify({headers: ['Source', 'Destination', 'Direction', 'One-way latency (ms)', 'Jitter (ms)', 'Bandwidth (B/s)', 'Packet loss (%)'], rows: (shared&&!customLinks?[]:overrides).map(item => [item.sourceZone, item.targetZone, item.bidirectional ? 'Bidirectional' : 'One-way', ...[item.latencyMilliseconds, item.jitterMilliseconds, item.bandwidthBytesPerSecond, item.packetLossPercent].map(value => value === undefined ? '—' : String(value))])})} readOnly />
    {shared&&<label className="checkbox-field"><input type="checkbox" checked={customLinks} onChange={event=>setCustomLinks(event.target.checked)}/><span>Link overrides</span></label>}
    <fieldset hidden={shared&&!customLinks} disabled={shared&&!customLinks} className="network-link-settings form-stack form-stack-compact"><div className="timeline-heading"><ResourceAction action="add" label={distribution === 'cluster' ? 'Add cluster link' : 'Add zone link'} disabled={!nextPair || overrides.length >= 225} onClick={addOverride} /></div>
    {!allowNeutral && !overrides.length && !shared && <ValidationMessage message={distribution === 'cluster' ? 'Add a cluster link.' : 'Add a zone link.'}/>}
    {overrides.length > 0 && <div className="network-links-editor-wrap" data-review-skip="true"><ResponsiveTable className="network-links-editor" aria-label={distribution === 'cluster' ? 'Cluster links' : 'Zone links'}><thead><tr><th>Source</th><th>Destination</th><th>Bidirectional</th><th>One-way latency (ms)</th><th>Jitter (ms)</th><th>Bandwidth (B/s)</th><th>Packet loss (%)</th><th>Actions</th></tr></thead><tbody>{overrides.map(item => <tr key={item.id}>
      <td><select aria-label={distribution === 'cluster' ? 'Source cluster' : 'Source zone'} value={item.sourceZone} onChange={event => {const sourceZone = event.target.value; const targetZone = targetAvailable(item, sourceZone, item.targetZone) ? item.targetZone : zones.find(zone => targetAvailable(item, sourceZone, zone)) ?? item.targetZone; update(setOverrides, item.id, {sourceZone, targetZone})}}>{zones.map(zone => <option key={zone} disabled={!zones.some(target => targetAvailable(item, zone, target))}>{zone}</option>)}</select></td>
      <td><select aria-label={distribution === 'cluster' ? 'Target cluster' : 'Target zone'} value={item.targetZone} onChange={event => update(setOverrides, item.id, {targetZone: event.target.value})}>{zones.map(zone => <option key={zone} disabled={!targetAvailable(item, item.sourceZone, zone)}>{zone}</option>)}</select></td>
      <td className="network-link-direction"><input type="checkbox" aria-label={`Bidirectional ${item.sourceZone} to ${item.targetZone}`} checked={item.bidirectional===true} title={!item.bidirectional&&!targetAvailable(item,item.sourceZone,item.targetZone,true)?"Remove the opposite link to enable both directions.":undefined} disabled={!item.bidirectional&&!targetAvailable(item,item.sourceZone,item.targetZone,true)} onChange={event=>update(setOverrides,item.id,{bidirectional:event.target.checked})}/></td>
      <td><OptionalNumber label="One-way latency (ms)" value={item.latencyMilliseconds} min={1} max={60000} set={value => update(setOverrides, item.id, {latencyMilliseconds: value})} /></td>
      <td><OptionalNumber label="Jitter (ms)" value={item.jitterMilliseconds} min={0} max={60000} set={value => update(setOverrides, item.id, {jitterMilliseconds: value})} /></td>
      <td><OptionalNumber label="Bandwidth (B/s)" value={item.bandwidthBytesPerSecond} min={1} max={1000000000000} set={value => update(setOverrides, item.id, {bandwidthBytesPerSecond: value})} /></td>
      <td><OptionalNumber label="Packet loss (%)" step={0.1} value={item.packetLossPercent} min={0} max={100} set={value => update(setOverrides, item.id, {packetLossPercent: value})} /></td>
      <td><button type="button" className="icon-button" aria-label="Remove override" title="Remove override" onClick={() => setOverrides(items => items.filter(candidate => candidate.id !== item.id))}><ActionIcon name="delete" /></button></td>
    </tr>)}</tbody></ResponsiveTable></div>}
    </fieldset>
    {showTarget && <FormDisclosure title="Network target"><div className="schema-fields" data-review-group="Network target"><label>Node group label<input name={`${prefix}.nodeGroupLabel`} defaultValue={text(values.nodeGroupLabel, 'topology.kubernetes.io/zone')} required /></label><label>Affected node selector<input name={`${prefix}.nodeSelector`} defaultValue={text(values.nodeSelector, 'kubephos.dev/role=application')} /></label><label>Network interface<input name={`${prefix}.networkInterface`} defaultValue={text(values.networkInterface, 'flannel.1')} required /></label><label className="checkbox-field wide"><input name={`${prefix}.hostNetwork`} type="checkbox" defaultChecked={Boolean(values.hostNetwork)} /><span>Shape host-network traffic instead of Pod CIDRs</span></label></div></FormDisclosure>}
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
  return Object.fromEntries(Object.entries(value).filter(([key, item]) => key !== 'id' && key !== 'correlationPercent' && !(key === 'bidirectional' && item !== true) && item !== undefined)) as Omit<Override, 'id'>
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

function covers(item: Override, source: string, target: string): boolean {
 return item.sourceZone === source && item.targetZone === target || item.bidirectional === true && item.sourceZone === target && item.targetZone === source
}
