import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest'
import type {Connection, ManagedResource, Plugin, Workspace} from '../types'
import {ApplicationDialog, ConnectionDialog, InfrastructureServiceDialog, KubernetesClusterDialog, MachineTemplateDialog} from './Forms'

afterEach(() => vi.unstubAllGlobals())

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

describe('ConnectionDialog', () => {
  it('creates and links a Proxmox API credential in one flow', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({id: 'cred_created', name: 'Lab token', kind: 'proxmox-api-token', fingerprint: 'sha256:test'}), {status: 201, headers: {'Content-Type': 'application/json'}}))
      .mockResolvedValueOnce(new Response(JSON.stringify({id: 'conn_created'}), {status: 201, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const close = vi.fn()
    const onDone = vi.fn().mockResolvedValue(undefined)
    render(<ConnectionDialog open close={close} plugins={[proxmoxPlugin]} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={[]} connections={[]} credentials={[]} onDone={onDone} />)

    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'Lab Proxmox'}})
    expect(screen.queryByRole('textbox', {name: 'Description'})).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', {name: 'Token ID'}), {target: {value: 'root@pam!kubephos'}})
    fireEvent.change(screen.getByLabelText('Token secret'), {target: {value: 'secret-value'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Endpoint'}), {target: {value: 'https://proxmox.test:8006'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate and save'}))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({name: 'Lab Proxmox API token', kind: 'proxmox-api-token', value: {tokenId: 'root@pam!kubephos', tokenSecret: 'secret-value'}})
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toMatchObject({
      name: 'Lab Proxmox',
      description: '',
      pluginId: 'io.kubephos.infrastructure.proxmox.discovery',
      configuration: {endpoint: 'https://proxmox.test:8006', credentialRef: 'cred_created', verifyTLS: true}
    })
    expect(close).toHaveBeenCalledOnce()
    expect(onDone).toHaveBeenCalledWith('Provider connection validated and saved.')
  })
})

describe('InfrastructureServiceDialog', () => {
  it('keeps the selected service and entered values across data refreshes', async () => {
    const props = {
      open: true,
      close: () => {},
      workspaces: [workspace],
      templates: [machineTemplate],
      session: {authenticated: true, csrfToken: 'csrf'},
      applications: [],
      artifacts: [],
      connections: [connection],
      credentials: [],
      onDone: async () => {}
    }
    const view = render(<InfrastructureServiceDialog {...props} initialKind="nfs" />)

    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'shared-data'}})
    expect(screen.getByRole('heading', {name: 'New NFS server'})).toBeInTheDocument()

    view.rerender(<InfrastructureServiceDialog {...props} initialKind="nfs" workspaces={[{...workspace}]} connections={[{...connection}]} templates={[{...machineTemplate}]} />)

    await waitFor(() => expect(screen.getByRole('heading', {name: 'New NFS server'})).toBeInTheDocument())
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('shared-data')
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    expect(screen.getByRole('heading', {name: 'Resources'})).toBeVisible()
    fireEvent.change(screen.getByRole('spinbutton', {name: 'CPU cores'}), {target: {value: '3'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    expect(screen.getByRole('heading', {name: 'Network'})).toBeVisible()
    fireEvent.click(screen.getByRole('button', {name: 'Previous'}))
    expect(screen.getByRole('spinbutton', {name: 'CPU cores'})).toHaveValue(3)
    fireEvent.click(screen.getByRole('button', {name: 'Previous'}))
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('shared-data')
  })
})

