import {useState, type FormEvent} from 'react'
import {request} from '../api'
import type {ManagedResource, Workspace} from '../types'
import {useProvisioningSuggestions} from '../hooks/useProvisioningSuggestions'
import type {CommonProps} from './Forms'
import {Dialog} from './Dialog'
import {NumericInput} from './NumericInput'
import {ResourceAction} from './ResourceAction'
import {WizardForm, WizardStep, WizardReview} from './WizardForm'
import {FormTabs} from './FormTabs'

type Capacity = {cores: number; memoryMiB: number; diskGiB: number}
type Pool = {name: string; count: number; labels: string; capacity: Capacity}
type Member = {key: number; name: string; clusterId: number; podCIDR: string; serviceCIDR: string; master: Pool; management: Pool; applications: Pool[]; machineNodes: Record<string, string>}
const pool = (name: string, count: number, cores: number, memoryMiB: number): Pool => ({name, count, labels: '', capacity: {cores, memoryMiB, diskGiB: 80}})
const member = (id: number): Member => ({key: id, name: `cluster-${id}`, clusterId: id, podCIDR: `10.${63 + id}.0.0/16`, serviceCIDR: `10.${127 + id}.0.0/16`, master: pool('control-plane', 1, 2, 4096), management: pool('management', 1, 4, 8192), applications: [pool('applications', 2, 4, 8192)], machineNodes: {}})
const count = (value: Member) => value.master.count + value.management.count + value.applications.reduce((total, item) => total + item.count, 0)
const labels = (value: string) => Object.fromEntries(value.split(',').map(part => part.trim()).filter(Boolean).map(part => {
  const split = part.indexOf('=')
  if (split < 1) throw new Error('Pool labels must use key=value, separated by commas.')
  return [part.slice(0, split).trim(), part.slice(split + 1).trim()]
}))
export function addIPv4(value: string, offset: number): string {
  const parts = value.split('.').map(Number)
  if (parts.length !== 4 || parts.some(part => !Number.isInteger(part) || part < 0 || part > 255)) throw new Error('Use a valid first node IPv4 address.')
  const number = parts.reduce((result, part) => result * 256 + part, 0) + offset
  if (number > 4294967295) throw new Error('The node IP range is invalid.')
  return [24, 16, 8, 0].map(shift => Math.floor(number / 2 ** shift) % 256).join('.')
}
export function MultiClusterForm({open, close, workspaces, templates, services, common, onSingle}: {open: boolean; close: () => void; workspaces: Workspace[]; templates: ManagedResource[]; services: ManagedResource[]; common: CommonProps; onSingle: () => void}) {
  const [connectionID, setConnectionID] = useState(common.connections[0]?.id ?? '')
  const [templateID, setTemplateID] = useState('')
  const [name, setName] = useState('')
  const [members, setMembers] = useState<Member[]>([member(1), member(2)])
  const [sequence, setSequence] = useState(3)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const compatible = (item: ManagedResource) => item.status === 'ready' && item.connectionId === connectionID
  const availableTemplates = templates.filter(compatible)
  const selectedTemplate = availableTemplates.find(item => item.id === templateID) ?? availableTemplates[0]
  const workspaceID = selectedTemplate?.workspaceId ?? workspaces[0]?.id ?? ''
  const harbor = services.filter(item => item.kind === 'harbor' && compatible(item) && item.workspaceId === workspaceID)
  const nfs = services.filter(item => item.kind === 'nfs' && compatible(item) && item.workspaceId === workspaceID)
  const allocation = useProvisioningSuggestions(open, connectionID, selectedTemplate?.id ?? '', members.reduce((total, item) => total + count(item), 0))
  const update = (key: number, values: Partial<Member>) => setMembers(items => items.map(item => item.key === key ? {...item, ...values} : item))
  const fullName = (item: Member) => name ? `${name}-${item.name}` : item.name
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault(); setError(''); setPending(true)
    try {
      const form = new FormData(event.currentTarget)
      let offset = 0
      const prepared = members.map(item => {
        const clusterName = fullName(item)
        const names = [item.master, item.management, ...item.applications].flatMap(pool => Array.from({length: pool.count}, (_, index) => `${clusterName}-${pool.name}-${index + 1}`))
        const result = {name: clusterName, clusterId: item.clusterId, podCIDR: item.podCIDR, serviceCIDR: item.serviceCIDR, baseVMID: Number(form.get('baseVMID')) + offset, addressStart: addIPv4(String(form.get('addressStart')), offset), prefixLength: Number(form.get('prefixLength')), gateway: form.get('gateway'), dnsServer: form.get('dnsServer'), controlPlanes: item.master.count, controlPlaneCapacity: item.master.capacity, managementPool: {...item.management, labels: labels(item.management.labels)}, applicationPools: item.applications.map(pool => ({...pool, labels: labels(pool.labels)})), machineNodes: Object.fromEntries(names.map(node => [node, item.machineNodes[node] ?? String(selectedTemplate?.spec.node ?? '')]))}
        offset += count(item)
        return result
      })
      await request('/kubernetes-clusters', {method: 'POST', body: JSON.stringify({mode: 'multi', name, workspaceId: workspaceID, connectionId: connectionID, templateId: selectedTemplate?.id, harborId: form.get('harborId'), nfsId: form.get('nfsId'), scrapeInterval: form.get('scrapeInterval'), retention: form.get('retention'), members: prepared})}, common.session.csrfToken)
      close(); await common.onDone('Cluster creation started.')
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not create the clusters.')} finally {setPending(false)}
  }
  return <Dialog open={open} onClose={close} title="Create multi cluster" className="template-modal cluster-modal" presentation="page">
    <WizardForm onSubmit={submit} onCancel={close} pending={pending} error={error} submitDisabled={!selectedTemplate || !harbor.length || !nfs.length || members.some(item => count(item) > 12)} submitLabel="Create clusters">
      <WizardStep title="Clusters">
        <div className="field-row field-row-deployment"><label>Deployment<select value="multi" onChange={onSingle}><option value="single">Single cluster</option><option value="multi">Multi cluster</option></select></label>
        <label>Name<input value={name} onChange={event => setName(event.target.value)} pattern="[a-z0-9][a-z0-9-]{0,15}" maxLength={16} required /></label></div>
    <div className="field-row"><label>Proxmox connection<select value={connectionID} onChange={event => setConnectionID(event.target.value)} required>{common.connections.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>
    <label>VM template<select value={selectedTemplate?.id ?? ''} onChange={event => setTemplateID(event.target.value)} required>{availableTemplates.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
    <div className="field-row"><label>Harbor registry<select name="harborId" required>{harbor.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label><label>NFS storage<select name="nfsId" required>{nfs.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label></div>
    <div className="pool-section pool-toolbar"><strong>Clusters</strong><ResourceAction action="add" label="Add cluster" disabled={members.length >= 6} onClick={() => {let available = 1; while (members.some(item => item.clusterId === available || item.name === `cluster-${available}`)) available++; setMembers(items => [...items, {...member(available), key: sequence}]); setSequence(value => value + 1)}} /></div>
        <ul className="wizard-member-list">{members.map(item => <li key={item.key}><span>{fullName(item)}</span><span>{count(item)} nodes</span>{members.length > 2 && <ResourceAction action="delete" label={`Remove ${item.name}`} onClick={() => setMembers(items => items.filter(member => member.key !== item.key))} />}</li>)}</ul>
      </WizardStep>
      <WizardStep title="Network">
        <label className="field-short">First VM ID<input name="baseVMID" type="number" min={100} max={999999928} {...allocation.field('vmid')} required /></label>
    <div className="field-row field-row-address"><label>First node IP<input name="addressStart" {...allocation.field('address')} required /></label><label>Prefix length<input name="prefixLength" type="number" min={8} max={30} {...allocation.field('prefixLength', 24)} required /></label></div>
    <div className="field-row field-row-network"><label>Gateway<input name="gateway" {...allocation.field('gateway')} required /></label><label>DNS server<input name="dnsServer" {...allocation.field('dnsServer', '1.1.1.1')} required /></label></div>
    <details className="schema-advanced"><summary>Monitoring</summary><div className="field-row"><label>Scraping period<select name="scrapeInterval" defaultValue="15s">{['5s', '10s', '15s', '30s'].map(value => <option key={value}>{value}</option>)}</select></label><label>Metrics retention<select name="retention" defaultValue="7d">{['1d', '7d', '15d'].map(value => <option key={value}>{value}</option>)}</select></label></div></details>
      </WizardStep>
      {members.map(item => <WizardStep title={fullName(item)} key={item.key}>
        <label>Name<input value={item.name} onChange={event => update(item.key, {name: event.target.value})} pattern="[a-z0-9]([a-z0-9-]{0,13}[a-z0-9])?" maxLength={15} required /></label>
        <FormTabs label={`${fullName(item)} settings`} items={[
          {id: 'master', title: 'Master', content: <>
      <MemberPool title="Master" value={item.master} master onChange={master => update(item.key, {master})} />
          </>},
          {id: 'management', title: 'Management', content: <>
      <MemberPool title="Management" value={item.management} onChange={management => update(item.key, {management})} />
          </>},
          {id: 'applications', title: 'Pools', content: <>
      <div className="pool-section pool-toolbar"><ResourceAction action="add" label={`Add pool to ${item.name}`} disabled={item.applications.length >= 8} onClick={() => update(item.key, {applications: [...item.applications, pool(`applications-${item.applications.length + 1}`, 1, 4, 8192)]})} /></div>
      {item.applications.map((application, index) => <div key={index}><MemberPool title={application.name} value={application} editableName onChange={value => update(item.key, {applications: item.applications.map((pool, position) => position === index ? value : pool)})} />{item.applications.length > 1 && <ResourceAction action="delete" label={`Remove ${application.name} from ${item.name}`} onClick={() => update(item.key, {applications: item.applications.filter((_, position) => position !== index)})} />}</div>)}
          </>},
          {id: 'placement', title: 'VM placement', content: <>
<div className="vm-placement-grid">{[item.master, item.management, ...item.applications].map(pool => <fieldset className="vm-placement-pool" key={pool.name}><legend>{pool === item.master ? 'Master' : pool === item.management ? 'Management' : pool.name}</legend><div className="vm-placement-columns" aria-hidden="true"><span>Node</span><span>Proxmox server</span></div>{Array.from({length: Math.min(12, pool.count)}, (_, index) => {const node = `${fullName(item)}-${pool.name}-${index + 1}`;return <label className="vm-placement-row" key={node}><span>{name ? node : '—'}</span><input aria-label={`${node} · Proxmox server`} value={item.machineNodes[node] ?? String(selectedTemplate?.spec.node ?? '')} onChange={event => update(item.key, {machineNodes: {...item.machineNodes, [node]: event.target.value}})} pattern="[A-Za-z0-9._-]{1,64}" required /></label>})}</fieldset>)}</div>
          </>},
          {id: 'network', title: 'Cilium', content: <>
      <details className="schema-advanced"><summary>Cluster network</summary><div className="field-row field-row-cilium"><label>Cilium cluster ID<NumericInput value={item.clusterId} min={1} max={255} onValueChange={clusterId => update(item.key, {clusterId})} required /></label><label>Pod CIDR<input value={item.podCIDR} onChange={event => update(item.key, {podCIDR: event.target.value})} required /></label><label>Service CIDR<input value={item.serviceCIDR} onChange={event => update(item.key, {serviceCIDR: event.target.value})} required /></label></div></details>
          </>}
        ]} />
        {count(item) > 12 && <p className="form-error">Each cluster can contain at most 12 nodes.</p>}
      </WizardStep>)}
      <WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm>
  </Dialog>
}

function MemberPool({title, value, onChange, master = false, editableName = false}: {title: string; value: Pool; onChange: (value: Pool) => void; master?: boolean; editableName?: boolean}) {
  return <div className="pool-card"><strong>{title}</strong><div className={`field-row ${editableName ? 'field-row-pool' : 'field-row-count'}`}>{editableName && <label>Name<input value={value.name} onChange={event => onChange({...value, name: event.target.value})} pattern="[a-z0-9][a-z0-9-]{0,31}" required /></label>}<label>Nodes{master ? <select value={value.count} onChange={event => onChange({...value, count: Number(event.target.value)})}><option value="1">1</option><option value="3">3</option></select> : <NumericInput value={value.count} min={1} max={12} onValueChange={count => onChange({...value, count})} required />}</label>{!master && <label>Labels<input value={value.labels} onChange={event => onChange({...value, labels: event.target.value})} /></label>}</div><div className="capacity-grid">{(['cores', 'memoryMiB', 'diskGiB'] as const).map(key => <label key={key}>{key === 'cores' ? 'CPU cores' : key === 'memoryMiB' ? 'Memory (MiB)' : 'Disk (GiB)'}<NumericInput value={value.capacity[key]} min={key === 'cores' ? 1 : key === 'memoryMiB' ? 512 : 8} max={key === 'cores' ? 32 : key === 'memoryMiB' ? 131072 : 2048} onValueChange={number => onChange({...value, capacity: {...value.capacity, [key]: number}})} required /></label>)}</div></div>
}
