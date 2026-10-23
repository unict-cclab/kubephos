import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {useDeleteConfirmation} from './ConfirmationDialog'

function Harness({remove}: {remove: () => void}) {
  const {confirmDelete, confirmationDialog} = useDeleteConfirmation()
  return <>{confirmationDialog}<button onClick={async () => {
    if (await confirmDelete('Delete cluster alpha and its dedicated VMs?')) remove()
  }}>Delete alpha</button></>
}

describe('deletion confirmation', () => {
  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
    HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
  })

  it('waits for explicit confirmation and executes only once', async () => {
    const remove = vi.fn()
    render(<Harness remove={remove} />)
    fireEvent.click(screen.getByRole('button', {name: 'Delete alpha'}))
    const dialog = screen.getByRole('dialog', {name: 'Confirm deletion'})
    expect(dialog).toHaveAccessibleDescription('Delete cluster alpha and its dedicated VMs?')
    expect(remove).not.toHaveBeenCalled()
    fireEvent.click(within(dialog).getByRole('button', {name: 'Delete'}))
    await waitFor(() => expect(remove).toHaveBeenCalledTimes(1))
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it.each(['Cancel', 'Close', 'Escape'])('cancels through %s without deleting', async action => {
    const remove = vi.fn()
    render(<Harness remove={remove} />)
    fireEvent.click(screen.getByRole('button', {name: 'Delete alpha'}))
    const dialog = screen.getByRole('dialog')
    if (action === 'Escape') fireEvent(dialog, new Event('cancel', {bubbles: true, cancelable: true}))
    else fireEvent.click(within(dialog).getByRole('button', {name: action}))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(remove).not.toHaveBeenCalled()
  })
})
