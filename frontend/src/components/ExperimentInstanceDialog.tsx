import {useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {Experiment, ExperimentConfiguration, Session} from '../types'
import {Dialog} from './Dialog'

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

export function ExperimentInstanceDialog({open, configurations, initialConfigurationId, lockConfiguration = false, close, session, changed, onCreated}: Props) {
  const available = configurations.filter(item => item.validation.valid)
  const availableKey = available.map(item => item.id).join('\n')
  const [configurationId, setConfigurationId] = useState('')
  const [name, setName] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const selected = available.find(item => item.id === configurationId)

  useEffect(() => {
    if (!open) return
    const configuration = available.find(item => item.id === initialConfigurationId) ?? available[0]
    setConfigurationId(configuration?.id ?? '')
    setName(configuration ? `${configuration.name} run` : '')
    setPending(false)
    setError('')
  }, [open, initialConfigurationId, availableKey])

  const selectConfiguration = (id: string) => {
    setConfigurationId(id)
    const configuration = available.find(item => item.id === id)
    setName(configuration ? `${configuration.name} run` : '')
  }

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    const values = new FormData(event.currentTarget)
    const scheduled = String(values.get('scheduledFor') ?? '')
    setPending(true)
    setError('')
    try {
      const created = await request<Experiment>('/experiment-instances', {method: 'POST', body: JSON.stringify({configurationId: selected.id, name, runs: Number(values.get('runs')), scheduledFor: scheduled ? new Date(scheduled).toISOString() : undefined})}, session.csrfToken)
      close()
      await changed()
      onCreated(created.id)
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not create the experiment.')
    } finally {
      setPending(false)
    }
  }

  return <Dialog open={open} onClose={close} title="Create experiment" className="template-modal">
    <form onSubmit={submit}>
      {lockConfiguration ? <div className="form-section"><span>Configuration</span><strong>{selected?.name}</strong></div> : <label>Configuration<select value={configurationId} onChange={event => selectConfiguration(event.target.value)} required autoFocus><option value="" disabled>Select a configuration</option>{available.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}</select></label>}
      <label>Experiment name<input name="name" maxLength={120} value={name} onChange={event => setName(event.target.value)} required autoFocus={lockConfiguration} /></label>
      <div className="field-row"><label>Number of runs<input name="runs" type="number" min={1} max={20} defaultValue={3} required /></label><label>Start later (optional)<input name="scheduledFor" type="datetime-local" /></label></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !selected}>{pending ? 'Validating…' : 'Validate and run'}</button></div>
    </form>
  </Dialog>
}
