import {fireEvent,render,screen,waitFor} from '@testing-library/react'
import {beforeAll,beforeEach,expect,it,vi} from 'vitest'
import {ApplicationDeploymentsView} from './ApplicationDeploymentsView'
import {request} from '../api'
import type {ManagedResource} from '../types'
vi.mock('../api',()=>({request:vi.fn()}))
beforeAll(()=>{HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','')};HTMLDialogElement.prototype.close=function(){this.removeAttribute('open')}})
beforeEach(()=>{window.location.hash='#deployments';vi.mocked(request).mockReset()})
const cluster={id:'cluster',name:'Mesh',kind:'kubernetes-cluster',provider:'proxmox',validation:{valid:true,issues:[]},createdAt:'2026-11-04T10:00:00Z',updatedAt:'2026-11-04T10:00:00Z',status:'ready',workspaceId:'ws',spec:{mode:'multi',members:[{name:'one'},{name:'two'},{name:'three'}]}} as ManagedResource
const deployment={id:'deployment',name:'Independent',status:'ready',createdBy:'alice',createdAt:'2026-11-04T10:00:00Z',spec:{clusterResourceId:'cluster',applicationRef:'app:test',ownerUserId:'alice',definition:{namespace:'my-app',applicationValues:{},components:[]}}} as unknown as ManagedResource
const props={deployments:[deployment],kubernetesClusters:[cluster],workspaces:[],applications:[],plugins:[],strategies:[],session:{authenticated:true,csrfToken:'csrf',user:{id:'alice',username:'alice',role:'user' as const}},isAdmin:false,changed:vi.fn(async()=>{}),openOperation:()=>{},pipelineRuns:[]}
it('shows one deployment with its cluster and namespace and all members in its detail',()=>{
 render(<ApplicationDeploymentsView {...props}/>)
 expect(screen.getByRole('cell',{name:'my-app'})).toBeInTheDocument()
 expect(screen.getByRole('cell',{name:'Mesh'})).toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'View Independent details'}))
 expect(screen.getByRole('heading',{name:'Independent'})).toBeInTheDocument()
 const table=screen.getByRole('table',{name:'Cluster members'})
 expect(table).toHaveTextContent('one');expect(table).toHaveTextContent('two');expect(table).toHaveTextContent('three')
 expect(screen.queryByText('Start mode')).not.toBeInTheDocument()
 expect(screen.getByText('Created by')).toBeInTheDocument()
})
it('confirms namespace removal before queuing ownership-checked cleanup',async()=>{
 vi.mocked(request).mockResolvedValue({} as never)
 render(<ApplicationDeploymentsView {...props}/>)
 fireEvent.click(screen.getByRole('button',{name:'Delete Independent'}))
 expect(screen.getByText(/Delete deployment Independent and its namespace my-app on all cluster members/)).toBeInTheDocument()
 expect(request).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('button',{name:'Delete'}))
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/application-deployments/deployment',{method:'DELETE'},'csrf'))
})
it('prevents removal while provisioning',()=>{
 render(<ApplicationDeploymentsView {...props} deployments={[{...deployment,status:'provisioning'}]}/>)
 expect(screen.getByRole('button',{name:'Delete Independent'})).toBeDisabled()
 expect(screen.getByRole('button',{name:'Edit placement for Independent'})).toBeEnabled()
 expect(screen.queryByRole('button',{name:'Retry'})).not.toBeInTheDocument()
})
it('retries a failed deployment without creating another resource or namespace',async()=>{
 vi.mocked(request).mockResolvedValue({} as never)
 render(<ApplicationDeploymentsView {...props} deployments={[{...deployment,status:'failed'}]}/>)
 fireEvent.click(screen.getByRole('button',{name:'Retry'}))
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/application-deployments/deployment/retry',{method:'POST'},'csrf'))
 expect(screen.getByRole('cell',{name:'my-app'})).toBeInTheDocument()
})
it('edits placement during provisioning using the deployed package defaults',async()=>{
 const timestamp='2026-11-06T15:00:00Z'
 const item={...deployment,status:'provisioning',updatedAt:timestamp,spec:{...deployment.spec,definition:{namespace:'my-app',applicationPlacement:{nodeSelector:{'kubephos.dev/pool':'wrong'}}}}} as ManagedResource
 vi.mocked(request).mockImplementation(async path=>path.endsWith('/placement-defaults')?{workloadTargets:[{id:'api'}],placementDefaults:[{id:'api',nodeSelector:{'kubephos.dev/role':'application'}}]}:item)
 render(<ApplicationDeploymentsView {...props} deployments={[item]}/>)
 fireEvent.click(screen.getByRole('button',{name:'Edit placement for Independent'}))
 await waitFor(()=>expect(screen.getByRole('textbox',{name:'Node selector value 1'})).toHaveValue('wrong'))
 expect(screen.getByRole('textbox',{name:'Node selector key 1'})).toHaveAttribute('placeholder','kubephos.dev/pool')
 fireEvent.change(screen.getByRole('textbox',{name:'Node selector value 1'}),{target:{value:'edge'}})
 fireEvent.click(screen.getByRole('button',{name:'Save'}))
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/application-deployments/deployment/placement',{method:'PUT',body:JSON.stringify({updatedAt:timestamp,applicationPlacement:{nodeSelector:{'kubephos.dev/pool':'edge'},serviceOverrides:{}}})},'csrf'))
})
