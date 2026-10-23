import {fireEvent, render, screen} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import {ClusterMaintenance} from './ClusterMaintenance'
import type {ManagedResource, Session, PipelineRun} from '../types'
beforeAll(() => {HTMLDialogElement.prototype.showModal = function() {this.setAttribute('open', '')}; HTMLDialogElement.prototype.close = function() {this.removeAttribute('open')}})
vi.mock('../api', () => ({request: vi.fn()}))
const resource = {id: 'cluster', name: 'k3s', kind: 'kubernetes-cluster', status: 'ready', spec: {name: 'k3s', controlPlanes: 1, managementPool: {name: 'management', count: 1}, applicationPools: [{name: 'edge', count: 2, zones: ['zone-a'], capacity: {cores: 2, memoryMiB: 4096, diskGiB: 80}}], machineNodes: {'k3s-control-plane-1': 'pve', 'k3s-management-1': 'pve', 'k3s-edge-1': 'pve', 'k3s-edge-2': 'pve'}}} as unknown as ManagedResource
const session = {user: {role: 'admin'}} as Session
describe('Cluster maintenance', () => {
  it('keeps protected pool sizes fixed while allowing migration and application scaling', () => {
    render(<ClusterMaintenance resource={resource} runs={[]} session={session} />)
    fireEvent.click(screen.getByRole('button', {name: 'Manage nodes'}))
    expect(screen.queryByLabelText('management nodes')).toBeNull()
    expect(screen.queryByLabelText('control-plane nodes')).toBeNull()
    expect(screen.getByLabelText('k3s-control-plane-1 Proxmox server')).toBeEnabled()
    fireEvent.change(screen.getByLabelText('edge nodes'), {target: {value: '3'}})
    expect(screen.getByLabelText('k3s-edge-3 Proxmox server')).toBeInTheDocument()
    expect(screen.getByLabelText('First new VMID')).toBeRequired()
    fireEvent.click(screen.getByRole('button', {name: 'Remove pool edge'}))
    expect(screen.getByText(/Confirm removal of these nodes/)).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Apply maintenance'})).toBeDisabled()
  })
  it('blocks maintenance while an experiment is queued', () => {
    render(<ClusterMaintenance resource={resource} runs={[{id: 'run', clusterResourceId: 'cluster', status: 'queued'} as PipelineRun]} session={session} />)
    expect(screen.getByRole('button', {name: 'Manage nodes'})).toBeDisabled()
  })
})
