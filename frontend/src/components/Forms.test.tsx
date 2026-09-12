import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest'
import type {Artifact, Connection, ManagedResource, Plugin, Workspace} from '../types'
import {ConnectionDialog, InfrastructureServiceDialog, RuntimeDialog} from './Forms'

const artifacts: Artifact[] = [
  artifact('art_executor_endpoint', 'op_executor', 'executor-endpoint', 'OCIExecutorEndpoint', false),
  artifact('art_executor_credential', 'op_executor', 'executor-credential', 'OCIExecutorCredential', true),
  artifact('art_registry_one', 'op_registry_one', 'registry-one', 'RegistryEndpoint', false),
  artifact('art_registry_one_pull', 'op_registry_one', 'registry-one-pull', 'RegistryCredential', true),
  artifact('art_registry_two', 'op_registry_two', 'registry-two', 'RegistryEndpoint', false),
  artifact('art_registry_two_pull', 'op_registry_two', 'registry-two-pull', 'RegistryCredential', true),
]

afterEach(() => vi.unstubAllGlobals())

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

describe('RuntimeDialog', () => {
  it('validates the resolved profile before activation', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({valid: true, message: 'All runtime gates passed.'}), {status: 200, headers: {'Content-Type': 'application/json'}}))
      .mockResolvedValueOnce(new Response(JSON.stringify({configured: true}), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const close = vi.fn()
    const onDone = vi.fn().mockResolvedValue(undefined)
    render(<RuntimeDialog open close={close} runtime={null} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={artifacts} connections={[]} credentials={[]} onDone={onDone} />)

    fireEvent.change(screen.getByRole('combobox', {name: 'Executor endpoint'}), {target: {value: 'art_executor_endpoint'}})
    fireEvent.change(screen.getByRole('combobox', {name: 'Registry endpoint'}), {target: {value: 'art_registry_one'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate configuration'}))

    await screen.findByText('All runtime gates passed.')
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/plugin-runtime/validate')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      endpointArtifactId: 'art_executor_endpoint',
      credentialArtifactId: 'art_executor_credential',
      registries: [{endpointArtifactId: 'art_registry_one', credentialArtifactId: 'art_registry_one_pull'}],
    })

    fireEvent.click(screen.getByRole('button', {name: 'Activate runtime'}))
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/plugin-runtime/activate')
    expect(close).toHaveBeenCalledOnce()
    expect(onDone).toHaveBeenCalledWith('Managed OCI runtime activated.')
  })

  it('does not offer the same registry endpoint twice', () => {
    render(<RuntimeDialog open close={() => {}} runtime={null} session={{authenticated: true}} applications={[]} artifacts={artifacts} connections={[]} credentials={[]} onDone={async () => {}} />)
    fireEvent.change(screen.getByRole('combobox', {name: 'Registry endpoint'}), {target: {value: 'art_registry_one'}})
    fireEvent.click(screen.getByRole('button', {name: 'Add registry +'}))
    const selectors = screen.getAllByRole('combobox', {name: 'Registry endpoint'})
    expect(selectors).toHaveLength(2)
    expect(selectors[1].querySelector('option[value="art_registry_one"]')).not.toBeInTheDocument()
    expect(selectors[1].querySelector('option[value="art_registry_two"]')).toBeInTheDocument()
  })
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
    fireEvent.change(screen.getByRole('textbox', {name: 'Credential name'}), {target: {value: 'Lab token'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Token ID'}), {target: {value: 'root@pam!kubephos'}})
    fireEvent.change(screen.getByLabelText('Token secret'), {target: {value: 'secret-value'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Proxmox endpoint'}), {target: {value: 'https://proxmox.test:8006'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate and save'}))

    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({name: 'Lab token', kind: 'proxmox-api-token', value: {tokenId: 'root@pam!kubephos', tokenSecret: 'secret-value'}})
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toMatchObject({
      name: 'Lab Proxmox',
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
    const view = render(<InfrastructureServiceDialog {...props} />)

    fireEvent.click(screen.getByRole('button', {name: /NFS/}))
    fireEvent.change(screen.getByRole('textbox', {name: 'Service name'}), {target: {value: 'shared-data'}})
    expect(screen.getByRole('button', {name: 'Create NFS'})).toBeInTheDocument()

    view.rerender(<InfrastructureServiceDialog {...props} workspaces={[{...workspace}]} connections={[{...connection}]} templates={[{...machineTemplate}]} />)

    await waitFor(() => expect(screen.getByRole('button', {name: 'Create NFS'})).toBeInTheDocument())
    expect(screen.getByRole('textbox', {name: 'Service name'})).toHaveValue('shared-data')
  })
})

function artifact(id: string, operationId: string, name: string, type: string, sensitive: boolean): Artifact {
  return {id, operationId, name, type, version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:test', sizeBytes: 1, sensitive, verifiedAt: '2026-09-10T00:00:00Z'}
}

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
  status: 'ready', validation: {valid: true, issues: []}, spec: {}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'
}
