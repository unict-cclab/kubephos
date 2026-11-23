import type {ReactNode} from 'react'
import {missingPluginComponents} from '../clusterComponents'
import type {ManagedResource, Plugin} from '../types'
import './SchedulingPrerequisites.css'

const kindTitles: Record<string, string> = {scheduler: 'scheduler', descheduler: 'descheduler', autoscaler: 'autoscaler'}
// What the experiment does when no plugin of the kind is added.
const kindDefaults: Record<string, string> = {
  scheduler: 'Without a scheduler, pods are placed by the default Kubernetes scheduler.',
  descheduler: 'Without a descheduler, pods stay where they were first placed.',
  autoscaler: 'Without an autoscaler, every service keeps its initial replica count for the whole run.'
}

const article = (word: string) => /^[aeiou]/i.test(word) ? 'an' : 'a'

// Lists the reasons a feature cannot be used, each with the place where it is fixed.
// Features are never hidden or disabled without one of these notes.
export function Prerequisites({notes, label}: {notes: ReactNode[]; label?: string}) {
  if (!notes.length) return null
  return <div className="scheduling-prerequisites" role="note" aria-label={label}>{notes.map((note, index) => <p key={index}>{note}</p>)}</div>
}

// Names the ready clusters that already run every missing component.
function alternativesNote(missing: string[], cluster: ManagedResource, clusters: ManagedResource[]): ReactNode {
  const names = clusters.filter(item => item.id !== cluster.id && missingPluginComponents({requiredComponents: missing} as Plugin, item).length === 0).map(item => item.name)
  return names.length ? <> Or select cluster {names.map((name, index) => <span key={name}>{index ? ', ' : ''}<strong>{name}</strong></span>)} in General, which already has it.</> : null
}

function componentNote(subject: ReactNode, missing: string[], cluster: ManagedResource, title: (id: string) => string, clusters: ManagedResource[]): ReactNode {
  return <>{subject} needs the {missing.map(title).join(', ')} component on cluster <strong>{cluster.name}</strong>. Add it in <a href="#kubernetes">Kubernetes clusters</a> → {cluster.name} → Components.{alternativesNote(missing, cluster, clusters)}</>
}

// Explains why a scheduling/scaling plugin cannot be added instead of hiding it:
// a missing cluster component, or no ready catalog image for an image-based plugin.
export function SchedulingPrerequisites({kind, plugins, cluster, clusters = [], configured = 0, componentTitle: title, hasImage, pluginTitle: name}: {kind: string; plugins: Plugin[]; cluster?: ManagedResource; clusters?: ManagedResource[]; configured?: number; componentTitle: (id: string) => string; hasImage: (plugin: Plugin) => boolean; pluginTitle: (plugin: Plugin) => string}) {
  if (kind === 'other') return null
  const label = kindTitles[kind] ?? kind
  const notes: ReactNode[] = []
  if (!configured && kindDefaults[kind]) notes.push(kindDefaults[kind])
  if (!plugins.length && !configured) notes.push(<>No {label} plugin is installed, so {article(label)} {label} cannot be added. Ask an administrator to install one (<a href="#requests">Requests</a>).</>)
  else if (!cluster && plugins.length) notes.push(<>Select a Kubernetes cluster in General to add {article(label)} {label}.</>)
  else if (cluster) for (const plugin of plugins) {
    const missing = missingPluginComponents(plugin, cluster)
    if (missing.length) notes.push(componentNote(<strong>{name(plugin)}</strong>, missing, cluster, title, clusters))
    else if (!hasImage(plugin)) notes.push(<><strong>{name(plugin)}</strong> runs a strategy image, and none is ready in this workspace. Add one in <a href="#catalog">Catalog</a> → Strategies.</>)
  }
  return <Prerequisites notes={notes} />
}

// Network injection between zones: every condition the checkbox depends on.
export function NetworkPrerequisites({plugin, cluster, clusters = [], zones, managed, multi = false, componentTitle: title}: {plugin?: Plugin; cluster?: ManagedResource; clusters?: ManagedResource[]; zones: number; managed: boolean; multi?: boolean; componentTitle: (id: string) => string}) {
  const notes: ReactNode[] = []
  const unit = multi ? 'member clusters' : 'zones'
  if (!plugin) notes.push(<>No network injection plugin is installed. Ask an administrator to install one (<a href="#requests">Requests</a>).</>)
  else if (!cluster) notes.push('Select a Kubernetes cluster in General to inject network conditions.')
  else {
    if (!managed) notes.push(<>Network injection needs a cluster provisioned by KubePhos (managed profile); <strong>{cluster.name}</strong> has none.</>)
    if (zones < 2) notes.push(<>Network injection shapes traffic between {unit}, and cluster <strong>{cluster.name}</strong> has {zones === 1 ? 'only one' : 'none'}. Use a cluster whose application pools span at least two zones.</>)
    const missing = missingPluginComponents(plugin, cluster)
    if (missing.length) notes.push(componentNote('Network injection', missing, cluster, title, clusters))
  }
  return <Prerequisites notes={notes} label="Network injection requirements" />
}

// The monitoring agent profile only applies to clusters whose agent KubePhos manages.
export function AgentPrerequisites({plugin, cluster, managed}: {plugin?: Plugin; cluster?: ManagedResource; managed: boolean}) {
  const notes: ReactNode[] = []
  if (!plugin) notes.push(<>No mon-agent profile plugin is installed. Ask an administrator to install one (<a href="#requests">Requests</a>).</>)
  else if (cluster && !managed) notes.push(<>The mon-agent can be customized only on clusters provisioned by KubePhos (managed profile); <strong>{cluster.name}</strong> has none, so the experiment uses the agent as it runs there.</>)
  else notes.push('Off: the experiment uses the cluster’s default mon-agent settings.')
  return <Prerequisites notes={notes} label="Mon-agent customization" />
}
