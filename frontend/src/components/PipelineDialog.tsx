import {useEffect, useMemo, useState, type FormEvent} from 'react'
import {ApiError, request} from '../api'
import {readSchemaValues} from '../lib'
import type {Application, Artifact, Connection, Credential, PipelineBinding, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'

interface Props {
  open: boolean
  close: () => void
  session: Session
  workspaces: Workspace[]
  plugins: Plugin[]
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
  changed: () => Promise<void>
}

interface DraftStage {
  key: number
  pluginId: string
}

export function PipelineDialog({open, close, session, workspaces, plugins, applications, artifacts, connections, credentials, changed}: Props) {
  const [workspaceId, setWorkspaceId] = useState('')
  const [stages, setStages] = useState<DraftStage[]>(() => plugins.length ? [{key: 1, pluginId: plugins[0].id}] : [])
  const [nextKey, setNextKey] = useState(2)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState('')
  const selectedWorkspace = workspaceId || workspaces[0]?.id || ''
  const workspaceArtifacts = useMemo(() => artifacts.filter(item => item.workspaceId === selectedWorkspace), [artifacts, selectedWorkspace])
  const lastPlugin = plugins.find(item => item.id === stages.at(-1)?.pluginId)
  const resultContracts = uniqueContracts(lastPlugin?.artifactOutputs ?? [])

  useEffect(() => {
    if (!stages.length && plugins.length) {
      setStages([{key: nextKey, pluginId: plugins[0].id}])
      setNextKey(value => value + 1)
    }
  }, [nextKey, plugins, stages.length])

  const setPlugin = (key: number, pluginId: string) => setStages(current => current.map(stage => stage.key === key ? {...stage, pluginId} : stage))
  const addStage = () => {
    if (!plugins.length || stages.length >= 16) return
    setStages(current => [...current, {key: nextKey, pluginId: plugins[0].id}])
    setNextKey(value => value + 1)
  }
  const removeStage = (key: number) => setStages(current => current.filter(stage => stage.key !== key))

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selectedWorkspace || !stages.length) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    const definitionStages = stages.map((stage, index) => {
      const plugin = plugins.find(item => item.id === stage.pluginId)!
      const stageID = `stage-${index + 1}`
      const virtual = virtualArtifacts(stages.slice(0, index), plugins)
      const byID = new Map(virtual.map(item => [item.id, item]))
		const spec = readSchemaValues(form, plugin.schema, `pipeline-${stage.key}`, applications)
      const bindings: PipelineBinding[] = []
      for (const [name, property] of Object.entries(plugin.schema.properties ?? {})) {
        if (property.format !== 'kubephos-artifact-ref') continue
        const value = spec[name]
        if (typeof value !== 'string') continue
        const source = byID.get(value)
        if (!source) continue
        delete spec[name]
        bindings.push({path: `/${escapePointer(name)}`, fromStage: source.stepId!})
      }
      return {
        id: stageID,
        pluginId: plugin.id,
        title: String(new FormData(form).get(`pipeline-title-${stage.key}`) ?? plugin.name),
        spec,
        bindings
      }
    })
    const [resultType, resultVersion] = String(new FormData(form).get('resultContract') ?? '').split('|')
    try {
      await request('/pipelines', {method: 'POST', body: JSON.stringify({
        workspaceId: selectedWorkspace,
        name: new FormData(form).get('name'),
        description: new FormData(form).get('description'),
        definition: {stages: definitionStages, result: {stage: definitionStages.at(-1)?.id, type: resultType, version: resultVersion}}
      })}, session.csrfToken)
      close()
      await changed()
    } catch (cause) {
      setError(pipelineError(cause))
    } finally {
      setPending(false)
    }
  }

  return <Dialog open={open} onClose={close} title="Create reusable flow" eyebrow="GUIDED PIPELINE" className="pipeline-modal">
    <form onSubmit={submit}>
      <div className="pipeline-basics">
        <label>Workspace<select value={selectedWorkspace} onChange={event => setWorkspaceId(event.target.value)} required>{workspaces.map(workspace => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}</select></label>
        <label>Name<input name="name" maxLength={120} placeholder="Scheduler comparison flow" required autoFocus /></label>
        <label className="wide">Description<textarea name="description" maxLength={500} rows={2} placeholder="What should this flow prepare and measure?" /></label>
      </div>
      <div className="pipeline-guide"><span>1</span><p><strong>Add capabilities in execution order.</strong> Compatible outputs are connected automatically and every stage is validated before the flow is saved.</p></div>
      <div className="pipeline-stages">{stages.map((stage, index) => {
        const plugin = plugins.find(item => item.id === stage.pluginId) ?? plugins[0]
        const virtual = virtualArtifacts(stages.slice(0, index), plugins)
        const available = [...virtual, ...workspaceArtifacts]
        return <article className="pipeline-stage" key={stage.key}>
          <div className="pipeline-stage-head"><span>{index + 1}</span><div><strong>{plugin?.name ?? 'Capability'}</strong><small>{plugin?.description}</small></div>{stages.length > 1 && <button type="button" className="text-button danger" onClick={() => removeStage(stage.key)}>Remove</button>}</div>
          <div className="pipeline-stage-fields" key={plugin?.id}>
            <label>Capability<select value={plugin?.id ?? ''} onChange={event => setPlugin(stage.key, event.target.value)} required>{plugins.map(item => <option key={item.id} value={item.id}>{item.name} · {item.version}</option>)}</select></label>
            <label>Stage name<input name={`pipeline-title-${stage.key}`} maxLength={120} defaultValue={plugin?.name} required /></label>
            {plugin && <SchemaFields schema={plugin.schema} prefix={`pipeline-${stage.key}`} applications={applications} artifacts={available} connections={connections} credentials={credentials} />}
          </div>
          {!!virtual.length && <div className="pipeline-auto-link"><span>↳</span>{virtual.length} compatible output{virtual.length === 1 ? '' : 's'} available from earlier stages</div>}
        </article>
      })}</div>
      <button type="button" className="pipeline-add" onClick={addStage} disabled={!plugins.length || stages.length >= 16}><span>＋</span>Add another capability</button>
      <div className="pipeline-result"><div><strong>Result to keep</strong><small>The final verified output becomes the trial result.</small></div><select name="resultContract" required disabled={!resultContracts.length}>{resultContracts.length ? resultContracts.map(contract => <option key={`${contract.type}/${contract.version}`} value={`${contract.type}|${contract.version}`}>{contract.type} · {contract.version}</option>) : <option value="">The last stage has no output</option>}</select></div>
      <div className="validation-callout"><span>✓</span><p><strong>Nothing runs yet.</strong> KubePhos validates schemas, plug-in plans, artifact compatibility, existing inputs and reachable health checks before saving this flow.</p></div>
      <p className="form-error">{error}</p>
      <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending || !stages.length || !resultContracts.length}>{pending ? 'Validating every stage…' : 'Validate and save flow'}</button></div>
    </form>
  </Dialog>
}

