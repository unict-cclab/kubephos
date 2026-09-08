import {useMemo, useState, type FormEvent} from 'react'
import {ApiError, request} from '../api'
import {readSchemaValues} from '../lib'
import type {Application, Artifact, Connection, Credential, CredentialDefinition, Plugin, Session, Workspace} from '../types'
import {Dialog} from './Dialog'
import {SchemaFields} from './SchemaFields'

interface CommonProps {
  session: Session
  applications: Application[]
  artifacts: Artifact[]
  connections: Connection[]
  credentials: Credential[]
  onDone: (message: string) => Promise<void>
}

export function AuthDialog({setupRequired, onAuthenticated}: {setupRequired: boolean; onAuthenticated: (session: Session) => void}) {
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      const result = await request<Session>(setupRequired ? '/auth/setup' : '/auth/login', {
        method: 'POST',
        body: JSON.stringify({username: form.get('username'), password: form.get('password')})
      })
      onAuthenticated(result)
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open title={setupRequired ? 'Create administrator' : 'Sign in'} eyebrow={setupRequired ? 'FIRST RUN' : 'WELCOME BACK'} className="auth-modal">
    <form onSubmit={submit}>
      <div className="auth-brand"><span className="brand-mark">K</span><div><strong>KubePhos</strong><small>Secure control plane</small></div></div>
      <p className="auth-copy">{setupRequired ? 'Create the only account needed to start. You can add roles later.' : 'Use your KubePhos account to continue.'}</p>
      <label>Username<input name="username" minLength={3} maxLength={40} autoComplete="username" required autoFocus /></label>
      <label>Password<input name="password" type="password" minLength={12} maxLength={128} autoComplete={setupRequired ? 'new-password' : 'current-password'} required /></label>
      <p className="form-error">{error}</p>
      <button className="button primary auth-submit" disabled={pending}>{pending ? 'Please wait…' : setupRequired ? 'Create and continue' : 'Sign in'}</button>
    </form>
  </Dialog>
}

