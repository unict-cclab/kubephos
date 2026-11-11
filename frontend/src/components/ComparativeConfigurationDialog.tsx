import {ResourcePickerDialog, type PickerResource} from './ResourcePickerDialog'
import {ResponsiveTable} from './ResponsiveTable'
import {ResourceAction} from './ResourceAction'
import {ownerLabel} from './OwnerFilter'
import {ValidatedInput} from './ValidatedInput'
import {ErrorFeedback} from './ToastRegion'
import {comparativeSources} from './comparativeSources'
import {useErrorState} from './ToastRegion'
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
  const available = comparativeSources(configurations)
  useEffect(() => {
    if (!open) return
    setAdding(false)
    setSelected(item?.definition.variants?.map(member => member.configurationId) ?? [])
    setPresentation(Object.fromEntries((item?.definition.variants ?? []).map(member => [member.configurationId, {alias: member.alias, color: member.color}])))
    setRuns(item?.definition.runs ?? 3)
    setError('')
    setRevision(item?.updatedAt)
  }, [open, item?.id])
  const [selected, setSelected] = useState<string[]>([])
  const [adding, setAdding] = useState(false)
  const [presentation, setPresentation] = useState<Record<string, {alias: string; color: string}>>({})
  const [runs, setRuns] = useState(3)
  const [revision, setRevision] = useState(item?.updatedAt)
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const reviewMembers: ReadoutTableData = {headers: ['Order', 'Configuration', 'Plot label', 'Color'], rows: selected.map((id, index) => {
    const source = available.find(configuration => configuration.id === id)
    const alias = presentation[id]?.alias ?? source?.name ?? 'Unavailable configuration'
    return [String(index + 1), source?.name ?? 'Unavailable configuration', alias, {type: 'color', value: presentation[id]?.color ?? suiteColors[0], label: `Plot color for ${alias}`}]
  })}
  const add = (items: PickerResource[]) => {
    const additions = items.filter(item => available.some(source => source.id === item.id) && !selected.includes(item.id)).slice(0, Math.max(0, 8 - selected.length))
    setSelected(current => [...current, ...additions.filter(item => !current.includes(item.id)).map(item => item.id)])
    setPresentation(current => {
      const next = {...current}
      for (const item of additions) if (!next[item.id]) next[item.id] = {alias: item.name, color: suiteColors.find(color => !Object.values(next).some(item => item.color === color)) ?? suiteColors[Object.keys(next).length % suiteColors.length]}
      return next
    })
    setAdding(false)
  }
  const remove = (id: string) => {
    setSelected(current => current.filter(value => value !== id))
    setPresentation(current => {const next = {...current}; delete next[id]; return next})
  }
  const updatePresentation = (id: string, field: 'alias' | 'color', value: string) => setPresentation(current => ({...current, [id]: {...current[id], [field]: value}}))
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if(selected.length<2||selected.some(id=>!available.some(configuration=>configuration.id===id))){setError('Select at least two single configurations.');return}
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
  return <><Dialog open={open} onClose={close} title={item ? 'Edit comparative configuration' : 'Create comparative configuration'} className="template-modal experiment-modal" presentation="page"><WizardForm onSubmit={submit} onCancel={close} pending={pending} error={error} submitDisabled={selected.length < 2 || selected.some(id => !available.some(configuration => configuration.id === id))} submitLabel={item ? 'Save comparison' : 'Create comparison'}><WizardStep title="Configurations" blocked={selected.length < 2}>
    <label>Name<ValidatedInput aria-label="Name" name="name" maxLength={120} defaultValue={item?.name} placeholder="Default vs custom scheduler" required autoFocus /></label>
    <input type="hidden" data-review-title="Configurations" data-review-table={JSON.stringify(reviewMembers)} />
    <div className="form-section-actions"><ResourceAction action="add" label="Add configurations" disabled={selected.length >= 8} title={selected.length >= 8 ? 'Maximum: 8 configurations.' : undefined} onClick={() => setAdding(true)} /></div>
    {selected.some(id => available.some(item => item.id === id)) && <div className="resource-selection-scroll" data-review-skip="true"><ResponsiveTable aria-label="Selected configurations"><thead><tr><th>Configuration</th><th>Created by</th><th>Plot label</th><th>Color</th><th>Actions</th></tr></thead><tbody>{selected.filter(id => available.some(item => item.id === id)).map(id => {
      const item = available.find(item => item.id === id)!
      return <tr key={id}><td>{item.name}</td><td>{ownerLabel(item)}</td><td><ValidatedInput aria-label="Plot label" value={presentation[id]?.alias ?? item.name} maxLength={80} required onChange={event => updatePresentation(id, 'alias', event.target.value)} /></td><td><input type="color" value={presentation[id]?.color ?? suiteColors[0]} aria-label={`${item.name} plot color`} onChange={event => updatePresentation(id, 'color', event.target.value)} /></td><td><ResourceAction action="delete" label={`Remove ${item.name}`} onClick={() => remove(id)} /></td></tr>
    })}</tbody></ResponsiveTable></div>}
    {selected.filter(id => !available.some(configuration => configuration.id === id)).map((id, index) => <div key={`${id}-${index}`}><ErrorFeedback message={`Unavailable single configuration ${id || '(deleted)'}`}/><button type="button" className="button secondary compact" onClick={() => setSelected(current => current.filter(value => value !== id))}>Remove unavailable configuration</button></div>)}
    </WizardStep><WizardStep title="Execution">{selected.length > 0 && <>{selected.map((id, index) => <div className="suite-order-row" key={`${id}-${index}`}><span>{index + 1}. {presentation[id]?.alias ?? available.find(configuration => configuration.id === id)?.name ?? 'Unavailable configuration'}</span><button type="button" className="button secondary icon-button" aria-label={`Move configuration ${index + 1} up`} disabled={index === 0} onClick={() => setSelected(current => {const next = [...current]; [next[index - 1], next[index]] = [next[index], next[index - 1]]; return next})}><ActionIcon name="up" /></button><button type="button" className="button secondary icon-button" aria-label={`Move configuration ${index + 1} down`} disabled={index === selected.length - 1} onClick={() => setSelected(current => {const next = [...current]; [next[index], next[index + 1]] = [next[index + 1], next[index]]; return next})}><ActionIcon name="down" /></button></div>)}</>}
    <label>Executions per configuration<NumericInput name="runs" min={1} max={20} value={runs} onValueChange={setRuns} required /></label>
    </WizardStep><WizardStep title="Review"><WizardReview /></WizardStep>
  </WizardForm></Dialog>{adding && <ResourcePickerDialog resource="configurations" items={available} selectedIds={selected} session={session} close={() => setAdding(false)} add={add} />}</>
}
