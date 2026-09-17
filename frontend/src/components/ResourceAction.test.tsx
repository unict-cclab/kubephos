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
