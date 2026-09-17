import {useEffect, useState} from 'react'
import {request} from '../api'
import {formatDate, humanize} from '../lib'
import type {Application} from '../types'
import {ApplicationTopologyGraph} from './ApplicationTopologyGraph'
import {DetailBreadcrumb} from './DetailBreadcrumb'

interface Props {
  application: Application | null
  close: () => void
  canDelete: boolean
  remove: (application: Application) => Promise<boolean>
}

export function ApplicationDetailsPage({application, close, canDelete, remove}: Props) {
  const [manifest, setManifest] = useState('')
  const [manifestLoading, setManifestLoading] = useState(false)
  const [manifestError, setManifestError] = useState('')
  useEffect(() => { setManifest(''); setManifestError(''); setManifestLoading(false) }, [application?.reference])
  if (!application) return null
  const specification = application.descriptor.spec ?? {}
  const source = specification.package ?? {}
  const contract = specification.interface ?? {}
  const components = contract.components ?? []
  const endpoints = contract.endpoints ?? []
  const scenarios = contract.loadScenarios ?? []
  const generation = specification.materializerConfig ?? {}
  const orderedComponents = [...components].sort((left, right) => (left.index ?? Number.MAX_SAFE_INTEGER) - (right.index ?? Number.MAX_SAFE_INTEGER) || left.id.localeCompare(right.id))
  const loadManifest = async () => {
    setManifestLoading(true)
    setManifestError('')
    try {
      const result = await request<{content: string}>(`/catalog/applications/${encodeURIComponent(application.id)}/${encodeURIComponent(application.version)}/manifest`)
      setManifest(result.content)
    } catch (cause) {
      setManifestError(cause instanceof Error ? cause.message : 'Could not prepare the deployment manifest.')
    } finally {
      setManifestLoading(false)
    }
  }
  const downloadManifest = () => {
    const url = URL.createObjectURL(new Blob([manifest], {type: 'application/yaml;charset=utf-8'}))
    const link = document.createElement('a')
    link.href = url
    link.download = `${application.name.toLowerCase().replaceAll(/[^a-z0-9]+/g, '-').replaceAll(/(^-|-$)/g, '') || 'application'}-${application.version}.yml`
    link.click()
    URL.revokeObjectURL(url)
  }
  return <div className="detail-page"><DetailBreadcrumb parent="Catalog" current={application.name} back={close} /><div className="detail-page-heading"><div><h2>{application.name}</h2><p>{application.version} · {application.origin}</p></div><div className="detail-page-actions">{canDelete && <button className="button danger" onClick={() => remove(application)}>Delete from catalog</button>}</div></div><article className="detail-page-card">
    <div className="details-summary">
      <div><span>Version</span><strong>{application.version}</strong></div>
      <div><span>Application group</span><strong>{contract.group ?? '—'}</strong></div>
      <div><span>Origin</span><strong>{application.origin}</strong></div>
      <div><span>Deployment</span><strong>{String(source.type ?? 'Included')}</strong></div>
      <div><span>Created</span><strong>{application.createdAt ? formatDate(application.createdAt) : '—'}</strong></div>
      <div><span>Updated</span><strong>{application.updatedAt ? formatDate(application.updatedAt) : '—'}</strong></div>
    </div>
    {application.description && <section className="details-section"><div className="details-heading"><strong>Description</strong></div><p className="details-copy">{application.description}</p></section>}
    {Object.keys(generation).length > 0 && <section className="details-section"><div className="details-heading"><strong>Generated application</strong></div><div className="application-value-list">{Object.entries(generation).filter(([name]) => name !== 'serviceImage').map(([name, value]) => <div key={name}><strong>{humanize(name)}</strong><span>Generation setting</span><code>{String(value)}</code></div>)}</div></section>}
    <ApplicationTopologyGraph components={components} />
    <section className="details-section application-manifest-section"><div className="details-heading"><div><strong>Deployment manifest</strong><span>Validated default Kubernetes resources</span></div><div className="chart-export-actions">{manifest && <button type="button" onClick={downloadManifest}>Download .yml</button>}<button type="button" disabled={manifestLoading} onClick={() => void loadManifest()}>{manifestLoading ? 'Preparing…' : manifest ? 'Refresh' : 'View YAML'}</button></div></div>{manifestError && <p className="form-error">{manifestError}</p>}{manifest ? <pre aria-label="Deployment manifest YAML">{manifest}</pre> : <p className="details-copy">Generate the fully validated manifest using the application's default settings.</p>}</section>
    <section className="details-section"><div className="details-heading"><strong>Services</strong><span>{components.length}</span></div><div className="application-details-list">{orderedComponents.map(component => <article key={component.id}><div><strong>{component.id}</strong><span>{component.workload ? `${component.workload.kind ?? 'Workload'} · ${component.workload.name ?? component.id}` : 'Workload'}</span></div><div className="application-component-meta">{component.index !== undefined && <span className="topology-index">Index {component.index}</span>}<div className="trait-list">{(component.traits ?? []).map(trait => <span key={trait}>{trait}</span>)}</div></div></article>)}</div></section>
    <section className="details-section"><div className="details-heading"><strong>Endpoints</strong><span>{endpoints.length}</span></div>{endpoints.length ? <div className="details-dependencies">{endpoints.map((endpoint, index) => <div key={String(endpoint.id ?? index)}><span>{String(endpoint.protocol ?? 'endpoint')}</span><strong>{String(endpoint.id ?? endpoint.service ?? index)}</strong><small>{String(endpoint.service ?? '—')}:{String(endpoint.port ?? '—')}{String(endpoint.path ?? '')}</small></div>)}</div> : <p className="details-copy">No exposed endpoint.</p>}</section>
    <section className="details-section"><div className="details-heading"><strong>Load scenarios</strong><span>{scenarios.length}</span></div>{scenarios.length ? <div className="details-dependencies">{scenarios.map((scenario, index) => <div key={String(scenario.id ?? index)}><span>{String(scenario.engine ?? 'load')}</span><strong>{String(scenario.id ?? index)}</strong><small>Target: {String(scenario.targetEndpoint ?? '—')}</small></div>)}</div> : <p className="details-copy">No load scenario available.</p>}</section>
    <section className="details-section"><div className="details-heading"><strong>Application settings</strong></div>{Object.keys(specification.valuesSchema?.properties ?? {}).length ? <div className="application-value-list">{Object.entries(specification.valuesSchema?.properties ?? {}).map(([name, property]) => <div key={name}><strong>{property.title ?? name}</strong><span>{property.description ?? property.type ?? 'value'}</span><code>{String(specification.defaults?.[name] ?? property.default ?? 'no default')}</code></div>)}</div> : <p className="details-copy">No custom settings.</p>}</section>
  </article></div>
}
