import {useEffect, useId, useState} from 'react'
import {request} from '../api'
import type {ManagedResource, PipelineRun, Session} from '../types'
import {Dialog} from './Dialog'

type Capacity = {cores: number; memoryMiB: number; diskGiB: number}
type Pool = {name: string; count: number; zones: string[]; capacity: Capacity}
type ClusterSpec = {name: string; controlPlanes: number; managementPool: Pool; applicationPools: Pool[]; machineNodes: Record<string, string>; baseVMID: number; addressStart: string}
export function clusterMachineNames(spec: ClusterSpec, pools: Pool[]) {
  return [{name: 'control-plane', count: spec.controlPlanes}, spec.managementPool, ...pools].flatMap(pool => Array.from({length: Math.max(0, Math.min(12, pool.count || 0))}, (_, index) => `${spec.name}-${pool.name}-${index + 1}`))
}

export function ClusterMaintenance({resource, runs, session}: {resource: ManagedResource; runs: PipelineRun[]; session: Session}) {
  const [open, setOpen] = useState(false)
  const [queued, setQueued] = useState(false)
  useEffect(() => {if (resource.status !== 'ready') setQueued(false)}, [resource.status])
  const spec = resource.spec as unknown as ClusterSpec
  const blocked = resource.status !== 'ready' || queued || runs.some(run => run.clusterResourceId === resource.id && ['queued', 'running'].includes(run.status))
  if (session.user?.role !== 'admin') return null
  return <><button className="button secondary" disabled={blocked} title={blocked ? 'Maintenance requires a ready cluster with no queued or running experiments.' : undefined} onClick={() => setOpen(true)}>Manage nodes</button>{queued && <span role="status">Cluster maintenance queued</span>}{open && <MaintenanceEditor spec={spec} resource={resource} session={session} onClose={() => setOpen(false)} onQueued={() => {setQueued(true); setOpen(false)}} />}</>
}

