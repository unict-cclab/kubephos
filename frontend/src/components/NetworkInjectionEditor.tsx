import {useState, type Dispatch, type SetStateAction} from 'react'

type Override = {id: number; sourceZone: string; targetZone: string; latencyMilliseconds?: number; jitterMilliseconds?: number; correlationPercent?: number; bandwidthBytesPerSecond?: number; packetLossPercent?: number}

export function NetworkInjectionEditor({prefix, zones, values = {}}: {prefix: string; zones: string[]; values?: Record<string, unknown>}) {
	const initialSource = zones[0] ?? ''
	const initialTarget = zones.find(zone => zone !== initialSource) ?? ''
	const configuredOverrides = parseOverrides(values.linkOverrides)
	const [shared, setShared] = useState(Boolean(values.enableLatency || values.enableBandwidth || values.enablePacketLoss))
  const [latency, setLatency] = useState(Boolean(values.enableLatency))
  const [bandwidth, setBandwidth] = useState(Boolean(values.enableBandwidth))
  const [loss, setLoss] = useState(Boolean(values.enablePacketLoss))
  const [overrides, setOverrides] = useState<Override[]>(configuredOverrides.length ? configuredOverrides : [{id: 1, sourceZone: initialSource, targetZone: initialTarget, latencyMilliseconds: 50}])
  const [sequence, setSequence] = useState(Math.max(2, configuredOverrides.length + 1))
  const addOverride = () => {
    const sourceZone = zones[0] ?? ''
    const targetZone = zones.find(zone => zone !== sourceZone) ?? ''
    setOverrides(items => [...items, {id: sequence, sourceZone, targetZone, latencyMilliseconds: 50}])
    setSequence(value => value + 1)
  }
  return <div className="network-profile-editor">
    <div className="network-mode-heading"><div><strong>Zone links</strong><p>Configure only the directions that this experiment must alter.</p></div><label className="checkbox-field"><input type="checkbox" checked={shared} onChange={event => setShared(event.target.checked)} /><span>Use one profile for every inter-zone link</span></label></div>
    {shared ? <div className="effect-grid">
      <fieldset className={`effect-card ${latency ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableLatency`} type="checkbox" checked={latency} onChange={event => setLatency(event.target.checked)} /><span>Latency and jitter</span></label><div className="effect-fields"><NumberField name={`${prefix}.latencyMilliseconds`} label="Latency (ms)" value={number(values.latencyMilliseconds, 50)} min={1} max={60000} disabled={!latency} /><NumberField name={`${prefix}.jitterMilliseconds`} label="Jitter (ms)" value={number(values.jitterMilliseconds, 0)} min={0} max={60000} disabled={!latency} /><NumberField name={`${prefix}.correlationPercent`} label="Correlation (%)" value={number(values.correlationPercent, 0)} min={0} max={100} step={0.1} disabled={!latency} /></div></fieldset>
      <fieldset className={`effect-card ${bandwidth ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enableBandwidth`} type="checkbox" checked={bandwidth} onChange={event => setBandwidth(event.target.checked)} /><span>Bandwidth limit</span></label><div className="effect-fields"><NumberField name={`${prefix}.bandwidthBytesPerSecond`} label="Bytes / second" value={number(values.bandwidthBytesPerSecond, 12500000)} min={1} max={1000000000000} disabled={!bandwidth} /></div></fieldset>
      <fieldset className={`effect-card ${loss ? 'enabled' : ''}`}><label className="checkbox-field"><input name={`${prefix}.enablePacketLoss`} type="checkbox" checked={loss} onChange={event => setLoss(event.target.checked)} /><span>Packet loss</span></label><div className="effect-fields"><NumberField name={`${prefix}.packetLossPercent`} label="Packet loss (%)" value={number(values.packetLossPercent, 0)} min={0} max={100} step={0.1} disabled={!loss} /></div></fieldset>
    </div> : <><input name={`${prefix}.enableLatency`} type="checkbox" checked={false} readOnly hidden /><input name={`${prefix}.enableBandwidth`} type="checkbox" checked={false} readOnly hidden /><input name={`${prefix}.enablePacketLoss`} type="checkbox" checked={false} readOnly hidden /><input name={`${prefix}.latencyMilliseconds`} type="hidden" value="50" readOnly /><input name={`${prefix}.jitterMilliseconds`} type="hidden" value="0" readOnly /><input name={`${prefix}.correlationPercent`} type="hidden" value="0" readOnly /><input name={`${prefix}.bandwidthBytesPerSecond`} type="hidden" value="12500000" readOnly /><input name={`${prefix}.packetLossPercent`} type="hidden" value="0" readOnly /></>}
    {shared && !latency && !bandwidth && !loss && <p className="timeline-error">Enable at least one shared network effect.</p>}
    <details className="advanced-fields"><summary>Network target</summary><div className="schema-fields"><label>Node group label<input name={`${prefix}.nodeGroupLabel`} defaultValue={text(values.nodeGroupLabel, 'topology.kubernetes.io/zone')} required /></label><label>Affected node selector<input name={`${prefix}.nodeSelector`} defaultValue={text(values.nodeSelector, 'kubephos.dev/role=application')} /></label><label>Network interface<input name={`${prefix}.networkInterface`} defaultValue={text(values.networkInterface, 'flannel.1')} required /></label><label className="checkbox-field"><input name={`${prefix}.hostNetwork`} type="checkbox" defaultChecked={Boolean(values.hostNetwork)} /><span>Shape host-network traffic instead of Pod CIDRs</span></label></div></details>
    <div className="timeline-heading"><div><strong>{shared ? 'Directed overrides' : 'Directed conditions'}</strong><p>{shared ? 'Change the shared profile for selected directions.' : 'Only listed directions are altered. The reverse direction remains unchanged unless you add it.'}</p></div><button type="button" className="button secondary compact" onClick={addOverride}>Add zone link</button></div>
    <input type="hidden" name={`${prefix}.linkOverrides`} value={JSON.stringify(overrides.map(clean))} readOnly />
    {!overrides.length && !shared && <p className="timeline-error">Add at least one directed zone link.</p>}
    {overrides.length > 0 && <div className="override-list">{overrides.map((item, index) => <article className="override-card" key={item.id}><div className="override-route"><span>{index + 1}</span><select aria-label="Source zone" value={item.sourceZone} onChange={event => update(setOverrides, item.id, {sourceZone: event.target.value})}>{zones.map(zone => <option key={zone}>{zone}</option>)}</select><strong>→</strong><select aria-label="Target zone" value={item.targetZone} onChange={event => update(setOverrides, item.id, {targetZone: event.target.value})}>{zones.map(zone => <option key={zone}>{zone}</option>)}</select><button type="button" className="icon-button" aria-label="Remove override" onClick={() => setOverrides(items => items.filter(candidate => candidate.id !== item.id))}>×</button></div><div className="override-values"><OptionalNumber label="Latency ms" value={item.latencyMilliseconds} min={1} max={60000} set={value => update(setOverrides, item.id, {latencyMilliseconds: value})} /><OptionalNumber label="Jitter ms" value={item.jitterMilliseconds} min={0} max={60000} set={value => update(setOverrides, item.id, {jitterMilliseconds: value})} /><OptionalNumber label="Correlation %" value={item.correlationPercent} min={0} max={100} set={value => update(setOverrides, item.id, {correlationPercent: value})} /><OptionalNumber label="Bytes / second" value={item.bandwidthBytesPerSecond} min={1} max={1000000000000} set={value => update(setOverrides, item.id, {bandwidthBytesPerSecond: value})} /><OptionalNumber label="Packet loss %" value={item.packetLossPercent} min={0} max={100} set={value => update(setOverrides, item.id, {packetLossPercent: value})} /></div></article>)}</div>}
  </div>
}

function NumberField({name, label, value, min, max, step = 1, disabled}: {name: string; label: string; value: number; min: number; max: number; step?: number; disabled?: boolean}) {
  return <label>{label}<input name={name} type="number" defaultValue={value} min={min} max={max} step={step} required readOnly={disabled} /></label>
}

function OptionalNumber({label, value, min, max, set}: {label: string; value?: number; min: number; max: number; set: (value?: number) => void}) {
  return <label>{label}<input type="number" value={value ?? ''} min={min} max={max} step={0.1} placeholder="Not set" onChange={event => set(event.target.value === '' ? undefined : Number(event.target.value))} /></label>
}

function update(set: Dispatch<SetStateAction<Override[]>>, id: number, values: Partial<Override>) {
  set(items => items.map(item => item.id === id ? {...item, ...values} : item))
}

function clean(value: Override): Omit<Override, 'id'> {
  return Object.fromEntries(Object.entries(value).filter(([key, item]) => key !== 'id' && item !== undefined)) as Omit<Override, 'id'>
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
