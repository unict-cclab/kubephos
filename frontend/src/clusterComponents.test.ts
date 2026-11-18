import {describe, expect, it} from 'vitest'
import {clusterComponentTitle, clusterHasComponent, clusterIstioModes, pluginAvailableOnCluster, resolveComponentSelection, type ClusterComponent} from './clusterComponents'
import type {ManagedResource, Plugin} from './types'

const cluster = (spec: Record<string, unknown>) => ({spec}) as ManagedResource
const chaosPlugin = {id: 'chaos', requiredComponents: ['chaos-mesh']} as Plugin
const catalog: ClusterComponent[] = [
  {id: 'prometheus', title: 'Prometheus', description: '', core: true},
  {id: 'istio-sidecar', title: 'Istio (sidecar)', description: '', core: false, unsupportedNetworking: ['cilium']},
  {id: 'kiali', title: 'Kiali', description: '', core: false, requiresAny: ['istio-sidecar'], unsupportedNetworking: ['cilium']},
  {id: 'headlamp', title: 'Headlamp', description: '', core: false},
]

describe('cluster components', () => {
  it('offers only the components the cluster lists', () => {
    const selected = cluster({components: ['prometheus', 'istio-ambient']})
    expect(clusterIstioModes(selected)).toEqual(['ambient'])
    expect(clusterHasComponent(selected.spec, 'chaos-mesh')).toBe(false)
    expect(pluginAvailableOnCluster(chaosPlugin, selected)).toBe(false)
    expect(clusterIstioModes(cluster({}))).toEqual(['sidecar', 'ambient'])
  })

  it('adds core components and drops unsupported or unmet ones', () => {
    expect(resolveComponentSelection(catalog, ['kiali', 'headlamp'], 'flannel')).toEqual(['prometheus', 'headlamp'])
    expect(resolveComponentSelection(catalog, ['istio-sidecar', 'kiali'], 'cilium')).toEqual(['prometheus'])
  })

  it('names components from the catalog', () => {
    expect(clusterComponentTitle({items: catalog, defaults: {flannel: [], cilium: []}}, 'kiali')).toBe('Kiali')
    expect(clusterComponentTitle(undefined, 'kiali')).toBe('kiali')
  })
})
