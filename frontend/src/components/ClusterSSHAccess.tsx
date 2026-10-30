import {ResponsiveTable} from './ResponsiveTable'
import type {ManagedResource} from '../types'
import {ActionIcon} from './ActionIcon'

export function ClusterSSHAccess({resource, resources = []}: {resource: ManagedResource; resources?: ManagedResource[]}) {
  if (resource.kind !== 'kubernetes-cluster' || resource.status !== 'ready') return null
  const multi = resource.spec.mode === 'multi'
  const shared = multi && resource.spec.sharedSSHKey === true
  const downloadURL = `/api/v1/kubernetes-clusters/${encodeURIComponent(resource.id)}/ssh-key`
  const templateUser = resources.find(item => item.id === resource.spec.templateId)?.spec.sshUser
  const members = multi && !shared && Array.isArray(resource.spec.members)
    ? resource.spec.members.filter((member): member is Record<string, unknown> => !!member && typeof member === 'object')
    : []
  return <section className="details-section"><div className="details-heading"><strong>SSH access</strong></div>
    {!multi || shared ? <a className="button secondary compact ssh-key-download" href={downloadURL}><ActionIcon name="download" />Download SSH key</a> : <div className="resource-list-table-wrap"><ResponsiveTable className="resource-list-table" aria-label="Machine SSH access"><thead><tr><th scope="col">Cluster</th><th scope="col">Username</th><th scope="col">Private key</th></tr></thead><tbody>{members.map(member => {
      const name = String(member.name)
      const url = `${downloadURL}?cluster=${encodeURIComponent(name)}`
      return <tr key={name}><td>{name}</td><td>{String(member.sshUser || templateUser || '—')}</td><td><a className="button secondary compact ssh-key-download" href={url} aria-label={`Download SSH key for ${name}`}><ActionIcon name="download" />Download</a></td></tr>
    })}</tbody></ResponsiveTable></div>}
  </section>
}
