import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'

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
  Views: ({openOperation, openPipelineRun, view}: {openOperation: (id: string) => void; openPipelineRun: (id: string) => void; view: string}) => <><div>Current view: {view}</div><button onClick={() => openOperation('op_test')}>Open activity logs</button><button onClick={() => openPipelineRun('run_test')}>Open process logs</button></>
}))

vi.mock('./components/OperationDrawer', () => ({
  OperationDetailsPage: () => <div>Activity logs</div>,
  PipelineRunLogsPage: () => <div>Process logs</div>
}))

import App from './App'

describe('application navigation', () => {
  beforeEach(() => {
    window.location.hash = 'overview'
  })

  it('closes activity logs when a menu item is selected', async () => {
    render(<App />)

    fireEvent.click(await screen.findByRole('button', {name: 'Open activity logs'}))
    expect(screen.getByText('Activity logs')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: 'Catalog'}))
    expect(screen.queryByText('Activity logs')).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Open activity logs'})).toBeInTheDocument()
  })

  it('opens users on the first menu selection', async () => {
    render(<App />)

    fireEvent.click(await screen.findByRole('button', {name: 'Users'}))

    await waitFor(() => expect(screen.getByText('Current view: users')).toBeInTheDocument())
  })
})
