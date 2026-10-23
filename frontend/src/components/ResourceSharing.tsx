import {ActionIcon} from './ActionIcon'
import {useState} from 'react'
import {request} from '../api'
import type {Session} from '../types'
import {Dialog} from './Dialog'

type ResourceKind = 'experiments' | 'experiment-configurations'
type Sharing = {visibility: 'private' | 'selected' | 'everyone'; userIds: string[]}
type Recipient = {id: string; username: string}

export function canManageResource(session: Session, ownerUserId?: string): boolean {
  return session.user?.role === 'admin' || Boolean(session.user?.id && session.user.id === ownerUserId)
}

export function ResourceSharing({kind, id, name, ownerUserId, session, changed}: {kind: ResourceKind; id: string; name: string; ownerUserId?: string; session: Session; changed: () => Promise<void>}) {
  const [open, setOpen] = useState(false)
  const [settings, setSettings] = useState<Sharing>({visibility: 'private', userIds: []})
  const [original, setOriginal] = useState('')
  const dirty = original !== sharingKey(settings)
  const [users, setUsers] = useState<Recipient[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  if (!canManageResource(session, ownerUserId)) return null
  const show = async () => {
    setOpen(true); setLoading(true); setLoaded(false); setError('')
    try {
      const [value, recipients] = await Promise.all([request<Sharing>(`/${kind}/${encodeURIComponent(id)}/sharing`), request<{items: Recipient[]}>('/sharing/users')])
      setSettings(value); setOriginal(sharingKey(value)); setUsers(recipients.items.filter(user => user.id !== ownerUserId)); setLoaded(true)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not load sharing settings.')}
    finally {setLoading(false)}
  }
  const save = async () => {
    if (!loaded || saving || !dirty) return
    setSaving(true); setError('')
    try {
      await request(`/${kind}/${encodeURIComponent(id)}/sharing`, {method: 'PUT', body: JSON.stringify({visibility: settings.visibility, userIds: settings.visibility === 'selected' ? settings.userIds : []})}, session.csrfToken)
      await changed(); setOpen(false)
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save sharing settings.')}
    finally {setSaving(false)}
  }
  return <><button className="button secondary icon-button" type="button" title={`Share ${name}`} aria-label={`Share ${name}`} onClick={() => void show()}><ActionIcon name="share" /></button><Dialog open={open} title={`Share ${name}`} className="resource-sharing-modal" onClose={saving ? undefined : () => setOpen(false)} footer={<>
    <button type="button" className="button secondary" disabled={saving} onClick={() => setOpen(false)}>Cancel</button>
    <button type="button" className="button primary" disabled={!loaded || saving || !dirty || settings.visibility === 'selected' && !settings.userIds.length} onClick={() => void save()}>{saving ? 'Saving…' : 'Save sharing'}</button>
  </>}>
    <div className="resource-sharing-fields">
      {loading ? <p role="status">Loading sharing settings…</p> : loaded && <>
        <label>Who can view<select value={settings.visibility} onChange={event => setSettings(value => ({...value, visibility: event.target.value as Sharing['visibility']}))} disabled={saving}>
          <option value="private">Private</option><option value="selected">Selected users</option><option value="everyone">All users</option>
        </select></label>
        {settings.visibility === 'selected' && <fieldset className="sharing-recipients" disabled={saving}><legend>Users</legend><div className="sharing-recipient-list">{users.length ? users.map(user => <label className="checkbox-field" key={user.id}><input type="checkbox" checked={settings.userIds.includes(user.id)} onChange={event => setSettings(value => ({...value, userIds: event.target.checked ? [...value.userIds, user.id] : value.userIds.filter(id => id !== user.id)}))} /><span>{user.username}</span></label>) : <p>No other users available.</p>}</div></fieldset>}
      </>}
      {error && <div className="sharing-error"><p className="form-error" role="alert">{error}</p>{!loaded && !loading && <button className="button secondary compact" type="button" onClick={() => void show()}>Retry</button>}</div>}
    </div>
  </Dialog></>
}

function sharingKey(value: Sharing) {
  return JSON.stringify({visibility: value.visibility, userIds: value.visibility === 'selected' ? [...value.userIds].sort() : []})
}
