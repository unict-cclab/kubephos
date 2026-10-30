import {ResourceAction} from './ResourceAction'
import {ActionIcon} from './ActionIcon'
import {useEffect, useMemo, useState, type Dispatch, type FormEvent, type SetStateAction} from 'react'
import {ApiError, request} from '../api'
import {readSchemaValues} from '../lib'
import type {Application, Artifact, Connection, Credential, CredentialDefinition, JsonSchema, ManagedResource, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'
import {NumericInput} from './NumericInput'
import {useProvisioningSuggestions} from '../hooks/useProvisioningSuggestions'
import {MultiClusterForm} from './MultiClusterForm'
import {WizardForm, WizardStep, WizardReview} from './WizardForm'
import {FormTabs} from './FormTabs'
import {ProxmoxNodeSelect} from './ProxmoxNodeSelect'

export interface CommonProps {
  session: Session
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
  onDone: (message: string) => Promise<void>
}

export function AdministratorSetupDialog({onAuthenticated}: {onAuthenticated: (session: Session) => void}) {
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
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
  return <Dialog open title="Create administrator" className="auth-modal">
    <form onSubmit={submit}>
      <div className="auth-brand"><span className="brand-mark">K</span><strong>KubePhos</strong></div><p className="section-copy">Create the first administrator account to configure KubePhos.</p>
      <><label>Username<input name="username" minLength={3} maxLength={40} autoComplete="username" required autoFocus /></label><label>Email<input name="email" type="email" autoComplete="email" /></label><label>Password<input name="password" type="password" minLength={12} maxLength={128} autoComplete="new-password" required /></label></>
      <p className="form-error">{error}</p>
      <button className="button primary auth-submit" disabled={pending}>{pending ? 'Creating account…' : 'Create administrator'}</button>
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
    <form className="form-stack" onSubmit={submit} key={selected?.id ?? ''}>
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
  const [connectionID, setConnectionID] = useState('')
  const selectedConnectionID = connectionID || connections[0]?.id || ''
  const allocation = useProvisioningSuggestions(open, selectedConnectionID)
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
      await common.onDone('Template creation started.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="New VM template" className="template-modal" presentation="page">
    {!plugin ? <div className="empty-state"><div><strong>Template capability unavailable</strong></div></div> : <WizardForm onSubmit={submit} key={plugin.id} onCancel={close} pending={pending} error={error} submitDisabled={!connections.length || !workspaces.length} submitLabel="Create template">
      <WizardStep title="General">
      <div className="field-row"><label>Proxmox connection<select name="connectionId" value={selectedConnectionID} onChange={event => setConnectionID(event.target.value)} required>{connections.length ? connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">Create a Proxmox connection first</option>}</select></label>
      <label>Name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder="ubuntu-k3s" required /></label></div>
        <SchemaFields key={selectedConnectionID} suggestions={allocation.values} schema={schemaSubset(schema, ['vmid','storage','imageStorage','diskGiB','bridge','address','prefixLength','gateway','dnsServer'], true)} prefix="template" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />
      </WizardStep>
      <WizardStep title="Storage">
        <SchemaFields suggestions={allocation.values} schema={schemaSubset(schema, ['vmid','diskGiB','storage','imageStorage'])} prefix="template" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />
      </WizardStep>
      <WizardStep title="Network">
        <SchemaFields layout="network" suggestions={allocation.values} schema={schemaSubset(schema, ['address','prefixLength','gateway','dnsServer','bridge'])} prefix="template" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />
      </WizardStep>
      <WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm>}
  </Dialog>
}

export function InfrastructureServiceDialog({open, close, workspaces, templates, initialKind = 'harbor', ...common}: CommonProps & {open: boolean; close: () => void; workspaces: Workspace[]; templates: ManagedResource[]; initialKind?: 'harbor' | 'nfs'}) {
  const defaultWorkspaceID = workspaces[0]?.id ?? ''
  const defaultConnectionID = common.connections[0]?.id ?? ''
  const [connectionID, setConnectionID] = useState(defaultConnectionID)
  const [kind, setKind] = useState<'harbor' | 'nfs'>(initialKind)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (!open) return
    setConnectionID(defaultConnectionID)
    setKind(initialKind)
    setError('')
  }, [open, defaultWorkspaceID, defaultConnectionID, initialKind])
  const availableTemplates = templates.filter(item => item.status === 'ready' && item.connectionId === connectionID)
  const [templateID, setTemplateID] = useState('')
  const selectedTemplateID = availableTemplates.find(item => item.id === templateID)?.id ?? availableTemplates[0]?.id ?? ''
  const workspaceID = availableTemplates.find(item => item.id === selectedTemplateID)?.workspaceId ?? defaultWorkspaceID
  const allocation = useProvisioningSuggestions(open, connectionID, selectedTemplateID)
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
        node: values.get('node'),
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
  return <Dialog open={open} onClose={close} title={`New ${kind === 'harbor' ? 'Harbor registry' : 'NFS server'}`} className="template-modal" presentation="page">
    <WizardForm onSubmit={submit} key={`${kind}-${workspaceID}-${connectionID}`} onCancel={close} pending={pending} error={error} submitDisabled={!availableTemplates.length || !workspaceID || !connectionID} submitLabel={`Create ${kind === 'harbor' ? 'Harbor' : 'NFS'}`}>
      <WizardStep title="General">
<div className="field-row"><label>Name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder={kind === 'harbor' ? 'main-registry' : 'shared-data'} required autoFocus /></label>
      <label>Proxmox connection<select value={connectionID} onChange={event => setConnectionID(event.target.value)} required>{common.connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
      <div className="field-row"><label>VM template<select name="templateId" value={selectedTemplateID} onChange={event => setTemplateID(event.target.value)} required>{availableTemplates.length ? availableTemplates.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready VM template</option>}</select></label>
      <ProxmoxNodeSelect connectionID={connectionID} defaultNode={String(availableTemplates.find(item => item.id === selectedTemplateID)?.spec.node ?? '')} /></div>
      </WizardStep>
      <WizardStep title="Resources">
      <div className="capacity-grid"><label>CPU (cores)<input name="cores" type="number" min={1} max={32} defaultValue={defaults.cores} /></label><label>Memory (MiB)<input name="memoryMiB" type="number" min={512} max={131072} defaultValue={defaults.memory} /></label><label>Disk (GiB)<input name="diskGiB" type="number" min={8} max={2048} defaultValue={defaults.disk} /></label></div>
      </WizardStep>
      <WizardStep title="Network">
<label className="field-short">Proxmox VM ID<input name="vmid" {...allocation.field('vmid')} type="number" min={100} max={999999999} required /></label>
      <div className="field-row field-row-address"><label>IP address<input name="address" {...allocation.field('address')} placeholder="192.168.1.120" required /></label><label>Prefix length<input name="prefixLength" {...allocation.field('prefixLength', 24)} type="number" min={8} max={30} required /></label></div>
      <div className="field-row field-row-network"><label>Gateway<input name="gateway" {...allocation.field('gateway')} placeholder="192.168.1.1" required /></label><label>DNS server<input name="dnsServer" {...allocation.field('dnsServer', '1.1.1.1')} required /></label></div>
      </WizardStep>
      <WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm>
  </Dialog>
}

type MachineCapacityDraft = {cores: number; memoryMiB: number; diskGiB: number}
type ClusterPoolDraft = {name: string; count: number; zones: string; capacity: MachineCapacityDraft}

const applicationPoolDefaults = (): ClusterPoolDraft => ({name: 'applications', count: 2, zones: 'zone-a', capacity: {cores: 4, memoryMiB: 8192, diskGiB: 80}})

export function KubernetesClusterDialog({open, close, workspaces, templates, services, ...common}: CommonProps & {open: boolean; close: () => void; workspaces: Workspace[]; templates: ManagedResource[]; services: ManagedResource[]}) {
  const [mode, setMode] = useState<'single' | 'multi'>('single')
  const defaultWorkspaceID = workspaces[0]?.id ?? ''
  const defaultConnectionID = common.connections[0]?.id ?? ''
  const [connectionID, setConnectionID] = useState(defaultConnectionID)
  const [clusterName, setClusterName] = useState('')
  const [templateID, setTemplateID] = useState('')
  const [controlPlanes, setControlPlanes] = useState(1)
  const [managementCount, setManagementCount] = useState(1)
  const [machineNodes, setMachineNodes] = useState<Record<string, string>>({})
  const [applicationPools, setApplicationPools] = useState<ClusterPoolDraft[]>([applicationPoolDefaults()])
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (!open) return
    setConnectionID(defaultConnectionID)
    setApplicationPools([applicationPoolDefaults()])
    setClusterName('')
    setTemplateID('')
    setControlPlanes(1)
    setManagementCount(1)
    setMachineNodes({})
    setError('')
  }, [open, defaultWorkspaceID, defaultConnectionID])
  const compatible = (item: ManagedResource) => item.status === 'ready' && item.connectionId === connectionID
  const availableTemplates = templates.filter(compatible)
  const selectedTemplate = availableTemplates.find(item => item.id === templateID) ?? availableTemplates[0]
  const workspaceID = selectedTemplate?.workspaceId ?? defaultWorkspaceID
  const harbor = services.filter(item => item.kind === 'harbor' && compatible(item) && item.workspaceId === workspaceID)
  const nfs = services.filter(item => item.kind === 'nfs' && compatible(item) && item.workspaceId === workspaceID)
  const templateNode = String(selectedTemplate?.spec.node ?? '')
  const placementPools = [{name: 'control-plane', label: 'Master', count: controlPlanes}, {name: 'management', label: 'Management', count: managementCount}, ...applicationPools.map(pool => ({...pool, label: pool.name}))].map(pool => ({...pool, machines: Array.from({length: Math.max(0, Math.min(12, pool.count))}, (_, index) => `${clusterName}-${pool.name}-${index + 1}`)}))
  const machineNames = placementPools.flatMap(pool => pool.machines)
  const allocation = useProvisioningSuggestions(open && mode === 'single', connectionID, selectedTemplate?.id ?? '', machineNames.length)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const values = new FormData(event.currentTarget)
    try {
      await request('/kubernetes-clusters', {method: 'POST', body: JSON.stringify({
        workspaceId: workspaceID,
        connectionId: connectionID,
        templateId: selectedTemplate?.id,
        machineNodes: Object.fromEntries(machineNames.map(name => [name, machineNodes[name] ?? templateNode])),
        harborId: values.get('harborId'),
        nfsId: values.get('nfsId'),
        name: values.get('name'),
        baseVMID: Number(values.get('baseVMID')),
        addressStart: values.get('addressStart'),
        prefixLength: Number(values.get('prefixLength')),
        gateway: values.get('gateway'),
        dnsServer: values.get('dnsServer'),
        scrapeInterval: values.get('scrapeInterval'),
 retention: values.get('retention'),
 controlPlanes: Number(values.get('controlPlanes')),
        controlPlaneZones: parseZones(String(values.get('controlPlaneZones') ?? '')),
        controlPlaneCapacity: readCapacity(values, 'controlPlane'),
        managementPool: {name: 'management', count: Number(values.get('managementCount')), zones: parseZones(String(values.get('managementZones') ?? '')), capacity: readCapacity(values, 'management')},
        applicationPools: applicationPools.map(pool => ({name: pool.name, count: pool.count, zones: parseZones(pool.zones), capacity: pool.capacity}))
      })}, common.session.csrfToken)
      close()
      await common.onDone('Cluster creation started.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  const ready = availableTemplates.length > 0 && harbor.length > 0 && nfs.length > 0
  if (mode === 'multi') return <MultiClusterForm open={open} close={close} workspaces={workspaces} templates={templates} services={services} common={common} onSingle={() => setMode('single')} />
  return <Dialog open={open} onClose={close} title="Create Kubernetes cluster" className="template-modal cluster-modal" presentation="page">
    <WizardForm onSubmit={submit} onCancel={close} pending={pending} error={error} submitDisabled={!ready || !workspaceID || !connectionID} submitLabel="Create cluster">
      <WizardStep title="Cluster">
        <div className="field-row field-row-deployment"><label>Deployment<select aria-label="Deployment" value={mode} onChange={event => setMode(event.target.value as 'single' | 'multi')}><option value="single">Single cluster</option><option value="multi">Multi cluster</option></select></label><label>Name<input name="name" value={clusterName} onChange={event => setClusterName(event.target.value)} pattern="[a-z0-9][a-z0-9-]{0,31}" maxLength={32} placeholder="development" required /></label></div>
        <div className="field-row"><label>Proxmox connection<select value={connectionID} onChange={event => setConnectionID(event.target.value)} required>{common.connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>VM template<select name="templateId" value={selectedTemplate?.id ?? ''} onChange={event => setTemplateID(event.target.value)} required>{availableTemplates.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
        <div className="field-row"><label>Harbor registry<select name="harborId" required>{harbor.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>NFS storage<select name="nfsId" required>{nfs.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
        {!ready && <p className="form-error" role="status">Create a VM template, Harbor and NFS on this connection first.</p>}
      </WizardStep>
      <WizardStep title="Node pools"><FormTabs label="Node pools" items={[
        {id: 'master', title: 'Master', content: <>
      <div className="field-row field-row-count"><label>Nodes<select name="controlPlanes" value={controlPlanes} onChange={event => setControlPlanes(Number(event.target.value))}><option value="1">1 · development</option><option value="3">3 · high availability</option></select></label><label>Zones<input name="controlPlaneZones" defaultValue="zone-a" pattern="[a-z0-9,-]+" required /></label></div>
      <MachineCapacityFields prefix="controlPlane" defaults={{cores: 2, memoryMiB: 4096, diskGiB: 80}} />
        </>},
        {id: 'management', title: 'Management', content: <>
      <div className="field-row field-row-count"><label>Nodes<NumericInput name="managementCount" value={managementCount} min={1} max={12} onValueChange={setManagementCount} required /></label><label>Zones<input name="managementZones" defaultValue="zone-a" pattern="[a-z0-9,-]+" required /></label></div>
      <MachineCapacityFields prefix="management" defaults={{cores: 4, memoryMiB: 8192, diskGiB: 80}} />
        </>},
        {id: 'applications', title: 'Pools', content: <>
      <div className="pool-section pool-toolbar"><ResourceAction action="add" label="Add pool" disabled={applicationPools.length >= 8} onClick={() => setApplicationPools(items => [...items, {...applicationPoolDefaults(), name: `applications-${items.length + 1}`, count: 1}])} /></div>
      <div className="pool-editor">{applicationPools.map((pool, index) => <div className="pool-card" key={index}><div className="pool-row"><label>Name<input value={pool.name} pattern="[a-z0-9][a-z0-9-]{0,31}" onChange={event => updateApplicationPool(setApplicationPools, index, {name: event.target.value})} required /></label><label>Nodes<NumericInput value={pool.count} min={1} max={12} onValueChange={count => updateApplicationPool(setApplicationPools, index, {count})} required /></label><label>Zones<input value={pool.zones} pattern="[a-z0-9,-]+" onChange={event => updateApplicationPool(setApplicationPools, index, {zones: event.target.value})} required /></label>{applicationPools.length > 1 && <button type="button" className="icon-button pool-remove" aria-label={`Remove ${pool.name}`} title={`Remove ${pool.name}`} onClick={() => setApplicationPools(items => items.filter((_, position) => position !== index))}><ActionIcon name="close" /></button>}</div><div className="capacity-grid"><label>CPU (cores)<NumericInput value={pool.capacity.cores} min={1} max={32} onValueChange={cores => updateApplicationPoolCapacity(setApplicationPools, index, {cores})} required /></label><label>Memory (MiB)<NumericInput value={pool.capacity.memoryMiB} min={1024} max={131072} onValueChange={memoryMiB => updateApplicationPoolCapacity(setApplicationPools, index, {memoryMiB})} required /></label><label>Disk (GiB)<NumericInput value={pool.capacity.diskGiB} min={8} max={2048} onValueChange={diskGiB => updateApplicationPoolCapacity(setApplicationPools, index, {diskGiB})} required /></label></div></div>)}</div>
        </>}
      ]} /></WizardStep>
      <WizardStep title="Network">
<label className="field-short">First VM ID<input name="baseVMID" {...allocation.field('vmid')} type="number" min={100} max={999999988} required /></label>
      <div className="field-row field-row-address"><label>First node IP<input name="addressStart" {...allocation.field('address')} placeholder="192.168.1.130" required /></label><label>Prefix length<input name="prefixLength" {...allocation.field('prefixLength', 24)} type="number" min={8} max={30} required /></label></div>
      <div className="field-row field-row-network"><label>Gateway<input name="gateway" {...allocation.field('gateway')} placeholder="192.168.1.1" required /></label><label>DNS server<input name="dnsServer" {...allocation.field('dnsServer', '1.1.1.1')} required /></label></div>
      <details className="schema-advanced"><summary>Monitoring</summary>
      <div className="field-row"><label>Scraping period<select name="scrapeInterval" defaultValue="15s">{['5s', '10s', '15s', '30s'].map(value => <option key={value} value={value}>{value}</option>)}</select></label><label>Metrics retention<select name="retention" defaultValue="7d">{['1d', '7d', '15d'].map(value => <option key={value} value={value}>{value}</option>)}</select></label></div>
      </details>
      </WizardStep>
      <WizardStep title="VM placement">
      <div className="vm-placement-grid">{placementPools.map(pool => <fieldset className="vm-placement-pool" key={pool.name}><legend>{pool.label}</legend><div className="vm-placement-columns" aria-hidden="true"><span>Node</span><span>Proxmox server</span></div>{pool.machines.map((name, index) => <label className="vm-placement-row" key={name}><span>{clusterName ? name : '—'}</span><input aria-label={clusterName ? `${name} · Proxmox server` : `${pool.label} · Node ${index + 1} · Proxmox server`} value={machineNodes[name] ?? templateNode} onChange={event => setMachineNodes(nodes => ({...nodes, [name]: event.target.value}))} pattern="[A-Za-z0-9._-]{1,64}" maxLength={64} placeholder="pve" required /></label>)}</fieldset>)}</div>
      </WizardStep>
      <WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm>
  </Dialog>
}

function MachineCapacityFields({prefix, defaults}: {prefix: string; defaults: MachineCapacityDraft}) {
  return <div className="capacity-grid"><label>CPU (cores)<input name={`${prefix}Cores`} type="number" min={1} max={32} defaultValue={defaults.cores} required /></label><label>Memory (MiB)<input name={`${prefix}MemoryMiB`} type="number" min={1024} max={131072} defaultValue={defaults.memoryMiB} required /></label><label>Disk (GiB)<input name={`${prefix}DiskGiB`} type="number" min={8} max={2048} defaultValue={defaults.diskGiB} required /></label></div>
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
  return <Dialog open={open} onClose={close} title="Import application" className="template-modal" presentation="page">
    <form onSubmit={submit}>
      <label>Application descriptor<input type="file" accept=".yaml,.yml,.json,application/yaml,application/json" required onChange={event => setFile(event.target.files?.[0] ?? null)} /></label>
      {file && <span className="field-description">{file.name} · {Math.ceil(file.size / 1024)} KB</span>}
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate and import" />
    </form>
  </Dialog>
}

function Actions({close, pending, disabled = false, label}: {close: () => void; pending: boolean; disabled?: boolean; label: string}) {
  return <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || disabled}>{pending ? 'Please wait…' : label}</button></div>
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
  const validation = cause.body.validation as {issues?: Array<{path?: string; message?: string}>} | undefined
  const issues = validation?.issues ?? cause.body.issues as Array<{path?: string; message?: string}> | undefined
  return issues?.map(issue => issue.message ? `${issue.path ? `${issue.path}: ` : ''}${issue.message}` : '').filter(Boolean).join(' ') || cause.message
}

function schemaSubset(schema: JsonSchema, keys: string[], exclude = false): JsonSchema {
  const entries = Object.entries(schema.properties ?? {})
  const properties = Object.fromEntries(exclude ? entries.filter(([name]) => !keys.includes(name)) : keys.flatMap(name => entries.filter(([key]) => key === name)))
  return {...schema, properties, required: schema.required?.filter(name => name in properties)}
}
