import type {Connectivity, Connection, ManagedResource} from './types'

export function currentConnectivity(value?: Connectivity, now = Date.now()): Connectivity | undefined {
  if (!value) return undefined
  const checked = Date.parse(value.checkedAt)
  return !Number.isFinite(checked) || now - checked > 180_000
    ? {...value, status: 'unknown', message: 'The last connectivity check is out of date.', targets: value.targets?.map(target => ({...target, status: 'unknown', message: 'The last connectivity check is out of date.'}))}
    : value
}

export function resourceStatus(resource: Pick<ManagedResource, 'status' | 'connectivity'> | Pick<Connection, 'connectivity'>): string {
  const base = 'status' in resource ? resource.status : 'reachable'
  if (base !== 'ready' && base !== 'reachable') return base
  const connectivity = currentConnectivity(resource.connectivity)
  if (!connectivity) return 'unknown'
  return connectivity.status === 'reachable' ? base : connectivity.status
}
