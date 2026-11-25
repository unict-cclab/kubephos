import {useEffect, useState} from 'react'
import {request} from '../../api'
import type {Session} from '../../types'
import {ActionIcon} from '../ActionIcon'
import {Dialog} from '../Dialog'
import {ErrorFeedback, useErrorState} from '../ToastRegion'

// Who can see an experiment, from the results page's ⋯ menu: the item names the current state
// ("Private", "Visible to all users"…) and opens a dialog to change it. Same API as the shared
// ResourceSharing control (/experiments/{id}/sharing), worded as what it is: visibility.

export type Sharing = {visibility: 'private' | 'selected' | 'everyone'; userIds: string[]}
type Recipient = {id: string; username: string}

const sharingPath = (id: string) => `/experiments/${encodeURIComponent(id)}/sharing`
const valid = (value: unknown): value is Sharing => Boolean(value) && typeof value === 'object' && ['private', 'selected', 'everyone'].includes((value as Sharing).visibility) && Array.isArray((value as Sharing).userIds)
export const visibilityLabel = (value: Sharing) => value.visibility === 'private' ? 'Private' : value.visibility === 'everyone' ? 'Visible to all users' : `Shared with ${value.userIds.length} user${value.userIds.length === 1 ? '' : 's'}`

// The ⋯ menu item: loads the current state when the menu opens.
export function VisibilityMenuItem({id, known, onOpen}: {id: string; known?: Sharing; onOpen: () => void}) {
  const [current, setCurrent] = useState<Sharing | undefined>(known)
  useEffect(() => {
    if (known) {setCurrent(known); return}
    let active = true
    request<unknown>(sharingPath(id)).then(value => {if (active && valid(value)) setCurrent(value)}, () => {})
    return () => {active = false}
  }, [id, known])
  return <button type="button" className="menu-item" onClick={onOpen}><ActionIcon name="view" /><span className="menu-item-text">Visibility…<small>{current ? visibilityLabel(current) : 'Who can see this experiment'}</small></span></button>
}

export function VisibilityDialog({id, name, ownerUserId, open, session, onClose, onSaved}: {id: string; name: string; ownerUserId?: string; open: boolean; session: Session; onClose: () => void; onSaved: (value: Sharing) => Promise<void> | void}) {
  const [settings, setSettings] = useState<Sharing>({visibility: 'private', userIds: []})
  const [original, setOriginal] = useState('')
  const [users, setUsers] = useState<Recipient[]>([])
  const [loaded, setLoaded] = useState(false)
  const [saving, setSaving] = useState(false)
  const [revision, setRevision] = useState(0)
  const [error, setError] = useErrorState()
  const key = (value: Sharing) => JSON.stringify({visibility: value.visibility, userIds: value.visibility === 'selected' ? [...value.userIds].sort() : []})
  useEffect(() => {
    if (!open) return
    let active = true
    setLoaded(false); setError('')
    Promise.all([request<Sharing>(sharingPath(id)), request<{items: Recipient[]}>('/sharing/users')]).then(([value, recipients]) => {
      if (!active) return
      setSettings(value); setOriginal(key(value)); setUsers((recipients?.items ?? []).filter(user => user.id !== ownerUserId)); setLoaded(true)
    }, (cause: unknown) => {if (active) setError(cause instanceof Error ? cause.message : 'Could not load who can see this experiment.')})
    return () => {active = false}
  }, [open, id, revision])
  const dirty = original !== key(settings)
  const save = async () => {
    if (!loaded || saving || !dirty) return
    setSaving(true); setError('')
    try {
      const next = {visibility: settings.visibility, userIds: settings.visibility === 'selected' ? settings.userIds : []}
      await request(sharingPath(id), {method: 'PUT', body: JSON.stringify(next)}, session.csrfToken)
      await onSaved(next)
      onClose()
    } catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not save who can see this experiment.')}
    finally {setSaving(false)}
  }
  return <Dialog open={open} title={`Visibility of ${name}`} className="resource-sharing-modal" onClose={saving ? undefined : onClose} footer={<>
    <button type="button" className="button secondary" disabled={saving} onClick={onClose}>Cancel</button>
    <button type="button" className="button primary" disabled={!loaded || saving || !dirty || settings.visibility === 'selected' && !settings.userIds.length} onClick={() => void save()}>{saving ? 'Saving…' : 'Save visibility'}</button>
  </>}>
    <div className="resource-sharing-fields">
      {!loaded && !error ? <p role="status">Loading…</p> : loaded && <>
        <label>Who can see it<select value={settings.visibility} onChange={event => setSettings(value => ({...value, visibility: event.target.value as Sharing['visibility']}))} disabled={saving}>
          <option value="private">Private: only you (and administrators)</option><option value="selected">Selected users</option><option value="everyone">All users</option>
        </select></label>
        {settings.visibility === 'selected' && <fieldset className="sharing-recipients" disabled={saving}><legend>Users</legend><div className="sharing-recipient-list">{users.length ? users.map(user => <label className="checkbox-field" key={user.id}><input type="checkbox" checked={settings.userIds.includes(user.id)} onChange={event => setSettings(value => ({...value, userIds: event.target.checked ? [...value.userIds, user.id] : value.userIds.filter(entry => entry !== user.id)}))} /><span>{user.username}</span></label>) : <p>No other users available.</p>}</div></fieldset>}
      </>}
      {error && <div className="sharing-error"><ErrorFeedback message={error} />{!loaded && <button className="button secondary compact" type="button" onClick={() => setRevision(value => value + 1)}>Retry</button>}</div>}
    </div>
  </Dialog>
}