function MaintenanceEditor({spec, resource, session, onClose, onQueued}: {spec: ClusterSpec; resource: ManagedResource; session: Session; onClose: () => void; onQueued: () => void}) {
  const formId = useId()
  const [pools, setPools] = useState<Pool[]>(() => structuredClone(spec.applicationPools ?? []))
  const [nodes, setNodes] = useState({...spec.machineNodes})
  const [newVMIDStart, setNewVMIDStart] = useState<number | ''>('')
  const [newAddressStart, setNewAddressStart] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const [confirmed, setConfirmed] = useState(false)
  const names = clusterMachineNames(spec, pools)
  const originalNames = clusterMachineNames(spec, spec.applicationPools ?? [])
  const added = names.filter(name => !originalNames.includes(name))
  const removed = originalNames.filter(name => !names.includes(name))
  const defaultNode = Object.values(spec.machineNodes ?? {})[0] ?? ''
  const moved = names.filter(name => originalNames.includes(name) && (nodes[name] ?? defaultNode) !== (spec.machineNodes?.[name] ?? defaultNode))
  const changed = added.length + removed.length + moved.length > 0
  const updatePool = (index: number, change: Partial<Pool>) => {setConfirmed(false); setPools(items => items.map((item, position) => position === index ? {...item, ...change} : item))}
  const submit = async (event: React.FormEvent) => {
    event.preventDefault(); setPending(true); setError('')
    try {
      await request(`/kubernetes-clusters/${resource.id}/reconfigure`, {method: 'POST', body: JSON.stringify({applicationPools: pools, machineNodes: Object.fromEntries(names.map(name => [name, nodes[name] ?? defaultNode])), newVMIDStart: newVMIDStart || 0, newAddressStart})}, session.csrfToken)
      onQueued()
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not queue cluster maintenance.')} finally {setPending(false)}
  }
  return <Dialog open title="Manage cluster nodes" onClose={pending ? undefined : onClose} className="cluster-maintenance-modal" footer={<>
    <button type="button" className="button secondary" disabled={pending} onClick={onClose}>Cancel</button>
    <button className="button" type="submit" form={formId} disabled={pending || !changed || names.length > 12 || removed.length > 0 && !confirmed}>{pending ? 'Queuing…' : 'Apply maintenance'}</button>
  </>}><form id={formId} className="cluster-maintenance-form" onSubmit={event => void submit(event)}>
    <fieldset disabled={pending} className="maintenance-section"><legend>Protected pools</legend>
      <div className="maintenance-protected-pools"><div><span>Control plane</span><strong>{spec.controlPlanes} {spec.controlPlanes === 1 ? 'node' : 'nodes'}</strong></div><div><span>{spec.managementPool.name}</span><strong>{spec.managementPool.count} {spec.managementPool.count === 1 ? 'node' : 'nodes'}</strong></div></div>
    </fieldset>
    <fieldset disabled={pending} className="maintenance-section"><legend>Pools</legend>
      <div className="maintenance-pool-list">{pools.map((pool, index) => {
        const existing = (spec.applicationPools ?? []).some(item => item.name === pool.name)
        return <section className="maintenance-pool-card" key={index} aria-label={`Pool ${pool.name}`}>
          <div className="maintenance-pool-header"><strong>{pool.name || 'New pool'}</strong><button type="button" className="button secondary compact" onClick={() => {setPools(items => items.filter((_, position) => position !== index));setConfirmed(false)}}>Remove pool {pool.name}</button></div>
          <div className="maintenance-fields">
            <label>Name<input aria-label={`Pool ${index + 1} name`} value={pool.name} readOnly={existing} pattern="[a-z0-9][a-z0-9-]{0,31}" required onChange={event => updatePool(index, {name: event.target.value})} /></label>
            <label>Nodes<input aria-label={`${pool.name} nodes`} type="number" min={1} max={12} required value={pool.count} onChange={event => updatePool(index, {count: Number(event.target.value)})} /></label>
            {!existing && <><label>Zones<input aria-label={`${pool.name} zones`} required value={pool.zones.join(', ')} onChange={event => updatePool(index, {zones: event.target.value.split(',').map(value => value.trim())})} /></label>{(['cores', 'memoryMiB', 'diskGiB'] as const).map(key => <label key={key}>{({cores: 'CPU (cores)', memoryMiB: 'Memory (MiB)', diskGiB: 'Disk (GiB)'})[key]}<input aria-label={`${pool.name} ${key}`} type="number" min={key === 'cores' ? 1 : key === 'memoryMiB' ? 1024 : 8} max={key === 'cores' ? 32 : key === 'memoryMiB' ? 131072 : 2048} value={pool.capacity[key]} required onChange={event => updatePool(index, {capacity: {...pool.capacity, [key]: Number(event.target.value)}})} /></label>)}</>}
          </div>
        </section>
      })}</div>
      {pools.length === 0 && <p>No pools.</p>}
      <div className="maintenance-add-pool"><button type="button" className="button secondary" onClick={() => {setPools(items => [...items, {name: `pool-${items.length + 1}`, count: 1, zones: ['zone-a'], capacity: {cores: 4, memoryMiB: 8192, diskGiB: 80}}]);setConfirmed(false)}}>Add pool</button></div>
    </fieldset>
    <fieldset disabled={pending} className="maintenance-section"><legend>VM placement</legend>
      <div className="maintenance-placement">{names.map(name => <label className="maintenance-placement-row" key={name}><code>{name}</code><span><span className="maintenance-server-label">Proxmox server</span><input aria-label={`${name} Proxmox server`} value={nodes[name] ?? defaultNode} pattern="[A-Za-z0-9._-]{1,64}" required onChange={event => {setNodes(items => ({...items, [name]: event.target.value}));setConfirmed(false)}} /></span></label>)}</div>
    </fieldset>
    {added.length > 0 && <fieldset disabled={pending} className="maintenance-section"><legend>New VM allocation</legend>
      <div className="maintenance-fields"><label>First new VMID<input aria-label="First new VMID" type="number" min={100} max={999999999} required value={newVMIDStart} onChange={event => setNewVMIDStart(Number(event.target.value))} /></label><label>First new IPv4 address<input aria-label="First new IPv4 address" required value={newAddressStart} onChange={event => setNewAddressStart(event.target.value)} /></label></div>
    </fieldset>}
    <section className="maintenance-review" aria-label="Maintenance summary"><h3>Review changes</h3><div className="maintenance-change-counts"><span><strong>{added.length}</strong> nodes to add</span><span><strong>{removed.length}</strong> nodes to remove</span><span><strong>{moved.length}</strong> VMs to migrate</span></div>
      {names.length > 12 && <p role="alert" className="managed-error">The cluster limit is 12 nodes. Reduce the node count before applying maintenance.</p>}
      {removed.length > 0 && <div className="maintenance-removal"><ul>{removed.map(name => <li key={name}><code>{name}</code></li>)}</ul><label className="checkbox-field"><input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} required disabled={pending} /><span>Confirm removal of these nodes and their ephemeral data</span></label></div>}
    </section>
    {error && <p role="alert" className="managed-error">{error}</p>}
  </form></Dialog>
}
