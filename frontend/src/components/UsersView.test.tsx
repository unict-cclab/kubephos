import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import type {ManagedResource, Session, User} from '../types'
import {UsersView} from './UsersView'

afterEach(() => vi.unstubAllGlobals())

describe('users view', () => {
  it('renders aligned user data with a readable role', () => {
    const user: User = {id: 'admin-1', username: 'administrator', email: 'admin@example.test', role: 'admin', enabled: true, createdAt: '2026-09-24T10:00:00Z'}
    const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin-1', username: 'administrator', role: 'admin'}} as Session

    render(<UsersView users={[user]} assignments={[]} clusters={[]} session={session} changed={vi.fn()} />)

    const table = screen.getByRole('table')
    expect(table).toHaveClass('resource-list-table')
    expect(within(table).getByText('Administrator')).toHaveClass('user-role', 'admin')
    expect(within(table).getByRole('columnheader', {name: 'Email'})).toBeInTheDocument()
    expect(screen.getByText('admin@example.test').closest('td')).toHaveAttribute('data-label', 'Email')
    expect(screen.getByText('All')).toHaveClass('user-cluster-count')
  })

  it('renders an empty table state', () => {
    const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin-1', username: 'administrator', role: 'admin'}} as Session

    render(<UsersView users={[]} assignments={[]} clusters={[]} session={session} changed={vi.fn()} />)

    expect(screen.getByText('No users.').closest('td')).toHaveAttribute('colspan', '9')
  })

  it.each(['Cancel', 'Close', 'success'])('starts with a fresh form after %s', async exit => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', {status: 201})))
    const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin', username: 'admin', role: 'admin'}} as Session
    render(<UsersView users={[]} assignments={[]} clusters={[]} session={session} changed={vi.fn().mockResolvedValue(undefined)} />)
    fireEvent.click(screen.getByRole('button', {name: 'Create user'}))
    let dialog = screen.getByRole('dialog')
    fireEvent.change(within(dialog).getByLabelText('Username'), {target: {value: 'researcher'}})
    fireEvent.change(within(dialog).getByLabelText('Email'), {target: {value: 'researcher@example.test'}})
    fireEvent.change(within(dialog).getByLabelText('Temporary password'), {target: {value: 'temporary-test-password'}})
    fireEvent.change(within(dialog).getByLabelText('Role'), {target: {value: 'admin'}})
    fireEvent.click(within(dialog).getByRole('button', {name: exit === 'success' ? 'Create' : exit}))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: 'Create user'}))
    dialog = screen.getByRole('dialog')
    for (const label of ['Username', 'Email', 'Temporary password']) expect(within(dialog).getByLabelText(label)).toHaveValue('')
    expect(within(dialog).getByLabelText('Role')).toHaveValue('user')
  })

  it('opens a full detail page and saves cluster assignments', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, {status: 204}))
    vi.stubGlobal('fetch', fetch)
    const user: User = {id: 'user-1', username: 'researcher', email: 'researcher@example.test', role: 'user', enabled: true, createdAt: '2026-09-24T10:00:00Z', updatedAt: '2026-09-24T11:00:00Z'}
    const cluster = {id: 'cluster-1', name: 'research-cluster', status: 'ready'} as ManagedResource
    const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'admin', username: 'admin', role: 'admin'}} as Session
    const changed = vi.fn().mockResolvedValue(undefined)

    render(<UsersView users={[user]} assignments={[]} clusters={[cluster]} session={session} changed={changed} />)
    fireEvent.click(screen.getByRole('button', {name: 'View researcher'}))

    expect(screen.getByRole('heading', {name: 'researcher'})).toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByText('0 of 1')).toBeInTheDocument()
    const assignment = screen.getByRole('checkbox', {name: /research-cluster/i})
    fireEvent.click(assignment)
    expect(assignment.closest('label')).toHaveClass('selected')
    expect(screen.getByText('1 of 1')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Save'}))

    await waitFor(() => expect(fetch).toHaveBeenCalledWith('/api/v1/users/user-1/clusters', expect.objectContaining({method: 'PUT', body: JSON.stringify({clusterIds: ['cluster-1']})})))
    expect(changed).toHaveBeenCalled()
  })
})

describe('user account actions',()=>{
 const user={id:'user-1',username:'researcher',email:'old@example.test',role:'user',enabled:true} as User
 const session={authenticated:true,csrfToken:'csrf',user:{id:'admin',username:'admin',role:'admin'}} as Session
 it('edits email and resets passwords without retaining password fields',async()=>{
  const fetch=vi.fn().mockImplementation(async()=>new Response('{}',{status:200}));vi.stubGlobal('fetch',fetch)
  render(<UsersView users={[user]} assignments={[]} clusters={[]} session={session} changed={vi.fn().mockResolvedValue(undefined)}/>)
  fireEvent.click(screen.getByRole('button',{name:'Edit email for researcher'}))
  let dialog=screen.getByRole('dialog')
  expect(within(dialog).getByLabelText('Email')).toHaveValue('old@example.test')
  fireEvent.change(within(dialog).getByLabelText('Email'),{target:{value:'new@example.test'}})
  fireEvent.click(within(dialog).getByRole('button',{name:'Save'}))
  await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/api/v1/users/user-1',expect.objectContaining({method:'PATCH',body:JSON.stringify({email:'new@example.test'})})))
  await waitFor(()=>expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  fireEvent.click(screen.getByRole('button',{name:'View researcher'}))
  fireEvent.click(screen.getByRole('button',{name:'Reset password'}))
  dialog=screen.getByRole('dialog')
  fireEvent.change(within(dialog).getByLabelText('Temporary password'),{target:{value:'temporary-test-password'}})
  fireEvent.change(within(dialog).getByLabelText('Confirm password'),{target:{value:'other-test-password'}})
  fireEvent.click(within(dialog).getByRole('button',{name:'Reset password'}))
  expect(await screen.findByRole('alert')).toHaveTextContent('Passwords do not match.')
  fireEvent.change(within(dialog).getByLabelText('Confirm password'),{target:{value:'temporary-test-password'}})
  fireEvent.click(within(dialog).getByRole('button',{name:'Reset password'}))
  await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/api/v1/users/user-1/password',expect.objectContaining({method:'POST',body:JSON.stringify({password:'temporary-test-password'})})))
  await waitFor(()=>expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  fireEvent.click(screen.getByRole('button',{name:'Reset password'}))
  expect(screen.getByLabelText('Temporary password')).toHaveValue('')
  expect(screen.getByLabelText('Confirm password')).toHaveValue('')
 })
 it('requires confirmation before deleting another account',async()=>{
  const fetch=vi.fn().mockResolvedValue(new Response(null,{status:204}));vi.stubGlobal('fetch',fetch)
  render(<UsersView users={[user]} assignments={[]} clusters={[]} session={session} changed={vi.fn().mockResolvedValue(undefined)}/>)
  fireEvent.click(screen.getByRole('button',{name:'View researcher'}))
  fireEvent.click(screen.getByRole('button',{name:'Delete user researcher'}))
  expect(fetch).not.toHaveBeenCalled()
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button',{name:'Delete'}))
  await waitFor(()=>expect(fetch).toHaveBeenCalledWith('/api/v1/users/user-1',expect.objectContaining({method:'DELETE'})))
 })
})
