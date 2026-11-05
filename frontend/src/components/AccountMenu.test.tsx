import {afterEach, expect, it, vi} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {AccountMenu, accountLinks} from './AccountMenu'

afterEach(() => {
  document.documentElement.dataset.theme = 'light'
  window.localStorage.clear()
})

function renderMenu(isAdmin: boolean) {
  const actions = {refresh: vi.fn(), openBackup: vi.fn(), signOut: vi.fn()}
  render(<AccountMenu username="fabio" isAdmin={isAdmin} version="1.2.3" refreshing={false} {...actions} />)
  return actions
}

it('marks administrators next to their name and nobody else', () => {
  renderMenu(true)
  expect(screen.getByText('Admin')).toHaveClass('role-badge')
})

it('shows no role for other users', () => {
  renderMenu(false)
  expect(screen.queryByText('Admin')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Account settings'}))
  expect(screen.queryByText(/Member|Administrator/)).not.toBeInTheDocument()
  expect(screen.getByText('KubePhos 1.2.3')).toBeInTheDocument()
})

it('opens account options from the gear and closes with Escape', () => {
  renderMenu(false)
  const gear = screen.getByRole('button', {name: 'Account settings'})
  expect(gear).toHaveAttribute('aria-expanded', 'false')

  fireEvent.click(gear)
  expect(screen.getByRole('link', {name: /Change password/})).toHaveAttribute('href', accountLinks.password)
  expect(screen.getByRole('link', {name: /Account details/})).toHaveAttribute('target', '_blank')
  expect(screen.queryByRole('button', {name: 'Backup and transfer'})).not.toBeInTheDocument()

  fireEvent.keyDown(screen.getByRole('button', {name: 'Sign out'}), {key: 'Escape'})
  expect(screen.queryByRole('group', {name: 'Account'})).not.toBeInTheDocument()
  expect(gear).toHaveFocus()
})

it('runs session actions and offers backups to administrators', () => {
  const actions = renderMenu(true)
  fireEvent.click(screen.getByRole('button', {name: 'Account settings'}))
  fireEvent.click(screen.getByRole('button', {name: 'Backup and transfer'}))
  expect(actions.openBackup).toHaveBeenCalled()
  expect(screen.queryByRole('group', {name: 'Account'})).not.toBeInTheDocument()

  fireEvent.click(screen.getByRole('button', {name: 'Account settings'}))
  fireEvent.click(screen.getByRole('button', {name: 'Sign out'}))
  expect(actions.signOut).toHaveBeenCalled()
})

it('switches the theme without closing the menu', () => {
  renderMenu(false)
  fireEvent.click(screen.getByRole('button', {name: 'Account settings'}))
  fireEvent.click(screen.getByRole('button', {name: 'Dark mode'}))
  expect(document.documentElement.dataset.theme).toBe('dark')
  expect(screen.getByRole('button', {name: 'Dark mode'})).toHaveAttribute('aria-pressed', 'true')
})
