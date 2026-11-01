import {act, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import App from './App'
import {request} from './api'
import {emptyData, usePlatformData} from './hooks/usePlatformData'
import type {ManagedResource, Session} from './types'

vi.mock('./api', () => ({request: vi.fn()}))
vi.mock('./hooks/usePlatformData', async importOriginal => ({...await importOriginal<typeof import('./hooks/usePlatformData')>(), usePlatformData: vi.fn()}))

const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin', username: 'admin', role: 'admin'}} as Session
function setup() {
  const cluster = {id: 'cluster', name: 'Alpha', kind: 'kubernetes-cluster', status: 'ready', spec: {}, createdAt: '', updatedAt: '', connectivity: {status: 'reachable', checkedAt: new Date().toISOString()}} as ManagedResource
  const data = {...emptyData, kubernetesClusters: [cluster]}
  const load = vi.fn(async () => {cluster.status = 'deleting'})
  vi.mocked(usePlatformData).mockReturnValue({data, connected: true, refreshing: false, ready: true, load})
  let accept!: () => void, reject!: (cause: Error) => void
  const deletion = new Promise<void>((resolve, fail) => {accept = resolve; reject = fail})
  vi.mocked(request).mockImplementation((path: string) => {
    if (path === '/auth/status') return Promise.resolve(session) as never
    if (path === '/kubernetes-clusters/cluster') return deletion as never
    return Promise.reject(new Error('Unexpected request'))
  })
  render(<App />)
  return {accept, reject, load}
}
beforeEach(() => {vi.clearAllMocks(); window.location.hash = '#kubernetes'})
async function confirmDeletion() {
  fireEvent.click(await screen.findByRole('button', {name: 'Delete Alpha'}))
  fireEvent.click(screen.getByRole('button', {name: /^Delete$/}))
  await screen.findByText('Validating deletion')
}

describe('cluster deletion feedback', () => {
  it('changes status while the request is still validating, then shows the queued lifecycle', async () => {
    const {accept, load} = setup()
    await confirmDeletion()
    expect(load).not.toHaveBeenCalled()
    expect(screen.getByRole('button', {name: 'Delete Alpha'})).toBeDisabled()
    expect(screen.getByRole('heading', {name: 'Alpha'}).closest('td')).toHaveTextContent(/^Alpha$/)
    expect(screen.queryByText(/Validating cluster health and cleanup targets/)).not.toBeInTheDocument()
    await act(async () => accept())
    await screen.findByText('Deleting')
    expect(screen.queryByText('Validating deletion')).not.toBeInTheDocument()
  })
  it('restores the real status and reports the error if validation rejects deletion', async () => {
    const {reject, load} = setup()
    await confirmDeletion()
    await act(async () => reject(new Error('The cluster has an experiment in progress.')))
    await waitFor(() => expect(screen.getByRole('button', {name: 'Delete Alpha'})).toBeEnabled())
    expect(load).not.toHaveBeenCalled()
    expect(screen.queryByText('Validating deletion')).not.toBeInTheDocument()
    expect(screen.getByRole('alert')).toHaveTextContent('The cluster has an experiment in progress.')
  })
})
