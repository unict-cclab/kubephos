import {describe, expect, it, vi} from 'vitest'
import {createElement, type ComponentProps} from 'react'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {Views, buildRecentActivity, canDeleteKubernetesCluster} from './Views'
import type {Experiment, ManagedResource} from '../types'

describe('buildRecentActivity', () => {
  it('shows user resources and experiments instead of internal operations', () => {
    const experiment = {id: 'exp-1', name: 'Scheduler comparison', status: 'running', createdAt: '2026-09-17T10:00:00Z', updatedAt: '2026-09-17T12:00:00Z'} as Experiment
    const cluster = {id: 'cluster-1', name: 'research-cluster', kind: 'kubernetes-cluster', status: 'ready', createdAt: '2026-09-17T09:00:00Z', updatedAt: '2026-09-17T11:00:00Z'} as ManagedResource
    const items = buildRecentActivity({experiments: [experiment], configurations: [], connections: [], resources: [cluster], applications: []})

    expect(items.map(item => item.title)).toEqual(['Experiment in progress', 'Kubernetes cluster ready'])
    expect(items[0].detail).toBe('Scheduler comparison')
    expect(items[0].href).toBe('results/instance/exp-1')
  })
})

describe('canDeleteKubernetesCluster', () => {
  it('allows retrying a failed deletion', () => {
    expect(canDeleteKubernetesCluster('deletion-failed')).toBe(true)
    expect(canDeleteKubernetesCluster('deleting')).toBe(false)
  })
})

function viewProps(view: 'catalog' | 'kubernetes' | 'infrastructure'): ComponentProps<typeof Views> {
  const noop = () => {}
  const asyncNoop = async () => {}
  return {
    view, system: null, pluginRuntime: null, workspaces: [], pipelines: [], pipelineRuns: [], operations: [], artifacts: [], experiments: [], experimentConfigurations: [], plugins: [], pluginPackages: [], pluginImports: [], applications: [], strategies: [], credentials: [], connections: [], machineTemplates: [], infrastructureServices: [], kubernetesClusters: [], resources: [], audit: [], users: [], clusterAssignments: [], clusterActions: {}, session: {authenticated: true}, isAdmin: true,
    changed: asyncNoop, navigate: noop, openOperation: noop, importApplication: noop, deleteApplication: async () => true, addConnection: noop, addMachineTemplate: noop, addInfrastructureService: noop, deleteConnection: asyncNoop, deleteMachineTemplate: asyncNoop, deleteInfrastructureService: asyncNoop, addKubernetesCluster: noop, deleteKubernetesCluster: asyncNoop, recreateKubernetesCluster: asyncNoop,
  }
}

it('shows clusters in a searchable table and preserves deletion guards', () => {
  window.location.hash = '#kubernetes'
  const props = viewProps('kubernetes')
  props.kubernetesClusters = [
    {id: 'one', name: 'Alpha', status: 'ready', spec: {}, createdAt: '', updatedAt: ''},
    {id: 'two', name: 'Beta', status: 'provisioning', spec: {}, createdAt: '', updatedAt: ''},
  ] as ManagedResource[]
  render(createElement(Views, props))
  const table = screen.getByRole('table', {name: 'Kubernetes clusters'})
  expect(within(table).getByRole('button', {name: 'Delete Alpha'})).toBeEnabled()
  expect(within(table).getByRole('button', {name: 'Delete Beta'})).toBeDisabled()
  expect(within(table).getByRole('columnheader', {name: 'Nodes'})).toBeInTheDocument()
  expect(within(table).getByRole('columnheader', {name: 'Proxmox connection'})).toBeInTheDocument()
  expect(within(table).getByRole('columnheader', {name: 'Type'})).toBeInTheDocument()
  expect(within(table).getAllByRole('cell', {name: 'Single'})).toHaveLength(2)
  expect(within(table).getByRole('columnheader', {name: 'Last updated by'})).toBeInTheDocument()
  fireEvent.change(screen.getByRole('searchbox', {name: 'Search kubernetes clusters'}), {target: {value: 'Beta'}})
  expect(within(table).queryByText('Alpha')).not.toBeInTheDocument()
  expect(within(table).getByText('Beta')).toBeInTheDocument()
})

