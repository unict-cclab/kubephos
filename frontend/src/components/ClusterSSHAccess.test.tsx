import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {ManagedResource, Session} from '../types'
import {ResourceDetailsPage} from './ResourceDetailsDialog'

const single = {id:'cluster_ssh', name:'test',kind:'kubernetes-cluster',status:'ready',spec:{sshUser:'ubuntu'}} as unknown as ManagedResource
const props = {close:()=>{},connections:[],workspaces:[],resources:[],runs:[],openOperation:()=>{}}
describe('machine SSH access',()=>{
  it('offers one key for a single cluster and one for each multi cluster member',()=>{
    const session={authenticated:true,user:{role:'admin'}} as Session
    const {rerender}=render(<ResourceDetailsPage {...props} resource={single} session={session}/> )
    fireEvent.click(screen.getByRole('button',{name:'Access'}))
    expect(screen.getByRole('link',{name:'Download SSH key'})).toHaveAttribute('href','/api/v1/kubernetes-clusters/cluster_ssh/ssh-key')
    expect(screen.queryByRole('table',{name:'Machine SSH access'})).not.toBeInTheDocument()
    expect(screen.queryByText('ubuntu')).not.toBeInTheDocument()
    const multi={...single,spec:{mode:'multi',members:[{name:'east',sshUser:'ubuntu'},{name:'west',sshUser:'debian'}]}}
    rerender(<ResourceDetailsPage {...props} resource={multi} session={session}/>)
    expect(screen.getAllByRole('link',{name:/Download SSH key/})).toHaveLength(2)
    expect(screen.getByRole('link',{name:'Download SSH key for west'})).toHaveAttribute('href','/api/v1/kubernetes-clusters/cluster_ssh/ssh-key?cluster=west')
    expect(screen.getByText('debian')).toBeInTheDocument()
    rerender(<ResourceDetailsPage {...props} resource={{...multi,spec:{...multi.spec,sharedSSHKey:true,templateId:'template_ssh'}}} resources={[{id:'template_ssh',spec:{sshUser:'ubuntu'}} as unknown as ManagedResource]} session={session}/>)
    expect(screen.getAllByRole('link',{name:/Download SSH key/})).toHaveLength(1)
    expect(screen.getByRole('link',{name:'Download SSH key'})).toHaveAttribute('href','/api/v1/kubernetes-clusters/cluster_ssh/ssh-key')
    expect(screen.queryByRole('table',{name:'Machine SSH access'})).not.toBeInTheDocument()
    expect(screen.queryByText('ubuntu')).not.toBeInTheDocument()
    rerender(<ResourceDetailsPage {...props} resource={multi} session={{authenticated:true,user:{role:'user'}} as Session}/>)
    expect(screen.queryByRole('link',{name:/Download SSH key/})).not.toBeInTheDocument()
    expect(screen.queryByRole('link',{name:'Download kubeconfig'})).not.toBeInTheDocument()
  })
})
