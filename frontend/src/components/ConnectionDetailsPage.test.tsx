import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import type {Connection, Session} from '../types'
import {ConnectionDetailsPage} from './ConnectionDetailsPage'

const connection: Connection = {
  id: 'connection_1',
  name: 'Research Proxmox',
  description: 'Main lab',
  provider: 'proxmox',
  pluginId: 'proxmox',
  createdAt: '2026-09-20T10:00:00Z',
  updatedAt: '2026-09-21T10:00:00Z'
}

afterEach(() => vi.unstubAllGlobals())

describe('connection details', () => {
  it('shows only configured values and reveals the token on demand', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({...connection, configuration: {endpoint: 'https://proxmox.example:8006', verifyTLS: true, tokenId: 'root@pam!kubephos', tokenSecret: '••••••••'}}), {status: 200, headers: {'Content-Type': 'application/json'}}))
      .mockResolvedValueOnce(new Response(JSON.stringify({tokenSecret: 'actual-secret'}), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin', username: 'admin', role: 'admin'}} as Session

    render(<ConnectionDetailsPage connection={connection} session={session} close={() => undefined} />)

    expect(await screen.findByText('https://proxmox.example:8006')).toBeInTheDocument()
    expect(screen.queryByText('Main lab')).not.toBeInTheDocument()
    expect(screen.queryByText('Description')).not.toBeInTheDocument()
    expect(screen.queryByText('Saved credential')).not.toBeInTheDocument()
    const secret = screen.getByLabelText('Token secret')
    expect(secret).toHaveAttribute('type', 'password')
    fireEvent.click(screen.getByRole('button', {name: 'Show'}))
    await waitFor(() => expect(secret).toHaveAttribute('type', 'text'))
    expect(secret).toHaveValue('actual-secret')
    expect(fetch).toHaveBeenLastCalledWith('/api/v1/connections/connection_1/secret', expect.objectContaining({method: 'POST'}))
  })
})
