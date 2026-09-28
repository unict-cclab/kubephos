import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import {formatDate} from '../lib'
import type {ClusterUserAssignment, ManagedResource, Session, User} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {Dialog} from './Dialog'

export function UsersView({users, assignments, clusters, session, changed}: {users: User[]; assignments: ClusterUserAssignment[]; clusters: ManagedResource[]; session: Session; changed: () => Promise<void>}) {
  const [creating, setCreating] = useState(false)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const selected = users.find(user => user.id === selectedID)
  if (selected) return <UserDetailsPage user={selected} close={() => setSelectedID(null)} assignments={assignments} clusters={clusters} session={session} changed={changed} />
  return <>
    <div className="section-heading"><div><h2>Users</h2></div><button className="button primary icon-button" aria-label="Create user" title="Create user" onClick={() => setCreating(true)}>+</button></div>
    <div className="table-card users-table-card"><table className="users-table"><thead><tr><th scope="col">User</th><th scope="col">Role</th><th scope="col">Clusters</th><th scope="col" aria-label="Actions" /></tr></thead><tbody>{users.length ? users.map(user => <tr key={user.id}><td><span className="user-identity"><strong>{user.username}</strong>{user.email && <small>{user.email}</small>}</span></td><td><span className={`user-role ${user.role}`}>{user.role === 'admin' ? 'Administrator' : 'User'}</span></td><td><span className="user-cluster-count">{assignments.filter(item => item.userId === user.id).length}</span></td><td className="users-table-action"><button className="icon-button subtle" aria-label={`View ${user.username}`} title="View" onClick={() => setSelectedID(user.id)}><svg viewBox="0 0 24 24" aria-hidden="true"><path d="M2.5 12s3.5-6 9.5-6 9.5 6 9.5 6-3.5 6-9.5 6-9.5-6-9.5-6Z"/><circle cx="12" cy="12" r="2.7"/></svg></button></td></tr>) : <tr><td className="users-table-empty" colSpan={4}>No users.</td></tr>}</tbody></table></div>
    <CreateUserDialog open={creating} close={() => setCreating(false)} session={session} changed={changed} />
  </>
}

function CreateUserDialog({open, close, session, changed}: {open: boolean; close: () => void; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      await request('/users', {method: 'POST', body: JSON.stringify({username: form.get('username'), email: form.get('email'), password: form.get('password'), role: form.get('role')})}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create user.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create user"><form onSubmit={submit}><label>Username<input name="username" minLength={3} maxLength={40} required autoFocus /></label><label>Email<input name="email" type="email" /></label><label>Password<input name="password" type="password" minLength={12} maxLength={128} required /></label><label>Role<select name="role" defaultValue="user"><option value="user">User</option><option value="admin">Administrator</option></select></label><p className="form-error">{error}</p><div className="dialog-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Creating…' : 'Create'}</button></div></form></Dialog>
}

function UserDetailsPage({user, close, assignments, clusters, session, changed}: {user: User; close: () => void; assignments: ClusterUserAssignment[]; clusters: ManagedResource[]; session: Session; changed: () => Promise<void>}) {
  const initial = useMemo(() => assignments.filter(item => item.userId === user.id).map(item => item.clusterResourceId), [assignments, user.id])
  const [selected, setSelected] = useState(initial)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => setSelected(initial), [initial])
  const save = async () => {
    setPending(true)
    setError('')
    try {
      await request(`/users/${user.id}/clusters`, {method: 'PUT', body: JSON.stringify({clusterIds: selected})}, session.csrfToken)
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save assignments.')
    } finally {
      setPending(false)
    }
  }
  const remove = async () => {
    if (!window.confirm(`Delete user ${user.username}?`)) return
    setPending(true)
    setError('')
    try {
      await request(`/users/${user.id}`, {method: 'DELETE'}, session.csrfToken)
      await changed()
      close()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not delete user.')
    } finally {
      setPending(false)
    }
  }
  return <div className="detail-page"><DetailBreadcrumb parent="Users" current={user.username} back={close} /><div className="detail-page-heading"><h2>{user.username}</h2><div className="detail-page-actions">{user.role === 'user' && <><button className="button danger" disabled={pending} onClick={() => void remove()}>Delete</button><button className="button primary" disabled={pending} onClick={() => void save()}>{pending ? 'Saving…' : 'Save'}</button></>}</div></div><article className="detail-page-card simple-resource-detail"><dl className="simple-detail-grid"><div><dt>Username</dt><dd>{user.username}</dd></div><div><dt>Email</dt><dd>{user.email || '—'}</dd></div><div><dt>Role</dt><dd>{user.role}</dd></div><div><dt>Status</dt><dd>{user.enabled ? 'Enabled' : 'Disabled'}</dd></div><div><dt>Created</dt><dd>{user.createdAt ? formatDate(user.createdAt) : '—'}</dd></div><div><dt>Updated</dt><dd>{user.updatedAt ? formatDate(user.updatedAt) : '—'}</dd></div></dl>{user.role === 'user' && <section className="details-section user-cluster-assignment"><div className="details-heading"><strong>Assigned clusters</strong><span>{selected.length} of {clusters.length}</span></div><div className="cluster-assignment-list">{clusters.length ? clusters.map(cluster => {
    const checked = selected.includes(cluster.id)
    return <label className={`cluster-assignment-row${checked ? ' selected' : ''}`} key={cluster.id}><input type="checkbox" checked={checked} onChange={event => setSelected(items => event.target.checked ? [...items, cluster.id] : items.filter(id => id !== cluster.id))} /><span className="cluster-assignment-check" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="m3.5 8.2 2.7 2.7 6.3-6.1" /></svg></span><span className="cluster-assignment-name">{cluster.name}</span><span className={`status ${cluster.status}`}>{cluster.status}</span></label>
  }) : <p className="cluster-assignment-empty">No Kubernetes clusters available.</p>}</div></section>}{error && <p className="form-error">{error}</p>}</article></div>
}
