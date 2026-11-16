import {useEffect, useMemo, useState} from 'react'
import type {LoadedDataset} from '../resultsData'
import type {PlacementHistory} from '../types'

export function usablePlacement(value: unknown): value is PlacementHistory {
  if (!value || typeof value !== 'object') return false
  const h = value as PlacementHistory
  return h.status === 'available' && Number.isFinite(h.stepSeconds) && h.stepSeconds > 0 && Array.isArray(h.nodes) && h.nodes.length <= 2000 && h.nodes.every(n => n && typeof n.name === 'string' && typeof n.cluster === 'string' && (n.zone === undefined || typeof n.zone === 'string')) && Array.isArray(h.snapshots) && h.snapshots.length > 0 && h.snapshots.length <= 2000 && h.snapshots.every((f, i) => f && Number.isFinite(Date.parse(f.timestamp)) && (i === 0 || Date.parse(f.timestamp) > Date.parse(h.snapshots[i - 1].timestamp)) && typeof f.observed === 'boolean' && Array.isArray(f.pods) && f.pods.length <= 2000 && f.pods.every(p => p && ['uid', 'name', 'service', 'node', 'cluster'].every(k => typeof p[k as keyof typeof p] === 'string')))
}

export function PlacementReplay({datasets}: {datasets: LoadedDataset[]}) {
  const [runID, setRunID] = useState('')
  const run = datasets.find(d => d.artifact.id === runID) ?? datasets[0]
  const history = useMemo(() => usablePlacement(run?.dataset.spec.placement) ? run.dataset.spec.placement : undefined, [run])
  const [index, setIndex] = useState(0)
  const [service, setService] = useState('')
  const [playing, setPlaying] = useState(false)
  const [speed, setSpeed] = useState(1)
  useEffect(() => {setIndex(0); setService(''); setPlaying(false)}, [run?.artifact.id])
  const services = useMemo(() => [...new Set(history?.snapshots.flatMap(f => f.pods.map(p => p.service)) ?? [])].sort(), [history])
  const groups = useMemo(() => {
    const groups = new Map<string, {cluster: string; zone: string; nodes: PlacementHistory['nodes']}>()
    for (const node of history?.nodes ?? []) {
      const zone = node.zone || ''
      const key = JSON.stringify([node.cluster, zone])
      if (!groups.has(key)) groups.set(key, {cluster: node.cluster, zone, nodes: []})
      groups.get(key)!.nodes.push(node)
    }
    return [...groups.values()].sort((a, b) => a.cluster.localeCompare(b.cluster) || a.zone.localeCompare(b.zone))
  }, [history])
  const position = Math.min(index, Math.max(0, (history?.snapshots.length ?? 1) - 1))
  const frame = history?.snapshots[position]
  useEffect(() => {
    if (!playing || !history) return
    if (position === history.snapshots.length - 1) {setPlaying(false); return}
    const timer = window.setTimeout(() => setIndex(i => Math.min(i + 1, history.snapshots.length - 1)), history.stepSeconds * 1000 / speed)
    return () => window.clearTimeout(timer)
  }, [playing, position, history, speed])
  const elapsed = frame && history ? Math.max(0, (Date.parse(frame.timestamp) - Date.parse(history.snapshots[0].timestamp)) / 1000) : 0
  const time = `${Math.floor(elapsed / 60)}:${String(Math.floor(elapsed % 60)).padStart(2, '0')}`
  return <section className="placement-replay" aria-label="Placement replay">
    <div className="placement-controls">
      {datasets.length > 1 && <label>Run<select aria-label="Placement run" value={run?.artifact.id ?? ''} onChange={e => setRunID(e.target.value)}>{datasets.map(d => <option key={d.artifact.id} value={d.artifact.id}>{d.label}</option>)}</select></label>}
      {history && <><label>Service<select value={service} onChange={e => setService(e.target.value)}><option value="">All services</option>{services.map(s => <option key={s}>{s}</option>)}</select></label><label>Playback speed<select value={speed} onChange={e => setSpeed(Number(e.target.value))}>{[1, 5, 15, 30].map(s => <option key={s} value={s}>{s}×</option>)}</select></label></>}
    </div>
    {!history ? <p className="muted">Placement history is not available for this run.</p> : <>
      <div className="placement-timeline"><button className="button secondary" onClick={() => {if (position === history.snapshots.length - 1) setIndex(0); setPlaying(v => !v)}}>{playing ? 'Pause' : 'Play'}</button><input aria-label="Placement time" type="range" min={0} max={history.snapshots.length - 1} value={position} onChange={e => {setPlaying(false); setIndex(Number(e.target.value))}} /><output aria-live="off">{time}</output><span className="muted">Resolution: {history.stepSeconds}s</span></div>
      <p className="placement-status muted" role="status">{frame?.observed ? 'Scheduled replicas' : 'No placement samples at this time.'}</p><div className="placement-zones">{groups.map(group => <section className="placement-zone" key={JSON.stringify([group.cluster, group.zone])}><h4>{group.cluster}{group.zone && ` · ${group.zone}`}</h4><div className="placement-nodes">{group.nodes.map(node => {
        const pods = (frame?.observed ? frame.pods : []).filter(p => p.node === node.name && p.cluster === node.cluster && (!service || p.service === service))
        const counts = new Map<string, number>()
        for (const pod of pods) counts.set(pod.service, (counts.get(pod.service) ?? 0) + 1)
        return <article className="placement-node" key={node.name}><h5>{node.name}</h5><div className="placement-replicas">{[...counts.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([name, count]) => <span className="placement-service" key={name} title={pods.filter(p => p.service === name).map(p => p.name).join('\n')}>{name}<span aria-label={`${count} replicas`}>{count}</span></span>)}{!pods.length && <span className="muted">{frame?.observed ? 'No matching replicas' : 'No samples'}</span>}</div></article>
      })}</div></section>)}</div>
    </>}
  </section>
}
