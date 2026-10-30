import {render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {ResourceConnectivity, ResourceStatus} from './ResourceConnectivity'
import {resourceStatus} from '../connectivity'
import type {Connectivity} from '../types'

const checked = (): Connectivity => ({status: 'reachable', checkedAt: new Date().toISOString(), targets: [{name: 'west', status: 'reachable'}]})

describe('resource connectivity', () => {
  it('shows failed access and automatic recovery without replacing provisioning state', () => {
    const resource = {status: 'ready', connectivity: checked()}
    const {rerender} = render(<ResourceStatus resource={resource} />)
    expect(screen.getByText('Ready')).toBeInTheDocument()
    rerender(<ResourceStatus resource={{...resource, connectivity: {...checked(), status: 'unreachable'}}} />)
    expect(screen.getByText('Not reachable')).toBeInTheDocument()
    expect(resource.status).toBe('ready')
    rerender(<ResourceStatus resource={resource} />)
    expect(screen.getByText('Ready')).toBeInTheDocument()
    rerender(<ResourceStatus resource={{...resource, status: 'provisioning', connectivity: {...checked(), status: 'unreachable'}}} />)
    expect(screen.getByText('Creating')).toBeInTheDocument()
  })
  it('does not present stale or unchecked connections as reachable', () => {
    expect(resourceStatus({connectivity: undefined})).toBe('unknown')
    expect(resourceStatus({status: 'ready', connectivity: undefined})).toBe('unknown')
    expect(resourceStatus({connectivity: {...checked(), checkedAt: new Date(Date.now() - 240_000).toISOString()}})).toBe('unknown')
    expect(resourceStatus({connectivity: checked()})).toBe('reachable')
  })
  it('identifies the unavailable multi cluster member and preserves healthy members', () => {
    render(<ResourceConnectivity value={{...checked(), status: 'unreachable', targets: [{name: 'east', status: 'reachable'}, {name: 'west', status: 'unreachable', message: 'Could not connect to Kubernetes.'}]}} />)
    const table = screen.getByRole('table', {name: 'Cluster connectivity'})
    expect(within(table).getByRole('row', {name: 'east Reachable'})).toBeInTheDocument()
    expect(within(table).getByRole('row', {name: /west Not reachable/})).toBeInTheDocument()
    expect(screen.getByText('Last checked')).toBeInTheDocument()
  })
})
