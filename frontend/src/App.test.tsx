import {useState} from 'react'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'

vi.mock('./api', () => ({
  ApiError: class extends Error {status = 500; body = {}},
  request: vi.fn(async (path: string) => {
    if (path === '/auth/status') return {authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'}}
    if (path === '/system') return {status: 'healthy', version: 'test'}
    if (path === '/plugin-runtime') return null
    return {items: []}
  })
}))

vi.mock('./components/Views', () => ({
  Views: ({openOperation, openPipelineRun, view}: {openOperation: (id: string) => void; openPipelineRun: (id: string) => void; view: string}) => {const [draft, setDraft] = useState(''); return <><input aria-label="Draft filter" value={draft} onChange={event => setDraft(event.target.value)} /><div>Current view: {view}</div><button onClick={() => openOperation('op_test')}>Open activity logs</button><button onClick={() => openPipelineRun('run_test')}>Open process logs</button></>}
}))

vi.mock('./components/OperationDrawer', () => ({
  OperationDetailsPage: () => <div>Activity logs</div>,
  PipelineRunLogsPage: ({close}: {close: () => void}) => <div>Process logs<button onClick={close}>Back</button></div>
}))

import App from './App'
import {request} from './api'

afterEach(() => vi.unstubAllGlobals())

describe('application navigation', () => {
  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
    HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
    vi.mocked(request).mockClear()
    window.location.hash = 'overview'
  })

  it('closes activity logs when a menu item is selected', async () => {
    render(<App />)

    fireEvent.click(await screen.findByRole('button', {name: 'Open activity logs'}))
    expect(screen.getByText('Activity logs')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: 'Lab'}))
    expect(screen.queryByText('Activity logs')).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Open activity logs'})).toBeInTheDocument()
  })

  it('keeps configurations inside experiments, without a separate suites menu', async () => {
    render(<App />)
    fireEvent.click(await screen.findByRole('button', {name: 'Lab'}))
    expect(await screen.findByRole('button', {name: 'Experiments'})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Configurations'})).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Suites'})).not.toBeInTheDocument()
  })

  it('opens the administrator state transfer section', async () => {
    render(<App />)
    fireEvent.click(await screen.findByRole('button', {name: 'Account settings'}))
    fireEvent.click(screen.getByRole('button', {name: 'Backup and transfer'}))
    await waitFor(() => expect(screen.getByText('Current view: administration')).toBeInTheDocument())
  })

  it('jumps to a section from the keyboard palette', async () => {
    render(<App />)
    await screen.findByRole('button', {name: 'Lab'})

    fireEvent.keyDown(window, {key: 'k', ctrlKey: true})
    const input = screen.getByRole('combobox', {name: 'Jump to'})
    fireEvent.change(input, {target: {value: 'accounts'}})
    fireEvent.keyDown(input, {key: 'Enter'})

    await waitFor(() => expect(screen.getByText('Current view: users')).toBeInTheDocument())
    expect(screen.queryByRole('combobox', {name: 'Jump to'})).not.toBeInTheDocument()
  })

  it('sends the new experiment action to configurations until one can run', async () => {
    render(<App />)
    fireEvent.click(await screen.findByRole('button', {name: 'New experiment'}))
    await waitFor(() => expect(screen.getByText('Current view: experiments')).toBeInTheDocument())
    expect(await screen.findByText('Experiments run from a validated configuration. Create one to get started.')).toBeInTheDocument()
  })

  it('opens team members on the first menu selection', async () => {
    render(<App />)

    fireEvent.click(await screen.findByRole('button', {name: 'Team'}))

    await waitFor(() => expect(screen.getByText('Current view: users')).toBeInTheDocument())
  })
})


describe('automatic authentication', () => {
  it('redirects unauthenticated users without showing an intermediate sign-in dialog', async () => {
    const replace = vi.fn()
    vi.stubGlobal('location', {hash: '#overview', replace})
    vi.mocked(request).mockResolvedValueOnce({authenticated: false, setupRequired: false})
    render(<App />)
    await waitFor(() => expect(replace).toHaveBeenCalledWith('/api/v1/auth/login'))
    expect(screen.queryByRole('button', {name: 'Sign in with Keycloak'})).not.toBeInTheDocument()
    expect(vi.mocked(request).mock.calls.every(([path]) => path === '/auth/status')).toBe(true)
  })

  it('preserves initial administrator setup', async () => {
    const replace = vi.fn()
    vi.stubGlobal('location', {hash: '#overview', replace})
    vi.mocked(request).mockResolvedValueOnce({authenticated: false, setupRequired: true})
    render(<App />)
    expect(await screen.findByRole('button', {name: 'Create administrator'})).toBeInTheDocument()
    expect(replace).not.toHaveBeenCalled()
  })
})


it('preserves the workspace state when returning from process logs', async () => {
  render(<App />)
  const input = await screen.findByRole('textbox', {name: 'Draft filter'})
  fireEvent.change(input, {target: {value: 'Run 2'}})
  fireEvent.click(screen.getByRole('button', {name: 'Open process logs'}))
  expect(screen.queryByRole('textbox', {name: 'Draft filter'})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Back'}))
  expect(screen.getByRole('textbox', {name: 'Draft filter'})).toHaveValue('Run 2')
})
