import {useEffect, useState} from 'react'
import {request} from './api'
import type {IstioDataPlane, ManagedResource, Plugin} from './types'

// The component catalog lives in internal/managedcomponents and is served by the API;
// only the two Istio data planes are named here, because experiments choose between them.
const istioSidecarComponent = 'istio-sidecar'
const istioAmbientComponent = 'istio-ambient'

export interface ClusterDashboard {
  name: string
  port: number
  path?: string
}

export interface ClusterComponent {
  id: string
  title: string
  description: string
  core: boolean
  requiresAny?: string[]
  unsupportedNetworking?: string[]
  dashboards?: ClusterDashboard[]
}

export interface ClusterComponentCatalog {
  items: ClusterComponent[]
  defaults: Record<'flannel' | 'cilium', string[]>
}

export function useClusterComponentCatalog() {
  const [catalog, setCatalog] = useState<ClusterComponentCatalog>()
  const [error, setError] = useState('')
  useEffect(() => {
    const controller = new AbortController()
    void (async () => {
      try {
        const value = await request<ClusterComponentCatalog>('/cluster-components', {}, undefined, controller.signal)
        if (Array.isArray(value?.items)) setCatalog(value)
        else setError('Could not load the cluster components.')
      } catch (cause) {
        if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : 'Could not load the cluster components.')
      }
    })()
    return () => controller.abort()
  }, [])
  return {catalog, error}
}

export function clusterComponentTitle(catalog: ClusterComponentCatalog | undefined, id: string): string {
  return catalog?.items.find(component => component.id === id)?.title ?? id
}

// The API lists the components of every cluster, including clusters created before
// component selection; a spec without the list is treated as running everything.
export function clusterHasComponent(spec: Record<string, unknown> | undefined, id: string): boolean {
  const components = spec?.components
  return Array.isArray(components) ? components.includes(id) : true
}

export function pluginAvailableOnCluster(plugin: Plugin, cluster: ManagedResource | undefined): boolean {
  return missingPluginComponents(plugin, cluster).length === 0
}

export function missingPluginComponents(plugin: Plugin, cluster: ManagedResource | undefined): string[] {
  return (plugin.requiredComponents ?? []).filter(id => !clusterHasComponent(cluster?.spec, id))
}

export function clusterIstioModes(cluster: ManagedResource | undefined): IstioDataPlane[] {
  const modes: IstioDataPlane[] = []
  if (clusterHasComponent(cluster?.spec, istioSidecarComponent)) modes.push('sidecar')
  if (clusterHasComponent(cluster?.spec, istioAmbientComponent)) modes.push('ambient')
  return modes
}

// resolveComponentSelection adds the core components and drops the ones the networking
// mode cannot run or whose dependency is missing, mirroring the server-side rules.
export function resolveComponentSelection(catalog: ClusterComponent[], selected: string[], networking: string): string[] {
  const available = catalog.filter(component => !(component.unsupportedNetworking ?? []).includes(networking))
  const chosen = new Set(selected.filter(id => available.some(component => component.id === id)))
  for (const component of available) if (component.core) chosen.add(component.id)
  for (const component of available) {
    if (!chosen.has(component.id) || !component.requiresAny?.length) continue
    if (!component.requiresAny.some(id => chosen.has(id))) chosen.delete(component.id)
  }
  return available.filter(component => chosen.has(component.id)).map(component => component.id)
}
