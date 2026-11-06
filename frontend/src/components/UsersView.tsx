import {ValidatedInput} from './ValidatedInput'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {ResourceListHeading} from './ResourceListHeading'
import {ResourceListTable} from './ResourceListTable'
import {ResourceAttribution} from './ResourceAttribution'
import {ResourceAction} from './ResourceAction'
import {useDeleteConfirmation} from './ConfirmationDialog'
import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {ClusterUserAssignment, ManagedResource, Session, User} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {Dialog} from './Dialog'

export function UsersView({users, assignments, clusters, session, changed}: {users: User[]; assignments: ClusterUserAssignment[]; clusters: ManagedResource[]; session: Session; changed: () => Promise<void>}) {
  const [creating, setCreating] = useState(false)
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [account, setAccount] = useState<{user: User; mode: 'email' | 'password'} | null>(null)
  const selected = users.find(user => user.id === selectedID)
  const accountDialog = account && <UserAccountDialog key={`${account.user.id}-${account.mode}`} user={account.user} mode={account.mode} close={() => setAccount(null)} session={session} changed={changed} />
  if (selected) return <><UserDetailsPage user={selected} close={() => setSelectedID(null)} assignments={assignments} clusters={clusters} session={session} changed={changed} edit={() => setAccount({user: selected, mode: 'email'})} reset={() => setAccount({user: selected, mode: 'password'})} />{accountDialog}</>
  return <>
    <ResourceListHeading title="Users" action={<ResourceAction action="create" label="Create user" onClick={() => setCreating(true)} />} />
    <ResourceListTable items={users.map(user => ({...user, createdAt: user.createdAt ?? '', updatedAt: user.updatedAt ?? ''}))} label="Users" editable searchText={user => `${user.username} ${user.email ?? ''} ${user.role}`} columns={[
      {label: 'Username', render: user => <span className="user-identity">{user.username}</span>},
      {label: 'Email', render: user => user.email || '—'},
      {label: 'Role', render: user => <span className={`user-role ${user.role}`}>{user.role === 'admin' ? 'Administrator' : 'User'}</span>},
      {label: 'Clusters', render: user => <span className="user-cluster-count">{user.role === 'admin' ? 'All' : assignments.filter(item => item.userId === user.id).length}</span>}
    ]} actions={user => <ResourceAction action="view" label={`View ${user.username}`} onClick={() => setSelectedID(user.id)} />} />
    {creating && <CreateUserDialog close={() => setCreating(false)} session={session} changed={changed} />}
  </>
}

function CreateUserDialog({close, session, changed}: {close: () => void; session: Session; changed: () => Promise<void>}) {
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
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
  return <Dialog open onClose={close} title="Create user"><form className="form-stack" onSubmit={submit}><div className="field-row"><label>Username<ValidatedInput aria-label="Username" name="username" minLength={3} maxLength={40} required autoFocus /></label><label>Email<ValidatedInput aria-label="Email" name="email" type="email" /></label></div><div className="field-row"><label>Temporary password<ValidatedInput aria-label="Temporary password" name="password" autoComplete="new-password" type="password" minLength={12} maxLength={128} required /></label><label>Role<select name="role" defaultValue="user"><option value="user">User</option><option value="admin">Administrator</option></select></label></div><ErrorFeedback message={error} /><div className="dialog-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Creating…' : 'Create'}</button></div></form></Dialog>
}

