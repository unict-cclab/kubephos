import {ResourceListAction} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {InputData} from './InputData'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {useDetailRoute} from '../detailRoute'
import {ResourceAttribution} from './ResourceAttribution'
import {useCallback, useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {CatalogModel, Session} from '../types'
import {Dialog} from './Dialog'
import {ResourceAction} from './ResourceAction'

type ModelCatalogProps = {session: Session; isAdmin: boolean; changed: () => Promise<void>}

export function useCatalogModels() {
  const [models, setModels] = useState<CatalogModel[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const load = useCallback(async () => {
    try { setModels((await request<{items: CatalogModel[]}>('/catalog/models')).items); setError('') }
    catch (cause) { setError(cause instanceof Error ? cause.message : 'Could not load models.') }
    finally { setLoading(false) }
  }, [])
  useEffect(() => {void load()}, [load])
  return {models, loading, error, setError, load}
}

export function ModelCatalog(props: ModelCatalogProps) {
  const catalog = useCatalogModels()
  return <ModelCatalogContent {...props} catalog={catalog} />
}

export function ModelCatalogContent({session, isAdmin, changed, catalog}: ModelCatalogProps & {catalog: ReturnType<typeof useCatalogModels>}) {
  const {models, loading, error, setError, load} = catalog
  const detail = useDetailRoute('catalog')
  const selected = detail.route?.kind === 'model' ? models.find(model => model.id === detail.route?.id) : undefined
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<CatalogModel>()
  const [pending, setPending] = useState(false)
  const saved = async () => {await load(); await changed()}
  const archive = async (model: CatalogModel) => {
    setPending(true)
    try {await request(`/catalog/models/${encodeURIComponent(model.id)}`, {method: 'DELETE'}, session.csrfToken); await saved()}
    catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not archive model.')}
    finally {setPending(false)}
  }
  const active = models.filter(model => model.enabled).length
  const actions = (model: CatalogModel) => <>{isAdmin && <><ResourceAction action="edit" label={`Edit ${model.id}`} onClick={() => {setEditing(model); setOpen(true)}} />{model.enabled && <ResourceAction action="archive" label={`Archive ${model.id}`} disabled={pending || active <= 1} onClick={() => void archive(model)} />}</>}</>
  const dialog = open && <ModelDialog key={editing?.id ?? 'new'} editing={editing} close={() => setOpen(false)} session={session} saved={saved} />
  if (selected) return <>{dialog}<div className="detail-page"><DetailBreadcrumb parent="Catalog" current={selected.id} back={detail.close} /><div className="detail-page-heading"><h2>{selected.id}</h2><div className="detail-page-actions">{actions(selected)}</div></div><ResourceAttribution resource={selected} editable />{error && <p className="form-error" role="alert">{error}</p>}<article className="detail-page-card"><InputData value={{status: selected.enabled ? 'Enabled' : 'Archived', source: selected.origin === 'built-in' ? 'Included' : 'Custom', backend: selected.backend, quantization: selected.quantization, size: selected.size > 0 ? (selected.size / 1024 ** 3).toFixed(2) : '—', url: selected.url, sha256: selected.sha256}} schema={{properties: {size: {title: 'File size (GiB)'}, url: {title: 'Download URL'}, sha256: {title: 'SHA-256'}}}} /></article></div></>
  return <>
    {isAdmin && <ResourceListAction><ResourceAction action="create" label="Add model" onClick={() => {setEditing(undefined); setOpen(true)}} /></ResourceListAction>}
    {error && <p className="form-error" role="alert">{error}</p>}
    {loading ? <p role="status">Loading models…</p> : <ResourceListTable items={models.map(model => ({...model, createdAt: model.createdAt ?? '', updatedAt: model.updatedAt ?? ''}))} label="AI models" editable searchText={model => `${model.id} ${model.quantization} ${model.origin}`} columns={[
      {label: 'Name', render: model => <h3>{model.id}</h3>},
      {label: 'Quantization', render: model => model.quantization || '—'},
      {label: 'Status', render: model => model.enabled ? 'Enabled' : 'Archived'}
    ]} actions={model => <><ResourceAction action="view" label={`View ${model.id} details`} onClick={() => detail.open('model', model.id)} />{actions(model)}</>} />}
    {dialog}
  </>
}

function ModelDialog({editing, close, session, saved}: {editing?: CatalogModel; close: () => void; session: Session; saved: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const fields = new FormData(event.currentTarget)
    const model = editing ? {...editing, url: String(fields.get('url')).trim(), enabled: fields.has('enabled')} : {url: String(fields.get('url')).trim()}
    setPending(true); setError('')
    try {await request(editing ? `/catalog/models/${encodeURIComponent(editing.id)}` : '/catalog/models', {method: editing ? 'PUT' : 'POST', body: JSON.stringify(model)}, session.csrfToken); await saved(); close()}
    catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save model.')}
    finally {setPending(false)}
  }
  return <Dialog open title={editing ? 'Edit model' : 'Add model'} onClose={close}>
    <form className="form-stack" onSubmit={submit}>
      {editing ? <><label>Name<input readOnly value={editing.id} /></label><label>GGUF download URL<input name="url" type="url" required defaultValue={editing.url} /></label><label className="checkbox-field"><input name="enabled" type="checkbox" defaultChecked={editing.enabled} />Enabled</label></> : <label>GGUF download URL<input name="url" type="url" required placeholder="https://…/model.gguf" autoFocus /></label>}
      {error && <p className="form-error" role="alert">{error}</p>}
      <div className="dialog-actions"><button type="button" className="button secondary" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Saving…' : 'Save model'}</button></div>
    </form>
  </Dialog>
}