function virtualArtifacts(stages: DraftStage[], plugins: Plugin[]): Artifact[] {
  return stages.flatMap((stage, stageIndex) => {
    const plugin = plugins.find(item => item.id === stage.pluginId)
    return uniqueContracts(plugin?.artifactOutputs ?? []).map((contract, outputIndex) => ({
      id: `art_pipeline_${stage.key}_${outputIndex + 1}`,
      operationId: `stage-${stageIndex + 1}`,
      stepId: `stage-${stageIndex + 1}`,
      name: `From ${plugin?.name ?? `stage ${stageIndex + 1}`}`,
      type: contract.type,
      version: contract.version,
      mediaType: 'application/json',
      digest: '',
      sizeBytes: 0,
      sensitive: false,
      verifiedAt: ''
    }))
  })
}

function uniqueContracts(contracts: Array<{type: string; version: string}>): Array<{type: string; version: string}> {
  const values = new Map<string, {type: string; version: string}>()
  for (const contract of contracts) values.set(`${contract.type}/${contract.version}`, contract)
  return [...values.values()]
}

function escapePointer(value: string): string {
  return value.replaceAll('~', '~0').replaceAll('/', '~1')
}

function pipelineError(cause: unknown): string {
  if (!(cause instanceof ApiError)) return cause instanceof Error ? cause.message : 'Could not save the flow.'
  const validation = cause.body.validation as {issues?: Array<{message?: string}>} | undefined
  return validation?.issues?.map(issue => issue.message).filter(Boolean).join(' ') || cause.message
}
