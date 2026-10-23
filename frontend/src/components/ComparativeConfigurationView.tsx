import {ResourceAttribution} from './ResourceAttribution'
import type {Application, ExperimentConfiguration, ManagedResource} from '../types'
import {EditorPage} from './EditorPage'

export function ComparativeConfigurationView({item, configurations, applications, clusters, close}: {item: ExperimentConfiguration; configurations: ExperimentConfiguration[]; applications: Application[]; clusters: ManagedResource[]; close: () => void}) {
  const variants = item.definition.variants ?? []
  const runs = item.definition.runs ?? 3
  return <EditorPage title={item.name} onClose={close} className="comparative-view-page">
    <div className="comparative-view"><ResourceAttribution resource={item} editable />
      {item.description && <p className="comparative-description">{item.description}</p>}
      <dl className="comparative-summary">
        <div><dt>Runs per configuration</dt><dd>{runs}</dd></div>
        <div><dt>Total runs</dt><dd>{variants.length * runs}</dd></div>
      </dl>
      <div className="comparative-order-heading"><h3>Execution order</h3></div>
      <ol className="comparative-view-list" aria-label="Execution order">
        {variants.map((member, index) => {
          const source = configurations.find(configuration => configuration.id === member.configurationId)
          const cluster = clusters.find(cluster => cluster.id === source?.clusterResourceId)
          const application = applications.find(application => application.reference === source?.applicationRef)
          const external = source?.definition.destinationMode === 'external'
          const alias = member.alias || source?.name || 'Unavailable configuration'
          return <li key={`${member.configurationId}-${index}`}>
            <div className="comparative-view-member-heading">
              <span className="comparative-order-number" aria-label={`Position ${index + 1}`}>{String(index + 1).padStart(2, '0')}</span>
              <h4>{alias}</h4>
              <span className="configuration-color-swatch" role="img" aria-label={`Plot color for ${alias}: ${member.color}`} title={`Plot color: ${member.color}`} style={{backgroundColor: member.color}} />
            </div>
            <dl className="comparative-member-facts">
              {source?.name !== alias && <div><dt>Configuration</dt><dd>{source?.name ?? 'Unavailable configuration'}</dd></div>}
              <div><dt>Cluster</dt><dd>{cluster?.name ?? 'Unavailable cluster'}</dd></div>
              <div><dt>{external ? 'External destination' : 'Application'}</dt><dd>{external ? source?.definition.externalDestination?.targetUrl || 'Unavailable destination' : application?.name ?? 'Unavailable application'}</dd></div>
            </dl>
            {(!source || !source.validation.valid) && <p className="comparative-member-warning">{source ? 'This configuration needs validation.' : 'This configuration is no longer available.'}</p>}
          </li>
        })}
      </ol>
      {!variants.length && <div className="empty-state"><strong>No configurations selected</strong></div>}
      {!item.validation.valid && <p className="form-error">This comparison needs validation before it can run.</p>}
    </div>
  </EditorPage>
}
