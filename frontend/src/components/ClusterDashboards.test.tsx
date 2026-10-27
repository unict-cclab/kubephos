import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {ManagedResource} from '../types'
import {ClusterDashboards} from './ClusterDashboards'

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
    }
  })

  it('keeps single cluster access unchanged and hides unavailable dashboards', () => {
    const {rerender} = render(<ClusterDashboards resource={{...resource, spec: {...spec, networking: 'istio'}}} />)
    expect(screen.getByRole('link', {name: /Cluster Lens/})).toHaveAttribute('href', 'http://192.0.2.10:32088')
    expect(screen.getByRole('link', {name: /Kiali/})).toHaveAttribute('href', 'http://192.0.2.10:30001/kiali')
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
    expect(screen.queryByRole('link', {name: /Hubble/})).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, status: 'failed'}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
    rerender(<ClusterDashboards resource={{...resource, spec: {mode: 'multi', members: [null, {...spec, addressStart: ''}]}}} />)
    expect(screen.queryByRole('link')).not.toBeInTheDocument()
  })
})
