import {useState, type FormEvent, type ReactNode} from 'react'
import {request} from '../api'
import {formatDate, formatRelativeTime} from '../lib'
import type {AdminRequest, Session} from '../types'
import {Dialog} from './Dialog'
import {ResourceListHeading} from './ResourceListHeading'
import {ErrorFeedback, useErrorState} from './ToastRegion'

// Requests work like tickets, not a chat: the limits match the API.
const maxTextLength = 4000
const maxOpenRequests = 3

interface Props {
  requests: AdminRequest[]
  isAdmin: boolean
  session: Session
  changed: (message?: string) => Promise<void>
}

// Administrators review every request; other users write to them and follow their own.
export function RequestsView(props: Props) {
  return props.isAdmin ? <RequestInbox {...props} /> : <RequestComposer {...props} />
}

function RequestInbox({requests, session, changed}: Props) {
  const [filter, setFilter] = useState<AdminRequest['status']>('open')
  const [resolving, setResolving] = useState<AdminRequest | null>(null)
  const [pending, setPending] = useState<string | null>(null)
  const [error, setError] = useErrorState()
  const counts = {open: requests.filter(item => item.status === 'open').length, resolved: requests.filter(item => item.status === 'resolved').length}
  const visible = requests.filter(item => item.status === filter)
  const reopen = async (item: AdminRequest) => {
    setPending(item.id)
    setError('')
    try {
      await request(`/admin-requests/${encodeURIComponent(item.id)}`, {method: 'PATCH', body: JSON.stringify({status: 'open'})}, session.csrfToken)
      await changed('Request reopened.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not reopen the request.')
    } finally {
      setPending(null)
    }
  }
  const action = (item: AdminRequest) => item.status === 'open'
    ? <button type="button" className="button secondary compact" onClick={() => setResolving(item)}>Resolve</button>
    : <button type="button" className="button secondary compact" disabled={pending === item.id} onClick={() => void reopen(item)}>{pending === item.id ? 'Reopening…' : 'Reopen'}</button>
  return <>
    <ResourceListHeading title="Requests" />
    {error && <ErrorFeedback message={error} />}
    <div className="history-filters" role="group" aria-label="Filter requests">
      {(['open', 'resolved'] as const).map(value => <button key={value} type="button" className={filter === value ? 'active' : ''} aria-pressed={filter === value} onClick={() => setFilter(value)}>{value === 'open' ? 'Open' : 'Resolved'} <span className="request-filter-count">{counts[value]}</span></button>)}
    </div>
    {visible.length
      ? <ul className="request-list" aria-label={filter === 'open' ? 'Open requests' : 'Resolved requests'}>{visible.map(item => <li key={item.id}><RequestCard item={item} showRequester action={action(item)} /></li>)}</ul>
      : <div className="empty-state request-empty"><strong>{filter === 'open' ? 'No open requests' : 'No resolved requests'}</strong><p>{filter === 'open' ? 'Messages from other users appear here.' : 'Requests you resolve are kept here.'}</p></div>}
    {resolving && <ResolveDialog item={resolving} session={session} changed={changed} close={() => setResolving(null)} />}
  </>
}

// Resolving closes the ticket; the optional reply is the only answer the member receives.
function ResolveDialog({item, session, changed, close}: {item: AdminRequest; session: Session; changed: Props['changed']; close: () => void}) {
  const [reply, setReply] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    try {
      await request(`/admin-requests/${encodeURIComponent(item.id)}`, {method: 'PATCH', body: JSON.stringify({status: 'resolved', reply})}, session.csrfToken)
      close()
      await changed('Request resolved.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not resolve the request.')
    } finally {
      setPending(false)
    }
  }
  return <Dialog open title={`Resolve request from ${item.requester}`} onClose={pending ? undefined : close}>
    <form className="form-stack" onSubmit={submit} aria-busy={pending}>
      <p className="request-message request-quoted">{item.message}</p>
      <label>Reply (optional)<textarea name="reply" rows={4} maxLength={maxTextLength} value={reply} autoFocus disabled={pending} placeholder="Shared the edge cluster with you." onChange={event => setReply(event.target.value)} /></label>
      <p className="details-copy">{item.requester} sees this reply with the resolved request and cannot answer it.</p>
      <ErrorFeedback message={error} />
      <div className="dialog-actions"><button type="button" className="button secondary" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Resolving…' : 'Resolve request'}</button></div>
    </form>
  </Dialog>
}

function RequestComposer({requests, session, changed}: Props) {
  const [message, setMessage] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useErrorState()
  const open = requests.filter(item => item.status === 'open').length
  const full = open >= maxOpenRequests
  const send = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!message.trim()) {
      setError('Write a message before sending it.')
      return
    }
    setSending(true)
    setError('')
    try {
      await request('/admin-requests', {method: 'POST', body: JSON.stringify({message})}, session.csrfToken)
      setMessage('')
      await changed('Request sent to the administrators.')
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not send the request.')
    } finally {
      setSending(false)
    }
  }
  return <>
    <ResourceListHeading title="Ask an admin" />
    <form className="request-composer" onSubmit={send}>
      <label>Message<textarea name="message" rows={5} maxLength={maxTextLength} required disabled={full} value={message} placeholder="I need access to the edge cluster for my experiments." onChange={event => setMessage(event.target.value)} /></label>
      <div className="request-composer-footer">
        <span className="request-composer-hint">{full
          ? `You have ${maxOpenRequests} open requests. You can send a new one when an administrator resolves one of them.`
          : `Put everything in one message: an administrator resolves it and may reply once. ${open} of ${maxOpenRequests} open requests.`}</span>
        <span className="request-composer-count" aria-live="polite">{message.length}/{maxTextLength}</span>
        <button className="button primary" type="submit" disabled={sending || full}>{sending ? 'Sending…' : 'Send request'}</button>
      </div>
      {error && <ErrorFeedback message={error} />}
    </form>
    <h3 className="request-section-title">Your requests</h3>
    {requests.length
      ? <ul className="request-list" aria-label="Your requests">{requests.map(item => <li key={item.id}><RequestCard item={item} /></li>)}</ul>
      : <div className="empty-state request-empty"><strong>No requests yet</strong><p>Requests you send appear here with their status.</p></div>}
  </>
}

function RequestCard({item, showRequester = false, action}: {item: AdminRequest; showRequester?: boolean; action?: ReactNode}) {
  const resolved = item.status === 'resolved'
  return <article className={`request-card is-${item.status}`}>
    <header className="request-card-head">
      {showRequester && <span className="list-person"><span className="list-person-avatar" aria-hidden="true">{item.requester.charAt(0)}</span>{item.requester}</span>}
      <time dateTime={item.createdAt} title={formatDate(item.createdAt)}>{formatRelativeTime(item.createdAt)}</time>
      <span className={`status ${resolved ? 'succeeded' : 'waiting'}`}>{resolved ? 'Resolved' : 'Open'}</span>
    </header>
    <p className="request-message">{item.message}</p>
    {resolved && item.reply && <blockquote className="request-reply"><span className="request-reply-author">Reply from {item.resolvedBy || 'an administrator'}</span><p className="request-message">{item.reply}</p></blockquote>}
    {((resolved && item.resolvedBy) || action) && <footer className="request-card-foot">
      {resolved && item.resolvedBy && <span>Resolved by {item.resolvedBy}{item.resolvedAt && <> · <time dateTime={item.resolvedAt} title={formatDate(item.resolvedAt)}>{formatRelativeTime(item.resolvedAt)}</time></>}</span>}
      {action}
    </footer>}
  </article>
}
