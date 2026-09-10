import type {Artifact, Operation, Plugin, Workspace} from '../types'
import {Dialog} from './Dialog'
import {Status} from './Views'

interface Props {
  open: boolean
  workspace: Workspace | null
  plugins: Plugin[]
  artifacts: Artifact[]
  operations: Operation[]
  close: () => void
  configure: (pluginID: string) => void
}

interface FlowItem {
  plugin: Plugin
  state: 'active' | 'available' | 'executed' | 'waiting'
  interactive: boolean
  missing: Array<{type: string; version: string}>
  outputs: Artifact[]
}

const activeStates = new Set(['ready', 'queued', 'prechecking', 'running', 'verifying'])

export function WorkspaceFlowDialog({open, workspace, plugins, artifacts, operations, close, configure}: Props) {
  const items = workspace ? buildFlow(workspace.id, plugins, artifacts, operations) : []
  const available = items.filter(item => item.state === 'available').length
  const active = items.filter(item => item.state === 'active').length
  const produced = new Set(items.flatMap(item => item.outputs.map(output => `${output.type}/${output.version}`))).size
  return <Dialog open={open} onClose={close} title={workspace?.name ?? 'Workspace'} eyebrow="GUIDED WORKSPACE" className="flow-modal">
    <div className="flow-intro">
      <p>{workspace?.description || 'Compose the environment from compatible capabilities.'}</p>
      <div className="flow-stats"><FlowStat value={available} label="available now" /><FlowStat value={active} label="in progress" /><FlowStat value={produced} label="output types" /></div>
    </div>
    <div className="flow-explainer"><span>↳</span><p>The sequence is derived from versioned artifact contracts. A capability unlocks only when every required input is available in this workspace.</p></div>
    <div className="flow-list">{items.length ? items.map(item => <FlowRow key={item.plugin.id} item={item} configure={configure} />) : <div className="empty-state"><div><strong>No composable capabilities</strong>Install a plugin that declares typed inputs or outputs.</div></div>}</div>
  </Dialog>
}

function FlowStat({value, label}: {value: number; label: string}) {
  return <div><strong>{value}</strong><span>{label}</span></div>
}

function FlowRow({item, configure}: {item: FlowItem; configure: (pluginID: string) => void}) {
  const inputText = item.plugin.artifactInputs?.length ? item.plugin.artifactInputs.map(contractName).join(', ') : 'No typed input'
  const outputText = item.plugin.artifactOutputs?.length ? item.plugin.artifactOutputs.map(contractName).join(', ') : 'No typed output'
  const status = item.state === 'active' ? 'Running' : item.state === 'executed' ? 'Output available' : item.interactive && item.state === 'available' ? 'Interactive' : item.state === 'available' ? 'Available' : 'Waiting'
  return <article className={`flow-row ${item.state}`}>
    <div className="flow-node"><span /></div>
    <div className="flow-copy">
      <div className="flow-title"><h3>{item.plugin.name}</h3><Status value={status} /></div>
      <p>{item.plugin.description}</p>
      <div className="contract-line"><span>IN</span>{inputText}</div>
      <div className="contract-line"><span>OUT</span>{outputText}</div>
      {!!item.missing.length && <small>Waiting for {item.missing.map(contractName).join(', ')}</small>}
      {!!item.outputs.length && <small>{item.outputs.length} verified artifact{item.outputs.length === 1 ? '' : 's'} available</small>}
    </div>
    <button className="button secondary compact" disabled={item.state === 'waiting' || item.state === 'active'} onClick={() => configure(item.plugin.id)}>{item.interactive && item.state === 'available' ? 'Open control' : item.state === 'executed' ? 'Run again' : item.state === 'active' ? 'In progress' : item.state === 'waiting' ? 'Needs input' : 'Configure'}</button>
  </article>
}

function contractName(contract: {type: string; version: string}): string {
  return `${contract.type}/${contract.version}`
}

export function buildFlow(workspaceID: string, plugins: Plugin[], artifacts: Artifact[], operations: Operation[]): FlowItem[] {
  const workspaceArtifacts = artifacts.filter(item => item.workspaceId === workspaceID).sort((left, right) => right.verifiedAt.localeCompare(left.verifiedAt))
  const workspaceOperations = operations.filter(item => item.workspaceId === workspaceID)
  return plugins
    .filter(plugin => Boolean(plugin.artifactInputs?.length || plugin.artifactOutputs?.length))
    .map(plugin => {
      const missing = (plugin.artifactInputs ?? []).filter(contract => !workspaceArtifacts.some(artifact => artifact.type === contract.type && artifact.version === contract.version))
      const matchingOperations = workspaceOperations.filter(operation => operation.pluginId === plugin.id)
      const running = matchingOperations.some(operation => activeStates.has(operation.status))
      const executed = matchingOperations.some(operation => operation.status === 'succeeded')
      const interactive = (plugin.capabilities ?? []).some(capability => capability.endsWith('.control'))
      const operationIDs = new Set(matchingOperations.filter(operation => operation.status === 'succeeded').map(operation => operation.id))
      const outputs = workspaceArtifacts.filter(artifact => operationIDs.has(artifact.operationId) && (plugin.artifactOutputs ?? []).some(contract => artifact.type === contract.type && artifact.version === contract.version))
      const state = running ? 'active' : interactive && !missing.length ? 'available' : executed ? 'executed' : missing.length ? 'waiting' : 'available'
      return {plugin, state, interactive, missing, outputs} satisfies FlowItem
    })
    .sort((left, right) => flowRank(left.state) - flowRank(right.state) || left.plugin.name.localeCompare(right.plugin.name))
}

function flowRank(state: FlowItem['state']): number {
  return {active: 0, available: 1, waiting: 2, executed: 3}[state]
}
