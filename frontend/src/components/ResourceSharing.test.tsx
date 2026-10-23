import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import {ResourceSharing} from './ResourceSharing'

vi.mock('../api', () => ({request: vi.fn()}))
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})
afterEach(() => vi.clearAllMocks())
const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'alice', username: 'alice', role: 'user' as const}}
const recipients = {items: [{id: 'alice', username: 'alice'}, {id: 'bob', username: 'bob'}, {id: 'carol', username: 'carol'}]}

describe('resource sharing', () => {
  it('starts private and saves selected recipients without changing ownership', async () => {
    vi.mocked(request).mockImplementation(async path => path === '/sharing/users' ? recipients as never : {visibility: 'private', userIds: []} as never)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<ResourceSharing kind="experiments" id="exp" name="Research" ownerUserId="alice" session={session} changed={changed} />)
    fireEvent.click(screen.getByRole('button', {name: 'Share Research'}))
    expect(await screen.findByRole('combobox', {name: 'Who can view'})).toHaveValue('private')
    expect(screen.getByRole('button', {name: 'Save sharing'})).toBeDisabled()
    fireEvent.change(screen.getByRole('combobox'), {target: {value: 'selected'}})
    expect(screen.queryByRole('checkbox', {name: 'alice'})).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Save sharing'})).toBeDisabled()
    fireEvent.click(screen.getByRole('checkbox', {name: 'bob'}))
    fireEvent.click(screen.getByRole('button', {name: 'Save sharing'}))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/experiments/exp/sharing', {method: 'PUT', body: JSON.stringify({visibility: 'selected', userIds: ['bob']})}, 'csrf'))
    expect(changed).toHaveBeenCalledOnce()
  })

  it('revokes a public share by saving private with no recipients', async () => {
    vi.mocked(request).mockImplementation(async path => path === '/sharing/users' ? recipients as never : {visibility: 'everyone', userIds: []} as never)
    render(<ResourceSharing kind="experiment-configurations" id="cfg" name="Recipe" ownerUserId="alice" session={session} changed={async () => {}} />)
    fireEvent.click(screen.getByRole('button', {name: 'Share Recipe'}))
    expect(await screen.findByRole('combobox')).toHaveValue('everyone')
    fireEvent.change(screen.getByRole('combobox'), {target: {value: 'private'}})
    fireEvent.click(screen.getByRole('button', {name: 'Save sharing'}))
    await waitFor(() => expect(request).toHaveBeenCalledWith('/experiment-configurations/cfg/sharing', {method: 'PUT', body: JSON.stringify({visibility: 'private', userIds: []})}, 'csrf'))
  })

  it('blocks saving failed settings and allows retrying the load', async () => {
    vi.mocked(request).mockRejectedValue(new Error('Sharing unavailable'))
    render(<ResourceSharing kind="experiments" id="exp" name="Research" ownerUserId="alice" session={session} changed={async () => {}} />)
    fireEvent.click(screen.getByRole('button', {name: 'Share Research'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('Sharing unavailable')
    expect(screen.getByRole('button', {name: 'Save sharing'})).toBeDisabled()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    vi.mocked(request).mockImplementation(async path => path === '/sharing/users' ? recipients as never : {visibility: 'everyone', userIds: []} as never)
    fireEvent.click(screen.getByRole('button', {name: 'Retry'}))
    expect(await screen.findByRole('combobox')).toHaveValue('everyone')
    expect(screen.getByRole('button', {name: 'Save sharing'})).toBeDisabled()
    fireEvent.change(screen.getByRole('combobox'), {target: {value: 'private'}})
    expect(screen.getByRole('button', {name: 'Save sharing'})).toBeEnabled()
  })

  it('does not let a shared reader manage sharing', () => {
    render(<ResourceSharing kind="experiments" id="exp" name="Other" ownerUserId="bob" session={session} changed={async () => {}} />)
    expect(screen.queryByRole('button', {name: 'Share Other'})).not.toBeInTheDocument()
    expect(request).not.toHaveBeenCalled()
  })
})
