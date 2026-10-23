import {ResourceListAction} from './ResourceListHeading'
import {FormDisclosure} from './FormDisclosure'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAttribution} from './ResourceAttribution'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import {useDetailRoute} from '../detailRoute'
import type {CatalogStrategy, Session, Workspace} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {Dialog} from './Dialog'
import {InputData, hasInputData} from './InputData'
import {ResourceAction} from './ResourceAction'

interface Props {
  kind: CatalogStrategy['kind']
  items: CatalogStrategy[]
  workspaces: Workspace[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

export function StrategyCatalog(props: Props) {
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  const detail = useDetailRoute('catalog')
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<CatalogStrategy>()
  const [error, setError] = useState('')
  const catalogItems = props.items.filter(item => item.kind === props.kind)
  const remove = async (item: CatalogStrategy) => {
    if (!await confirmDelete(`Remove ${item.name} from the catalog? Previous experiment results will be retained.`)) return
    setError('')
    try {
      await request(`/catalog/strategies/${encodeURIComponent(item.id)}`, {method: 'DELETE'}, props.session.csrfToken)
      await props.changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not remove the catalog image.')
    }
  }
  const edit = (item: CatalogStrategy) => { setEditing(item); setOpen(true) }
  const editor = <StrategyDialog key={editing?.id ?? 'new'} editing={editing} open={open} kind={props.kind} close={() => setOpen(false)} workspaces={props.workspaces} session={props.session} changed={props.changed} failed={setError} />
  const selected = detail.route?.kind === 'strategy' ? props.items.find(item => item.id === detail.route?.id) : undefined
  if (selected) return <>{editor}{error && <p className="form-error">{error}</p>}<div className="detail-page"><DetailBreadcrumb parent="Catalog" current={selected.name} back={detail.close} /><div className="detail-page-heading"><div><h2>{selected.name}</h2></div></div><ResourceAttribution resource={selected} editable /><article className="detail-page-card">
    <div className="details-summary"><div><span>Status</span><strong>{strategyStatus(selected.status)}</strong></div><div><span>Type</span><strong>{selected.kind}</strong></div><div><span>Image</span><strong>{selected.sourceImage}</strong></div></div>
    {hasInputData(selected.defaultConfiguration) && <section className="details-section"><div className="details-heading"><strong>Default settings</strong></div><StrategyDefaults value={selected.defaultConfiguration} /></section>}
    {selected.error && <p className="managed-error">{selected.error}</p>}
    <div className="managed-actions">{props.isAdmin && <button className="button secondary compact" disabled={!['succeeded', 'failed', 'canceled'].includes(selected.status)} onClick={() => edit(selected)}>Edit settings</button>}{selected.operationId && <button className="button secondary compact" onClick={() => props.openOperation(selected.operationId!)}>Activity</button>}<button className="button primary compact" onClick={() => navigator.clipboard.writeText(selected.sourceImage)}>Copy image</button></div>
  </article></div></>
  return <>
    {confirmationDialog}
    {props.isAdmin && <ResourceListAction><ResourceAction action="create" label={`Add ${props.kind}`} onClick={() => { setEditing(undefined); setOpen(true) }} /></ResourceListAction>}
    {error && <p className="form-error page-error">{error}</p>}
    <ResourceListTable items={catalogItems} label={`${props.kind.charAt(0).toUpperCase()}${props.kind.slice(1)}s`} editable searchText={item => `${item.name} ${item.sourceImage}`} columns={[
      {label: 'Name', render: item => <h3>{item.name}</h3>},
      {label: 'Status', render: item => <span className={`status ${item.status}`}>{strategyStatus(item.status)}</span>}
    ]} actions={item => <><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('strategy', item.id)} />{props.isAdmin && <ResourceAction action="edit" label={`Edit ${item.name} settings`} disabled={!['succeeded', 'failed', 'canceled'].includes(item.status)} onClick={() => edit(item)} />}{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!['succeeded', 'failed', 'canceled'].includes(item.status)} onClick={() => void remove(item)} />}</>} />
    {editor}
  </>
}

function strategyStatus(status: string) {
  return status === 'succeeded' ? 'Ready' : status.charAt(0).toUpperCase() + status.slice(1)
}

function StrategyDefaults({value}: {value: Record<string, unknown>}) {
  const {configFile, parameters, requiredReplicas: _requiredReplicas, startupEvent: _startupEvent, ...settings} = value
  return <div className="form-stack"><InputData value={settings} />{hasInputData(configFile ?? parameters) && <FormDisclosure title="Configuration"><InputData value={configFile ?? parameters} /></FormDisclosure>}</div>
}

