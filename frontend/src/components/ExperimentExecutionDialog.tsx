import {ValidatedInput} from './ValidatedInput'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {lazy, Suspense, useEffect, useRef, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {Experiment, ExperimentConfiguration, Session} from '../types'
import {Dialog} from './Dialog'
import {ReadoutTable} from './ReadoutTable'
import {canManageResource} from './ResourceSharing'
import {TrialScheduleFields, isTrialScheduleError, savedTrialSchedule, trialScheduleError, trialSchedulePayload, trialScheduleRows, type TrialSchedule} from './TrialScheduleFields'

// Loaded on demand: the estimate is not needed for the initial render.
const RunEstimate = lazy(() => import('./RunSummary').then(module => ({default: module.RunEstimate})))

export function suggestedRunName(configuration: string, date = new Date()): string {
  const pad = (value: number) => String(value).padStart(2, '0')
  const suffix = `-${pad(date.getDate())}/${pad(date.getMonth() + 1)}/${date.getFullYear()}-${pad(date.getHours())}:${pad(date.getMinutes())}`
  return configuration.slice(0, 120 - suffix.length) + suffix
}

// Configurations the current user may launch: valid and owned (or any, for administrators).
export function runnableConfigurations(session: Session, configurations: ExperimentConfiguration[]): ExperimentConfiguration[] {
  return configurations.filter(item => item.validation.valid && canManageResource(session, item.ownerUserId))
}

interface Props {
  open: boolean
  configurations: ExperimentConfiguration[]
  initialConfigurationId?: string
  lockConfiguration?: boolean
  close: () => void
  session: Session
  changed: () => Promise<void>
  onCreated: (id: string) => void
}

// Single configurations start from their saved choice, "first" when none was saved;
// comparative ones keep each member's choice unless the suite saved its own.
function initialSchedule(configuration?: ExperimentConfiguration): TrialSchedule {
  const schedule = savedTrialSchedule(configuration?.definition)
  return configuration && configuration.kind !== 'suite' && configuration.definition.twoPhaseStart ? {...schedule, manualStartScope: schedule.manualStartScope ?? 'first'} : schedule
}

export function ExperimentExecutionDialog({open, configurations, initialConfigurationId, lockConfiguration = false, close, session, changed, onCreated}: Props) {
  const available = configurations.filter(item => item.validation.valid)
  const availableKey = available.map(item => item.id).join('\n')
  const [configurationId, setConfigurationId] = useState('')
  const [name, setName] = useState('')
  const nameEdited = useRef(false)
  const [scheduledFor, setScheduledFor] = useState('')
  const [schedule, setSchedule] = useState<TrialSchedule>(savedTrialSchedule())
  const [scheduleError, setScheduleError] = useState('')
  const [runs, setRuns] = useState(3)
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const selected = available.find(item => item.id === configurationId)
  const suite = selected?.kind === 'suite'
  // Manual start: a single configuration's start mode, or any member of a comparative one.
  const manualStart = Boolean(suite ? selected?.definition.variants?.some(member => configurations.find(item => item.id === member.configurationId)?.definition.twoPhaseStart) : selected?.definition.twoPhaseStart)
  const repetitions = (suite ? selected?.definition.runs ?? 3 : runs) + (schedule.firstTrialPilot ? 1 : 0)
  const manualStartShown = manualStart && repetitions > 1

  useEffect(() => {
    if (!open) return
    const configuration = available.find(item => item.id === initialConfigurationId) ?? available[0]
    setConfigurationId(configuration?.id ?? '')
    setName(configuration ? suggestedRunName(configuration.name) : '')
    nameEdited.current = false
    setScheduledFor('')
    setRuns(3)
    setSchedule(initialSchedule(configuration))
    setScheduleError('')
    setPending(false)
    setError('')
  }, [open, initialConfigurationId, availableKey])

  const selectConfiguration = (id: string) => {
    setConfigurationId(id)
    const configuration = available.find(item => item.id === id)
    setSchedule(initialSchedule(configuration))
    setScheduleError('')
    if (!nameEdited.current) setName(configuration ? suggestedRunName(configuration.name, scheduledFor ? new Date(scheduledFor) : new Date()) : '')
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected || trialScheduleError(schedule)) return
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      const created = await request<Experiment>('/experiments/start', {method: 'POST', body: JSON.stringify({configurationId: selected.id, name, runs: selected.kind === 'suite' ? selected.definition.runs ?? 3 : Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined, ...trialSchedulePayload(schedule, manualStartShown)})}, session.csrfToken)
      close()
      await changed()
      onCreated(created.id)
    } catch (cause) {
      // Schedule rejections belong next to the field the user has to change.
      if (isTrialScheduleError(cause)) setScheduleError(cause.message || 'Rest between repetitions must be between 0 and 3600 seconds.')
      else setError(cause instanceof Error ? cause.message : 'Could not create the experiment.')
    } finally {
      setPending(false)
    }
  }

  return <Dialog open={open} onClose={close} title="New run" className="template-modal execution-modal">
    <form onSubmit={submit}>
      {lockConfiguration ? <div className="execution-summary"><span>Configuration</span><strong>{selected?.name}</strong></div> : <label>Configuration<select value={configurationId} onChange={event => selectConfiguration(event.target.value)} required autoFocus><option value="" disabled>Select a configuration</option>{available.map(item => <option key={item.id} value={item.id}>{item.name} · {item.kind === 'suite' ? 'Comparative' : 'Single'}</option>)}</select></label>}
      <label>Name<ValidatedInput aria-label="Name" name="name" maxLength={120} value={name} onChange={event => {nameEdited.current = true; setName(event.target.value)}} required autoFocus={lockConfiguration} /></label>
      <div className={selected?.kind === 'suite' ? undefined : 'field-row'}>{selected?.kind !== 'suite' && <label>Measured repetitions<ValidatedInput aria-label="Measured repetitions" onChange={event => setRuns(Number(event.target.value))} name="runs" type="number" min={1} max={20} defaultValue={3} required /></label>}<label>Start later (optional)<ValidatedInput aria-label="Start later (optional)" name="scheduledFor" type="datetime-local" value={scheduledFor} onChange={event => {const value = event.target.value; setScheduledFor(value); if (!nameEdited.current && selected) setName(suggestedRunName(selected.name, value ? new Date(value) : new Date()))}} /></label></div>
      <TrialScheduleFields value={schedule} error={scheduleError} manualStart={manualStartShown} perConfiguration={suite} onChange={value => {setSchedule(value); setScheduleError('')}} />
      {selected?.kind !== 'suite' && <Suspense fallback={null}><RunEstimate configuration={selected} runs={runs} restSeconds={schedule.restSecondsBetweenTrials} pilot={schedule.firstTrialPilot} manualStart={manualStart ? schedule.manualStartScope ?? 'first' : undefined} /></Suspense>}
      {selected&&<ReadoutTable label="Execution settings" data={{headers:['Field','Value'],rows:[['Start mode',suite?'Per configuration':selected.definition.twoPhaseStart?'Manual':'Automatic'],...trialScheduleRows(trialSchedulePayload(schedule, manualStartShown), manualStartShown)]}}/>}
      <ErrorFeedback message={error} />
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !selected}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
    </form>
  </Dialog>
}
