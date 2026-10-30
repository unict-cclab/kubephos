import {ResourceStatus, ResourceConnectivity} from './ResourceConnectivity'
import {ResourceAttribution} from './ResourceAttribution'
import {useEffect, useState} from 'react'
import {request} from '../api'
import type {Connection, Session} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'

interface ConnectionDetails extends Connection {
  configuration: {endpoint?: string; verifyTLS?: boolean; tokenId?: string; tokenSecret?: string}
}

export function ConnectionDetailsPage({connection, session, close}: {connection: Connection; session: Session; close: () => void}) {
  const [details, setDetails] = useState<ConnectionDetails | null>(null)
  const [error, setError] = useState('')
  const [secret, setSecret] = useState('')
  const [visible, setVisible] = useState(false)
  const [pending, setPending] = useState(false)
  useEffect(() => {
    let cancelled = false
    setDetails(null)
    setError('')
    setSecret('')
    setVisible(false)
    request<ConnectionDetails>(`/connections/${encodeURIComponent(connection.id)}`).then(value => {if (!cancelled) setDetails(value)}).catch(cause => {if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load connection details.')})
    return () => {cancelled = true}
  }, [connection.id])
  const reveal = async () => {
    if (visible) {
      setVisible(false)
      return
    }
    if (secret) {
      setVisible(true)
      return
    }
    setPending(true)
    setError('')
    try {
      const result = await request<{tokenSecret: string}>(`/connections/${encodeURIComponent(connection.id)}/secret`, {method: 'POST', body: '{}'}, session.csrfToken)
      setSecret(result.tokenSecret)
      setVisible(true)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not reveal token secret.')
    } finally {
      setPending(false)
    }
  }
  return <div className="detail-page"><DetailBreadcrumb parent="Infrastructure" current={connection.name} back={close} /><div className="detail-page-heading"><div><h2>{connection.name}</h2></div><ResourceStatus resource={connection} /></div><ResourceAttribution resource={connection} editable /><article className="detail-page-card simple-resource-detail">

    {details ? <dl className="simple-detail-grid">
      <div><dt>Name</dt><dd>{details.name}</dd></div>
      <div><dt>Endpoint</dt><dd>{details.configuration.endpoint || '—'}</dd></div>
      <div><dt>Verify TLS</dt><dd>{details.configuration.verifyTLS ? 'Yes' : 'No'}</dd></div>
      <div><dt>Token ID</dt><dd>{details.configuration.tokenId || '—'}</dd></div>
      <div className="simple-secret-field"><dt>Token secret</dt><dd><input aria-label="Token secret" type={visible ? 'text' : 'password'} value={visible ? secret : details.configuration.tokenSecret ?? '••••••••'} readOnly />{session.user?.role === 'admin' && <button className="text-button" type="button" disabled={pending} onClick={() => void reveal()}>{pending ? 'Loading…' : visible ? 'Hide' : 'Show'}</button>}</dd></div>


    </dl> : error ? <p className="managed-error">{error}</p> : <p className="details-copy">Loading…</p>}
    <ResourceConnectivity value={connection.connectivity} />
    {details && error && <p className="managed-error">{error}</p>}
  </article></div>
}
