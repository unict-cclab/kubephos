import {act, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {CloneDialog} from './CloneDialog'
import {NotificationProvider} from './ToastRegion'

describe('clone configuration dialog', () => {
  it('uses a focused editable name and cancellation never submits', () => {
    const clone=vi.fn(), close=vi.fn()
    render(<CloneDialog sourceName="Suite" clone={clone} close={close} />)
    expect(screen.getByRole('dialog', {name:'Clone configuration'})).toBeInTheDocument()
    expect(screen.getByLabelText('Name')).toHaveValue('Suite copy')
    expect(screen.getByLabelText('Name')).toHaveFocus()
    fireEvent.click(screen.getByText('Cancel'))
    expect(close).toHaveBeenCalledOnce(); expect(clone).not.toHaveBeenCalled()
  })
  it('preserves the name after failure and allows retry with the same form', async () => {
    const clone=vi.fn().mockRejectedValueOnce(new Error('Name already exists.')).mockResolvedValueOnce(undefined),close=vi.fn()
    render(<NotificationProvider><CloneDialog sourceName="Baseline" clone={clone} close={close} /></NotificationProvider>)
    fireEvent.change(screen.getByLabelText('Name'), {target:{value:'  Alternative  '}})
    fireEvent.click(screen.getByText('Clone'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Name already exists.')
    expect(close).not.toHaveBeenCalled()
    expect(screen.getByLabelText('Name')).toHaveValue('  Alternative  ')
    expect(screen.getByLabelText('Name')).toHaveFocus()
    fireEvent.click(screen.getByText('Clone'))
    await waitFor(()=>expect(close).toHaveBeenCalledOnce())
    expect(clone).toHaveBeenLastCalledWith('Alternative')
  })
  it('prevents duplicate submissions and closing during a pending clone', async () => {
    let finish!:()=>void
    const clone=vi.fn(()=>new Promise<void>(resolve=>{finish=resolve}))
    render(<CloneDialog sourceName="Baseline" clone={clone} close={()=>{}} />)
    fireEvent.click(screen.getByText('Clone'))
    expect(screen.getByText('Cloning…')).toBeDisabled()
    expect(screen.getByText('Cancel')).toBeDisabled()
    expect(screen.queryByRole('button',{name:'Close'})).not.toBeInTheDocument()
    expect(clone).toHaveBeenCalledOnce()
    await act(async()=>finish())
  })
})
