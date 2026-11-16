import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import type {ManagedResource} from '../types'
import {ClusterDashboards} from './ClusterDashboards'
import {request} from '../api'

vi.mock('../api', () => ({request: vi.fn()}))

const spec = {name: 'east', managedProfile: 'managed', addressStart: '192.0.2.10', networking: 'cilium'}
const resource = {kind: 'kubernetes-cluster', name: 'Mesh', status: 'ready', spec: {mode: 'multi', members: [spec, {...spec, name: 'west', addressStart: '192.0.2.20'}]}} as unknown as ManagedResource

describe('cluster dashboards', () => {
  it('provides one mesh entry while retaining the choice of workload source', () => {
    render(<ClusterDashboards resource={resource} />)
    expect(screen.getAllByRole('link', {name: /Cluster Lens/})).toHaveLength(1)
    expect(screen.getByText('Network · All clusters')).toBeInTheDocument()
    expect(screen.getByRole('link', {name: 'Open Cluster Lens'})).toHaveAttribute('href', 'http://192.0.2.10:32088')
    fireEvent.change(screen.getByRole('combobox', {name: 'Workload cluster'}), {target: {value: 'west'}})
    expect(screen.getByRole('link', {name: 'Open Cluster Lens'})).toHaveAttribute('href', 'http://192.0.2.20:32088')
    for (const [name, address] of [['east', '192.0.2.10'], ['west', '192.0.2.20']]) {
      const group = within(screen.getByRole('region', {name: `${name} dashboards`}))
      expect(group.getByRole('link', {name: /Grafana/})).toHaveAttribute('href', `http://${address}:32000`)
      expect(group.getByRole('link', {name: /Hubble/})).toBeInTheDocument()
      expect(group.queryByRole('link', {name: /Kiali|Cluster Lens/})).not.toBeInTheDocument()
      expect(group.queryByRole('button', {name: /Live experiment/})).not.toBeInTheDocument()
    }
  })

  it('keeps single cluster access unchanged and hides unavailable dashboards', () => {
    const {rerender} = render(<ClusterDashboards resource={{...resource, spec: {...spec, networking: 'istio'}}} />)
    expect(screen.getByRole('link', {name: /Cluster Lens/})).toHaveAttribute('href', 'http://192.0.2.10:32088')
    expect(screen.getByRole('link', {name: /Kiali/})).toHaveAttribute('href', 'http://192.0.2.10:30001/kiali')
    expect(screen.getByRole('button', {name: /Live experiment/})).toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', {name: /Hubble/})).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, status: 'failed'}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, spec: {mode: 'multi', members: [null, {...spec, addressStart: ''}]}}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

  it('prepares the live dashboard on the server before opening it', async () => {
    vi.mocked(request).mockResolvedValue({url: 'http://192.0.2.10:32000/d/kubephos-experiment?refresh=10s'} as never)
    const tab = {opener: {}, location: {href: ''}, close: vi.fn()}
    const open = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
    render(<ClusterDashboards resource={{...resource, id: 'cluster-1', spec: {...spec, networking: 'istio'}}} session={{authenticated: true, csrfToken: 'token'}} />)
    fireEvent.click(screen.getByRole('button', {name: /Live experiment/}))
    await waitFor(() => expect(tab.location.href).toBe('http://192.0.2.10:32000/d/kubephos-experiment?refresh=10s'))
    expect(request).toHaveBeenCalledWith('/kubernetes-clusters/cluster-1/live-dashboard', {method: 'POST', body: '{}'}, 'token')
    expect(tab.opener).toBeNull()
    open.mockRestore()
  })

  it('closes the pending tab and explains why when Grafana is unavailable', async () => {
    vi.mocked(request).mockRejectedValue(new Error('Managed Grafana details are unavailable for this cluster.'))
    const tab = {opener: {}, location: {href: ''}, close: vi.fn()}
    const open = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
    render(<ClusterDashboards resource={{...resource, id: 'cluster-1', spec: {...spec, networking: 'istio'}}} />)
    fireEvent.click(screen.getByRole('button', {name: /Live experiment/}))
    expect(await screen.findByRole('alert')).toHaveTextContent('Managed Grafana details are unavailable')
    expect(tab.close).toHaveBeenCalled()
    open.mockRestore()
  })
})
