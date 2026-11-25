import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import type {ManagedResource} from '../types'
import {ClusterDashboards} from './ClusterDashboards'
import {request} from '../api'

vi.mock('../api', () => ({request: vi.fn()}))

const catalog = {defaults: {}, items: [
  {id: 'prometheus', dashboards: [{name: 'Grafana', port: 32000}, {name: 'Prometheus', port: 32090}]},
  {id: 'cluster-lens', dashboards: [{name: 'Cluster Lens', port: 32088}]},
  {id: 'kiali', dashboards: [{name: 'Kiali', port: 30001, path: '/kiali'}]},
  {id: 'jaeger', dashboards: [{name: 'Jaeger', port: 30002}]},
  {id: 'headlamp', dashboards: [{name: 'Headlamp', port: 30003}]},
]}
// The catalog request is answered here; every other request goes to `api`.
function mockApi(api: () => Promise<unknown> = () => Promise.resolve({})) {
  vi.mocked(request).mockImplementation((path => path === '/cluster-components' ? Promise.resolve(catalog) : api()) as typeof request)
}
const spec = {name: 'east', managedProfile: 'managed', addressStart: '192.0.2.10', networking: 'cilium', components: ['prometheus', 'cluster-lens', 'headlamp']}
const single = {...spec, networking: 'flannel', components: ['prometheus', 'cluster-lens', 'kiali', 'headlamp']}
const resource = {kind: 'kubernetes-cluster', name: 'Mesh', status: 'ready', spec: {mode: 'multi', members: [spec, {...spec, name: 'west', addressStart: '192.0.2.20'}]}} as unknown as ManagedResource

describe('cluster dashboards', () => {
  it('provides one mesh entry while retaining the choice of workload source', async () => {
    mockApi()
    render(<ClusterDashboards resource={resource} />)
    expect(await screen.findAllByRole('link', {name: /Cluster Lens/})).toHaveLength(1)
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

  it('shows the dashboards of the installed components on a single cluster', async () => {
    mockApi()
    const {rerender} = render(<ClusterDashboards resource={{...resource, spec: single}} />)
    expect(await screen.findByRole('link', {name: /Cluster Lens/})).toHaveAttribute('href', 'http://192.0.2.10:32088')
    expect(screen.getByRole('link', {name: /Kiali/})).toHaveAttribute('href', 'http://192.0.2.10:30001/kiali')
    expect(screen.queryByRole('button', {name: /Live experiment/})).not.toBeInTheDocument()
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', {name: /Hubble|Jaeger/})).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, status: 'failed'}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, spec: {mode: 'multi', members: [null, {...spec, addressStart: ''}]}}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })

})
