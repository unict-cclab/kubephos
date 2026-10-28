import type {ManagedResource} from '../types'
import {ActionIcon} from './ActionIcon'

export function ClusterSSHAccess({resource}: {resource: ManagedResource}) {
  if (resource.kind !== 'kubernetes-cluster' || resource.status !== 'ready') return null
  const multi = resource.spec.mode === 'multi'
  const members = multi && Array.isArray(resource.spec.members)
    ? resource.spec.members.filter((member): member is Record<string, unknown> => !!member && typeof member === 'object')
    : [{...resource.spec, name: resource.name}]
  return <section className="details-section"><div className="details-heading"><strong>SSH access</strong></div>
    <div className="resource-list-table-wrap"><table className="resource-list-table" aria-label="Machine SSH access"><thead><tr><th scope="col">Cluster</th><th scope="col">Username</th><th scope="col">Private key</th></tr></thead><tbody>{members.map(member => {
      const name = String(member.name)
      const url = `/api/v1/kubernetes-clusters/${encodeURIComponent(resource.id)}/ssh-key${multi ? `?cluster=${encodeURIComponent(name)}` : ''}`
      return <tr key={name}><td>{name}</td><td>{String(member.sshUser || '—')}</td><td><a className="button secondary compact ssh-key-download" href={url} aria-label={`Download SSH key for ${name}`}><ActionIcon name="download" />Download</a></td></tr>
    })}</tbody></table></div>
  </section>
}
