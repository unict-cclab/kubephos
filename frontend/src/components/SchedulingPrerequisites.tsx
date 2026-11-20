import type {ReactNode} from 'react'
import {missingPluginComponents} from '../clusterComponents'
import type {ManagedResource, Plugin} from '../types'
import './SchedulingPrerequisites.css'

const kindTitles: Record<string, string> = {scheduler: 'scheduler', descheduler: 'descheduler', autoscaler: 'autoscaler'}

// Explains why a scheduling/scaling plugin cannot be added instead of hiding it:
// a missing cluster component, or no ready catalog image for an image-based plugin.
export function SchedulingPrerequisites({kind, plugins, cluster, componentTitle: title, hasImage, pluginTitle: name}: {kind: string; plugins: Plugin[]; cluster?: ManagedResource; componentTitle: (id: string) => string; hasImage: (plugin: Plugin) => boolean; pluginTitle: (plugin: Plugin) => string}) {
  if (kind === 'other' || !plugins.length) return null
  const notes: ReactNode[] = []
  if (!cluster) notes.push(<>Select a Kubernetes cluster to add a {kindTitles[kind] ?? kind}.</>)
  else for (const plugin of plugins) {
    const missing = missingPluginComponents(plugin, cluster)
    if (missing.length) notes.push(<><strong>{name(plugin)}</strong> needs the {missing.map(title).join(', ')} component on cluster <strong>{cluster.name}</strong>. Add it in <a href="#kubernetes">Kubernetes clusters</a> → {cluster.name} → Components.</>)
    else if (!hasImage(plugin)) notes.push(<><strong>{name(plugin)}</strong> runs a strategy image, and none is ready in this workspace. Add one in <a href="#catalog">Catalog</a> → Strategies.</>)
  }
  if (!notes.length) return null
  return <div className="scheduling-prerequisites" role="note">{notes.map((note, index) => <p key={index}>{note}</p>)}</div>
}
