import {useEffect, useId} from 'react'
import {NumericInput} from './NumericInput'

export const maxRestSeconds = 3600

export interface TrialSchedule {restSecondsBetweenTrials: number; firstTrialPilot: boolean}

export function savedTrialSchedule(definition?: {restSecondsBetweenTrials?: number; firstTrialPilot?: boolean}): TrialSchedule {
  return {restSecondsBetweenTrials: definition?.restSecondsBetweenTrials ?? 0, firstTrialPilot: Boolean(definition?.firstTrialPilot)}
}

// Mirrors the API bounds, so invalid values never leave the browser.
export function trialScheduleError(schedule: TrialSchedule): string {
  const rest = schedule.restSecondsBetweenTrials
  return Number.isInteger(rest) && rest >= 0 && rest <= maxRestSeconds ? '' : `Rest between repetitions must be a whole number from 0 to ${maxRestSeconds} seconds.`
}

// True when the API rejected the rest or warm-up settings (422 invalid_trial_schedule).
export function isTrialScheduleError(cause: unknown): cause is Error & {status: number} {
  if (!cause || typeof cause !== 'object' || !('status' in cause) || cause.status !== 422 || !('body' in cause)) return false
  const body = cause.body as Record<string, unknown> | undefined
  return body?.code === 'invalid_trial_schedule'
}

export function trialScheduleRows(schedule: TrialSchedule): [string, string][] {
  return [
    ['Rest between repetitions', schedule.restSecondsBetweenTrials ? `${schedule.restSecondsBetweenTrials} s` : 'None'],
    ['Warm-up repetition', schedule.firstTrialPilot ? 'Yes · excluded from statistics' : 'No']
  ]
}

export function TrialScheduleFields({value, onChange, error = ''}: {value: TrialSchedule; onChange: (value: TrialSchedule) => void; error?: string}) {
  const id = useId()
  const message = error || trialScheduleError(value)
  // A rejection from the API moves focus to the field that has to change.
  useEffect(() => {if (error) document.getElementById(`${id}-input`)?.focus()}, [error, id])
  return <div className="trial-schedule-fields">
    <div className="trial-schedule-field">
      <label>Rest between repetitions (s)<NumericInput id={`${id}-input`} aria-label="Rest between repetitions (s)" data-review-label="Rest between repetitions (s)" aria-describedby={`${id}-rest`} name="restSecondsBetweenTrials" min={0} max={maxRestSeconds} step={1} required value={value.restSecondsBetweenTrials} validationMessage={message} onValueChange={restSecondsBetweenTrials => onChange({...value, restSecondsBetweenTrials})} /></label>
      <small id={`${id}-rest`} className="field-hint">Lets the cluster return to steady state before the next repetition. Recommended ≥ 60 s for publication runs.</small>
    </div>
    <div className="trial-schedule-field">
      <label className="checkbox-field"><input type="checkbox" name="firstTrialPilot" data-review-label="Warm-up repetition" aria-describedby={`${id}-pilot`} checked={value.firstTrialPilot} onChange={event => onChange({...value, firstTrialPilot: event.target.checked})} /><span>First repetition is a warm-up (excluded from statistics)</span></label>
      <small id={`${id}-pilot`} className="field-hint">Adds one extra repetition before the measured ones that absorbs image pulls and cold caches.</small>
    </div>
  </div>
}
