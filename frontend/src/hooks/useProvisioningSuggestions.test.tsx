import {act, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {useProvisioningSuggestions} from './useProvisioningSuggestions'

afterEach(() => vi.unstubAllGlobals())
function Form({connection = 'pve', count = 1}: {connection?: string; count?: number}) {
  const allocation = useProvisioningSuggestions(true, connection, 'template', count)
  return <><input aria-label="VMID" {...allocation.field('vmid')} /><input aria-label="IP" {...allocation.field('address')} /><input aria-label="Gateway" {...allocation.field('gateway')} /></>
}
const response = (vmid: number, address: string) => new Response(JSON.stringify({vmid, address, gateway: '10.0.0.1'}), {status: 200})
describe('provisioning suggestions', () => {
  it('fills untouched values and preserves edits made before the inventory arrives', async () => {
    let resolve!: (value: Response) => void
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(done => {resolve = done})))
    render(<Form />)
    fireEvent.change(screen.getByLabelText('VMID'), {target: {value: '450'}})
    await act(async () => resolve(response(300, '10.0.0.30')))
    expect(screen.getByLabelText('VMID')).toHaveValue('450')
    expect(screen.getByLabelText('IP')).toHaveValue('10.0.0.30')
    fireEvent.change(screen.getByLabelText('IP'), {target: {value: '10.0.0.50'}})
    expect(screen.getByLabelText('IP')).toHaveValue('10.0.0.50')
  })
  it('discards stale responses when the selected connection changes', async () => {
    const resolves: Array<(value: Response) => void> = []
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(done => resolves.push(done))))
    const view = render(<Form />)
    view.rerender(<Form connection="pve-2" />)
    await act(async () => resolves[1](response(500, '10.0.0.50')))
    await act(async () => resolves[0](response(200, '10.0.0.20')))
    expect(screen.getByLabelText('VMID')).toHaveValue('500')
    expect(screen.getByLabelText('IP')).toHaveValue('10.0.0.50')
  })
  it('requests a new block when the node count changes without overwriting manual values', async () => {
    const fetch = vi.fn().mockImplementation(() => Promise.resolve(response(300, '10.0.0.30')))
    vi.stubGlobal('fetch', fetch)
    const view = render(<Form />)
    await waitFor(() => expect(screen.getByLabelText('VMID')).toHaveValue('300'))
    fireEvent.change(screen.getByLabelText('IP'), {target: {value: '10.0.0.99'}})
    view.rerender(<Form count={9} />)
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(fetch.mock.calls[1][0]).toContain('count=9')
    expect(screen.getByLabelText('IP')).toHaveValue('10.0.0.99')
  })
})
