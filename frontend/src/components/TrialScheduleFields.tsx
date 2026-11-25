import {useEffect, useId} from 'react'
import {NumericInput} from './NumericInput'

export const maxRestSeconds = 3600

/** Which repetitions of a manual-start configuration wait for the user's Start. */
export type ManualStartScope = 'first' | 'every'

export interface TrialSchedule {
  restSecondsBetweenTrials: number
  firstTrialPilot: boolean
  /** Absent: the saved choice of each configuration applies (default "first"). */
  manualStartScope?: ManualStartScope
}

export const manualStartScopeOf = (value: unknown): ManualStartScope | undefined => value === 'first' || value === 'every' ? value : undefined

export function savedTrialSchedule(definition?: {restSecondsBetweenTrials?: number; firstTrialPilot?: boolean; manualStartScope?: string}): TrialSchedule {
  const schedule: TrialSchedule = {restSecondsBetweenTrials: definition?.restSecondsBetweenTrials ?? 0, firstTrialPilot: Boolean(definition?.firstTrialPilot)}
  const scope = manualStartScopeOf(definition?.manualStartScope)
  return scope ? {...schedule, manualStartScope: scope} : schedule
}

// The request fields: the manual start scope travels only while its control is shown.
export function trialSchedulePayload(schedule: TrialSchedule, manualStartShown: boolean): TrialSchedule {
  const {manualStartScope, ...rest} = schedule
  return manualStartShown && manualStartScope ? {...rest, manualStartScope} : rest
}

export const manualStartScopeLabel = (scope?: ManualStartScope) => scope === 'every' ? 'Every repetition' : 'First repetition only'

export const manualStartScopeHint = (scope?: ManualStartScope) => scope === 'every'
  ? 'Every repetition waits for your Start after its setup.'
  : 'Later repetitions start by themselves after the rest interval.'

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

export function trialScheduleRows(schedule: TrialSchedule, manualStart = false): [string, string][] {
  const rows: [string, string][] = [
    ['Rest between repetitions', schedule.restSecondsBetweenTrials ? `${schedule.restSecondsBetweenTrials} s` : 'None'],
    ['Warm-up repetition', schedule.firstTrialPilot ? 'Yes · excluded from statistics' : 'No']
  ]
  if (manualStart || schedule.manualStartScope) rows.push(['Wait for my Start', schedule.manualStartScope || !manualStart ? manualStartScopeLabel(schedule.manualStartScope) : 'As saved in each configuration'])
  return rows
}

// Shown only with manual start and more than one repetition. perConfiguration (suites)
// adds an option that keeps each configuration's own saved choice.
export function ManualStartScopeField({value, onChange, perConfiguration = false}: {value?: ManualStartScope; onChange: (value: ManualStartScope | undefined) => void; perConfiguration?: boolean}) {
  const id = useId()
  return <div className="trial-schedule-field">
    <label>Wait for my Start<select name="manualStartScope" data-review-label="Wait for my Start" aria-describedby={`${id}-hint`} value={value ?? (perConfiguration ? '' : 'first')} onChange={event => onChange(manualStartScopeOf(event.target.value))}>
      {perConfiguration && <option value="">As saved in each configuration</option>}
      <option value="first">First repetition only</option>
      <option value="every">Every repetition</option>
    </select></label>
    <small id={`${id}-hint`} className="field-hint">{perConfiguration && !value ? 'Each manual-start configuration keeps its own choice.' : manualStartScopeHint(value)}</small>
  </div>
}

export function TrialScheduleFields({value, onChange, error = '', manualStart = false, perConfiguration = false}: {value: TrialSchedule; onChange: (value: TrialSchedule) => void; error?: string; manualStart?: boolean; perConfiguration?: boolean}) {
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
    {manualStart && <ManualStartScopeField value={value.manualStartScope} perConfiguration={perConfiguration} onChange={manualStartScope => onChange({...trialSchedulePayload(value, false), ...(manualStartScope ? {manualStartScope} : {})})} />}
  </div>
}