function StrategyDialog({editing, open, kind, close, workspaces, session, changed, failed}: {editing?: CatalogStrategy; open: boolean; kind: CatalogStrategy['kind']; close: () => void; workspaces: Workspace[]; session: Session; changed: () => Promise<void>; failed: (value: string) => void}) {
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? '')
  const [pending, setPending] = useState(false)
  useEffect(() => {
    if (!open) return
    setWorkspaceId(current => workspaces.some(item => item.id === current) ? current : workspaces[0]?.id ?? '')
  }, [open, workspaces])
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values = new FormData(event.currentTarget)
    let configuration: Record<string, unknown> = editing?.defaultConfiguration ?? {}
    if (kind === 'scheduler') configuration = {configFile: values.get('configFile')}
    if (kind === 'descheduler') configuration = {configFile: values.get('configFile'), intervalSeconds: Number(values.get('intervalSeconds'))}
    if (kind === 'autoscaler') {
      try {
        const parameters = JSON.parse(String(values.get('parameters') ?? '{}'))
        if (!parameters || Array.isArray(parameters) || typeof parameters !== 'object') throw new Error()
        configuration = {intervalSeconds: Number(values.get('intervalSeconds')), minReplicas: Number(values.get('minReplicas')), maxReplicas: Number(values.get('maxReplicas')), parameters: JSON.stringify(parameters, null, 2)}
      } catch {
        failed('Autoscaler parameters must be a JSON object.')
        return
      }
    }
    setPending(true)
    failed('')
    try {
      await request(editing ? `/catalog/strategies/${encodeURIComponent(editing.id)}` : '/catalog/strategies', {method: editing ? 'PUT' : 'POST', body: JSON.stringify({workspaceId, name: values.get('name'), kind, sourceImage: values.get('sourceImage'), defaultConfiguration: configuration})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      failed(cause instanceof Error ? cause.message : 'Could not import the custom image.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title={editing ? `Edit ${editing.name}` : `Add ${kind}`} className="template-modal" presentation="page">
    <form className="form-stack" key={`${open}-${editing?.sourceImage ?? 'new'}`} onSubmit={submit}>
      <label>Name<input defaultValue={editing?.name} name="name" pattern="[a-z0-9][a-z0-9-]{0,62}" placeholder={`my-${kind}`} required /></label>
      <label>Image<input name="sourceImage" defaultValue={editing?.sourceImage} placeholder={`ghcr.io/team/${kind}:v1.2.0`} required autoFocus /></label>
      <details className="advanced-fields" open={!!editing}><summary>Default settings</summary>
        {kind === 'scheduler' && <label>Configuration<textarea className="strategy-config-editor" name="configFile" rows={20} defaultValue={String(editing?.defaultConfiguration.configFile ?? schedulerConfiguration)} spellCheck={false} required /></label>}
        {kind === 'descheduler' && <><label>Configuration<textarea className="strategy-config-editor" name="configFile" rows={20} defaultValue={String(editing?.defaultConfiguration.configFile ?? deschedulerConfiguration)} spellCheck={false} required /></label><label>Interval (s)<input name="intervalSeconds" type="number" min={10} max={86400} defaultValue={Number(editing?.defaultConfiguration.intervalSeconds ?? 60)} required /></label></>}
        {kind === 'autoscaler' && <><div className="field-row"><label>Interval (s)<input name="intervalSeconds" type="number" min={5} max={3600} defaultValue={Number(editing?.defaultConfiguration.intervalSeconds ?? 15)} required /></label><label>Minimum replicas<input name="minReplicas" type="number" min={1} max={1000} defaultValue={Number(editing?.defaultConfiguration.minReplicas ?? 1)} required /></label><label>Maximum replicas<input name="maxReplicas" type="number" min={1} max={1000} defaultValue={Number(editing?.defaultConfiguration.maxReplicas ?? 10)} required /></label></div><label>Configuration<textarea className="strategy-config-editor" name="parameters" rows={20} defaultValue={typeof editing?.defaultConfiguration.parameters === 'string' ? editing.defaultConfiguration.parameters : JSON.stringify(editing?.defaultConfiguration.parameters ?? {}, null, 2)} spellCheck={false} required /></label></>}
      </details>
      <div className="modal-actions"><button type="button" className="button secondary" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Validating…' : 'Validate and save'}</button></div>
    </form>
  </Dialog>
}

const schedulerConfiguration = `apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
  - schedulerName: kubephos-managed
leaderElection:
  leaderElect: false`

const deschedulerConfiguration = `apiVersion: descheduler/v1alpha2
kind: DeschedulerPolicy
profiles:
  - name: network-aware
    pluginConfig:
      - name: DefaultEvictor
        args:
          podProtections:
            defaultDisabled:
              - PodsWithLocalStorage
      - name: NetworkAware
        args:
          minPodAgeSeconds: 10
          minPodIndex: 0
          minCommunicationCost: 0
          minCostImprovement: 0
          maxPodsToEvict: 1
          ignoreSameZoneNetworkCost: true
    plugins:
      deschedule:
        enabled:
          - NetworkAware`
