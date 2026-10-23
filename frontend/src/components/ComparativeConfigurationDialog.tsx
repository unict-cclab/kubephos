import {WizardForm, WizardStep, WizardReview} from './WizardForm'
import {useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {Application, ExperimentConfiguration, ManagedResource, Session} from '../types'
import {Dialog} from './Dialog'
import {NumericInput} from './NumericInput'
import {ComparativeConfigurationView} from './ComparativeConfigurationView'
import {ActionIcon} from './ActionIcon'
import type {ReadoutTableData} from './ReadoutTable'
const suiteColors = ['#1f77b4', '#d55e00', '#009e73', '#cc79a7', '#e69f00', '#56b4e9', '#000000', '#7f7f7f']

export function ComparativeConfigurationDialog({open, close, configurations, applications, clusters, session, changed, item, readOnly = false}: {open: boolean; close: () => void; configurations: ExperimentConfiguration[]; applications: Application[]; clusters: ManagedResource[]; session: Session; changed: () => Promise<void>; item?: ExperimentConfiguration; readOnly?: boolean}) {
  const available = configurations.filter(configuration => configuration.kind !== 'suite' && configuration.definition.kind !== 'suite' && configuration.validation.valid)
  useEffect(() => {
    if (!open) return
    setSelected(item?.definition.variants?.map(member => member.configurationId) ?? [])
    setPresentation(Object.fromEntries((item?.definition.variants ?? []).map(member => [member.configurationId, {alias: member.alias, color: member.color}])))
    setRuns(item?.definition.runs ?? 3)
    setError('')
    setRevision(item?.updatedAt)
  }, [open, item?.id])
  const [selected, setSelected] = useState<string[]>([])
  const [presentation, setPresentation] = useState<Record<string, {alias: string; color: string}>>({})
  const [runs, setRuns] = useState(3)
  const [revision, setRevision] = useState(item?.updatedAt)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const reviewMembers: ReadoutTableData = {headers: ['Order', 'Configuration', 'Plot alias', 'Color'], rows: selected.map((id, index) => {
    const source = available.find(configuration => configuration.id === id)
    const alias = presentation[id]?.alias ?? source?.name ?? 'Unavailable configuration'
    return [String(index + 1), source?.name ?? 'Unavailable configuration', alias, {type: 'color', value: presentation[id]?.color ?? suiteColors[0], label: `Plot color for ${alias}`}]
  })}
  const toggle = (configuration: ExperimentConfiguration) => setSelected(current => {
    if (current.includes(configuration.id)) {
      setPresentation(values => { const next = {...values}; delete next[configuration.id]; return next })
      return current.filter(item => item !== configuration.id)
    }
    if (current.length >= 8) return current
    const used = new Set(Object.values(presentation).map(item => item.color))
    const color = suiteColors.find(item => !used.has(item)) ?? suiteColors[current.length % suiteColors.length]
    setPresentation(values => ({...values, [configuration.id]: {alias: configuration.name, color}}))
    return [...current, configuration.id]
  })
  const updatePresentation = (id: string, field: 'alias' | 'color', value: string) => setPresentation(current => ({...current, [id]: {...current[id], [field]: value}}))
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const values = new FormData(event.currentTarget)
    setPending(true)
    setError('')
    try {
      await request<ExperimentConfiguration>(item ? `/experiment-configurations/${item.id}` : '/experiment-configurations', {method: item ? 'PUT' : 'POST', body: JSON.stringify({name: values.get('name'), description: item?.description ?? '', kind: 'suite', updatedAt: revision, definition: {kind: 'suite', variants: selected.map(configurationId => ({configurationId, alias: presentation[configurationId]?.alias, color: presentation[configurationId]?.color})), runs}})}, session.csrfToken)
      setSelected([])
      setPresentation({})
      close()
      await changed()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not save the comparison.')
    } finally {
      setPending(false)
    }
  }
  if (readOnly && item) return open ? <ComparativeConfigurationView item={item} configurations={configurations} applications={applications} clusters={clusters} close={close} /> : null
  return <Dialog open={open} onClose={close} title={item ? 'Edit comparative configuration' : 'Create comparative configuration'} className="template-modal experiment-modal" presentation="page"><WizardForm onSubmit={submit} onCancel={close} pending={pending} error={error} submitDisabled={selected.length < 2 || selected.some(id => !available.some(configuration => configuration.id === id))} submitLabel={item ? 'Save comparison' : 'Create comparison'}><WizardStep title="Configurations" blocked={selected.length < 2}>
    <label>Name<input name="name" maxLength={120} defaultValue={item?.name} placeholder="Default vs custom scheduler" required autoFocus /></label>
    <input type="hidden" data-review-title="Configurations" data-review-table={JSON.stringify(reviewMembers)} />
    <div className="suite-configuration-list" data-review-skip="true">{available.map(item => {
      const cluster = clusters.find(clusterItem => clusterItem.id === item.clusterResourceId)
      const isSelected = selected.includes(item.id)
      return <div key={item.id} className={`suite-configuration-option${isSelected ? ' selected' : ''}`}><label><input type="checkbox" checked={isSelected} onChange={() => toggle(item)} /><span><strong>{item.name}</strong><small>{cluster?.name ?? 'Unavailable cluster'} · {applications.find(application => application.reference === item.applicationRef)?.name ?? 'Unavailable application'}</small></span></label>{isSelected && <div className="suite-presentation"><label>Plot alias<input value={presentation[item.id]?.alias ?? item.name} maxLength={80} required onChange={event => updatePresentation(item.id, 'alias', event.target.value)} /></label><label>Line color<input type="color" value={presentation[item.id]?.color ?? suiteColors[0]} aria-label={`${item.name} plot color`} onChange={event => updatePresentation(item.id, 'color', event.target.value)} /></label></div>}</div>
    })}</div>
    {selected.filter(id => !available.some(configuration => configuration.id === id)).map((id, index) => <p key={`${id}-${index}`} className="form-error">Unavailable single configuration {id || '(deleted)'} <button type="button" className="button secondary compact" onClick={() => setSelected(current => current.filter(value => value !== id))}>Remove unavailable configuration</button></p>)}
    </WizardStep><WizardStep title="Execution">{selected.length > 0 && <>{selected.map((id, index) => <div className="suite-order-row" key={`${id}-${index}`}><span>{index + 1}. {presentation[id]?.alias ?? available.find(configuration => configuration.id === id)?.name ?? 'Unavailable configuration'}</span><button type="button" className="button secondary icon-button" aria-label={`Move configuration ${index + 1} up`} disabled={index === 0} onClick={() => setSelected(current => {const next = [...current]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; return next})}><ActionIcon name="up" /></button><button type="button" className="button secondary icon-button" aria-label={`Move configuration ${index + 1} down`} disabled={index === selected.length - 1} onClick={() => setSelected(current => {const next = [...current]; [next[index], next[index + 1]] = [next[index + 1], next[index]]; return next})}><ActionIcon name="down" /></button></div>)}</>}
    <label>Runs per configuration<NumericInput name="runs" min={1} max={20} value={runs} onValueChange={setRuns} required /></label>
    </WizardStep><WizardStep title="Review"><WizardReview /></WizardStep>
  </WizardForm></Dialog>
}
