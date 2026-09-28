import {useEffect, useMemo, useState, type Dispatch, type FormEvent, type SetStateAction} from 'react'
import {ApiError, request} from '../api'
import {readSchemaValues} from '../lib'
import type {Application, Artifact, Connection, Credential, CredentialDefinition, JsonSchema, ManagedResource, Plugin, PluginRuntimeProfile, PluginRuntimeStatus, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'
import {NumericInput} from './NumericInput'

interface CommonProps {
  session: Session
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
  onDone: (message: string) => Promise<void>
}

export function AuthDialog({setupRequired, onAuthenticated}: {setupRequired: boolean; onAuthenticated: (session: Session) => void}) {
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      if (!setupRequired) {
        window.location.assign('/api/v1/auth/login')
        return
      }
      const result = await request<Session>('/auth/setup', {
        method: 'POST',
        body: JSON.stringify({username: form.get('username'), email: form.get('email'), password: form.get('password')})
      })
      onAuthenticated(result)
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open title={setupRequired ? 'Create administrator' : 'Sign in'} className="auth-modal">
    <form onSubmit={submit}>
      <div className="auth-brand"><span className="brand-mark">K</span><strong>KubePhos</strong></div>
      {setupRequired && <><label>Username<input name="username" minLength={3} maxLength={40} autoComplete="username" required autoFocus /></label><label>Email<input name="email" type="email" autoComplete="email" /></label><label>Password<input name="password" type="password" minLength={12} maxLength={128} autoComplete="new-password" required /></label></>}
      <p className="form-error">{error}</p>
      <button className="button primary auth-submit" disabled={pending}>{pending ? 'Please wait…' : setupRequired ? 'Create administrator' : 'Sign in with Keycloak'}</button>
    </form>
  </Dialog>
}

export function WorkspaceDialog({open, close, ...common}: CommonProps & {open: boolean; close: () => void}) {
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      await request('/workspaces', {method: 'POST', body: JSON.stringify({name: form.get('name'), description: ''})}, common.session.csrfToken)
      close()
      await common.onDone('Workspace created.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create workspace">
    <form onSubmit={submit}>
      <label>Name<input name="name" maxLength={80} placeholder="Scheduler development" required autoFocus /></label>
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Create workspace" />
    </form>
  </Dialog>
}

export function OperationDialog({workspace, plugins, initialPluginID, initialSpec = {}, open, close, onCreated, ...common}: CommonProps & {workspace: Workspace | null; plugins: Plugin[]; initialPluginID?: string; initialSpec?: Record<string, unknown>; open: boolean; close: () => void; onCreated: (id: string) => Promise<void>}) {
	const [pluginID, setPluginID] = useState(initialPluginID ?? '')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const selected = plugins.find(item => item.id === pluginID) ?? plugins[0]
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!workspace || !selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    try {
      const result = await request<{id: string}>('/operations', {method: 'POST', body: JSON.stringify({
        workspaceId: workspace.id,
        pluginId: selected.id,
        title: new FormData(form).get('title'),
		spec: readSchemaValues(form, selected.schema, 'schema', common.applications)
      })}, common.session.csrfToken)
      close()
      await common.onDone('Validation passed. Review the plan before starting.')
      await onCreated(result.id)
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Configure operation">
    <form onSubmit={submit} key={`${workspace?.id ?? ''}-${selected?.id ?? ''}`}>
      <label>Capability<select name="pluginId" value={selected?.id ?? ''} onChange={event => setPluginID(event.target.value)} required>
        {plugins.map(plugin => <option key={plugin.id} value={plugin.id}>{plugin.name} · {plugin.version}</option>)}
      </select></label>
      <label>Operation name<input name="title" maxLength={120} defaultValue={operationTitle(selected, initialSpec)} required /></label>
      {selected && <SchemaFields schema={selected.schema} values={selected.id === initialPluginID ? initialSpec : {}} applications={common.applications} artifacts={common.artifacts.filter(item => item.workspaceId === workspace?.id)} connections={common.connections} credentials={common.credentials} />}
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate plan" />
    </form>
  </Dialog>
}

function operationTitle(plugin: Plugin | undefined, initialSpec: Record<string, unknown>): string {
  if (!plugin) return ''
  const primary = Object.entries(plugin.schema.properties ?? {}).find(([, property]) => property['x-kubephos-primary-action'])
  const value = primary ? initialSpec[primary[0]] : undefined
  return typeof value === 'string' && value ? `${value.slice(0, 1).toUpperCase()}${value.slice(1)} · ${plugin.name}` : plugin.name
}

export function CredentialDialog({open, close, plugins, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]}) {
  const definitions = useMemo(() => credentialDefinitions(plugins), [plugins])
  const [kind, setKind] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const selected = definitions.find(item => item.kind === kind) ?? definitions[0]
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    try {
      await request('/credentials', {method: 'POST', body: JSON.stringify({name: new FormData(form).get('name'), kind: selected.kind, value: readSchemaValues(form, selected.schema, 'credential')})}, common.session.csrfToken)
      close()
      await common.onDone('Credential encrypted and stored.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Add credential">
    <form onSubmit={submit} key={selected?.kind ?? ''}>
      <label>Credential type<select value={selected?.kind ?? ''} onChange={event => setKind(event.target.value)} required>{definitions.map(item => <option key={item.kind} value={item.kind}>{item.name}</option>)}</select></label>
      <label>Name<input name="name" maxLength={80} placeholder="Development infrastructure" required /></label>
      {selected && <SchemaFields schema={selected.schema} prefix="credential" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />}
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Encrypt and save" />
    </form>
  </Dialog>
}

export function ConnectionDialog({open, close, plugins, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]}) {
  const compatible = plugins.filter(item => item.provider && item.capabilities?.includes('infrastructure.discovery'))
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const [createdCredential, setCreatedCredential] = useState<Credential | null>(null)
  const selected = compatible.find(item => item.provider === 'proxmox') ?? compatible[0]
  const credentialInput = useMemo(() => connectionCredentialInput(selected), [selected])

  useEffect(() => {
    if (!open) return
    setCreatedCredential(null)
    setError('')
  }, [open, selected?.id])

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    const values = new FormData(form)
    let storedDuringSubmit = false
    try {
      const configuration: Record<string, unknown> = {endpoint: values.get('endpoint'), verifyTLS: values.get('verifyTLS') === 'on'}
      if (credentialInput) {
        let credentialRef = createdCredential?.id ?? ''
        if (!credentialRef) {
          const credential = await request<Credential>('/credentials', {method: 'POST', body: JSON.stringify({
            name: `${String(values.get('name') ?? '').trim()} API token`,
            kind: credentialInput.definition.kind,
            value: {tokenId: values.get('tokenId'), tokenSecret: values.get('tokenSecret')}
          })}, common.session.csrfToken)
          setCreatedCredential(credential)
          credentialRef = credential.id
          storedDuringSubmit = true
        }
        configuration[credentialInput.property] = credentialRef
      }
      await request('/connections', {method: 'POST', body: JSON.stringify({name: values.get('name'), description: '', pluginId: selected.id, configuration})}, common.session.csrfToken)
      close()
      await common.onDone('Provider connection validated and saved.')
    } catch (cause) {
      const message = validationMessage(cause)
      setError(storedDuringSubmit ? `The API credential was encrypted and saved. Correct the connection settings and retry; it will be reused. ${message}` : message)
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="New Proxmox connection">
    <form onSubmit={submit} key={selected?.id ?? ''}>
      <label>Name<input name="name" maxLength={80} placeholder="Development Proxmox" required /></label>
      <label>Endpoint<input name="endpoint" type="url" placeholder="https://proxmox.example:8006" required /></label>
      <label className="checkbox-field"><input name="verifyTLS" type="checkbox" defaultChecked /><span>Verify TLS</span></label>
      <label>Token ID<input name="tokenId" maxLength={240} required /></label>
      <label>Token secret<input name="tokenSecret" type="password" minLength={8} maxLength={512} required /></label>
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate and save" />
    </form>
  </Dialog>
}

export function MachineTemplateDialog({open, close, plugins, workspaces, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]; workspaces: Workspace[]}) {
  const plugin = plugins.find(item => item.capabilities?.includes('infrastructure.machine-template.provision'))
  const connections = common.connections.filter(item => item.provider === plugin?.provider)
  const schema = useMemo<JsonSchema>(() => {
    if (!plugin) return {type: 'object', properties: {}}
    const properties = Object.fromEntries(Object.entries(plugin.schema.properties ?? {}).filter(([name]) => name !== 'connectionRef' && name !== 'name'))
    return {...plugin.schema, required: plugin.schema.required?.filter(name => name !== 'connectionRef' && name !== 'name'), properties}
  }, [plugin])
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!plugin) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    const values = new FormData(form)
    try {
      await request('/machine-templates', {method: 'POST', body: JSON.stringify({
        workspaceId: workspaces[0]?.id,
        connectionId: values.get('connectionId'),
        name: values.get('name'),
        configuration: readSchemaValues(form, schema, 'template')
      })}, common.session.csrfToken)
      close()
      await common.onDone('Template validated and queued.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="New VM template" className="template-modal">
    {!plugin ? <div className="empty-state"><div><strong>Template capability unavailable</strong>Install a compatible infrastructure plugin.</div></div> : <form onSubmit={submit} key={plugin.id}>
      <label>Proxmox connection<select name="connectionId" required>{connections.length ? connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">Create a Proxmox connection first</option>}</select></label>
      <label>Template name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder="ubuntu-k3s" required autoFocus /></label>
      <div className="compact-schema"><SchemaFields schema={schema} prefix="template" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} /></div>
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} disabled={!connections.length || !workspaces.length} label="Validate and create" />
    </form>}
  </Dialog>
}

export function InfrastructureServiceDialog({open, close, workspaces, templates, initialKind = 'harbor', ...common}: CommonProps & {open: boolean; close: () => void; workspaces: Workspace[]; templates: ManagedResource[]; initialKind?: 'harbor' | 'nfs'}) {
  const defaultWorkspaceID = workspaces[0]?.id ?? ''
  const defaultConnectionID = common.connections[0]?.id ?? ''
  const [workspaceID, setWorkspaceID] = useState(defaultWorkspaceID)
  const [connectionID, setConnectionID] = useState(defaultConnectionID)
  const [kind, setKind] = useState<'harbor' | 'nfs'>(initialKind)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (!open) return
    setWorkspaceID(defaultWorkspaceID)
    setConnectionID(defaultConnectionID)
    setKind(initialKind)
    setError('')
  }, [open, defaultWorkspaceID, defaultConnectionID, initialKind])
  const availableTemplates = templates.filter(item => item.status === 'ready' && item.workspaceId === workspaceID && item.connectionId === connectionID)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const values = new FormData(event.currentTarget)
    try {
      await request('/infrastructure-services', {method: 'POST', body: JSON.stringify({
        workspaceId: workspaceID,
        connectionId: connectionID,
        templateId: values.get('templateId'),
        kind,
        name: values.get('name'),
        vmid: Number(values.get('vmid')),
        address: values.get('address'),
        prefixLength: Number(values.get('prefixLength')),
        gateway: values.get('gateway'),
        dnsServer: values.get('dnsServer'),
        cores: Number(values.get('cores')),
        memoryMiB: Number(values.get('memoryMiB')),
        diskGiB: Number(values.get('diskGiB'))
      })}, common.session.csrfToken)
      close()
      await common.onDone(`${kind === 'harbor' ? 'Harbor' : 'NFS'} validation passed and provisioning started.`)
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  const defaults = kind === 'harbor' ? {cores: 4, memory: 8192, disk: 100} : {cores: 2, memory: 4096, disk: 200}
  return <Dialog open={open} onClose={close} title={`New ${kind === 'harbor' ? 'Harbor registry' : 'NFS server'}`} className="template-modal">
    <form onSubmit={submit} key={`${kind}-${workspaceID}-${connectionID}`}>
      <label>Proxmox connection<select value={connectionID} onChange={event => setConnectionID(event.target.value)} required>{common.connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <label>VM template<select name="templateId" required>{availableTemplates.length ? availableTemplates.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready template for this environment</option>}</select></label>
      <div className="field-row"><label>Service name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder={kind === 'harbor' ? 'main-registry' : 'shared-data'} required autoFocus /></label><label>Proxmox VM ID<input name="vmid" type="number" min={100} max={999999999} required /></label></div>
      <div className="field-row"><label>IP address<input name="address" placeholder="192.168.1.120" required /></label><label>Prefix length<input name="prefixLength" type="number" min={8} max={30} defaultValue={24} required /></label></div>
      <div className="field-row"><label>Gateway<input name="gateway" placeholder="192.168.1.1" required /></label><label>DNS server<input name="dnsServer" defaultValue="1.1.1.1" required /></label></div>
      <div className="capacity-grid"><label>CPU cores<input name="cores" type="number" min={1} max={32} defaultValue={defaults.cores} /></label><label>Memory MiB<input name="memoryMiB" type="number" min={512} max={131072} defaultValue={defaults.memory} /></label><label>Disk GiB<input name="diskGiB" type="number" min={8} max={2048} defaultValue={defaults.disk} /></label></div>
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} disabled={!availableTemplates.length || !workspaceID || !connectionID} label={`Create ${kind === 'harbor' ? 'Harbor' : 'NFS'}`} />
    </form>
  </Dialog>
}

type MachineCapacityDraft = {cores: number; memoryMiB: number; diskGiB: number}
type ClusterPoolDraft = {name: string; count: number; zones: string; capacity: MachineCapacityDraft}

const applicationPoolDefaults = (): ClusterPoolDraft => ({name: 'applications', count: 2, zones: 'zone-a', capacity: {cores: 4, memoryMiB: 8192, diskGiB: 80}})

export function KubernetesClusterDialog({open, close, workspaces, templates, services, ...common}: CommonProps & {open: boolean; close: () => void; workspaces: Workspace[]; templates: ManagedResource[]; services: ManagedResource[]}) {
  const defaultWorkspaceID = workspaces[0]?.id ?? ''
  const defaultConnectionID = common.connections[0]?.id ?? ''
  const [workspaceID, setWorkspaceID] = useState(defaultWorkspaceID)
  const [connectionID, setConnectionID] = useState(defaultConnectionID)
  const [applicationPools, setApplicationPools] = useState<ClusterPoolDraft[]>([applicationPoolDefaults()])
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (!open) return
    setWorkspaceID(defaultWorkspaceID)
    setConnectionID(defaultConnectionID)
    setApplicationPools([applicationPoolDefaults()])
    setError('')
  }, [open, defaultWorkspaceID, defaultConnectionID])
  const compatible = (item: ManagedResource) => item.status === 'ready' && item.workspaceId === workspaceID && item.connectionId === connectionID
  const availableTemplates = templates.filter(compatible)
  const harbor = services.filter(item => item.kind === 'harbor' && compatible(item))
  const nfs = services.filter(item => item.kind === 'nfs' && compatible(item))
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const values = new FormData(event.currentTarget)
    try {
      await request('/kubernetes-clusters', {method: 'POST', body: JSON.stringify({
        workspaceId: workspaceID,
        connectionId: connectionID,
        templateId: values.get('templateId'),
        harborId: values.get('harborId'),
        nfsId: values.get('nfsId'),
        name: values.get('name'),
        baseVMID: Number(values.get('baseVMID')),
        addressStart: values.get('addressStart'),
        prefixLength: Number(values.get('prefixLength')),
        gateway: values.get('gateway'),
        dnsServer: values.get('dnsServer'),
        controlPlanes: Number(values.get('controlPlanes')),
        controlPlaneZones: parseZones(String(values.get('controlPlaneZones') ?? '')),
        controlPlaneCapacity: readCapacity(values, 'controlPlane'),
        managementPool: {name: 'management', count: Number(values.get('managementCount')), zones: parseZones(String(values.get('managementZones') ?? '')), capacity: readCapacity(values, 'management')},
        applicationPools: applicationPools.map(pool => ({name: pool.name, count: pool.count, zones: parseZones(pool.zones), capacity: pool.capacity}))
      })}, common.session.csrfToken)
      close()
      await common.onDone('Cluster validation passed and provisioning started.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  const ready = availableTemplates.length > 0 && harbor.length > 0 && nfs.length > 0
  return <Dialog open={open} onClose={close} title="Create Kubernetes cluster" className="template-modal cluster-modal">
    <form onSubmit={submit}>
      <div className="form-section"><strong>Infrastructure</strong></div>
      {workspaces.length > 1 ? <label>Environment<select value={workspaceID} onChange={event => setWorkspaceID(event.target.value)} required>{workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : <input type="hidden" value={workspaceID} readOnly />}
      <label>Proxmox connection<select value={connectionID} onChange={event => setConnectionID(event.target.value)} required>{common.connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <div className="field-row"><label>VM template<select name="templateId" required>{availableTemplates.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>Harbor registry<select name="harborId" required>{harbor.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
      <label>NFS storage<select name="nfsId" required>{nfs.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
      <div className="form-section"><strong>Cluster identity</strong></div>
      <div className="field-row"><label>Cluster name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder="development" required autoFocus /></label><label>First VM ID<input name="baseVMID" type="number" min={100} max={999999988} required /></label></div>
      <div className="form-section"><strong>Cluster node network</strong></div>
      <div className="field-row"><label>First node IP<input name="addressStart" placeholder="192.168.1.130" required /></label><label>Prefix length<input name="prefixLength" type="number" min={8} max={30} defaultValue={24} required /></label></div>
      <div className="field-row"><label>Gateway<input name="gateway" placeholder="192.168.1.1" required /></label><label>DNS server<input name="dnsServer" defaultValue="1.1.1.1" required /></label></div>
      <div className="form-section"><strong>Control plane</strong></div>
      <div className="field-row"><label>Nodes<select name="controlPlanes" defaultValue="1"><option value="1">1 · development</option><option value="3">3 · high availability</option></select></label><label>Zones<input name="controlPlaneZones" defaultValue="zone-a" pattern="[a-z0-9,-]+" required /></label></div>
      <MachineCapacityFields prefix="controlPlane" defaults={{cores: 2, memoryMiB: 4096, diskGiB: 80}} />
      <div className="form-section"><strong>Management pool</strong></div>
      <div className="field-row"><label>Nodes<input name="managementCount" type="number" min={1} max={12} defaultValue={1} required /></label><label>Zones<input name="managementZones" defaultValue="zone-a" pattern="[a-z0-9,-]+" required /></label></div>
      <MachineCapacityFields prefix="management" defaults={{cores: 4, memoryMiB: 8192, diskGiB: 80}} />
      <div className="form-section pool-section"><strong>Application pools</strong><button className="button secondary compact" type="button" disabled={applicationPools.length >= 8} onClick={() => setApplicationPools(items => [...items, {...applicationPoolDefaults(), name: `applications-${items.length + 1}`, count: 1}])}>Add pool</button></div>
      <div className="pool-editor">{applicationPools.map((pool, index) => <div className="pool-card" key={index}><div className="pool-row"><label>Pool name<input value={pool.name} pattern="[a-z0-9][a-z0-9-]{0,31}" onChange={event => updateApplicationPool(setApplicationPools, index, {name: event.target.value})} required /></label><label>Nodes<NumericInput value={pool.count} min={1} max={12} onValueChange={count => updateApplicationPool(setApplicationPools, index, {count})} required /></label><label>Zones<input value={pool.zones} pattern="[a-z0-9,-]+" onChange={event => updateApplicationPool(setApplicationPools, index, {zones: event.target.value})} required /></label>{applicationPools.length > 1 && <button type="button" className="icon-button pool-remove" aria-label={`Remove ${pool.name}`} onClick={() => setApplicationPools(items => items.filter((_, position) => position !== index))}>×</button>}</div><div className="capacity-grid"><label>CPU cores<NumericInput value={pool.capacity.cores} min={1} max={32} onValueChange={cores => updateApplicationPoolCapacity(setApplicationPools, index, {cores})} required /></label><label>Memory MiB<NumericInput value={pool.capacity.memoryMiB} min={1024} max={131072} onValueChange={memoryMiB => updateApplicationPoolCapacity(setApplicationPools, index, {memoryMiB})} required /></label><label>Disk GiB<NumericInput value={pool.capacity.diskGiB} min={8} max={2048} onValueChange={diskGiB => updateApplicationPoolCapacity(setApplicationPools, index, {diskGiB})} required /></label></div></div>)}</div>
      {!ready && <p className="form-hint">A ready VM template, Harbor service and NFS service are required on the same connection.</p>}
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} disabled={!ready || !workspaceID || !connectionID} label="Validate and create" />
    </form>
  </Dialog>
}

function MachineCapacityFields({prefix, defaults}: {prefix: string; defaults: MachineCapacityDraft}) {
  return <div className="capacity-grid"><label>CPU cores<input name={`${prefix}Cores`} type="number" min={1} max={32} defaultValue={defaults.cores} required /></label><label>Memory MiB<input name={`${prefix}MemoryMiB`} type="number" min={1024} max={131072} defaultValue={defaults.memoryMiB} required /></label><label>Disk GiB<input name={`${prefix}DiskGiB`} type="number" min={8} max={2048} defaultValue={defaults.diskGiB} required /></label></div>
}

function readCapacity(values: FormData, prefix: string): MachineCapacityDraft {
  return {cores: Number(values.get(`${prefix}Cores`)), memoryMiB: Number(values.get(`${prefix}MemoryMiB`)), diskGiB: Number(values.get(`${prefix}DiskGiB`))}
}

function updateApplicationPool(setPools: Dispatch<SetStateAction<ClusterPoolDraft[]>>, index: number, change: Partial<Omit<ClusterPoolDraft, 'capacity'>>) {
  setPools(items => items.map((item, position) => position === index ? {...item, ...change} : item))
}

function updateApplicationPoolCapacity(setPools: Dispatch<SetStateAction<ClusterPoolDraft[]>>, index: number, change: Partial<MachineCapacityDraft>) {
  setPools(items => items.map((item, position) => position === index ? {...item, capacity: {...item.capacity, ...change}} : item))
}

function parseZones(value: string): string[] {
  return [...new Set(value.split(',').map(item => item.trim()).filter(Boolean))]
}

export function ApplicationDialog({open, close, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]}) {
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (open) {
      setFile(null)
      setError('')
    }
  }, [open])
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!file) return
    if (file.size > 512 * 1024) {
      setError('The descriptor cannot exceed 512 KB.')
      return
    }
    setPending(true)
    setError('')
    try {
      await request('/catalog/applications', {method: 'POST', body: JSON.stringify({descriptor: await file.text()})}, common.session.csrfToken)
      close()
      await common.onDone('Application validated and imported.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Import application" className="template-modal">
    <form onSubmit={submit}>
      <label>Application descriptor<input type="file" accept=".yaml,.yml,.json,application/yaml,application/json" required onChange={event => setFile(event.target.files?.[0] ?? null)} /></label>
      {file && <span className="field-description">{file.name} · {Math.ceil(file.size / 1024)} KB</span>}
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate and import" />
    </form>
  </Dialog>
}

export function PluginDialog({open, close, ...common}: CommonProps & {open: boolean; close: () => void}) {
  const [file, setFile] = useState<File | null>(null)
  const [descriptor, setDescriptor] = useState('')
  const [preview, setPreview] = useState<{manifest: Plugin; executorAvailable: boolean; policyAccepted: boolean; policyMessage: string} | null>(null)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const dismiss = () => {
    setFile(null)
    setDescriptor('')
    setPreview(null)
    setError('')
    close()
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!file) return
    if (file.size > 512 * 1024) {
      setError('The descriptor cannot exceed 512 KB.')
      return
    }
    setPending(true)
    setError('')
    try {
      const value = descriptor || await file.text()
      if (!preview) {
        const result = await request<{manifest: Plugin; executorAvailable: boolean; policyAccepted: boolean; policyMessage: string}>('/plugins/inspect', {method: 'POST', body: JSON.stringify({descriptor: value})}, common.session.csrfToken)
        setDescriptor(value)
        setPreview(result)
        return
      }
      await request('/plugins', {method: 'POST', body: JSON.stringify({descriptor: value})}, common.session.csrfToken)
      dismiss()
      await common.onDone('Plugin import queued. Progress is visible in the Plugins view.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={dismiss} title="Import plugin">
    <form onSubmit={submit} key={file?.name ?? 'empty'}>
      <label>Plugin descriptor<input type="file" accept=".yaml,.yml,application/yaml" required onChange={event => {setFile(event.target.files?.[0] ?? null); setDescriptor(''); setPreview(null); setError('')}} /></label>
      {file && <span className="field-description">{file.name} · {Math.ceil(file.size / 1024)} KB</span>}
      {preview && <div className="notice"><strong>{preview.manifest.name} · {preview.manifest.version}</strong><p>{preview.manifest.id}</p><p className="digest">{preview.manifest.runtime.reference}</p><p>{preview.manifest.permissions?.length ? `Permissions: ${preview.manifest.permissions.join(', ')}` : 'No permissions requested.'}</p><p>{preview.manifest.capabilities?.length ? `Capabilities: ${preview.manifest.capabilities.join(', ')}` : 'No capabilities declared.'}</p><p className={preview.policyAccepted ? 'field-description' : 'form-error'}>{preview.policyMessage}</p></div>}
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={dismiss}>Cancel</button><button className="button primary" disabled={pending || Boolean(preview && (!preview.executorAvailable || !preview.policyAccepted))}>{pending ? 'Queuing…' : preview ? 'Queue validation and activation' : 'Review plugin'}</button></div>
    </form>
  </Dialog>
}

export function RuntimeDialog({open, close, runtime, ...common}: CommonProps & {open: boolean; close: () => void; runtime: PluginRuntimeStatus | null}) {
  const executorEndpoints = common.artifacts.filter(item => !item.sensitive && item.type === 'OCIExecutorEndpoint' && item.version === 'v1alpha1')
  const executorCredentials = common.artifacts.filter(item => item.sensitive && item.type === 'OCIExecutorCredential' && item.version === 'v1alpha1')
  const registryEndpoints = common.artifacts.filter(item => !item.sensitive && item.type === 'RegistryEndpoint' && item.version === 'v1alpha1')
  const registryCredentials = common.artifacts.filter(item => item.sensitive && item.type === 'RegistryCredential' && item.version === 'v1alpha1')
  const [profile, setProfile] = useState<PluginRuntimeProfile>({endpointArtifactId: '', credentialArtifactId: '', registries: [{endpointArtifactId: '', credentialArtifactId: ''}]})
  const [validated, setValidated] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)

  useEffect(() => {
    if (!open) return
    setProfile(runtime?.profile ? {...runtime.profile, registries: runtime.profile.registries.map(item => ({...item}))} : {endpointArtifactId: '', credentialArtifactId: '', registries: [{endpointArtifactId: '', credentialArtifactId: ''}]})
    setValidated('')
    setMessage('')
    setError('')
  }, [open, runtime])

  const changeProfile = (next: PluginRuntimeProfile) => {
    setProfile(next)
    setValidated('')
    setMessage('')
    setError('')
  }
  const chooseExecutor = (artifactId: string) => {
    const endpoint = executorEndpoints.find(item => item.id === artifactId)
    const credential = executorCredentials.find(item => item.operationId === endpoint?.operationId)
    changeProfile({...profile, endpointArtifactId: artifactId, credentialArtifactId: credential?.id ?? ''})
  }
  const chooseRegistry = (index: number, artifactId: string) => {
    const endpoint = registryEndpoints.find(item => item.id === artifactId)
    const credential = registryCredentials.find(item => item.operationId === endpoint?.operationId)
    const registries = profile.registries.map((item, position) => position === index ? {endpointArtifactId: artifactId, credentialArtifactId: credential?.id ?? ''} : item)
    changeProfile({...profile, registries})
  }
  const updateRegistryCredential = (index: number, artifactId: string) => {
    changeProfile({...profile, registries: profile.registries.map((item, position) => position === index ? {...item, credentialArtifactId: artifactId} : item)})
  }
  const availableRegistryEndpoints = (index: number) => {
    const selected = new Set(profile.registries.filter((_, position) => position !== index).map(item => item.endpointArtifactId))
    return registryEndpoints.filter(item => !selected.has(item.id))
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const payload = JSON.stringify({endpointArtifactId: profile.endpointArtifactId, credentialArtifactId: profile.credentialArtifactId, registries: profile.registries})
    try {
      if (validated !== payload) {
        const result = await request<{message: string}>('/plugin-runtime/validate', {method: 'POST', body: payload}, common.session.csrfToken)
        setValidated(payload)
        setMessage(result.message)
      } else {
        await request('/plugin-runtime/activate', {method: 'POST', body: payload}, common.session.csrfToken)
        close()
        await common.onDone('Managed OCI runtime activated.')
      }
    } catch (cause) {
      setValidated('')
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  const deactivate = async () => {
    if (!window.confirm('Disable external OCI plugin execution? Executor and registry infrastructure will be preserved.')) return
    setPending(true)
    setError('')
    try {
      await request('/plugin-runtime/deactivate', {method: 'POST', body: '{}'}, common.session.csrfToken)
      close()
      await common.onDone('External OCI plugin execution disabled.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  const executorEndpoint = executorEndpoints.find(item => item.id === profile.endpointArtifactId)
  const compatibleExecutorCredentials = executorCredentials.filter(item => !profile.endpointArtifactId || item.operationId === executorEndpoint?.operationId)
  const executorAvailable = Boolean(executorEndpoint && compatibleExecutorCredentials.some(item => item.id === profile.credentialArtifactId))
  const registriesAvailable = profile.registries.length > 0 && profile.registries.every(registry => {
    const endpoint = registryEndpoints.find(item => item.id === registry.endpointArtifactId)
    return Boolean(endpoint && registryCredentials.some(item => item.id === registry.credentialArtifactId && item.operationId === endpoint.operationId))
  })
  const ready = executorAvailable && registriesAvailable
  return <Dialog open={open} onClose={close} title="Configure OCI runtime" className="runtime-modal">
    <form onSubmit={submit}>
      <div className="field-row">
        <label>Executor endpoint<select value={profile.endpointArtifactId} required onChange={event => chooseExecutor(event.target.value)}><option value="">Select endpoint</option><UnavailableArtifactOption id={profile.endpointArtifactId} artifacts={executorEndpoints} />{executorEndpoints.map(item => <option key={item.id} value={item.id}>{artifactLabel(item)}</option>)}</select></label>
        <label>Executor credential<select value={profile.credentialArtifactId} required onChange={event => changeProfile({...profile, credentialArtifactId: event.target.value})}><option value="">Select credential</option><UnavailableArtifactOption id={profile.credentialArtifactId} artifacts={compatibleExecutorCredentials} />{compatibleExecutorCredentials.map(item => <option key={item.id} value={item.id}>{artifactLabel(item)}</option>)}</select></label>
      </div>
      <div className="runtime-registry-heading"><strong>Allowed registries</strong><button className="text-button" type="button" disabled={profile.registries.length >= 16 || profile.registries.length >= registryEndpoints.length} onClick={() => changeProfile({...profile, registries: [...profile.registries, {endpointArtifactId: '', credentialArtifactId: ''}]})}>Add registry +</button></div>
      {profile.registries.map((registry, index) => { const compatibleRegistryCredentials = registryCredentials.filter(item => !registry.endpointArtifactId || item.operationId === registryEndpoints.find(endpoint => endpoint.id === registry.endpointArtifactId)?.operationId); return <div className="runtime-registry-row" key={index}>
        <label>Registry endpoint<select value={registry.endpointArtifactId} required onChange={event => chooseRegistry(index, event.target.value)}><option value="">Select registry</option><UnavailableArtifactOption id={registry.endpointArtifactId} artifacts={registryEndpoints} />{availableRegistryEndpoints(index).map(item => <option key={item.id} value={item.id}>{artifactLabel(item)}</option>)}</select></label>
        <label>Pull credential<select value={registry.credentialArtifactId} required onChange={event => updateRegistryCredential(index, event.target.value)}><option value="">Select credential</option><UnavailableArtifactOption id={registry.credentialArtifactId} artifacts={compatibleRegistryCredentials} />{compatibleRegistryCredentials.map(item => <option key={item.id} value={item.id}>{artifactLabel(item)}</option>)}</select></label>
        {profile.registries.length > 1 && <button className="icon-button runtime-remove" type="button" aria-label="Remove registry" onClick={() => changeProfile({...profile, registries: profile.registries.filter((_, position) => position !== index)})}>×</button>}
      </div>})}
      {(!executorEndpoints.length || !registryEndpoints.length) && <div className="validation-item warning">Managed executor and registry required.</div>}
      {message && <div className="validation-item">{message}</div>}
      <p className="form-error">{error}</p>
      <div className="modal-actions">{runtime?.configured && <button className="button danger" type="button" disabled={pending} onClick={deactivate}>Disable</button>}<button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !ready}>{pending ? 'Checking…' : validated === JSON.stringify({endpointArtifactId: profile.endpointArtifactId, credentialArtifactId: profile.credentialArtifactId, registries: profile.registries}) ? 'Activate runtime' : 'Validate configuration'}</button></div>
    </form>
  </Dialog>
}

function Actions({close, pending, disabled = false, label}: {close: () => void; pending: boolean; disabled?: boolean; label: string}) {
  return <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || disabled}>{pending ? 'Please wait…' : label}</button></div>
}

function artifactLabel(artifact: Artifact): string {
  return `${artifact.name} · ${artifact.id.slice(0, 14)}…`
}

function UnavailableArtifactOption({id, artifacts}: {id: string; artifacts: Artifact[]}) {
  return id && !artifacts.some(item => item.id === id) ? <option value={id} disabled>{id} · unavailable</option> : null
}

function credentialDefinitions(plugins: Plugin[]): CredentialDefinition[] {
  const definitions = new Map<string, CredentialDefinition>()
  for (const plugin of plugins) for (const definition of plugin.credentialSchemas ?? []) definitions.set(definition.kind, definition)
  return [...definitions.values()].sort((left, right) => left.name.localeCompare(right.name))
}

function connectionCredentialInput(plugin: Plugin | undefined): {property: string; definition: CredentialDefinition} | null {
  if (!plugin) return null
  for (const [property, schema] of Object.entries(plugin.schema.properties ?? {})) {
    if (schema.format !== 'kubephos-secret-ref') continue
    const kind = schema['x-kubephos-secret-kind']
    const definition = plugin.credentialSchemas?.find(item => item.kind === kind)
    if (definition) return {property, definition}
  }
  return null
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : 'The request could not be completed.'
}

function validationMessage(cause: unknown): string {
  if (!(cause instanceof ApiError)) return errorMessage(cause)
  const validation = cause.body.validation as {issues?: Array<{message?: string}>} | undefined
  const issues = validation?.issues ?? cause.body.issues as Array<{message?: string}> | undefined
  return issues?.map(issue => issue.message).filter(Boolean).join(' ') || cause.message
}