function UserDetailsPage({user, close, assignments, clusters, session, changed, edit, reset}: {user: User; close: () => void; edit: () => void; reset: () => void; assignments: ClusterUserAssignment[]; clusters: ManagedResource[]; session: Session; changed: () => Promise<void>}) {
  const {confirmDelete, confirmAction, confirmationDialog} = useDeleteConfirmation()
  const initial = useMemo(() => assignments.filter(item => item.userId === user.id).map(item => item.clusterResourceId), [assignments, user.id])
  const [selected, setSelected] = useState(initial)
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
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
    if (!await confirmDelete(`Delete user ${user.username}?`)) return
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
  const promote = user.role !== 'admin'
  const changeRole = async () => {
    const confirmed = await confirmAction(promote
      ? `${user.username} will manage users, resources and requests, and see every cluster.`
      : `${user.username} will only see the clusters assigned to them.`,
    {title: promote ? `Make ${user.username} an administrator?` : `Make ${user.username} a user?`, label: promote ? 'Make administrator' : 'Make user'})
    if (!confirmed) return
    setPending(true)
    setError('')
    try {
      await request(`/users/${user.id}/role`, {method: 'PUT', body: JSON.stringify({role: promote ? 'admin' : 'user'})}, session.csrfToken)
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not change the role.')
    } finally {
      setPending(false)
    }
  }
  const self = user.id === session.user?.id
  return <div className="detail-page">{confirmationDialog}<DetailBreadcrumb parent="Users" current={user.username} back={close} /><div className="detail-page-heading"><h2>{user.username}</h2><div className="detail-page-actions"><button className="button secondary" onClick={edit} disabled={pending}>Edit email</button><button className="button secondary" onClick={reset} disabled={pending}>Reset password</button>{!self && <button className="button secondary" onClick={() => void changeRole()} disabled={pending}>{promote ? 'Make administrator' : 'Make user'}</button>}{!self && <ResourceAction action="delete" label={`Delete user ${user.username}`} disabled={pending} onClick={() => void remove()} />}{user.role === 'user' && <button className="button primary" disabled={pending} onClick={() => void save()}>{pending ? 'Saving…' : 'Save'}</button>}</div></div><ResourceAttribution resource={user} editable /><article className="detail-page-card simple-resource-detail"><dl className="simple-detail-grid"><div><dt>Username</dt><dd>{user.username}</dd></div><div><dt>Email</dt><dd>{user.email || '—'}</dd></div><div><dt>Role</dt><dd>{user.role === 'admin' ? 'Administrator' : 'User'}</dd></div><div><dt>Status</dt><dd>{user.enabled ? 'Enabled' : 'Disabled'}</dd></div></dl>{user.role === 'user' && <section className="details-section user-cluster-assignment"><div className="details-heading"><strong>Assigned clusters</strong><span>{selected.length} of {clusters.length}</span></div><div className="cluster-assignment-list">{clusters.length ? clusters.map(cluster => {
    const checked = selected.includes(cluster.id)
    return <label className={`cluster-assignment-row${checked ? ' selected' : ''}`} key={cluster.id}><input type="checkbox" checked={checked} onChange={event => setSelected(items => event.target.checked ? [...items, cluster.id] : items.filter(id => id !== cluster.id))} /><span className="cluster-assignment-check" aria-hidden="true"><svg viewBox="0 0 16 16"><path d="m3.5 8.2 2.7 2.7 6.3-6.1" /></svg></span><span className="cluster-assignment-name">{cluster.name}</span><span className={`status ${cluster.status}`}>{cluster.status}</span></label>
  }) : <p className="cluster-assignment-empty">No Kubernetes clusters available.</p>}</div></section>}{error && <ErrorFeedback message={error} />}</article></div>
}

function UserAccountDialog({user,mode,close,session,changed}: {user:User;mode:'email'|'password';close:()=>void;session:Session;changed:()=>Promise<void>}) {
 const [password,setPassword]=useState('')
 const [confirmation,setConfirmation]=useState('')
 const [pending,setPending]=useState(false)
 const [error,setError]=useErrorState()
 const submit=async(event:FormEvent<HTMLFormElement>)=>{
  event.preventDefault()
  const form=new FormData(event.currentTarget)
  if(mode==='password'&&form.get('password')!==form.get('confirmation')){setError('Passwords do not match.');return}
  setPending(true);setError('')
  try{
   await request(`/users/${user.id}${mode==='password'?'/password':''}`,{method:mode==='password'?'POST':'PATCH',body:JSON.stringify(mode==='password'?{password:form.get('password')}:{email:form.get('email')})},session.csrfToken)
   close();await changed()
  }catch(cause){setError(cause instanceof Error?cause.message:'Could not update the user.')}
  finally{setPending(false)}
 }
 return <Dialog open onClose={()=>{if(!pending)close()}} title={`${mode==='email'?'Edit email':'Reset password'} · ${user.username}`}><form className="form-stack" onSubmit={submit}>
 {mode==='email'?<label>Email<ValidatedInput aria-label="Email" name="email" type="email" defaultValue={user.email||''} autoFocus /></label>:<><label>Temporary password<ValidatedInput aria-label="Temporary password" name="password" type="password" autoComplete="new-password" minLength={12} maxLength={128} required autoFocus value={password} onChange={event=>setPassword(event.target.value)}/></label><label>Confirm password<ValidatedInput aria-label="Confirm password" name="confirmation" type="password" autoComplete="new-password" minLength={12} maxLength={128} required value={confirmation} onChange={event=>setConfirmation(event.target.value)} error={confirmation&&confirmation!==password?'Passwords do not match.':''}/></label><p className="details-copy">A password change is required at the next sign-in.</p></>}
 {error&&<ErrorFeedback message={error} />}
 <div className="dialog-actions"><button className="button secondary" type="button" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending}>{pending?'Saving…':mode==='email'?'Save':'Reset password'}</button></div>
 </form></Dialog>
}
