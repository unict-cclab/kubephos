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
    expect(screen.queryByRole('list', {name: 'Open requests'})).not.toBeInTheDocument()
    const own = within(screen.getByRole('list', {name: 'Resolved requests'}))
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

  it('edit the text of their own open request in place', async () => {
    const changed = vi.fn(async () => {})
    render(<RequestsView requests={requests} isAdmin={false} session={member} changed={changed} />)
    const own = within(screen.getByRole('list', {name: 'Open requests'}))
    expect(own.getAllByRole('button', {name: 'Edit'})).toHaveLength(1)
    fireEvent.click(own.getByRole('button', {name: 'Edit'}))
    const editor = own.getByRole('textbox', {name: 'Message'})
    expect(editor).toHaveValue('Please share the edge cluster.')
    fireEvent.change(editor, {target: {value: 'Please share the edge cluster until Friday.'}})
    fireEvent.click(own.getByRole('button', {name: 'Save'}))
    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request updated.'))
    expect(request).toHaveBeenCalledWith('/admin-requests/req_open', {method: 'PATCH', body: JSON.stringify({message: 'Please share the edge cluster until Friday.'})}, 'csrf')
  })

  it('mark their own open request as resolved and see that they resolved it', async () => {
    const changed = vi.fn(async () => {})
    const {rerender} = render(<RequestsView requests={requests.slice(0, 1)} isAdmin={false} session={member} changed={changed} />)
    fireEvent.click(screen.getByRole('button', {name: 'Mark as resolved'}))
    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request marked as resolved.'))
    expect(request).toHaveBeenCalledWith('/admin-requests/req_open', {method: 'PATCH', body: JSON.stringify({status: 'resolved'})}, 'csrf')
    rerender(<RequestsView requests={[{...requests[0], status: 'resolved', resolvedBy: 'alice', resolvedAt: '2026-11-06T11:00:00Z'}]} isAdmin={false} session={member} changed={changed} />)
    expect(screen.getByText('Resolved by you', {exact: false})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Edit'})).not.toBeInTheDocument()
  })
})

describe('request history', () => {
  const resolved = Array.from({length: 8}, (_, index) => ({...requests[1], id: `req_old_${index}`, requesterUserId: 'alice', requester: 'alice', message: `Old request ${index + 1}`}))

  it('folds resolved requests away for members and reveals older ones on demand', () => {
    const {container} = render(<RequestsView requests={[requests[0], ...resolved]} isAdmin={false} session={member} changed={async () => {}} />)
    const history = container.querySelector('details.request-history')!
    expect(history).not.toHaveAttribute('open')
    expect(history.querySelector('summary')).toHaveTextContent('Resolved 8')
    const list = within(screen.getByRole('list', {name: 'Resolved requests'}))
    expect(list.getAllByRole('listitem')).toHaveLength(5)
    fireEvent.click(screen.getByRole('button', {name: 'Show 3 older requests'}))
    expect(list.getAllByRole('listitem')).toHaveLength(8)
  })

  it('shows administrators the newest resolved requests first', () => {
    render(<RequestsView requests={Array.from({length: 12}, (_, index) => ({...requests[1], id: `req_${index}`}))} isAdmin session={admin} changed={async () => {}} />)
    fireEvent.click(screen.getByRole('button', {name: /Resolved\s*12/}))
    expect(within(screen.getByRole('list', {name: 'Resolved requests'})).getAllByRole('listitem')).toHaveLength(10)
    expect(screen.getByRole('button', {name: 'Show 2 older requests'})).toBeInTheDocument()
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

  it('find the requests of one user by typing part of their username', () => {
    const inbox = [...requests, {...requests[0], id: 'req_bob_open', requesterUserId: 'bob', requester: 'bob', message: 'Bob needs a cluster.'}]
    render(<RequestsView requests={inbox} isAdmin session={admin} changed={async () => {}} />)
    expect(screen.getByRole('button', {name: /Open\s*2/})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Search requests by username'}), {target: {value: 'BO'}})
    const open = within(screen.getByRole('list', {name: 'Open requests'}))
    expect(open.getByText('Bob needs a cluster.')).toBeInTheDocument()
    expect(open.queryByText('Please share the edge cluster.')).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: /Open\s*1/})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: /Resolved\s*1/})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Search requests by username'}), {target: {value: 'carol'}})
    expect(screen.getByText('No open requests from “carol”')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Clear search'}))
    expect(screen.getByRole('button', {name: /Open\s*2/})).toBeInTheDocument()
  })

  it('delete a request after confirming it', async () => {
    const changed = vi.fn(async () => {})
    render(<RequestsView requests={requests} isAdmin session={admin} changed={changed} />)
    fireEvent.click(screen.getByRole('button', {name: 'Delete request from alice'}))
    const dialog = within(screen.getByRole('dialog', {name: 'Confirm deletion'}))
    expect(dialog.getByText(/It disappears for everyone, alice included\./)).toBeInTheDocument()
    expect(request).not.toHaveBeenCalled()
    fireEvent.click(dialog.getByRole('button', {name: 'Delete'}))
    await waitFor(() => expect(changed).toHaveBeenCalledWith('Request deleted.'))
    expect(request).toHaveBeenCalledWith('/admin-requests/req_open', {method: 'DELETE'}, 'csrf')
  })

  it('explain an empty inbox', () => {
    render(<RequestsView requests={[]} isAdmin session={admin} changed={async () => {}} />)
    expect(screen.getByText('No open requests')).toBeInTheDocument()
  })
})
