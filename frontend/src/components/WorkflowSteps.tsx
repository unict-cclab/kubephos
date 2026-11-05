import type {View} from '../types'

export interface WorkflowStep {
  view: View
  verb: string
  label: string
  count?: number
}

export interface Workflow {
  label: string
  steps: WorkflowStep[]
}

export interface WorkflowCounts {
  isAdmin: boolean
  infrastructure: number
  clusters: number
  deployments: number
  configurations: number
  runs: number
}

// Pages that belong to one job read as its ordered steps.
export function workflows(counts: WorkflowCounts): Workflow[] {
  return [
    {label: 'Platform steps', steps: [
      ...(counts.isAdmin ? [{view: 'infrastructure', verb: 'Provision', label: 'Infrastructure', count: counts.infrastructure} as WorkflowStep] : []),
      {view: 'kubernetes', verb: 'Create', label: 'Clusters', count: counts.clusters},
      {view: 'deployments', verb: 'Deploy', label: 'Deployments', count: counts.deployments}
    ]},
    {label: 'Experiment steps', steps: [
      {view: 'experiments', verb: 'Configure', label: 'Configurations', count: counts.configurations},
      {view: 'results', verb: 'Run', label: 'Runs', count: counts.runs},
      {view: 'comparisons', verb: 'Compare', label: 'Comparisons'}
    ]}
  ]
}

export function workflowFor(view: View, counts: WorkflowCounts): Workflow | undefined {
  return workflows(counts).find(workflow => workflow.steps.some(step => step.view === view))
}

export function WorkflowSteps({workflow, view, navigate}: {workflow: Workflow; view: View; navigate: (view: View) => void}) {
  return <nav className="workflow-steps" aria-label={workflow.label}>
    <ol>
      {workflow.steps.map((step, index) => <li key={step.view}>
        <button type="button" className={`workflow-step${view === step.view ? ' active' : ''}`} aria-current={view === step.view ? 'step' : undefined} onClick={() => navigate(step.view)}>
          <span className="workflow-step-number" aria-hidden="true">{index + 1}</span>
          <span className="workflow-step-copy"><strong>{step.verb}</strong><small>{step.label}{step.count !== undefined && <> · <span className="workflow-step-count">{step.count}</span></>}</small></span>
        </button>
      </li>)}
    </ol>
  </nav>
}
