import type {View} from '../types'
import {ActionIcon, type ActionIconName} from './ActionIcon'

export interface WorkflowStep {
  view: View
  verb: string
  label: string
  count?: number
}

// Pages used alongside the sequence, whenever they are needed.
export interface WorkflowTool {
  view: View
  label: string
  caption: string
  icon: ActionIconName
  count?: number
}

export interface Workflow {
  label: string
  steps: WorkflowStep[]
  tools: WorkflowTool[]
}

export interface WorkflowCounts {
  isAdmin: boolean
  infrastructure: number
  clusters: number
  deployments: number
  configurations: number
  runs: number
}

// Each job has an ordered sequence plus tools that do not depend on its order.
export function workflows(counts: WorkflowCounts): Workflow[] {
  return [
    {
      label: 'Platform',
      steps: [
        ...(counts.isAdmin ? [{view: 'infrastructure', verb: 'Provision', label: 'Infrastructure', count: counts.infrastructure} as WorkflowStep] : []),
        {view: 'kubernetes', verb: 'Create', label: 'Clusters', count: counts.clusters}
      ],
      tools: [{view: 'deployments', label: 'Deployments', caption: 'Install applications on clusters', icon: 'stack', count: counts.deployments}]
    },
    {
      label: 'Experiments',
      steps: [
        {view: 'experiments', verb: 'Configure', label: 'Configurations', count: counts.configurations},
        {view: 'results', verb: 'Run', label: 'Runs', count: counts.runs}
      ],
      tools: [{view: 'comparisons', label: 'Comparisons', caption: 'Compare saved results', icon: 'chart'}]
    }
  ]
}

export function workflowFor(view: View, counts: WorkflowCounts): Workflow | undefined {
  return workflows(counts).find(workflow => [...workflow.steps, ...workflow.tools].some(entry => entry.view === view))
}

function Count({value}: {value?: number}) {
  return value === undefined ? null : <> · <span className="workflow-step-count">{value}</span></>
}

export function WorkflowSteps({workflow, view, navigate}: {workflow: Workflow; view: View; navigate: (view: View) => void}) {
  const numbered = workflow.steps.length > 1
  return <nav className="workflow-steps" aria-label={`${workflow.label} sections`}>
    <ol aria-label={`${workflow.label} steps`}>
      {workflow.steps.map((step, index) => <li key={step.view}>
        <button type="button" className={`workflow-step${view === step.view ? ' active' : ''}`} aria-current={view === step.view ? 'step' : undefined} onClick={() => navigate(step.view)}>
          {numbered && <span className="workflow-step-number" aria-hidden="true">{index + 1}</span>}
          <span className="workflow-step-copy"><strong>{step.verb}</strong><small>{step.label}<Count value={step.count} /></small></span>
        </button>
      </li>)}
    </ol>
    <ul className="workflow-tools" aria-label={`${workflow.label} tools`}>
      {workflow.tools.map(tool => <li key={tool.view}>
        <button type="button" className={`workflow-step workflow-tool${view === tool.view ? ' active' : ''}`} aria-current={view === tool.view ? 'page' : undefined} onClick={() => navigate(tool.view)}>
          <span className="workflow-tool-icon" aria-hidden="true"><ActionIcon name={tool.icon} /></span>
          <span className="workflow-step-copy"><strong>{tool.label}<Count value={tool.count} /></strong><small>{tool.caption}</small></span>
        </button>
      </li>)}
    </ul>
  </nav>
}