it('shows the AI model count before opening its tab and reuses the loaded list', async () => {
  window.location.hash = '#catalog'
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({items: [{id: 'm1', enabled: true}, {id: 'm2', enabled: false}]}), {status: 200, headers: {'Content-Type': 'application/json'}}))
  vi.stubGlobal('fetch', fetch)
  try {
    render(createElement(Views, viewProps('catalog')))
    const tab = await screen.findByRole('button', {name: 'AI models2'})
    fireEvent.click(tab)
    expect(screen.getByText('m1')).toBeInTheDocument()
    expect(screen.getByText('m2')).toBeInTheDocument()
    expect(fetch).toHaveBeenCalledTimes(1)
  } finally {vi.unstubAllGlobals()}
})

it('uses consistent Infrastructure names and attribution columns across all resource tables', () => {
  window.location.hash = '#infrastructure'
  const props = viewProps('infrastructure')
  props.connections = [{id: 'pve', name: 'Lab', provider: 'proxmox', pluginId: 'proxmox', createdAt: '', updatedAt: '', modifiedBy: 'angelo.marchese'}]
  props.machineTemplates = [{id: 'template', name: 'Ubuntu', kind: 'machine-template', connectionId: 'pve', status: 'ready', spec: {}, createdAt: '', updatedAt: ''}] as ManagedResource[]
  props.infrastructureServices = ['harbor', 'nfs'].map(kind => ({id: kind, name: kind, kind, connectionId: 'pve', status: 'ready', spec: {}, createdAt: '', updatedAt: ''})) as ManagedResource[]
  render(createElement(Views, props))
  expect(screen.getByRole('button', {name: 'New Proxmox connection'})).toBeInTheDocument()
  for (const [tab, tableLabel] of [['Proxmox connections1', 'Proxmox connections'], ['VM templates1', 'VM templates'], ['Harbor1', 'Harbor registries'], ['NFS1', 'NFS servers']]) {
    fireEvent.click(screen.getByRole('button', {name: tab}))
    const table = within(screen.getByRole('table', {name: tableLabel}))
    expect(table.getByRole('columnheader', {name: 'Name'})).toBeInTheDocument()
    expect(table.getByRole('columnheader', {name: 'Last updated by'})).toBeInTheDocument()
    expect(table.queryByRole('columnheader', {name: 'Provider'})).not.toBeInTheDocument()
  }
})

it('opens a model detail URL and returns to its catalog table', async () => {
  window.location.hash = '#catalog/model/test-model'
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({items: [{id: 'test-model', url: 'https://example.org/model.gguf', backend: 'llama-cpp', quantization: 'Q4_K_M', size: 1024, sha256: 'abc', origin: 'custom', enabled: true}]}), {status: 200})))
  try {
    render(createElement(Views, {...viewProps('catalog'), isAdmin: false}))
    expect(await screen.findByRole('heading', {name: 'test-model'})).toBeInTheDocument()
    expect(screen.getByText('Download URL')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Back to Catalog'}))
    expect(screen.getByRole('table', {name: 'AI models'})).toBeInTheDocument()
  } finally {vi.unstubAllGlobals()}
})


it('keeps the active catalog creation action in the shared heading and opens its own editor', async () => {
  window.location.hash = '#catalog'
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({items: []}), {status: 200})))
  HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
  HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
  try {
    const {container} = render(createElement(Views, viewProps('catalog')))
    const heading = within(container.querySelector<HTMLElement>('.resource-list-heading')!)
    expect(heading.getByRole('button', {name: 'Import application'})).toBeInTheDocument()
    for (const [tab, action] of [['Schedulers0', 'Add scheduler'], ['Deschedulers0', 'Add descheduler'], ['Autoscalers0', 'Add autoscaler'], ['AI models0', 'Add model']]) {
      fireEvent.click(await screen.findByRole('button', {name: tab}))
      expect(heading.queryByRole('button', {name: 'Import application'})).not.toBeInTheDocument()
      fireEvent.click(heading.getByRole('button', {name: action}))
      expect(screen.getByRole(action === 'Add model' ? 'dialog' : 'region', {name: action})).toBeInTheDocument()
      fireEvent.click(screen.getByRole('button', {name: 'Cancel'}))
      expect(heading.getAllByRole('button')).toHaveLength(1)
    }
  } finally {vi.unstubAllGlobals()}
})
