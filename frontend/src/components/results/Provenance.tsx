import type {ResolvedProvenance} from './runs'
import {metricTitle, provenanceLabel, sourceName} from './format'

const vantageDescriptions: Record<string, string> = {
  client: 'Client side: measured by the load generator, end to end (includes network and mesh proxies).',
  server: 'Server side: reported by the mesh data plane at the destination workload.',
  resource: 'Resource usage of the application workloads.',
  network: 'Network path measurement.',
  'control-plane': 'Kubernetes control-plane component.',
  configuration: 'Configured value, not a measurement.'
}

export const legacyProvenanceNote = 'This dataset predates recorded provenance: source and description come from the KubePhos metric catalog, and the exact query was not stored.'

export function ProvenanceBadge({provenance}: {provenance: ResolvedProvenance}) {
  const label = provenanceLabel(provenance)
  return <span className={`provenance-badge ${provenance.legacy ? 'legacy' : ''} ${provenance.mixed ? 'mixed' : ''}`} data-source={provenance.source} title={[provenance.description, provenance.legacy ? legacyProvenanceNote : ''].filter(Boolean).join(' ')}>
    {label}{provenance.legacy && <small> · catalog</small>}{provenance.mixed && <small> · mixed sources</small>}
  </span>
}

function Details({metric, provenance}: {metric: string; provenance: ResolvedProvenance}) {
  return <>
    <dl className="provenance-details">
      <div><dt>Metric</dt><dd><code>{metric}</code> · {metricTitle(metric, provenance)}</dd></div>
      <div><dt>Source</dt><dd>{sourceName(provenance.source)}{provenance.collector ? ` via ${provenance.collector}` : ''}</dd></div>
      <div><dt>Vantage point</dt><dd>{vantageDescriptions[provenance.vantage] ?? provenance.vantage}</dd></div>
      {provenance.description && <div><dt>Description</dt><dd>{provenance.description}</dd></div>}
      {provenance.rateWindowSeconds !== undefined && <div><dt>Rate window</dt><dd>{provenance.rateWindowSeconds} s</dd></div>}
      {provenance.query && <div><dt>Query</dt><dd><code className="provenance-query">{provenance.query}</code></dd></div>}
    </dl>
    {provenance.legacy && <p className="provenance-note">{legacyProvenanceNote}</p>}
    {provenance.mixed && <p className="provenance-note">The selected runs report this metric from different sources or vantage points; values may not be directly comparable.</p>}
  </>
}

// Expandable "About this metric" for figure cards.
export function AboutMetric({metric, provenance}: {metric: string; provenance: ResolvedProvenance}) {
  return <details className="about-metric"><summary>About this metric</summary><Details metric={metric} provenance={provenance} /></details>
}

// Table cell content: the badge is the disclosure summary, so details stay one keypress away.
// plain: small source text instead of the coloured badge (dense statistics tables).
export function ProvenanceDisclosure({metric, provenance, plain = false}: {metric: string; provenance: ResolvedProvenance; plain?: boolean}) {
  return <details className={`provenance-disclosure ${plain ? 'plain' : ''}`.trim()}><summary aria-label={`${provenanceLabel(provenance)}: about ${metricTitle(metric, provenance)}`}>{plain ? <span className="provenance-text" title={provenance.description || undefined}>{provenanceLabel(provenance)}{provenance.legacy && ' · catalog'}{provenance.mixed && ' · mixed'}</span> : <ProvenanceBadge provenance={provenance} />}</summary><Details metric={metric} provenance={provenance} /></details>
}
