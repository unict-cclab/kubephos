import {ActionIcon} from './ActionIcon'
import type {View} from '../types'

export type SetupStepID = 'connection' | 'cluster' | 'configuration' | 'experiment'

export interface SetupStep {
  id: SetupStepID
  title: string
  description: string
  action: string
  view: View
  done: boolean
}

export interface SetupProgress {
  isAdmin: boolean
  connections: number
  readyClusters: number
  configurations: number
  completedExperiments: number
}

// Proxmox connections and clusters are managed by administrators, so other users
// start by asking an administrator to assign them a cluster.
export function setupSteps(progress: SetupProgress): SetupStep[] {
  const clusterStep: SetupStep = progress.isAdmin
    ? {id: 'cluster', title: 'Prepare a cluster', description: 'Create a Kubernetes cluster from a VM template.', action: 'Open clusters', view: 'kubernetes', done: progress.readyClusters > 0}
    : {id: 'cluster', title: 'Get a cluster', description: 'Ask an administrator to assign you a cluster.', action: 'Ask an admin', view: 'requests', done: progress.readyClusters > 0}
  const steps: SetupStep[] = [
    {id: 'connection', title: 'Connect Proxmox', description: 'Add the Proxmox endpoint that hosts your VMs.', action: 'Add connection', view: 'infrastructure', done: progress.connections > 0},
    clusterStep,
    {id: 'configuration', title: 'Configure an experiment', description: 'Choose an application, a load profile and strategies.', action: 'New configuration', view: 'experiments', done: progress.configurations > 0},
    {id: 'experiment', title: 'Run and compare', description: 'Run a configuration and inspect its results.', action: 'Open experiments', view: 'results', done: progress.completedExperiments > 0}
  ]
  return progress.isAdmin ? steps : steps.filter(step => step.id !== 'connection')
}

export function SetupJourney({steps, navigate}: {steps: SetupStep[]; navigate: (view: View) => void}) {
  const current = steps.find(step => !step.done)
  if (!current) return null
  const remaining = steps.filter(step => !step.done).length
  const completed = steps.length - remaining
  return <section className="setup-journey" aria-labelledby="setup-journey-title">
    <div className="setup-journey-head">
      <h3 id="setup-journey-title">{remaining === 1 ? 'One step to your first experiment' : `${remaining} steps to your first experiment`}</h3>
      <span>{completed} of {steps.length} complete</span>
    </div>
    <ol className="setup-journey-steps">
      {steps.map((step, index) => {
        const isCurrent = step === current
        return <li key={step.id} className={`setup-step ${step.done ? 'is-done' : isCurrent ? 'is-current' : 'is-upcoming'}`} aria-current={isCurrent ? 'step' : undefined}>
          <span className="setup-step-marker" aria-hidden="true">{step.done ? <ActionIcon name="check" /> : index + 1}</span>
          <div className="setup-step-copy"><strong>{step.title}</strong><span>{step.description}</span></div>
          {isCurrent
            ? <button type="button" className="button primary compact" onClick={() => navigate(step.view)}>{step.action}</button>
            : <span className="setup-step-state">{step.done ? 'Done' : 'Next'}</span>}
        </li>
      })}
    </ol>
  </section>
}
