import {useEffect, useState} from 'react'
import {Dialog} from './Dialog'
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {ErrorFeedback, NotificationProvider, useNotifications, useErrorState} from './ToastRegion'

function Notify() {
  const {notify} = useNotifications()!
  return <><button onClick={() => notify('Could not save.', true)}>Fail</button><button onClick={() => notify('Saved.')}>Save</button></>
}
afterEach(() => vi.useRealTimers())
describe('notifications', () => {
  it('announces errors once, without a duplicate page banner', () => {
    render(<NotificationProvider><ErrorForm /></NotificationProvider>)
    fireEvent.click(screen.getByText('Submit'))
    expect(screen.getAllByText('Could not save.')).toHaveLength(1)
    expect(screen.getByRole('alert')).toHaveTextContent('Could not save.')
  })
  it('expires errors and pauses while a notification is being read', () => {
    vi.useFakeTimers()
    render(<NotificationProvider><Notify /></NotificationProvider>)
    fireEvent.click(screen.getByText('Fail'))
    const toast = screen.getByRole('alert').parentElement!
    act(() => vi.advanceTimersByTime(3000))
    fireEvent.mouseEnter(toast)
    act(() => vi.advanceTimersByTime(20000))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.mouseLeave(toast)
    act(() => vi.advanceTimersByTime(999))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    act(() => vi.advanceTimersByTime(1))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('keeps keyboard-focused notifications available and supports explicit dismissal', () => {
    vi.useFakeTimers()
    render(<NotificationProvider><Notify /></NotificationProvider>)
    fireEvent.click(screen.getByText('Fail'))
    fireEvent.focus(screen.getByRole('button', {name: 'Dismiss notification'}))
    act(() => vi.advanceTimersByTime(20000))
    expect(screen.getByRole('alert')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Dismiss notification'}))
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })
  it('deduplicates repeated errors and expires success feedback sooner', () => {
    vi.useFakeTimers()
    render(<NotificationProvider><Notify /></NotificationProvider>)
    fireEvent.click(screen.getByText('Fail')); fireEvent.click(screen.getByText('Fail')); fireEvent.click(screen.getByText('Save'))
    expect(screen.getAllByRole('alert')).toHaveLength(1)
    act(() => vi.advanceTimersByTime(3000))
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(screen.getByRole('alert')).toBeInTheDocument()
  })
})

it('keeps notifications clickable inside the active modal and clears errors when it closes', async () => {
  function Form() {
    const [open, setOpen] = useState(true)
    const [error, setError] = useErrorState()
    useEffect(() => setError('Could not save.'), [setError])
    return <Dialog open={open} title="Edit" onClose={() => setOpen(false)}><ErrorFeedback message={error} /><button onClick={() => setOpen(false)}>Cancel</button></Dialog>
  }
  render(<NotificationProvider><Form /></NotificationProvider>)
  await waitFor(() => expect(screen.getByRole('alert').closest('dialog')).toBe(screen.getByRole('dialog')))
  fireEvent.click(screen.getByText('Cancel'))
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument())
})


it('expires an untouched error after exactly four seconds', () => {
  vi.useFakeTimers()
  render(<NotificationProvider><Notify /></NotificationProvider>)
  fireEvent.click(screen.getByText('Fail'))
  act(() => vi.advanceTimersByTime(3999))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  act(() => vi.advanceTimersByTime(1))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it.each(['hashchange', 'popstate'])('clears notifications on %s', event => {
  render(<NotificationProvider><Notify /></NotificationProvider>)
  fireEvent.click(screen.getByText('Fail')); fireEvent.click(screen.getByText('Save'))
  act(() => window.dispatchEvent(new Event(event)))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(screen.queryByRole('status')).not.toBeInTheDocument()
})

it.each(['mouse', 'keyboard'])('clears notifications when selecting a different section by %s', input => {
  render(<NotificationProvider><Notify /><button role="tab">Application</button></NotificationProvider>)
  fireEvent.click(screen.getByText('Fail'))
  if (input === 'mouse') fireEvent.click(screen.getByRole('tab'))
  else fireEvent.keyDown(screen.getByRole('tab'), {key: 'ArrowRight'})
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('does not restart the timer when a modal opens', async () => {
  vi.useFakeTimers()
  function Form() {
    const [open, setOpen] = useState(false)
    return <><Notify /><button onClick={() => setOpen(true)}>Open</button><Dialog open={open} title="Edit" onClose={() => setOpen(false)}>Form</Dialog></>
  }
  render(<NotificationProvider><Form /></NotificationProvider>)
  fireEvent.click(screen.getByText('Fail'))
  act(() => vi.advanceTimersByTime(3000))
  await act(async () => {fireEvent.click(screen.getByText('Open'))})
  expect(screen.getByRole('alert').closest('dialog')).toBe(screen.getByRole('dialog'))
  act(() => vi.advanceTimersByTime(1000))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})


function ErrorForm({polling = false}: {polling?: boolean}) {
  const [error, setError] = useErrorState({repeat: !polling})
  return <><button onClick={() => setError('Could not save.')}>Submit</button><button onClick={() => setError('Another error.')}>Other error</button><ErrorFeedback message={error} /></>
}

it('notifies again for the same error after the previous toast expires', () => {
  vi.useFakeTimers()
  render(<NotificationProvider><ErrorForm /></NotificationProvider>)
  fireEvent.click(screen.getByText('Submit'))
  act(() => vi.advanceTimersByTime(4000))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  fireEvent.click(screen.getByText('Submit'))
  expect(screen.getByRole('alert')).toHaveTextContent('Could not save.')
})

it('notifies again after dismissal, and also reports a different error', () => {
  render(<NotificationProvider><ErrorForm /></NotificationProvider>)
  fireEvent.click(screen.getByText('Submit'))
  fireEvent.click(screen.getByRole('button', {name: 'Dismiss notification'}))
  fireEvent.click(screen.getByText('Submit'))
  expect(screen.getByRole('alert')).toHaveTextContent('Could not save.')
  fireEvent.click(screen.getByText('Other error'))
  expect(screen.getAllByRole('alert')).toHaveLength(2)
})

it('renews one visible toast for a repeated failed action', () => {
  vi.useFakeTimers()
  render(<NotificationProvider><ErrorForm /></NotificationProvider>)
  fireEvent.click(screen.getByText('Submit'))
  act(() => vi.advanceTimersByTime(3000))
  fireEvent.click(screen.getByText('Submit'))
  expect(screen.getAllByRole('alert')).toHaveLength(1)
  act(() => vi.advanceTimersByTime(3999))
  expect(screen.getByRole('alert')).toBeInTheDocument()
  act(() => vi.advanceTimersByTime(1))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('does not repeatedly notify unchanged background polling errors', () => {
  vi.useFakeTimers()
  render(<NotificationProvider><ErrorForm polling /></NotificationProvider>)
  fireEvent.click(screen.getByText('Submit'))
  act(() => vi.advanceTimersByTime(4000))
  fireEvent.click(screen.getByText('Submit'))
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

it('moves a success toast out of an unmounted modal before opening its native popover', async () => {
  const originalMatches = HTMLElement.prototype.matches
  const matches = vi.spyOn(HTMLElement.prototype, 'matches').mockImplementation(function (this: HTMLElement, selector: string) {
    return selector === ':popover-open' ? this.style.display === 'block' : originalMatches.call(this, selector)
  })
  Object.defineProperty(HTMLElement.prototype, 'showPopover', {configurable: true, value: function (this: HTMLElement) {
    if (!this.isConnected) throw new DOMException('Disconnected popover', 'InvalidStateError')
    this.style.display = 'block'
  }})
  Object.defineProperty(HTMLElement.prototype, 'hidePopover', {configurable: true, value: function (this: HTMLElement) {this.style.display = 'none'}})
  function Form() {
    const [open, setOpen] = useState(true)
    const {notify} = useNotifications()!
    return open ? <Dialog open title="Edit" onClose={() => setOpen(false)}><button onClick={() => {notify('Saved.'); setOpen(false)}}>Save</button></Dialog> : <p>Done</p>
  }
  const view = render(<NotificationProvider><Form /></NotificationProvider>)
  try {
    await waitFor(() => expect(document.querySelector('.toast-region')?.closest('dialog')).not.toBeNull())
    fireEvent.click(screen.getByText('Save'))
    await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('Saved.'))
    expect(screen.getByRole('status').closest('dialog')).toBeNull()
    expect(screen.getByText('Done')).toBeInTheDocument()
  } finally {
    view.unmount()
    matches.mockRestore()
    Reflect.deleteProperty(HTMLElement.prototype, 'showPopover')
    Reflect.deleteProperty(HTMLElement.prototype, 'hidePopover')
  }
})
