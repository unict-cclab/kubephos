import {beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {request} from '../api'
import type {AdminRequest, Session} from '../types'
import {RequestsView} from './RequestsView'

vi.mock('../api', () => ({request: vi.fn(async () => ({}))}))

const member: Session = {authenticated: true, csrfToken: 'csrf', user: {id: 'alice', username: 'alice', role: 'user'}} as Session
const admin: Session = {authenticated: true, csrfToken: 'csrf', user: {id: 'root', username: 'root', role: 'admin'}} as Session
const requests: AdminRequest[] = [
  {id: 'req_open', requesterUserId: 'alice', requester: 'alice', message: 'Please share the edge cluster.', status: 'open', createdAt: '2026-11-06T09:00:00Z', updatedAt: '2026-11-06T09:00:00Z'},
  {id: 'req_done', requesterUserId: 'bob', requester: 'bob', message: 'Increase my quota.', status: 'resolved', reply: 'Quota raised to 8 nodes.', resolvedBy: 'root', resolvedAt: '2026-11-06T10:00:00Z', createdAt: '2026-11-05T09:00:00Z', updatedAt: '2026-11-06T10:00:00Z'}
]

beforeEach(() => vi.mocked(request).mockClear())

describe('members', () => {
  it('send a trimmed message and see their requests with the outcome', async () => {
    const changed = vi.fn(async () => {})
    render(<RequestsView requests={requests.slice(1)} isAdmin={false} session={member} changed={changed} />)

    expect(screen.getByRole('heading', {name: 'Ask an admin'})).toBeInTheDocument()
    const own = within(screen.getByRole('list', {name: 'Your requests'}))
    expect(own.getByText('Resolved by root', {exact: false})).toBeInTheDocument()
    expect(own.getByText('Reply from root')).toBeInTheDocument()
    expect(own.getByText('Quota raised to 8 nodes.')).toBeInTheDocument()
    expect(screen.getByText(/0 of 3 open requests/)).toBeInTheDocument()

    fireEvent.change(screen.getByRole('textbox', {name: 'Message'}), {target: {value: 'Please add a GPU node.'}})
    fireEvent.click(screen.getByRole('button', {name: 'Send request'}))

    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request sent to the administrators.'))
    expect(request).toHaveBeenCalledWith('/admin-requests', {method: 'POST', body: JSON.stringify({message: 'Please add a GPU node.'})}, 'csrf')
    expect(screen.getByRole('textbox', {name: 'Message'})).toHaveValue('')
  })

  it('cannot send a blank message', () => {
    render(<RequestsView requests={[]} isAdmin={false} session={member} changed={async () => {}} />)
    fireEvent.change(screen.getByRole('textbox', {name: 'Message'}), {target: {value: '   '}})
    fireEvent.submit(screen.getByRole('button', {name: 'Send request'}).closest('form')!)
    expect(request).not.toHaveBeenCalled()
    expect(screen.getByText('No requests yet')).toBeInTheDocument()
  })

  it('wait for an answer once they have three open requests', () => {
    const open = [1, 2, 3].map(index => ({...requests[0], id: `req_${index}`}))
    render(<RequestsView requests={open} isAdmin={false} session={member} changed={async () => {}} />)
    expect(screen.getByRole('textbox', {name: 'Message'})).toBeDisabled()
    expect(screen.getByRole('button', {name: 'Send request'})).toBeDisabled()
    expect(screen.getByText(/You have 3 open requests/)).toBeInTheDocument()
  })
})

describe('administrators', () => {
  it('review open requests first and resolve them', async () => {
    const changed = vi.fn(async () => {})
    render(<RequestsView requests={requests} isAdmin session={admin} changed={changed} />)

    expect(screen.getByRole('button', {name: /Open\s*1/})).toHaveAttribute('aria-pressed', 'true')
    const open = within(screen.getByRole('list', {name: 'Open requests'}))
    expect(open.getByText('alice')).toBeInTheDocument()
    expect(open.queryByText('Increase my quota.')).not.toBeInTheDocument()

    fireEvent.click(open.getByRole('button', {name: 'Resolve'}))
    const dialog = within(screen.getByRole('dialog', {name: 'Resolve request from alice'}))
    fireEvent.change(dialog.getByRole('textbox', {name: 'Reply (optional)'}), {target: {value: 'Shared it with you.'}})
    fireEvent.click(dialog.getByRole('button', {name: 'Resolve request'}))
    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request resolved.'))
    expect(request).toHaveBeenCalledWith('/admin-requests/req_open', {method: 'PATCH', body: JSON.stringify({status: 'resolved', reply: 'Shared it with you.'})}, 'csrf')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('see the reply on resolved requests and can reopen them', async () => {
    const changed = vi.fn(async () => {})
    render(<RequestsView requests={requests} isAdmin session={admin} changed={changed} />)
    fireEvent.click(screen.getByRole('button', {name: /Resolved\s*1/}))
    const resolved = within(screen.getByRole('list', {name: 'Resolved requests'}))
    expect(resolved.getByText('Quota raised to 8 nodes.')).toBeInTheDocument()
    fireEvent.click(resolved.getByRole('button', {name: 'Reopen'}))
    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request reopened.'))
    expect(request).toHaveBeenCalledWith('/admin-requests/req_done', {method: 'PATCH', body: JSON.stringify({status: 'open'})}, 'csrf')
  })

  it('explain an empty inbox', () => {
    render(<RequestsView requests={[]} isAdmin session={admin} changed={async () => {}} />)
    expect(screen.getByText('No open requests')).toBeInTheDocument()
  })
})
