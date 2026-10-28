import {useEffect, useState} from 'react'
import {request} from '../api'

type Node = {name: string; online: boolean}

export function ProxmoxNodeSelect({connectionID, defaultNode}: {connectionID: string; defaultNode: string}) {
  const [nodes, setNodes] = useState<Node[]>([])
  const [value, setValue] = useState(defaultNode)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState(false)
  const [retry, setRetry] = useState(0)
  useEffect(() => setValue(nodes.length && !nodes.some(node => node.name === defaultNode && node.online) ? nodes.find(node => node.online)?.name ?? '' : defaultNode), [connectionID, defaultNode])
  useEffect(() => {
    const controller = new AbortController()
    setLoading(true); setError(false); setNodes([])
    request<{items: Node[]}>(`/connections/${encodeURIComponent(connectionID)}/nodes`, {}, undefined, controller.signal).then(result => {
      if (controller.signal.aborted) return
      setNodes(result.items)
      setValue(current => result.items.some(node => node.name === current && node.online) ? current : result.items.find(node => node.online)?.name ?? '')
    }).catch(() => {if (!controller.signal.aborted) setError(true)}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [connectionID, retry])
  return <div className="proxmox-node-select"><label>Proxmox server{error
    ? <input name="node" value={value} onChange={event => setValue(event.target.value)} pattern="[A-Za-z0-9._-]{1,64}" maxLength={64} required />
    : <select name="node" value={loading ? '' : value} onChange={event => setValue(event.target.value)} required>
      {(loading || !value) && <option value="">{loading ? 'Loading…' : 'No available server'}</option>}
      {nodes.map(node => <option key={node.name} value={node.name} disabled={!node.online}>{node.name}{node.online ? '' : ' · Offline'}</option>)}
    </select>}</label>{error && <p className="form-error">Could not load Proxmox servers. <button type="button" className="text-button" onClick={() => setRetry(count => count + 1)}>Retry</button></p>}</div>
}
