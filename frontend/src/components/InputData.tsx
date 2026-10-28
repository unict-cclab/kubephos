import {standardFieldLabel} from '../fieldLabels'
import type {ReactNode} from 'react'
import type {JsonSchema, SchemaProperty} from '../types'

const labels: Record<string, string> = {
  imageStorage: 'Cloud image storage', storage: 'VM disk storage', capacity: 'Resources', cores: 'CPU cores', sshUser: 'SSH user', os: 'Operating system', prefixLength: 'Prefix length', cloudImageURL: 'Cloud image URL', sourceZone: 'Source zone', targetZone: 'Target zone', initialReplicas: 'Initial replicas', replicaOverrides: 'Initial replicas by service', resourceOverrides: 'Resources by service', scrapeInterval: 'Scrape interval', retention: 'Retention', scenarioId: 'Load scenario', spawnRate: 'Users started / second', maxUsers: 'Maximum concurrent users per worker', replicas: 'Replicas', promQLRange: 'Query window', resolution: 'Data resolution', configFile: 'Configuration',
  addressStart: 'First VM address', addressPrefix: 'Network prefix', gateway: 'Network gateway', dnsServer: 'DNS server', vmidStart: 'First VMID', vmid: 'VMID', node: 'Proxmox server', templateId: 'VM template', harborId: 'Harbor registry', nfsId: 'NFS server', connectionId: 'Proxmox connection', machineNodes: 'Proxmox servers by VM', controlPlanes: 'Control planes', controlPlaneZones: 'Control plane zones', controlPlaneCapacity: 'Resources', managementPool: 'Management pool', applicationPools: 'Pools', machineCapacity: 'Resources', cpuCores: 'CPU cores', memoryMiB: 'Memory (MiB)', diskGiB: 'Disk (GiB)', podDisruptionBudgetMinAvailable: 'Minimum available replicas', targetUrl: 'Target URL', runtimeImage: 'Runtime image', script: 'Locustfile', sourceImage: 'Source Docker image', applicationRef: 'Application', clusterResourceId: 'Kubernetes cluster', durationSeconds: 'Duration (seconds)', geographicSteps: 'Geographic phases', steps: 'Workload phases', credentialRef: 'API credential', tokenRef: 'API credential'
}

const metricLabels: Record<string, string> = {latencyMilliseconds: 'Latency (ms)', jitterMilliseconds: 'Jitter (ms)', correlationPercent: 'Correlation (%)', packetLossPercent: 'Packet loss (%)', bandwidthBytesPerSecond: 'Bandwidth (B/s)', sloMilliseconds: 'Response time SLO (ms)', intervalSeconds: 'Interval (s)', scrapePeriodSeconds: 'Scrape period (s)', periodSeconds: 'Period (s)', phaseSeconds: 'Phase offset (s)', rps: 'Requests / second', baselineRps: 'Baseline (requests/s)', amplitudeRps: 'Amplitude (requests/s)', startRps: 'Start (requests/s)', endRps: 'End (requests/s)', responseSizeKB: 'Average response size (KB)', workloadProbeTimeoutSeconds: 'Workload probe timeout (s)', workloadLivenessInitialDelaySeconds: 'Workload liveness delay (s)'}

function labelFor(key: string): string {
  if (metricLabels[key]) return metricLabels[key]
  if (labels[key]) return labels[key]
  return key.replace(/([a-z0-9])([A-Z])/g, '$1 $2').replaceAll(/[_-]+/g, ' ').replace(/^./, value => value.toUpperCase())
}

function sensitive(key: string): boolean {
  if (/^(?:(?:max|min)(?:input|output|prompt|completion)?|input|output|prompt|completion|total|context|generated)tokens?$|^tokens?(?:count|limit|budget|persecond)$/i.test(key.replace(/[_-]/g, ''))) return false
  return !/(ref|id)$/i.test(key) && /(password|secret|token|private|credential|kubeconfig|apiKey|accessKey)/i.test(key)
}

export function hasInputData(value: unknown): boolean {
  if (value === null || value === undefined) return false
  if (typeof value === 'string') return value.trim().length > 0
  if (Array.isArray(value)) return value.some(hasInputData)
  if (typeof value === 'object') return Object.values(value).some(hasInputData)
  return true
}

export function safeInput(value: unknown, key = ''): unknown {
  if (!hasInputData(value)) return value
  if (sensitive(key)) return '••••••••'
  if (Array.isArray(value)) return value.map(item => safeInput(item))
  if (value && typeof value === 'object') return Object.fromEntries(Object.entries(value).map(([name, item]) => [name, safeInput(item, name)]))
  return value
}

function Value({value, keyName, references, schema}: {value: unknown; keyName: string; references: Record<string, string>; schema?: JsonSchema | SchemaProperty}): ReactNode {
  if (schema && 'writeOnly' in schema && schema.writeOnly) return <span>••••••••</span>
  if (Array.isArray(value) && value.every(item => item === null || typeof item !== 'object')) return <span className="input-data-inline-list">{value.filter(hasInputData).map((item, index) => <span key={index}><Value value={item} keyName={keyName} references={references} /></span>)}</span>
  if (Array.isArray(value)) return value.length ? <div className="input-data-list">{value.filter(hasInputData).map((item, index) => <div className="input-data-item" key={`${keyName}-${index}`}><span className="input-data-index">{index + 1}</span><Value value={item} keyName={keyName} references={references} schema={schema && 'items' in schema ? schema.items : undefined} /></div>)}</div> : <span className="input-data-empty">None</span>
  if (value && typeof value === 'object') return <dl className="input-data-grid">{Object.entries(value).filter(([name, item]) => !['workspaceId', 'workspace', 'environmentId'].includes(name) && !schema?.properties?.[name]?.['x-kubephos-hidden'] && hasInputData(item)).sort(([left], [right]) => (schema?.properties?.[left]?.['x-kubephos-order'] ?? Number.MAX_SAFE_INTEGER) - (schema?.properties?.[right]?.['x-kubephos-order'] ?? Number.MAX_SAFE_INTEGER)).map(([name, item]) => <div className={`input-data-field${typeof item === 'object' || typeof item === 'string' && item.includes('\n') ? ' input-data-field-wide' : ''}`} key={name}><dt>{standardFieldLabel(name, schema?.properties?.[name]?.title) ?? schema?.properties?.[name]?.title ?? (['weights', 'startWeights', 'endWeights', 'replicaOverrides', 'resourceOverrides', 'machineNodes', 'labels', 'parameters'].includes(keyName) ? name : labelFor(name))}</dt><dd><Value value={item} keyName={name} references={references} schema={schema?.properties?.[name]} /></dd></div>)}</dl>
  if (value === null || value === undefined || value === '') return <span className="input-data-empty">—</span>
  if (typeof value === 'boolean') return <span>{value ? 'Yes' : 'No'}</span>
  const text = String(value)
  return text.includes('\n') ? <pre className="input-data-code">{text}</pre> : <span className="input-data-value">{references[text] ?? text}</span>
}

export function InputData({value, references = {}, schema}: {value: unknown; references?: Record<string, string>; schema?: JsonSchema}) {
  if (!hasInputData(value)) return null
  return <div className="input-data"><Value value={safeInput(value)} keyName="root" references={references} schema={schema} /></div>
}
