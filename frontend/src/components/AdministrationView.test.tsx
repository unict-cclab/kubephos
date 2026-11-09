import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import axe from 'axe-core'
import {AdministrationView, describeContents} from './AdministrationView'
import type {Session} from '../types'

const session: Session = {authenticated: true, setupRequired: false, csrfToken: 'csrf', user: {id: 'admin', username: 'admin', role: 'admin'}}
afterEach(() => vi.unstubAllGlobals())

const readinessPath = '/api/v1/administration/state/readiness'
// Answers the import readiness check, then serves the transfer calls in order.
function transferFetch(...responses: Response[]) {
  return vi.fn((url: string) => Promise.resolve(url === readinessPath ? new Response(JSON.stringify({ready: true})) : responses.shift()!))
}
const transferCalls = (fetch: ReturnType<typeof transferFetch>) => fetch.mock.calls.filter(([url]) => url !== readinessPath)

describe('instance state transfer', () => {
  it('provides accessible forms and archive review', async () => {
    render(<main><h1>KubePhos</h1><AdministrationView session={session} changed={vi.fn()} /></main>)
    const result = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(result.violations.map(item => item.id)).toEqual([])
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    const importResult = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(importResult.violations.map(item => item.id)).toEqual([])
  })
  it('separates export and import into keyboard-accessible tabs without environment-file copy', () => {
    render(<AdministrationView session={session} changed={vi.fn()} />)
    expect(screen.getByRole('tabpanel', {name: 'Export'})).toBeVisible()
    expect(screen.queryByRole('button', {name: 'Review archive'})).not.toBeInTheDocument()
    expect(screen.queryByText(/\.env/)).not.toBeInTheDocument()
    fireEvent.keyDown(screen.getByRole('tab', {name: 'Export'}), {key: 'ArrowRight'})
    expect(screen.getByRole('tab', {name: 'Import'})).toHaveFocus()
    expect(screen.getByRole('button', {name: 'Review archive'})).toBeVisible()
    expect(screen.queryByRole('button', {name: 'Export state'})).not.toBeInTheDocument()
  })
  it('is unavailable to regular users', () => {
    render(<AdministrationView session={{...session, user: {...session.user!, role: 'user'}}} changed={vi.fn()} />)
    expect(screen.queryByRole('heading', {name: 'Import and export'})).not.toBeInTheDocument()
  })
  it('reviews an encrypted archive before importing and clears the review when credentials change', async () => {
    const fetch = transferFetch(new Response(JSON.stringify({version: 'test', createdAt: '2026-10-26T10:00:00Z', counts: {credentials: 2}})))
    vi.stubGlobal('fetch', fetch)
    render(<AdministrationView session={session} changed={vi.fn()} />)
    expect(screen.queryByRole('button', {name: 'Import state'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    fireEvent.change(screen.getByLabelText('State archive'), {target: {files: [new File(['encrypted'], 'state.kps')]}})
    fireEvent.change(screen.getAllByLabelText('Archive password')[1], {target: {value: 'archive-password'}})
    fireEvent.submit(screen.getByRole('button', {name: 'Review archive'}).closest('form')!)
    expect(await screen.findByRole('table', {name: 'Archive contents'})).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('heading', {name: 'Archive contents'})).toHaveFocus())
    const [[path, init]] = transferCalls(fetch) as unknown as [[string, RequestInit & {headers: Headers}]]
    expect(path).toBe('/api/v1/administration/state/inspect')
    expect(init.headers.get('X-CSRF-Token')).toBe('csrf')
    expect(init.headers.has('Content-Type')).toBe(false)
    expect(screen.getByRole('button', {name: 'Import state'})).toBeEnabled()
    fireEvent.change(screen.getAllByLabelText('Archive password')[1], {target: {value: 'different-password'}})
    expect(screen.queryByRole('button', {name: 'Import state'})).not.toBeInTheDocument()
  })
  it('reports archive errors and does not offer import', async () => {
    vi.stubGlobal('fetch', transferFetch(new Response(JSON.stringify({message: 'Invalid archive or incorrect password.'}), {status: 422})))
    render(<AdministrationView session={session} changed={vi.fn()} />)
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    fireEvent.change(screen.getByLabelText('State archive'), {target: {files: [new File(['bad'], 'state.kps')]}})
    fireEvent.change(screen.getAllByLabelText('Archive password')[1], {target: {value: 'wrong-password'}})
    fireEvent.submit(screen.getByRole('button', {name: 'Review archive'}).closest('form')!)
    expect(await screen.findByRole('alert')).toHaveTextContent('Invalid archive or incorrect password.')
    expect(screen.queryByRole('button', {name: 'Import state'})).not.toBeInTheDocument()
  })
  it('requires matching export passwords without sending them to the API on mismatch', () => {
    const fetch = transferFetch(); vi.stubGlobal('fetch', fetch)
    render(<AdministrationView session={session} changed={vi.fn()} />)
    fireEvent.change(screen.getAllByLabelText('Archive password')[0], {target: {value: 'archive-password'}})
    fireEvent.change(screen.getByLabelText('Confirm password'), {target: {value: 'other-password'}})
    fireEvent.click(screen.getByRole('button', {name: 'Export state'}))
    expect(screen.getByText('Passwords do not match.')).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(transferCalls(fetch)).toEqual([])
  })
  it('imports only after explicit review and refreshes resources', async () => {
    const changed = vi.fn().mockResolvedValue(undefined)
    const fetch = transferFetch(new Response(JSON.stringify({version: 'test', createdAt: '2026-10-26T10:00:00Z', counts: {}})), new Response(JSON.stringify({imported: true})))
    vi.stubGlobal('fetch', fetch)
    render(<AdministrationView session={session} changed={changed} />)
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    fireEvent.change(screen.getByLabelText('State archive'), {target: {files: [new File(['encrypted'], 'state.kps')]}})
    fireEvent.change(screen.getAllByLabelText('Archive password')[1], {target: {value: 'archive-password'}})
    fireEvent.submit(screen.getByRole('button', {name: 'Review archive'}).closest('form')!)
    fireEvent.click(await screen.findByRole('button', {name: 'Import state'}))
    await waitFor(() => expect(changed).toHaveBeenCalledOnce())
    expect(transferCalls(fetch)[1][0]).toBe('/api/v1/administration/state/import')
    expect(screen.getByRole('status')).toHaveTextContent('State imported.')
    expect(screen.getAllByLabelText('Archive password')[1]).toHaveValue('')
  })
  it('asks before replacing the data of an instance that is not empty', async () => {
    const contents = {connections: 2, credentials: 3, resources: 0, configurations: 1, experiments: 0}
    const fetch = vi.fn((url: string, _init?: RequestInit) => Promise.resolve(url === readinessPath
      ? new Response(JSON.stringify({ready: false, reason: 'not-empty', contents}))
      : url.endsWith('/inspect') ? new Response(JSON.stringify({version: 'test', createdAt: '2026-10-26T10:00:00Z', counts: {}})) : new Response(JSON.stringify({imported: true}))))
    vi.stubGlobal('fetch', fetch)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<AdministrationView session={session} changed={changed} />)
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    expect(await screen.findByText(/It holds 2 Proxmox connections, 3 credentials and 1 configuration\./)).toBeInTheDocument()
    fireEvent.change(screen.getByLabelText('State archive'), {target: {files: [new File(['encrypted'], 'state.kps')]}})
    fireEvent.change(screen.getAllByLabelText('Archive password')[1], {target: {value: 'archive-password'}})
    fireEvent.submit(screen.getByRole('button', {name: 'Review archive'}).closest('form')!)
    fireEvent.click(await screen.findByRole('button', {name: 'Overwrite and import…'}))
    expect(await screen.findByRole('dialog', {name: 'Overwrite everything on this instance?'})).toHaveTextContent('This cannot be undone.')
    expect(fetch.mock.calls.some(([url]) => url.endsWith('/import'))).toBe(false)
    fireEvent.click(screen.getByRole('button', {name: 'Overwrite and import'}))
    await waitFor(() => expect(changed).toHaveBeenCalledOnce())
    const init = fetch.mock.calls.find(([url]) => url.endsWith('/import'))![1]!
    expect((init.body as FormData).get('replace')).toBe('true')
    expect(screen.getByText('Instance state replaced with the archive.')).toBeInTheDocument()
  })
  it('keeps import unavailable while work is running', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ready: false, reason: 'busy'}))))
    render(<AdministrationView session={session} changed={vi.fn()} />)
    fireEvent.click(screen.getByRole('tab', {name: 'Import'}))
    expect(await screen.findByText('Work is still running')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Review archive'})).toBeDisabled()
  })
  it('describes instance contents in plain words', () => {
    expect(describeContents({connections: 1, credentials: 0, resources: 7, configurations: 0, experiments: 0})).toBe('1 Proxmox connection and 7 infrastructure resources')
    expect(describeContents({connections: 0, credentials: 0, resources: 0, configurations: 0, experiments: 0})).toBe('infrastructure history')
  })
})
