import {ValidatedInput} from './ValidatedInput'
import {ErrorFeedback, useErrorState} from './ToastRegion'
import {useCallback, useEffect, useRef, useState, type FormEvent} from 'react'
import {request, requestResponse} from '../api'
import {formatDate} from '../lib'
import type {Session} from '../types'
import {ResourceListHeading} from './ResourceListHeading'
import {FormTabs} from './FormTabs'
import {useDeleteConfirmation} from './ConfirmationDialog'

interface Summary {version: string; createdAt: string; counts: Record<string, number>}
const countLabels: Record<string, string> = {provider_connections: 'Proxmox connections', credentials: 'Credentials', managed_resources: 'Infrastructure resources', experiment_configurations: 'Configurations', catalog_applications: 'Applications', catalog_models: 'AI models', strategy_catalog: 'Strategies'}

// What an import would replace on this instance, as reported by the server.
interface InstanceContents {connections: number; credentials: number; resources: number; configurations: number; experiments: number}
type Readiness = {state: 'ready'} | {state: 'busy'} | {state: 'not-empty'; contents: InstanceContents}
const contentLabels: [keyof InstanceContents, string, string][] = [
  ['connections', 'Proxmox connection', 'Proxmox connections'],
  ['credentials', 'credential', 'credentials'],
  ['resources', 'infrastructure resource', 'infrastructure resources'],
  ['configurations', 'configuration', 'configurations'],
  ['experiments', 'experiment', 'experiments'],
]

// "2 Proxmox connections, 3 credentials and 7 infrastructure resources"
export function describeContents(contents: InstanceContents): string {
  const parts = contentLabels.filter(([key]) => contents[key] > 0).map(([key, one, many]) => `${contents[key]} ${contents[key] === 1 ? one : many}`)
  if (!parts.length) return 'infrastructure history'
  return parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
}

async function transferRequest(path: string, body: BodyInit, csrfToken?: string) {
  return requestResponse(`/administration/state/${path}`, {method: 'POST', body}, csrfToken)
}

