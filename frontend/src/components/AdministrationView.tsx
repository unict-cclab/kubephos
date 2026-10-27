import {useEffect, useRef, useState, type FormEvent} from 'react'
import {ApiError} from '../api'
import {formatDate} from '../lib'
import type {Session} from '../types'
import {ResourceListHeading} from './ResourceListHeading'
import {FormTabs} from './FormTabs'

interface Summary {version: string; createdAt: string; counts: Record<string, number>}
const countLabels: Record<string, string> = {provider_connections: 'Proxmox connections', credentials: 'Credentials', managed_resources: 'Infrastructure resources', experiment_configurations: 'Configurations', catalog_applications: 'Applications', catalog_models: 'AI models', strategy_catalog: 'Strategies'}

async function transferRequest(path: string, body: BodyInit, csrfToken?: string) {
  const headers = new Headers({'X-CSRF-Token': csrfToken ?? ''})
  if (typeof body === 'string') headers.set('Content-Type', 'application/json')
  const response = await fetch(`/api/v1/administration/state/${path}`, {method: 'POST', headers, body})
  if (!response.ok) throw new ApiError(response.status, await response.json().catch(() => ({})))
  return response
}

export function AdministrationView({session, changed}: {session: Session; changed: () => Promise<void>}) {
  const [exportPassword, setExportPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [importPassword, setImportPassword] = useState('')
  const [summary, setSummary] = useState<Summary | null>(null)
  const [pending, setPending] = useState<'export' | 'inspect' | 'import' | null>(null)
  const [error, setError] = useState('')
  const [success, setSuccess] = useState('')
  const [fileKey, setFileKey] = useState(0)
  const [feedbackTab, setFeedbackTab] = useState<'export' | 'import'>('export')
  const reviewHeading = useRef<HTMLHeadingElement>(null)
  useEffect(() => {if (summary) reviewHeading.current?.focus()}, [summary])
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
  const archiveBody = () => {
    if (!file) throw new Error('Choose a state archive.')
    if (file.size > 128 * 1024 * 1024) throw new Error('State archive exceeds 128 MiB.')
    const body = new FormData(); body.set('file', file); body.set('password', importPassword); return body
  }
  const inspect = (event: FormEvent) => {
    event.preventDefault()
    void run('inspect', async () => {
      setSummary(null)
      const response = await transferRequest('inspect', archiveBody(), session.csrfToken)
      setSummary(await response.json() as Summary)
    })
  }
  const importState = () => void run('import', async () => {
    const response = await transferRequest('import', archiveBody(), session.csrfToken)
    const result = await response.json() as {restartRequired?: boolean}
    setSummary(null); setFile(null); setImportPassword(''); setFileKey(value => value + 1)
    setSuccess(result.restartRequired ? 'State imported. Restart KubePhos to finish.' : 'State imported.')
    await changed()
  })
  if (session.user?.role !== 'admin') return null
  return <>
    <ResourceListHeading title="Administration" />
    <p className="state-transfer-scope">Move your infrastructure, catalog and configurations to another instance. Users and experiments are excluded.</p>
    <div className="state-transfer-tabs"><FormTabs label="State transfer" items={[
      {id: 'export', title: 'Export', content: <section className="state-transfer-panel" aria-label="Export state">
        {feedbackTab === 'export' && error && <p className="form-error" role="alert">{error}</p>}
        {feedbackTab === 'export' && success && <p className="state-transfer-success" role="status">{success}</p>}
        <form onSubmit={exportState}>
          <fieldset disabled={pending !== null}>
            <label>Archive password<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={exportPassword} onChange={event => setExportPassword(event.target.value)} /></label>
            <label>Confirm password<input type="password" autoComplete="new-password" minLength={12} maxLength={1024} required value={confirmation} onChange={event => setConfirmation(event.target.value)} /></label>
            <p className="state-transfer-hint">At least 12 characters. Keep this password for import.</p>
            <button className="button primary" type="submit">{pending === 'export' ? 'Exporting…' : 'Export state'}</button>
          </fieldset>
        </form>
      </section>},
      {id: 'import', title: 'Import', content: <section className="state-transfer-panel" aria-label="Import state">
        {feedbackTab === 'import' && error && <p className="form-error" role="alert">{error}</p>}
        {feedbackTab === 'import' && success && <p className="state-transfer-success" role="status">{success}</p>}
        <p>Import into a new instance. Configurations will be private and belong to your account.</p>
        <form onSubmit={inspect}>
          <fieldset disabled={pending !== null}>
            <label>State archive<input key={fileKey} type="file" accept=".kps,application/octet-stream" required onChange={event => {setFile(event.target.files?.[0] ?? null); setSummary(null); setError('')}} /></label>
            <label>Archive password<input type="password" autoComplete="off" required minLength={12} maxLength={1024} value={importPassword} onChange={event => {setImportPassword(event.target.value); setSummary(null)}} /></label>
            <button className="button secondary" type="submit">{pending === 'inspect' ? 'Checking…' : 'Review archive'}</button>
          </fieldset>
        </form>
        {summary && <div className="state-transfer-review">
          <h4 ref={reviewHeading} tabIndex={-1}>Archive contents</h4>
          <table aria-label="Archive contents"><tbody>
            <tr><th scope="row">Source version</th><td>{summary.version}</td></tr>
            <tr><th scope="row">Exported</th><td>{formatDate(summary.createdAt)}</td></tr>
            {Object.entries(countLabels).map(([key, label]) => <tr key={key}><th scope="row">{label}</th><td>{summary.counts[key] ?? 0}</td></tr>)}
          </tbody></table>
          <button className="button primary" type="button" disabled={pending !== null} onClick={importState}>{pending === 'import' ? 'Importing…' : 'Import state'}</button>
        </div>}
      </section>}
    ]} /></div>
  </>
}
