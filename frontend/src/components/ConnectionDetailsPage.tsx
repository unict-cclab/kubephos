import {useEffect, useState} from 'react'
import {request} from '../api'
import {formatDate} from '../lib'
import type {Connection, Credential} from '../types'
import {DetailBreadcrumb} from './DetailBreadcrumb'
import {InputData} from './InputData'

interface ConnectionDetails extends Connection {
  configuration: Record<string, unknown>
}

export function ConnectionDetailsPage({connection, credentials, close}: {connection: Connection; credentials: Credential[]; close: () => void}) {
  const [details, setDetails] = useState<ConnectionDetails | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    let cancelled = false
    setDetails(null)
    setError('')
    request<ConnectionDetails>(`/connections/${encodeURIComponent(connection.id)}`).then(value => {if (!cancelled) setDetails(value)}).catch(cause => {if (!cancelled) setError(cause instanceof Error ? cause.message : 'Could not load connection details.')})
    return () => {cancelled = true}
  }, [connection.id])
  const references = Object.fromEntries(credentials.map(item => [item.id, item.name]))
  return <div className="detail-page"><DetailBreadcrumb parent="Infrastructure" current={connection.name} back={close} /><div className="detail-page-heading"><div><h2>{connection.name}</h2><p>Proxmox connection</p></div><span className="status-pill ready">Ready</span></div><article className="detail-page-card">
    <div className="details-summary"><div><span>Provider</span><strong>{connection.provider}</strong></div><div><span>Created</span><strong>{formatDate(connection.createdAt)}</strong></div><div><span>Updated</span><strong>{formatDate(connection.updatedAt)}</strong></div></div>
    <section className="details-section"><div className="details-heading"><strong>Connection settings</strong></div>{details ? <InputData value={details.configuration} references={references} /> : error ? <p className="managed-error">{error}</p> : <p className="details-copy">Loading…</p>}</section>
  </article></div>
}