export function AdministrationView({session, changed}: {session: Session; changed: () => Promise<void>}) {
  const [exportPassword, setExportPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [importPassword, setImportPassword] = useState('')
  const [summary, setSummary] = useState<Summary | null>(null)
  const [pending, setPending] = useState<'export' | 'inspect' | 'import' | null>(null)
  const [error, setError] = useErrorState()
  const [success, setSuccess] = useState('')
  const [fileKey, setFileKey] = useState(0)
  const [feedbackTab, setFeedbackTab] = useState<'export' | 'import'>('export')
  const [readiness, setReadiness] = useState<Readiness | null>(null)
  const {confirmAction, confirmationDialog} = useDeleteConfirmation()
  const reviewHeading = useRef<HTMLHeadingElement>(null)
  const isAdmin = session.user?.role === 'admin'
  const replacing = readiness?.state === 'not-empty'
  useEffect(() => {if (summary) reviewHeading.current?.focus()}, [summary])
  // An unknown readiness leaves the form available: the server still refuses unsafe imports.
  const checkImportReadiness = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await request<{ready?: boolean; reason?: string; contents?: InstanceContents}>('/administration/state/readiness', {}, undefined, signal)
      setReadiness(result?.ready === true ? {state: 'ready'} : result?.reason === 'busy' ? {state: 'busy'} : result?.reason === 'not-empty' && result.contents ? {state: 'not-empty', contents: result.contents} : null)
    } catch {
      if (!signal?.aborted) setReadiness(null)
    }
  }, [])
  useEffect(() => {
    if (!isAdmin) return
    const controller = new AbortController()
    void checkImportReadiness(controller.signal)
    return () => controller.abort()
  }, [checkImportReadiness, isAdmin])
  const run = async (kind: 'export' | 'inspect' | 'import', action: () => Promise<void>) => {
    setError(''); setSuccess(''); setPending(kind); setFeedbackTab(kind === 'export' ? 'export' : 'import')
    try {await action()} catch (cause) {setError(cause instanceof Error ? cause.message : 'Could not transfer instance state.')} finally {setPending(null)}
  }
  const exportState = (event: FormEvent) => {
    event.preventDefault()
    if (exportPassword !== confirmation) {setFeedbackTab('export'); setSuccess(''); setError('Archive passwords do not match.'); return}
    void run('export', async () => {
      const response = await transferRequest('export', JSON.stringify({password: exportPassword}), session.csrfToken)
      const url = URL.createObjectURL(await response.blob())
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = response.headers.get('Content-Disposition')?.match(/filename="([A-Za-z0-9.-]+)"/)?.[1] ?? 'kubephos-state.kps'
      document.body.append(anchor); anchor.click(); anchor.remove()
      setTimeout(() => URL.revokeObjectURL(url), 1000)
      setExportPassword(''); setConfirmation(''); setSuccess('State archive exported.')
    })
  }
  // Replacing is only requested once the administrator has seen what it removes.
  const archiveBody = (replace: boolean) => {
    if (!file) throw new Error('Choose an archive.')
    if (file.size > 128 * 1024 * 1024) throw new Error('Maximum archive size: 128 MiB.')
    const body = new FormData(); body.set('file', file); body.set('password', importPassword)
    if (replace) body.set('replace', 'true')
    return body
  }
  const inspect = (event: FormEvent) => {
    event.preventDefault()
    void run('inspect', async () => {
      setSummary(null)
      const response = await transferRequest('inspect', archiveBody(replacing), session.csrfToken)
      setSummary(await response.json() as Summary)
    })
  }
  const importState = async () => {
    if (readiness?.state === 'not-empty' && !await confirmAction(
      `This instance already contains ${describeContents(readiness.contents)}. Importing deletes all of it, including experiments, their results and saved comparisons, and restores the archive in its place. Users and the audit log are kept; VMs already running on Proxmox are not shut down. This cannot be undone.`,
      {title: 'Overwrite everything on this instance?', label: 'Overwrite and import'},
    )) return
    await run('import', async () => {
      const response = await transferRequest('import', archiveBody(replacing), session.csrfToken)
      const result = await response.json() as {restartRequired?: boolean}
      setSummary(null); setFile(null); setImportPassword(''); setFileKey(value => value + 1)
      const imported = replacing ? 'Instance state replaced with the archive.' : 'State imported.'
      setSuccess(result.restartRequired ? `${imported} Restart KubePhos to finish.` : imported)
      await changed()
      await checkImportReadiness()
    })
  }
  if (!isAdmin) return null
  return <>
    {confirmationDialog}
    <ResourceListHeading title="Import and export" />
    <p className="state-transfer-scope">Move your infrastructure, catalog and configurations to another instance. Users and experiments are excluded.</p>
    <div className="state-transfer-tabs"><FormTabs label="State transfer" items={[
      {id: 'export', title: 'Export', content: <section className="state-transfer-panel" aria-label="Export state">
        {feedbackTab === 'export' && error && <ErrorFeedback message={error} />}
        {feedbackTab === 'export' && success && <p className="state-transfer-success" role="status">{success}</p>}
        <form onSubmit={exportState}>
          <fieldset disabled={pending !== null}>
            <label>Archive password<ValidatedInput aria-label="Archive password" type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={exportPassword} onChange={event => setExportPassword(event.target.value)} /></label>
            <label>Confirm password<ValidatedInput aria-label="Confirm password" error={confirmation&&confirmation!==exportPassword?'Passwords do not match.':''} type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label>
            <p className="state-transfer-hint">At least 12 characters. Keep this password for import.</p>
            <button className="button primary" type="submit">{pending === 'export' ? 'Exporting…' : 'Export state'}</button>
          </fieldset>
        </form>
      </section>},
      {id: 'import', title: 'Import', content: <section className="state-transfer-panel" aria-label="Import state">
        {feedbackTab === 'import' && error && <ErrorFeedback message={error} />}
        {feedbackTab === 'import' && success && <p className="state-transfer-success" role="status">{success}</p>}
        <p>Import restores the state saved in an archive. Configurations will be private and belong to your account.</p>
        {readiness?.state === 'busy' && <div className="state-transfer-notice" role="status"><strong>Work is still running</strong><p>Import becomes available when running operations, pipelines, experiments and plugin imports have finished.</p></div>}
        {readiness?.state === 'not-empty' && <div className="state-transfer-notice" role="status"><strong>This instance already contains data</strong><p>It holds {describeContents(readiness.contents)}. Importing an archive replaces all of it; you will be asked to confirm.</p></div>}
        <form onSubmit={inspect}>
          <fieldset disabled={pending !== null || readiness?.state === 'busy'}>
            <label>State archive<input key={fileKey} type="file" accept=".kps,application/octet-stream" required onChange={event => {setFile(event.target.files?.[0] ?? null); setSummary(null); setError('')}} /></label>
            <label>Archive password<ValidatedInput aria-label="Archive password" type="password" autoComplete="off" required minLength={12} maxLength={1024} value={importPassword} onChange={event => {setImportPassword(event.target.value); setSummary(null)}} /></label>
            <button className="button secondary" type="submit">{pending === 'inspect' ? 'Checking…' : 'Review archive'}</button>
          </fieldset>
        </form>
        {summary && <div className="state-transfer-review">
          <h4 ref={reviewHeading} tabIndex={-1}>Archive contents</h4>
          <table className="readout-table readout-field-value" aria-label="Archive contents"><tbody>
            <tr><th scope="row">Source version</th><td>{summary.version}</td></tr>
            <tr><th scope="row">Exported</th><td>{formatDate(summary.createdAt)}</td></tr>
            {Object.entries(countLabels).map(([key, label]) => <tr key={key}><th scope="row">{label}</th><td>{summary.counts[key] ?? 0}</td></tr>)}
          </tbody></table>
          <button className={`button ${replacing ? 'danger' : 'primary'}`} type="button" disabled={pending !== null} onClick={() => void importState()}>{pending === 'import' ? 'Importing…' : replacing ? 'Overwrite and import…' : 'Import state'}</button>
        </div>}
      </section>}
    ]} /></div>
  </>
}
