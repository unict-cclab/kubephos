import type {ReactNode} from 'react'

const labels: Record<string, string> = {
  addressStart: 'First VM address', addressPrefix: 'Network prefix', gateway: 'Network gateway', dnsServer: 'DNS server', vmidStart: 'First VMID', vmid: 'VMID', node: 'Proxmox node', templateId: 'VM template', harborId: 'Harbor registry', nfsId: 'NFS server', connectionId: 'Proxmox connection', workspaceId: 'Environment', controlPlanes: 'Control planes', controlPlaneZones: 'Control plane zones', controlPlaneCapacity: 'Control plane capacity', managementPool: 'Management pool', applicationPools: 'Application pools', machineCapacity: 'Machine capacity', cpuCores: 'CPU cores', memoryMiB: 'Memory (MiB)', diskGiB: 'Disk (GiB)', sourceImage: 'Source Docker image', applicationRef: 'Application', clusterResourceId: 'Kubernetes cluster', durationSeconds: 'Duration (seconds)', geographicSteps: 'Geographic phases', steps: 'Workload phases', credentialRef: 'API credential', tokenRef: 'API credential'
}

function labelFor(key: string): string {
  if (labels[key]) return labels[key]
  return key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').replaceAll(/[_-]+/g, ' ').replace(/^./, value => value.toUpperCase())
}

function sensitive(key: string): boolean {
  return !/(ref|id)$/i.test(key) && /(password|secret|token|private|credential|kubeconfig|apiKey|accessKey)/i.test(key)
}

export function safeInput(value: unknown, key = ''): unknown {
  if (sensitive(key)) return '••••••••'
  if (Array.isArray(value)) return value.map(item => safeInput(item))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, safeInput(item, name)]))
  return value
}

function Value({value, keyName, references}: {value: unknown; keyName: string; references: Record<string, string>}): ReactNode {
  if (Array.isArray(value)) return value.length ? <div className="input-data-list">{value.map((item, index) => <div className="input-data-item" key={`${keyName}-${index}`}><span className="input-data-index">{index + 1}</span><Value value={item} keyName={`${keyName}-${index}`} references={references} /></div>)}</div> : <span className="input-data-empty">None</span>
  if (value && typeof value === 'object') return <dl className="input-data-grid">{Object.entries(value).map(([name, item]) => <div className="input-data-field" key={name}><dt>{labelFor(name)}</dt><dd><Value value={item} keyName={name} references={references} /></dd></div>)}</dl>
  if (value === null || value === undefined || value === '') return <span className="input-data-empty">—</span>
  if (typeof value === 'boolean') return <span>{value ? 'Yes' : 'No'}</span>
  const text = String(value)
  return <span className="input-data-value">{references[text] ?? text}</span>
}

export function InputData({value, references = {}}: {value: unknown; references?: Record<string, string>}) {
  return <div className="input-data"><Value value={safeInput(value)} keyName="root" references={references} /></div>
}
