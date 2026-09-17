import {useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import {useDetailRoute} from '../detailRoute'
import type {CatalogStrategy, ManagedResource, Session, Workspace} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {Dialog} from './Dialog'
import {InputData} from './InputData'
import {SectionIcon} from './SectionIcon'
import {ResourceAction} from './ResourceAction'

interface Props {
  kind: CatalogStrategy['kind']
  items: CatalogStrategy[]
  workspaces: Workspace[]
  services: ManagedResource[]
  session: Session
  isAdmin: boolean
  changed: () => Promise<void>
  openOperation: (id: string) => void
}

export function StrategyCatalog(props: Props) {
  const detail = useDetailRoute('catalog')
  const [open, setOpen] = useState(false)
  const [error, setError] = useState('')
  const readyHarbor = props.services.filter(item => item.kind === 'harbor' && item.status === 'ready')
  const visible = props.items.filter(item => item.kind === props.kind)
  const title = {scheduler: 'Custom schedulers', descheduler: 'Custom deschedulers', autoscaler: 'Custom autoscalers'}[props.kind]
  const remove = async (item: CatalogStrategy) => {
    if (!window.confirm(`Remove ${item.name} from the catalog? The image and previous results will remain in Harbor and experiment history.`)) return
    setError('')
    try {
      await request(`/catalog/strategies/${encodeURIComponent(item.id)}`, {method: 'DELETE'}, props.session.csrfToken)
      await props.changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not remove the catalog image.')
    }
  }
  const selected = detail.route?.kind === 'strategy' ? props.items.find(item => item.id === detail.route?.id) : undefined
  if (selected) return <div className="detail-page"><DetailBreadcrumb parent="Catalog" current={selected.name} back={detail.close} /><div className="detail-page-heading"><div><h2>{selected.name}</h2><p>{selected.kind}</p></div><span className={`status ${selected.status}`}>{selected.status}</span></div><article className="detail-page-card">
    <div className="details-summary"><div><span>Type</span><strong>{selected.kind}</strong></div><div><span>Harbor registry</span><strong>{props.services.find(item => item.id === selected.harborResourceId)?.name ?? 'Unavailable'}</strong></div><div><span>Environment</span><strong>{props.workspaces.find(item => item.id === selected.workspaceId)?.name ?? 'Default'}</strong></div><div><span>Source image</span><strong>{selected.sourceImage}</strong></div><div><span>Harbor image</span><strong>{selected.mirroredImage ?? 'Pending'}</strong></div></div>
    <section className="details-section"><div className="details-heading"><strong>Default settings</strong></div><InputData value={selected.defaultConfiguration} /></section>
    {selected.error && <p className="managed-error">{selected.error}</p>}
    <div className="managed-actions"><button className="button secondary compact" onClick={() => props.openOperation(selected.operationId)}>Activity</button>{selected.mirroredImage && <button className="button primary compact" onClick={() => navigator.clipboard.writeText(selected.mirroredImage!)}>Copy Harbor image</button>}</div>
  </article></div>
  return <>
    <div className="section-heading"><div><SectionIcon kind={props.kind} /><h2>{title}</h2></div>{props.isAdmin && <ResourceAction action="create" label={`Import ${props.kind} image`} disabled={!readyHarbor.length} onClick={() => setOpen(true)} />}</div>
    {error && <p className="form-error page-error">{error}</p>}
    <div className="catalog-grid strategy-grid">{visible.length ? visible.map(item => <article className="application-card" key={item.id}>
      <div className="application-card-header"><span className="application-icon">{item.kind.slice(0, 1).toUpperCase()}</span><span className={`status ${item.status}`}>{item.status}</span></div>
      <h3>{item.name}</h3><p>{item.kind} · {item.mirroredImage ? 'Available in Harbor' : 'Importing'}</p>
      {item.error && <p className="managed-error">{item.error}</p>}
      <div className="managed-actions"><ResourceAction action="view" label={`View ${item.name} details`} onClick={() => detail.open('strategy', item.id)} />{props.isAdmin && <ResourceAction action="delete" label={`Delete ${item.name}`} disabled={!['succeeded', 'failed', 'canceled'].includes(item.status)} onClick={() => void remove(item)} />}</div>
    </article>) : <div className="empty-state"><div><strong>No custom {props.kind} yet</strong>Import a Docker image into managed Harbor.</div></div>}</div>
    <StrategyDialog open={open} kind={props.kind} close={() => setOpen(false)} workspaces={props.workspaces} harbors={readyHarbor} session={props.session} changed={props.changed} failed={setError} />
  </>
}

function StrategyDialog({open, kind, close, workspaces, harbors, session, changed, failed}: {open: boolean; kind: CatalogStrategy['kind']; close: () => void; workspaces: Workspace[]; harbors: ManagedResource[]; session: Session; changed: () => Promise<void>; failed: (value: string) => void}) {
  const [workspaceId, setWorkspaceId] = useState(workspaces[0]?.id ?? '')
  const [pending, setPending] = useState(false)
  const compatible = harbors.filter(item => item.workspaceId === workspaceId)
  useEffect(() => {
    if (!open) return
    setWorkspaceId(current => workspaces.some(item => item.id === current) ? current : workspaces[0]?.id ?? '')
  }, [open, workspaces])
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values = new FormData(event.currentTarget)
    let configuration: Record<string, unknown> = {}
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
      await request('/catalog/strategies', {method: 'POST', body: JSON.stringify({workspaceId, harborResourceId: values.get('harborResourceId'), name: values.get('name'), kind, sourceImage: values.get('sourceImage'), defaultConfiguration: configuration})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      failed(cause instanceof Error ? cause.message : 'Could not import the custom image.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title={`Import ${kind} image`} className="template-modal">
    <form onSubmit={submit}>
      {workspaces.length > 1 ? <label>Environment<select value={workspaceId} onChange={event => setWorkspaceId(event.target.value)}>{workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label> : <input type="hidden" value={workspaceId} readOnly />}
      <label>Harbor registry<select name="harborResourceId" required disabled={!compatible.length}>{compatible.length ? compatible.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No ready Harbor in this environment</option>}</select></label>
      <label>Name<input name="name" pattern="[a-z0-9][a-z0-9-]{0,62}" placeholder={`my-${kind}`} required /></label>
      <label>Docker image<input name="sourceImage" placeholder="ghcr.io/team/scheduler:v1.2.0" required autoFocus /></label>
      <details className="advanced-fields"><summary>Default settings</summary>
        {kind === 'scheduler' && <label>Scheduler configuration<textarea name="configFile" rows={12} defaultValue={schedulerConfiguration} spellCheck={false} required /></label>}
        {kind === 'descheduler' && <><label>Descheduler policy<textarea name="configFile" rows={12} defaultValue={deschedulerConfiguration} spellCheck={false} required /></label><label>Interval in seconds<input name="intervalSeconds" type="number" min={10} max={86400} defaultValue={60} required /></label></>}
        {kind === 'autoscaler' && <><div className="field-row"><label>Evaluation interval<input name="intervalSeconds" type="number" min={5} max={3600} defaultValue={15} required /></label><label>Minimum replicas<input name="minReplicas" type="number" min={1} max={1000} defaultValue={1} required /></label><label>Maximum replicas<input name="maxReplicas" type="number" min={1} max={1000} defaultValue={10} required /></label></div><label>Autoscaler parameters<textarea name="parameters" rows={10} defaultValue="{}" spellCheck={false} required /></label></>}
      </details>
      <div className="validation-callout"><span>✓</span><p>The image is mirrored to Harbor and verified by digest.</p></div>
      <div className="modal-actions"><button type="button" className="button secondary" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !compatible.length}>{pending ? 'Validating…' : 'Validate and import'}</button></div>
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
  - name: default
    pluginConfig: []
    plugins: {}`

function shortDigest(value?: string): string {
  return value ? value.replace('sha256:', '').slice(0, 12) : '—'
}
