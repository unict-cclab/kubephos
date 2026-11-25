import {formatDate} from '../lib'
import type {Experiment, ExperimentTrial} from '../types'

const terminal = ['succeeded', 'failed', 'canceled']

// Warm-up repetitions (pilot, position 0) never count as measured repetitions.
export function trialLabel(trial: Pick<ExperimentTrial, 'pilot' | 'position'>): string {
  return trial.pilot ? 'Warm-up (excluded)' : `Execution ${trial.position}`
}

export function measuredProgress(experiment: Pick<Experiment, 'variants'>): {completed: number; total: number; warmups: ExperimentTrial[]} {
  const trials = experiment.variants.flatMap(variant => variant.trials)
  const measured = trials.filter(trial => !trial.pilot)
  return {completed: measured.filter(trial => terminal.includes(trial.status)).length, total: measured.length, warmups: trials.filter(trial => trial.pilot)}
}

// The earliest queued repetition that waits for its rest period, once an earlier one has finished.
export function restingUntil(experiment: Pick<Experiment, 'variants'>, now = Date.now()): string | undefined {
  const trials = experiment.variants.flatMap(variant => variant.trials)
  if (!trials.some(trial => terminal.includes(trial.status))) return undefined
  return trials.filter(trial => trial.status === 'queued' && trial.scheduledFor && new Date(trial.scheduledFor).getTime() > now).map(trial => trial.scheduledFor!).sort()[0]
}

function WarningIcon() {
  return <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M10.3 3.9 2.4 17.6A2 2 0 0 0 4.1 20.6h15.8a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0Z" /><path d="M12 9v4m0 3.5h.01" /></svg>
}

// Execution counter of a run: measured repetitions only, warm-ups listed apart.
export function ExperimentExecutionCount({experiment}: {experiment: Experiment}) {
  const progress = measuredProgress(experiment)
  const warmups = progress.warmups
  return <span className="trial-count"><span>{progress.completed}/{progress.total}</span>{warmups.length > 0 && <small className="trial-note" title={warmups.map(trial => `${experiment.variants.find(variant => variant.id === trial.variantId)?.name ?? 'Warm-up'}: ${trial.status}`).join('\n')}>+ {warmups.length > 1 ? `${warmups.length} × ` : ''}Warm-up (excluded)</small>}</span>
}

// Notes under a run's status: resting between repetitions and kept results with failed cleanup.
export function ExperimentTrialNotes({experiment, now}: {experiment: Experiment; now?: number}) {
  const resting = ['queued', 'running'].includes(experiment.status) ? restingUntil(experiment, now) : undefined
  const failedCleanups = experiment.variants.flatMap(variant => variant.trials.filter(trial => trial.cleanupError).map(trial => ({variant, trial})))
  if (!resting && !failedCleanups.length) return null
  const details = failedCleanups.map(({variant, trial}) => `${variant.alias || variant.name} · ${trialLabel(trial)}: ${trial.cleanupError}`)
  return <span className="trial-notes">
    {resting && <small className="trial-note">Resting until {formatDate(resting)}</small>}
    {failedCleanups.length > 0 && <details className="trial-cleanup-warning" onClick={event => event.stopPropagation()}>
      <summary title={details.join('\n')}><WarningIcon />Result kept — cleanup failed{failedCleanups.length > 1 ? ` (${failedCleanups.length})` : ''}</summary>
      <ul>{details.map(detail => <li key={detail}>{detail}</li>)}</ul>
    </details>}
  </span>
}
