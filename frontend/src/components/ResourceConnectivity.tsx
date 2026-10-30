import {currentConnectivity, resourceStatus} from '../connectivity'
import {formatDate} from '../lib'
import type {Connectivity, Connection, ManagedResource} from '../types'
import {ResponsiveTable} from './ResponsiveTable'
import {Status} from './Status'

type Resource = Pick<ManagedResource, 'status' | 'connectivity'> | Pick<Connection, 'connectivity'>

export function ResourceStatus({resource, override}: {resource: Resource; override?: string}) {
  const report = currentConnectivity(resource.connectivity)
  return <Status value={override ?? resourceStatus(resource)} title={report ? [report.message, `Last checked: ${formatDate(report.checkedAt)}`].filter(Boolean).join(' · ') : undefined} />
}

export function ResourceConnectivity({value}: {value?: Connectivity}) {
  const report = currentConnectivity(value)
  if (!report) return null
  return <section className="details-section">
    <div className="details-heading"><strong>Connectivity</strong></div>
    <dl className="simple-detail-grid"><div><dt>Last checked</dt><dd>{formatDate(report.checkedAt)}</dd></div></dl>
    {(report.targets?.length ?? 0) > 1 ? <div className="readout-table-wrap"><ResponsiveTable className="readout-table" aria-label="Cluster connectivity"><thead><tr><th>Cluster</th><th>Status</th></tr></thead><tbody>{report.targets!.map(target => <tr key={target.name}><td>{target.name}</td><td><Status value={target.status} />{target.message && <p>{target.message}</p>}</td></tr>)}</tbody></ResponsiveTable></div> : null}
    {report.message && <p className="details-copy">{report.message}</p>}
  </section>
}
