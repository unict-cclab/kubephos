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

  it.each(['harbor','nfs'])('offers the SSH key of the machine a %s runs on, like a standalone VM',kind=>{
    const admin={authenticated:true,user:{role:'admin'}} as Session
    const template={id:'template',kind:'machine-template',spec:{sshUser:'ubuntu'}} as unknown as ManagedResource
    const service={id:`${kind}_01`,name:`${kind}-01`,kind,status:'ready',spec:{templateId:'template',address:'192.0.2.30'}} as unknown as ManagedResource
    const {rerender}=render(<ResourceDetailsPage {...props} resources={[template]} resource={service} session={admin}/>)
    fireEvent.click(screen.getByRole('button',{name:'Access'}))
    expect(screen.getByRole('link',{name:'Download SSH key'})).toHaveAttribute('href',`/api/v1/infrastructure-services/${kind}_01/ssh-key`)
    expect(screen.getByText('ubuntu')).toBeInTheDocument()
    expect(screen.getByText('192.0.2.30')).toBeInTheDocument()
    // Administrators only, once provisioned.
    rerender(<ResourceDetailsPage {...props} resources={[template]} resource={service} session={{authenticated:true,user:{role:'user'}} as Session}/>)
    expect(screen.queryByRole('link',{name:'Download SSH key'})).not.toBeInTheDocument()
    rerender(<ResourceDetailsPage {...props} resources={[template]} resource={{...service,status:'provisioning'}} session={admin}/>)
    expect(screen.queryByRole('link',{name:'Download SSH key'})).not.toBeInTheDocument()
  })
})
