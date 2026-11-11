import {ValidatedInput} from './ValidatedInput'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {useEffect, useRef, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {Experiment, ExperimentConfiguration, Session} from '../types'
import {Dialog} from './Dialog'
import {ReadoutTable} from './ReadoutTable'
import {canManageResource} from './ResourceSharing'

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

export function ExperimentExecutionDialog({open, configurations, initialConfigurationId, lockConfiguration = false, close, session, changed, onCreated}: Props) {
  const available = configurations.filter(item => item.validation.valid)
  const availableKey = available.map(item => item.id).join('\n')
  const [configurationId, setConfigurationId] = useState('')
  const [name, setName] = useState('')
  const nameEdited = useRef(false)
  const [scheduledFor, setScheduledFor] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const selected = available.find(item => item.id === configurationId)

  useEffect(() => {
    if (!open) return
    const configuration = available.find(item => item.id === initialConfigurationId) ?? available[0]
    setConfigurationId(configuration?.id ?? '')
    setName(configuration ? suggestedRunName(configuration.name) : '')
    nameEdited.current = false
    setScheduledFor('')
    setPending(false)
    setError('')
  }, [open, initialConfigurationId, availableKey])

  const selectConfiguration = (id: string) => {
    setConfigurationId(id)
    const configuration = available.find(item => item.id === id)
    if (!nameEdited.current) setName(configuration ? suggestedRunName(configuration.name, scheduledFor ? new Date(scheduledFor) : new Date()) : '')
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      const created = await request<Experiment>('/experiments/start', {method: 'POST', body: JSON.stringify({configurationId: selected.id, name, runs: selected.kind === 'suite' ? selected.definition.runs ?? 3 : Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      close()
      await changed()
      onCreated(created.id)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create the experiment.')
    } finally {
      setPending(false)
    }
  }

  return <Dialog open={open} onClose={close} title="New run" className="template-modal execution-modal">
    <form onSubmit={submit}>
      {lockConfiguration ? <div className="execution-summary"><span>Configuration</span><strong>{selected?.name}</strong></div> : <label>Configuration<select value={configurationId} onChange={event => selectConfiguration(event.target.value)} required autoFocus><option value="" disabled>Select a configuration</option>{available.map(item => <option key={item.id} value={item.id}>{item.name} · {item.kind === 'suite' ? 'Comparative' : 'Single'}</option>)}</select></label>}
      <label>Name<ValidatedInput aria-label="Name" name="name" maxLength={120} value={name} onChange={event => {nameEdited.current = true; setName(event.target.value)}} required autoFocus={lockConfiguration} /></label>
      <div className={selected?.kind === 'suite' ? undefined : 'field-row'}>{selected?.kind !== 'suite' && <label>Number of executions<ValidatedInput aria-label="Number of executions" name="runs" type="number" min={1} max={20} defaultValue={3} required /></label>}<label>Start later (optional)<ValidatedInput aria-label="Start later (optional)" name="scheduledFor" type="datetime-local" value={scheduledFor} onChange={event => {const value = event.target.value; setScheduledFor(value); if (!nameEdited.current && selected) setName(suggestedRunName(selected.name, value ? new Date(value) : new Date()))}} /></label></div>
      {selected&&<ReadoutTable label="Execution settings" data={{headers:['Field','Value'],rows:[['Start mode',selected.kind==='suite'?'Per configuration':selected.definition.twoPhaseStart?'Manual':'Automatic']]}}/>}
      <ErrorFeedback message={error} />
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !selected}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
    </form>
  </Dialog>
}