export function WorkspaceDialog({open, close, ...common}: CommonProps & {open: boolean; close: () => void}) {
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setPending(true)
    setError('')
    const form = new FormData(event.currentTarget)
    try {
      await request('/workspaces', {method: 'POST', body: JSON.stringify({name: form.get('name'), description: form.get('description')})}, common.session.csrfToken)
      close()
      await common.onDone('Workspace created.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Create workspace" eyebrow="NEW ENVIRONMENT">
    <form onSubmit={submit}>
      <label>Name<input name="name" maxLength={80} placeholder="Scheduler development" required autoFocus /></label>
      <label>Description<textarea name="description" maxLength={280} rows={3} placeholder="What will this workspace be used for?" /></label>
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Create workspace" />
    </form>
  </Dialog>
}

export function OperationDialog({workspace, plugins, open, close, onCreated, ...common}: CommonProps & {workspace: Workspace | null; plugins: Plugin[]; open: boolean; close: () => void; onCreated: (id: string) => Promise<void>}) {
  const [pluginID, setPluginID] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const selected = plugins.find(item => item.id === pluginID) ?? plugins[0]
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!workspace || !selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    try {
      const result = await request<{id: string}>('/operations', {method: 'POST', body: JSON.stringify({
        workspaceId: workspace.id,
        pluginId: selected.id,
        title: new FormData(form).get('title'),
        spec: readSchemaValues(form, selected.schema)
      })}, common.session.csrfToken)
      close()
      await common.onDone('Validation passed. Review the plan before starting.')
      await onCreated(result.id)
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Configure operation" eyebrow="VALIDATE FIRST">
    <form onSubmit={submit} key={`${workspace?.id ?? ''}-${selected?.id ?? ''}`}>
      <label>Capability<select name="pluginId" value={selected?.id ?? ''} onChange={event => setPluginID(event.target.value)} required>
        {plugins.map(plugin => <option key={plugin.id} value={plugin.id}>{plugin.name} · {plugin.version}</option>)}
      </select></label>
      <label>Operation name<input name="title" maxLength={120} defaultValue={selected?.name ?? ''} required /></label>
      {selected && <SchemaFields schema={selected.schema} applications={common.applications} artifacts={common.artifacts.filter(item => item.workspaceId === workspace?.id)} connections={common.connections} credentials={common.credentials} />}
      <ValidationCallout text="KubePhos validates every input and shows the resolved plan before it can be queued." />
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate plan" />
    </form>
  </Dialog>
}

export function CredentialDialog({open, close, plugins, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]}) {
  const definitions = useMemo(() => credentialDefinitions(plugins), [plugins])
  const [kind, setKind] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const selected = definitions.find(item => item.kind === kind) ?? definitions[0]
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    try {
      await request('/credentials', {method: 'POST', body: JSON.stringify({name: new FormData(form).get('name'), kind: selected.kind, value: readSchemaValues(form, selected.schema, 'credential')})}, common.session.csrfToken)
      close()
      await common.onDone('Credential encrypted and stored.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Add credential" eyebrow="ENCRYPTED STORAGE">
    <form onSubmit={submit} key={selected?.kind ?? ''}>
      <label>Credential type<select value={selected?.kind ?? ''} onChange={event => setKind(event.target.value)} required>{definitions.map(item => <option key={item.kind} value={item.kind}>{item.name}</option>)}</select></label>
      <p className="field-description">{selected?.description}</p>
      <label>Name<input name="name" maxLength={80} placeholder="Development infrastructure" required /></label>
      {selected && <SchemaFields schema={selected.schema} prefix="credential" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />}
      <ValidationCallout text="The plaintext is encrypted before storage and is never returned by the API." />
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Encrypt and save" />
    </form>
  </Dialog>
}

export function ConnectionDialog({open, close, plugins, ...common}: CommonProps & {open: boolean; close: () => void; plugins: Plugin[]}) {
  const compatible = plugins.filter(item => item.provider && item.capabilities?.includes('infrastructure.discovery'))
  const [pluginID, setPluginID] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const selected = compatible.find(item => item.id === pluginID) ?? compatible[0]
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    setPending(true)
    setError('')
    const form = event.currentTarget
    try {
      await request('/connections', {method: 'POST', body: JSON.stringify({name: new FormData(form).get('name'), pluginId: selected.id, configuration: readSchemaValues(form, selected.schema, 'connection')})}, common.session.csrfToken)
      close()
      await common.onDone('Provider connection validated and saved.')
    } catch (cause) {
      setError(validationMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Add provider connection" eyebrow="VALIDATE ONCE">
    <form onSubmit={submit} key={selected?.id ?? ''}>
      <label>Provider capability<select value={selected?.id ?? ''} onChange={event => setPluginID(event.target.value)} required>{compatible.map(item => <option key={item.id} value={item.id}>{item.name} · {item.provider}</option>)}</select></label>
      <label>Name<input name="name" maxLength={80} placeholder="Development Proxmox" required /></label>
      {selected && <SchemaFields schema={selected.schema} prefix="connection" applications={common.applications} artifacts={common.artifacts} connections={common.connections} credentials={common.credentials} />}
      <ValidationCallout text="Credentials remain encrypted and operations receive only this connection reference." />
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate and save" />
    </form>
  </Dialog>
}

export function ApplicationDialog({open, close, ...common}: CommonProps & {open: boolean; close: () => void}) {
  const [file, setFile] = useState<File | null>(null)
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!file) return
    if (file.size > 512 * 1024) {
      setError('The descriptor cannot exceed 512 KB.')
      return
    }
    setPending(true)
    setError('')
    try {
      await request('/catalog/applications', {method: 'POST', body: JSON.stringify({descriptor: await file.text()})}, common.session.csrfToken)
      close()
      await common.onDone('Application validated and imported.')
    } catch (cause) {
      setError(errorMessage(cause))
    } finally {
      setPending(false)
    }
  }
  return <Dialog open={open} onClose={close} title="Import application" eyebrow="VALIDATED CATALOG">
    <form onSubmit={submit}>
      <label>Application descriptor<input type="file" accept=".yaml,.yml,.json,application/yaml,application/json" required onChange={event => setFile(event.target.files?.[0] ?? null)} /></label>
      <p className="field-description">{file ? `${file.name} · ${Math.ceil(file.size / 1024)} KB` : 'Select a descriptor that implements catalog.kubephos.dev/v1alpha1.'}</p>
      <ValidationCallout text="Versions are immutable and remote packages must use a Git commit or OCI digest." />
      <p className="form-error">{error}</p>
      <Actions close={close} pending={pending} label="Validate and import" />
    </form>
  </Dialog>
}

function Actions({close, pending, label}: {close: () => void; pending: boolean; label: string}) {
  return <div className="modal-actions"><button className="button secondary" type="button" onClick={close}>Cancel</button><button className="button primary" disabled={pending}>{pending ? 'Please wait…' : label}</button></div>
}

function ValidationCallout({text}: {text: string}) {
  return <div className="validation-callout"><span>✓</span><p><strong>Validated before continuing.</strong> {text}</p></div>
}

function credentialDefinitions(plugins: Plugin[]): CredentialDefinition[] {
  const definitions = new Map<string, CredentialDefinition>()
  for (const plugin of plugins) for (const definition of plugin.credentialSchemas ?? []) definitions.set(definition.kind, definition)
  return [...definitions.values()].sort((left, right) => left.name.localeCompare(right.name))
}

function errorMessage(cause: unknown): string {
  return cause instanceof Error ? cause.message : 'The request could not be completed.'
}

function validationMessage(cause: unknown): string {
  if (!(cause instanceof ApiError)) return errorMessage(cause)
  const validation = cause.body.validation as {issues?: Array<{message?: string}>} | undefined
  const issues = validation?.issues ?? cause.body.issues as Array<{message?: string}> | undefined
  return issues?.map(issue => issue.message).filter(Boolean).join(' ') || cause.message
}