describe('KubernetesClusterDialog', () => {
  it('creates member clusters and their pools in one multi cluster form and request', async () => {
    const fetch = vi.fn().mockImplementation((path: string) => Promise.resolve(new Response(JSON.stringify(path.includes('provisioning-suggestions') ? {vmid: 300, address: '10.0.0.10', prefixLength: 24, gateway: '10.0.0.1', dnsServer: '1.1.1.1'} : {id: 'environment_created'}), {status: path.includes('provisioning-suggestions') ? 200 : 202})))
    vi.stubGlobal('fetch', fetch)
    const {container} = render(<KubernetesClusterDialog open close={() => {}} workspaces={[workspace]} templates={[machineTemplate]} services={[harborService, nfsService]} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={[]} connections={[connection]} credentials={[]} onDone={async () => {}} />)
    fireEvent.change(screen.getByRole('combobox', {name: 'Deployment'}), {target: {value: 'multi'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'lab'}})
    fireEvent.click(screen.getByRole('button', {name: 'Add cluster'}))
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    await waitFor(() => expect(screen.getByRole('spinbutton', {name: 'First VM ID'})).toHaveValue(300))
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('tab', {name: 'Pools'}))
    fireEvent.change(screen.getByRole('textbox', {name: 'Labels'}), {target: {value: 'hardware=gpu,tier=frontend'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    expect(container.querySelectorAll('form')).toHaveLength(1)
    expect(screen.queryByLabelText('Zones')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Create clusters'}))
    await waitFor(() => expect(fetch.mock.calls.filter(call => call[0] === '/api/v1/kubernetes-clusters')).toHaveLength(1))
    const call = fetch.mock.calls.find(call => call[0] === '/api/v1/kubernetes-clusters')!
    const body = JSON.parse(call[1].body)
    expect(body.mode).toBe('multi')
    expect(body.members.map((member: {name: string}) => member.name)).toEqual(['lab-cluster-1', 'lab-cluster-2', 'lab-cluster-3'])
    expect(body.members.map((member: {baseVMID: number}) => member.baseVMID)).toEqual([300, 304, 308])
    expect(body.members.map((member: {addressStart: string}) => member.addressStart)).toEqual(['10.0.0.10', '10.0.0.14', '10.0.0.18'])
    expect(body.members[0].applicationPools[0].labels).toEqual({hardware: 'gpu', tier: 'frontend'})
    expect(body.members[0].machineNodes['lab-cluster-1-control-plane-1']).toBe('pve')
  }, 15000)

  it('submits an independent capacity for every pool', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({id: 'cluster_created'}), {status: 202, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    render(<KubernetesClusterDialog open close={() => {}} workspaces={[workspace]} templates={[machineTemplate]} services={[harborService, nfsService]} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={[]} connections={[connection]} credentials={[]} onDone={async () => {}} />)

    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'development'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('tab', {name: 'Pools'}))
    fireEvent.change(screen.getByRole('spinbutton', {name: 'CPU cores'}), {target: {value: '6'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.change(screen.getByRole('spinbutton', {name: 'First VM ID'}), {target: {value: '200'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'First node IP'}), {target: {value: '10.10.0.20'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Gateway'}), {target: {value: '10.10.0.1'}})
    fireEvent.click(screen.getByText('Monitoring', {selector: 'summary'}))
    fireEvent.change(screen.getByRole('combobox', {name: 'Scraping period'}), {target: {value: '5s'}})
    fireEvent.change(screen.getByRole('combobox', {name: 'Metrics retention'}), {target: {value: '15d'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.change(screen.getByRole('textbox', {name: 'development-applications-2 · Proxmox server'}), {target: {value: 'pve-2'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('button', {name: 'Create cluster'}))

    await waitFor(() => expect(fetch.mock.calls.some(call => call[0] === '/api/v1/kubernetes-clusters')).toBe(true))
    const body = JSON.parse(fetch.mock.calls.find(call => call[0] === '/api/v1/kubernetes-clusters')![1].body)
    expect(body.machineNodes).toEqual({'development-control-plane-1': 'pve', 'development-management-1': 'pve', 'development-applications-1': 'pve', 'development-applications-2': 'pve-2'})
    expect(body.scrapeInterval).toBe('5s')
 expect(body.retention).toBe('15d')
 expect(body.controlPlaneCapacity).toEqual({cores: 2, memoryMiB: 4096, diskGiB: 80})
    expect(body.managementPool.capacity).toEqual({cores: 4, memoryMiB: 8192, diskGiB: 80})
    expect(body.applicationPools).toEqual([{name: 'applications', count: 2, zones: ['zone-a'], capacity: {cores: 6, memoryMiB: 8192, diskGiB: 80}}])
    expect(body).not.toHaveProperty('cores')
  })
})

describe('ApplicationDialog', () => {
  it('imports a validated application descriptor', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({reference: 'app:example.application'}), {status: 201, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const close = vi.fn()
    const onDone = vi.fn().mockResolvedValue(undefined)

    render(<ApplicationDialog open close={close} plugins={[]} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={[]} connections={[]} credentials={[]} onDone={onDone} />)

	const descriptor = 'apiVersion: catalog.kubephos.dev/v1alpha1\nkind: Application\n'
	const file = new File([descriptor], 'application.yaml', {type: 'application/yaml'})
	Object.defineProperty(file, 'text', {value: async () => descriptor})
	fireEvent.change(screen.getByLabelText('Application descriptor'), {target: {files: [file]}})
	fireEvent.submit(screen.getByRole('button', {name: 'Validate and import'}).closest('form')!)

    await waitFor(() => expect(fetch).toHaveBeenCalledOnce())
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/catalog/applications')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({descriptor})
    expect(close).toHaveBeenCalledOnce()
    expect(onDone).toHaveBeenCalledWith('Application validated and imported.')
  })
})

const proxmoxPlugin: Plugin = {
  id: 'io.kubephos.infrastructure.proxmox.discovery',
  provider: 'proxmox',
  name: 'Proxmox discovery',
  version: '0.1.0',
  description: 'Proxmox connection',
  capabilities: ['infrastructure.discovery'],
  runtime: {kind: 'process'},
  schema: {
    type: 'object',
    required: ['endpoint', 'credentialRef', 'verifyTLS'],
    properties: {
      endpoint: {type: 'string', title: 'Proxmox endpoint'},
      credentialRef: {type: 'string', title: 'API credential', format: 'kubephos-secret-ref', 'x-kubephos-secret-kind': 'proxmox-api-token'},
      verifyTLS: {type: 'boolean', title: 'Verify TLS certificate', default: true}
    }
  },
  credentialSchemas: [{
    kind: 'proxmox-api-token',
    name: 'Proxmox API token',
    description: 'Token ID and secret generated in Proxmox.',
    schema: {
      type: 'object',
      required: ['tokenId', 'tokenSecret'],
      properties: {
        tokenId: {type: 'string', title: 'Token ID'},
        tokenSecret: {type: 'string', title: 'Token secret', writeOnly: true}
      }
    }
  }]
}

const workspace: Workspace = {id: 'ws_default', name: 'Default', description: '', status: 'ready', createdAt: '2026-09-12T00:00:00Z'}
const connection: Connection = {id: 'conn_proxmox', name: 'Proxmox', provider: 'proxmox', pluginId: proxmoxPlugin.id, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
const machineTemplate: ManagedResource = {
  id: 'tmpl_ready', workspaceId: workspace.id, name: 'ubuntu', kind: 'machine-template', provider: 'proxmox', connectionId: connection.id,
  status: 'ready', validation: {valid: true, issues: []}, spec: {node: 'pve'}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'
}
const harborService: ManagedResource = {...machineTemplate, id: 'svc_harbor', name: 'harbor', kind: 'harbor', pipelineRunId: 'run_harbor', artifactId: 'art_harbor'}
const nfsService: ManagedResource = {...machineTemplate, id: 'svc_nfs', name: 'nfs', kind: 'nfs', pipelineRunId: 'run_nfs', artifactId: 'art_nfs'}

it('keeps Cloud image URL visible on the template step and retains it while navigating storage', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({}), {status: 200})))
  const plugin = {...proxmoxPlugin, capabilities: ['infrastructure.machine-template.provision'], schema: {type: 'object' as const, properties: {cloudImageURL: {type: 'string' as const, title: 'Cloud image URL'}, imageStorage: {type: 'string' as const, title: 'Cloud image storage'}}}}
  render(<MachineTemplateDialog open close={() => {}} plugins={[plugin]} workspaces={[workspace]} connections={[connection]} session={{authenticated: true}} applications={[]} artifacts={[]} credentials={[]} onDone={async () => {}} />)
  fireEvent.change(screen.getByLabelText('Cloud image URL'), {target: {value: 'https://example.org/image.img'}})
  fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'ubuntu'}})
  fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
  expect(screen.getByLabelText('Cloud image storage')).toBeVisible()
  fireEvent.click(screen.getByRole('button', {name: 'Previous'}))
  expect(screen.getByLabelText('Cloud image URL')).toHaveValue('https://example.org/image.img')
})
