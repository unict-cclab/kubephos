import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Application} from '../types'
import {ApplicationDetailsPage} from './ApplicationDetailsDialog'

vi.mock('../api', () => ({request: vi.fn()}))

const application: Application = {
  id: 'dev.example.app', reference: 'app:dev.example.app', name: 'Example app', description: '', origin: 'built-in', digest: 'sha256:test',
  descriptor: {spec: {package: {type: 'git', format: 'plain-yaml'}, interface: {group: 'example', components: [{id: 'api', workload: {apiVersion: 'apps/v1', kind: 'Deployment', name: 'api'}, traits: ['scalable']}], loadScenarios: [{id: 'journey', engine: 'locust', script: 'load/locustfile.py', targetEndpoint: 'http'}]}, valuesSchema: {type: 'object', 'x-kubephos-dynamic-topology': true, properties: {serviceCount: {type: 'integer', title: 'Services', description: 'Number of generated services', minimum: 2, maximum: 100, default: 6}, topology: {type: 'string', title: 'Request topology', enum: ['chain', 'tree'], default: 'chain'}}}, defaults: {serviceCount: 6, topology: 'chain'}, materializerConfig: {serviceImage: 'example/image:1.0.0', internalValue: 'hidden'}}}
}

describe('application details', () => {
  it('loads and presents the validated deployment manifest on demand', async () => {
    vi.mocked(request).mockResolvedValue({content: 'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n', loadScenarios: []} as never)
    render(<ApplicationDetailsPage application={application} close={() => {}} canDelete={false} remove={async () => false} />)
    fireEvent.click(screen.getByText('Example deployment files'))
    expect(await screen.findByLabelText('Deployment manifest YAML')).toHaveTextContent('kind: Deployment')
    expect(request).toHaveBeenCalledWith('/catalog/applications/dev.example.app/manifest')
    expect(screen.getByRole('button', {name: 'Download'})).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Refresh'}))
    expect(request).toHaveBeenLastCalledWith('/catalog/applications/dev.example.app/manifest?refresh=true')
  })

  it('shows the materialized Locust file for every application type', async () => {
    vi.mocked(request).mockResolvedValue({content: 'kind: Deployment\n', loadScenarios: [{id: 'journey', engine: 'locust', targetEndpoint: 'http', runtimeImage: 'locustio/locust:2.42.6', script: 'from locust import HttpUser\n', scriptDigest: 'sha256:script'}]} as never)
    render(<ApplicationDetailsPage application={application} close={() => {}} canDelete={false} remove={async () => false} />)
    fireEvent.click(screen.getByText('Example Locust file'))
    expect(await screen.findByLabelText('Locust file for default')).toHaveTextContent('from locust import HttpUser')
    expect(screen.queryByText('Target: http')).not.toBeInTheDocument()
    expect(screen.queryByText('File: load/locustfile.py')).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Download'}).parentElement).toHaveClass('code-block-actions')
  })

  it('keeps dynamic application sections in tabs without loading previews eagerly', () => {
    vi.mocked(request).mockClear()
    render(<ApplicationDetailsPage application={application} close={() => {}} canDelete={false} remove={async () => false} />)
    expect(screen.queryByText('Configurable fields')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Files'})).not.toBeInTheDocument()
    expect(screen.getByRole('tab', {name:'General'})).toHaveAttribute('aria-selected','true')
    expect(screen.getByRole('tab', {name:'Example deployment files'})).toBeInTheDocument()
    expect(request).not.toHaveBeenCalled()
    expect(screen.getByText('Example Locust file')).toBeInTheDocument()
    expect(screen.getByText('Configurable')).toBeInTheDocument()
    expect(screen.queryByRole('img', {name: 'Application component topology'})).not.toBeInTheDocument()
  })
})

 it('separates fixed topology, services and files, preserving the selected file on navigation',async()=>{
   vi.mocked(request).mockClear()
   vi.mocked(request).mockResolvedValue({content:'kind: Deployment',loadScenarios:[]})
   const fixed={...application,descriptor:{...application.descriptor,spec:{...application.descriptor.spec,valuesSchema:{type:'object'}}}}
   render(<ApplicationDetailsPage application={fixed} close={()=>{}} canDelete={false} remove={async()=>false}/>)
   expect(screen.getAllByRole('tab').map(tab=>tab.textContent)).toEqual(['General','Topology','Services','Deployment files','Locust file'])
   expect(screen.queryByRole('table',{name:'Services'})).not.toBeInTheDocument()
   fireEvent.click(screen.getByRole('tab',{name:'Services'}))
   expect(within(screen.getByRole('table',{name:'Services'})).getAllByText('api')).toHaveLength(2)
   expect(screen.queryByRole('table',{name:'Application information'})).not.toBeInTheDocument()
   fireEvent.click(screen.getByRole('tab',{name:'Deployment files'}))
   expect(await screen.findByLabelText('Deployment manifest YAML')).toHaveTextContent('kind: Deployment')
   fireEvent.click(screen.getByRole('tab',{name:'General'}))
   fireEvent.click(screen.getByRole('tab',{name:'Deployment files'}))
   expect(request).toHaveBeenCalledTimes(1)
 })
