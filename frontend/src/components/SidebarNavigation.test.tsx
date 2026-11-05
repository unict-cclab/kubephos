import {expect, it, vi} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {visibleNavigation} from '../navigation'
import {SidebarNavigation} from './SidebarNavigation'

const sections = visibleNavigation(true)

it('shows the four sections and only the pages of the open one', () => {
  render(<SidebarNavigation sections={sections} view="results" navigate={() => {}} />)

  for (const label of ['Home', 'Platform', 'Lab', 'Team']) expect(screen.getByRole('button', {name: label})).toBeInTheDocument()
  expect(screen.getByRole('button', {name: 'Experiments'})).toHaveAttribute('aria-current', 'page')
  expect(screen.getByRole('button', {name: 'Library'})).toBeInTheDocument()
  expect(screen.queryByRole('button', {name: 'Clusters'})).not.toBeInTheDocument()
})

it('opens a section on its first page and keeps the current page when it is already open', () => {
  const navigate = vi.fn()
  render(<SidebarNavigation sections={sections} view="results" navigate={navigate} />)

  fireEvent.click(screen.getByRole('button', {name: 'Platform'}))
  expect(navigate).toHaveBeenLastCalledWith('kubernetes')
  fireEvent.click(screen.getByRole('button', {name: 'Lab'}))
  expect(navigate).toHaveBeenLastCalledWith('results')
})

it('keeps Experiments current while one of its steps is open', () => {
  render(<SidebarNavigation sections={sections} view="experiments" navigate={() => {}} />)
  expect(screen.getByRole('button', {name: 'Experiments'})).toHaveAttribute('aria-current', 'page')
})

it('marks a single-page section itself as the current page', () => {
  render(<SidebarNavigation sections={sections} view="overview" navigate={() => {}} />)
  expect(screen.getByRole('button', {name: 'Home'})).toHaveAttribute('aria-current', 'page')
  expect(screen.queryByRole('list')).not.toBeInTheDocument()
})
