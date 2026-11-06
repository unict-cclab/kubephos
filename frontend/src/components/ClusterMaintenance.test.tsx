import {request} from '../api'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import {ClusterMaintenance} from './ClusterMaintenance'
import type {ManagedResource, Session, PipelineRun} from '../types'
beforeAll(() => {HTMLDialogElement.prototype.showModal = function() {this.setAttribute('open', '')}; HTMLDialogElement.prototype.close = function() {this.removeAttribute('open')}})
vi.mock('../api', () => ({request: vi.fn()}))
const resource = {updatedAt:'2026-11-05T10:00:00Z',id: 'cluster', name: 'k3s', kind: 'kubernetes-cluster', status: 'ready', spec: {name: 'k3s', controlPlanes: 1, managementPool: {name: 'management', count: 1}, applicationPools: [{name: 'edge', count: 2, zones: ['zone-a'], capacity: {cores: 2, memoryMiB: 4096, diskGiB: 80}}], machineNodes: {'k3s-control-plane-1': 'pve', 'k3s-management-1': 'pve', 'k3s-edge-1': 'pve', 'k3s-edge-2': 'pve'}}} as unknown as ManagedResource
const session = {user: {role: 'admin'}} as Session
describe('Cluster maintenance', () => {
  it('keeps protected pool sizes fixed while allowing migration and application scaling', () => {
    render(<ClusterMaintenance resource={resource} runs={[]} session={session} />)
    fireEvent.click(screen.getByRole('button', {name: 'Edit'}))
    fireEvent.click(screen.getByRole('button',{name:'Node pools'}))
    fireEvent.click(screen.getByRole('tab',{name:'Pools'}))
    expect(screen.queryByLabelText('management nodes')).toBeNull()
    expect(screen.queryByLabelText('control-plane nodes')).toBeNull()
    expect(screen.getByLabelText('k3s-control-plane-1 Proxmox server')).toBeEnabled()
    fireEvent.change(screen.getByLabelText('edge nodes'), {target: {value: '3'}})
    expect(screen.getByLabelText('k3s-edge-3 Proxmox server')).toBeInTheDocument()
    expect(screen.getByLabelText('First new VMID')).toBeRequired()
    fireEvent.click(screen.getByRole('button', {name: 'Remove pool edge'}))
    expect(screen.getByText(/Confirm removal of these nodes/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button',{name:'Review'}))
    expect(screen.getByRole('button', {name: 'Save changes'})).toBeDisabled()
  })
  it('blocks maintenance while an experiment is queued', () => {
    render(<ClusterMaintenance resource={resource} runs={[{id: 'run', clusterResourceId: 'cluster', status: 'queued'} as PipelineRun]} session={session} />)
    fireEvent.click(screen.getByRole('button',{name:'Edit'}))
 fireEvent.click(screen.getByRole('button',{name:'Node pools'}))
 fireEvent.click(screen.getByRole('tab',{name:'Pools'}))
    expect(screen.getByLabelText('edge nodes')).toBeDisabled()
    expect(screen.getByLabelText('k3s-edge-1 Proxmox server')).toBeDisabled()
    expect(screen.getByRole('button',{name:'Add pool'})).toBeDisabled()
  })
})

it('edits an existing label while an experiment runs using only pool settings',async()=>{
 const current={...resource,spec:{...resource.spec,applicationPools:[{...(resource.spec.applicationPools as Record<string,unknown>[])[0],labels:{hardware:'cpu'}}]}}
 vi.mocked(request).mockResolvedValue({...current,updatedAt:'2026-11-05T10:01:00Z'})
 render(<ClusterMaintenance resource={current} runs={[{clusterResourceId:'cluster',status:'running'} as PipelineRun]} session={session}/>)
 fireEvent.click(screen.getByRole('button',{name:'Edit'}))
 fireEvent.click(screen.getByRole('button',{name:'Node pools'}))
 fireEvent.click(screen.getByRole('tab',{name:'Pools'}))
 expect(screen.getByLabelText('edge nodes')).toBeDisabled()
 fireEvent.change(screen.getByLabelText('Label value 1'),{target:{value:'gpu'}})
 fireEvent.click(screen.getByRole('button',{name:'Review'}))
 fireEvent.click(screen.getByRole('button',{name:'Save changes'}))
 await waitFor(()=>expect(request).toHaveBeenCalled())
 const options=vi.mocked(request).mock.calls.at(-1)![1]!
 const body=JSON.parse(String(options.body))
 expect(body).toEqual({updatedAt:current.updatedAt,poolSettings:[{pool:'edge',labels:{hardware:'gpu'},taints:[]}]})
 expect(body.applicationPools).toBeUndefined()
})

it('keeps protected-pool labels and taints inside the selected pool without duplicate disclosure panels',()=>{
 render(<ClusterMaintenance resource={resource} runs={[]} session={session}/>)
 fireEvent.click(screen.getByRole('button',{name:'Edit'}))
 fireEvent.click(screen.getByRole('button',{name:'Node pools'}))
 expect(screen.getByRole('tab',{name:'Master'})).toHaveAttribute('aria-selected','true')
 fireEvent.click(screen.getByRole('tab',{name:'Labels'}))
 expect(screen.getByRole('table',{name:'Default labels'})).toHaveTextContent('control-plane')
 expect(screen.queryByRole('button',{name:'Labels and taints'})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('tab',{name:'Management'}))
 fireEvent.click(screen.getByRole('tab',{name:'Labels'}))
 expect(screen.getAllByRole('table',{name:'Default labels'})).toHaveLength(1)
 expect(screen.getByRole('table',{name:'Default labels'})).toHaveTextContent('management')
 fireEvent.click(screen.getByRole('tab',{name:'Taints'}))
 expect(screen.queryByRole('table',{name:'Default taints'})).not.toBeInTheDocument()
 expect(within(screen.getByRole('tabpanel',{name:'Taints'})).getByText('None')).toBeVisible()
})

it('reveals an invalid label in its wizard step and nested tabs before submitting',()=>{
 const current={...resource,spec:{...resource.spec,applicationPools:[{...(resource.spec.applicationPools as Record<string,unknown>[])[0],labels:{hardware:'cpu'}}]}}
 render(<ClusterMaintenance resource={current} runs={[]} session={session}/>)
 fireEvent.click(screen.getByRole('button',{name:'Edit'}))
 fireEvent.click(screen.getByRole('button',{name:'Node pools'}))
 fireEvent.click(screen.getByRole('tab',{name:'Pools'}))
 fireEvent.change(screen.getByLabelText('Label key 1'),{target:{value:'kubephos.dev/role'}})
 fireEvent.click(screen.getByRole('button',{name:'Review'}))
 fireEvent.click(screen.getByRole('button',{name:'Save changes'}))
 expect(screen.getByRole('tab',{name:'Pools'})).toHaveAttribute('aria-selected','true')
 expect(screen.getByRole('textbox',{name:'Label key 1'})).toBeVisible()
 expect((screen.getByLabelText('Label key 1') as HTMLInputElement).validity.valid).toBe(false)
})
