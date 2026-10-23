import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {ResourceAction} from './ResourceAction'

describe('resource list actions', () => {
  it('uses accessible icon-only view and delete buttons', () => {
    const view = vi.fn()
    const remove = vi.fn()
    render(<><ResourceAction action="view" label="View cluster details" onClick={view} /><ResourceAction action="delete" label="Delete cluster" onClick={remove} /></>)
    const viewButton = screen.getByRole('button', {name: 'View cluster details'})
    const deleteButton = screen.getByRole('button', {name: 'Delete cluster'})
    expect(viewButton).toHaveClass('action-view')
    expect(viewButton).not.toHaveClass('view')
    expect(viewButton.textContent).toBe('')
    expect(deleteButton.textContent).toBe('')
    fireEvent.click(viewButton)
    fireEvent.click(deleteButton)
    expect(view).toHaveBeenCalledOnce()
    expect(remove).toHaveBeenCalledOnce()
  })
})

it('keeps creation labels visible and respects disabled actions', () => {
  const create = vi.fn()
  render(<ResourceAction action="create" label="Create user" disabled onClick={create} />)
  const button = screen.getByRole('button', {name: 'Create user'})
  expect(button).toHaveTextContent('Create user')
  expect(button).toBeDisabled()
  fireEvent.click(button)
  expect(create).not.toHaveBeenCalled()
})

it('keeps add actions discoverable with a visible label across editors', () => {
  render(<ResourceAction action="add" label="Add workload phase" onClick={() => {}} />)
  const button = screen.getByRole('button', {name: 'Add workload phase'})
  expect(button).toHaveTextContent('Add workload phase')
  expect(button).not.toHaveClass('icon-button')
})
