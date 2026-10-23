import {ActionIcon} from './ActionIcon'
import {useState, type CSSProperties} from 'react'

export interface ProgressNode {
  id: string
  title: string
  status: string
  detail?: string
  dependencies?: string[]
  operationId?: string
  stageCount?: number
}

interface Props {
  nodes: ProgressNode[]
  label: string
  openOperation?: (id: string) => void
  showLogs?: () => void
}

function phase(status: string): 'complete' | 'active' | 'failed' | 'canceled' | 'pending' {
  if (status === 'succeeded') return 'complete'
  if (status === 'failed') return 'failed'
  if (status === 'canceled') return 'canceled'
  if (['running', 'prechecking', 'verifying', 'starting'].includes(status)) return 'active'
  return 'pending'
}

function statusLabel(status: string): string {
  return status.replace(/[_-]+/g, ' ').replace(/^./, letter => letter.toUpperCase())
}

export function highLevelExecutionNodes(nodes: ProgressNode[]): ProgressNode[] {
  if (nodes.some(node => node.id === 'external-destination')) return nodes.map(node => {
    const identity = `${node.id} ${node.title}`.toLowerCase()
    if (node.id === 'external-destination') return {...node, title: 'Prepare external workload', detail: node.detail || 'Prepare an isolated load driver and verify the external destination.'}
    if (identity.includes('metric')) return {...node, title: 'Collect results', detail: node.detail || 'Collect and verify the experiment metrics.'}
    if (identity.includes('load')) return {...node, title: 'Run workload', detail: node.detail || 'Generate the configured workload against the external destination.'}
    return node
  })
  const deployIndex = nodes.findIndex(node => node.id === 'deploy')
  if (deployIndex < 0 || !nodes.some(node => node.id === 'application') || !nodes.some(node => node.id === 'prepare')) return nodes
  const phases = [
    {id: 'application-preparation', title: 'Prepare application', detail: 'Resolve the application package and prepare an isolated environment.', members: [] as ProgressNode[]},
    {id: 'experiment-configuration', title: 'Configure experiment', detail: 'Apply network conditions and configure scheduling, descheduling and scaling.', members: [] as ProgressNode[]},
    {id: 'application-deployment', title: 'Deploy application', detail: 'Deploy the application and wait for its workloads to become ready.', members: [] as ProgressNode[]},
    {id: 'workload-execution', title: 'Run workload', detail: 'Generate the configured workload for the experiment duration.', members: [] as ProgressNode[]},
    {id: 'result-collection', title: 'Collect results', detail: 'Collect and verify the experiment metrics.', members: [] as ProgressNode[]}
  ]
  nodes.forEach((node, index) => {
    if (node.id === 'application' || node.id === 'prepare') phases[0].members.push(node)
    else if (node.id === 'deploy') phases[2].members.push(node)
    else if (index < deployIndex) phases[1].members.push(node)
    else if (`${node.id} ${node.title}`.toLowerCase().includes('metric')) phases[4].members.push(node)
    else phases[3].members.push(node)
  })
  return phases.filter(item => item.members.length).map(item => {
    const failed = item.members.find(node => ['failed', 'canceled'].includes(phase(node.status)))
    const active = item.members.find(node => phase(node.status) === 'active')
    const complete = item.members.every(node => phase(node.status) === 'complete')
    const representative = failed ?? active ?? [...item.members].reverse().find(node => node.operationId) ?? item.members[0]
    const error = item.members.find(node => node.detail && ['failed', 'canceled'].includes(phase(node.status)))?.detail
    return {id: item.id, title: item.title, status: failed?.status ?? active?.status ?? (complete ? 'succeeded' : 'pending'), detail: error ?? item.detail, operationId: representative.operationId, stageCount: item.members.length}
  })
}

export function ExecutionProgressGraph({nodes, label, openOperation, showLogs}: Props) {
  const [chosen, setChosen] = useState<string | null>(null)
  const visibleNodes = highLevelExecutionNodes(nodes)
  if (!visibleNodes.length) return <p className="execution-graph-empty">The execution plan is being prepared.</p>
  const selected = visibleNodes.find(node => node.id === chosen) ?? visibleNodes.find(node => ['active', 'failed', 'canceled'].includes(phase(node.status))) ?? [...visibleNodes].reverse().find(node => phase(node.status) === 'complete') ?? visibleNodes[0]
  const completed = visibleNodes.filter(node => phase(node.status) === 'complete').length
  const current = visibleNodes.findIndex(node => phase(node.status) === 'active')
  const heading = current >= 0 ? `Step ${current + 1} of ${visibleNodes.length}` : completed === visibleNodes.length ? 'All steps complete' : `${completed} of ${visibleNodes.length} complete`
  return <div className="execution-graph" aria-label={label}>
    <div className="execution-graph-heading"><strong>{heading}</strong>{current >= 0 && <span>{completed} / {visibleNodes.length} completed</span>}</div>
    <div className="execution-graph-meter" role="progressbar" aria-label={`${label} completed steps`} aria-valuemin={0} aria-valuemax={visibleNodes.length} aria-valuenow={completed}><span style={{width: `${completed / visibleNodes.length * 100}%`}} /></div>
    <div className={`execution-graph-track${visibleNodes.length > 6 ? ' dense' : ''}`} role="group" aria-label={`${label} steps`} style={{'--execution-step-count': visibleNodes.length} as CSSProperties}>{visibleNodes.map((node, index) => <button type="button" key={node.id} className={`execution-graph-node ${phase(node.status)} ${selected.id === node.id ? 'selected' : ''}`} aria-pressed={selected.id === node.id} aria-current={phase(node.status) === 'active' ? 'step' : undefined} aria-label={`${index + 1}. ${node.title}: ${statusLabel(node.status)}`} onClick={() => setChosen(node.id)}>
      <span className="execution-graph-marker" aria-hidden="true">{phase(node.status) === 'complete' ? <ActionIcon name="check" /> : phase(node.status) === 'failed' ? <ActionIcon name="close" /> : phase(node.status) === 'canceled' ? <svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><rect x="6" y="6" width="12" height="12" rx="2" /></svg> : index + 1}</span><span className="execution-graph-caption"><strong>{node.title}</strong><small>{statusLabel(node.status)}</small></span>
    </button>)}</div>
    <div className="execution-graph-inspector"><div><span className={`execution-graph-state ${phase(selected.status)}`}>{statusLabel(selected.status)}</span><strong>{selected.title}</strong>{selected.detail && <p>{selected.detail}</p>}</div>{selected.operationId && openOperation ? <button type="button" className="button secondary compact" onClick={() => openOperation(selected.operationId!)}>Open logs</button> : showLogs ? <button type="button" className="button secondary compact" onClick={showLogs}>Open logs</button> : null}</div>
  </div>
}
