import {useState} from 'react'

export interface ProgressNode {
  id: string
  title: string
  status: string
  detail?: string
  dependencies?: string[]
  operationId?: string
}

interface Props {
  nodes: ProgressNode[]
  label: string
  openOperation?: (id: string) => void
  showLogs?: () => void
}

function phase(status: string): 'complete' | 'active' | 'failed' | 'pending' {
  if (status === 'succeeded') return 'complete'
  if (status === 'failed' || status === 'canceled') return 'failed'
  if (['running', 'prechecking', 'verifying', 'starting'].includes(status)) return 'active'
  return 'pending'
}

function statusLabel(status: string): string {
  return status.replace(/[_-]+/g, ' ').replace(/^./, letter => letter.toUpperCase())
}

export function ExecutionProgressGraph({nodes, label, openOperation, showLogs}: Props) {
  const [chosen, setChosen] = useState<string | null>(null)
  if (!nodes.length) return <p className="execution-graph-empty">The execution plan is being prepared.</p>
  const selected = nodes.find(node => node.id === chosen) ?? nodes.find(node => phase(node.status) === 'active' || phase(node.status) === 'failed') ?? [...nodes].reverse().find(node => phase(node.status) === 'complete') ?? nodes[0]
  const completed = nodes.filter(node => phase(node.status) === 'complete').length
  const current = nodes.findIndex(node => phase(node.status) === 'active')
  const heading = current >= 0 ? `Step ${current + 1} of ${nodes.length}` : completed === nodes.length ? 'All steps complete' : `${completed} of ${nodes.length} complete`
  return <div className="execution-graph" aria-label={label}>
    <div className="execution-graph-heading"><strong>{heading}</strong><span>{Math.round(completed / nodes.length * 100)}% verified</span></div>
    <div className="execution-graph-track" role="group" aria-label={`${label} steps`}>{nodes.map((node, index) => <button type="button" key={node.id} className={`execution-graph-node ${phase(node.status)} ${selected.id === node.id ? 'selected' : ''}`} aria-pressed={selected.id === node.id} aria-label={`${index + 1}. ${node.title}: ${statusLabel(node.status)}`} onClick={() => setChosen(node.id)}>
      <span className="execution-graph-marker" aria-hidden="true">{phase(node.status) === 'complete' ? '✓' : index + 1}</span><strong>{node.title}</strong><small>{statusLabel(node.status)}</small>
    </button>)}</div>
    <div className="execution-graph-inspector"><div><span className={`execution-graph-state ${phase(selected.status)}`}>{statusLabel(selected.status)}</span><strong>{selected.title}</strong>{selected.detail && <p>{selected.detail}</p>}{selected.dependencies?.length ? <small>Depends on {selected.dependencies.join(', ')}</small> : null}</div>{selected.operationId && openOperation ? <button type="button" className="button secondary compact" onClick={() => openOperation(selected.operationId!)}>Open step logs</button> : showLogs ? <button type="button" className="button secondary compact" onClick={showLogs}>View live logs</button> : null}</div>
  </div>
}
