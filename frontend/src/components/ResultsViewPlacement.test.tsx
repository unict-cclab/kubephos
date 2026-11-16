import {fireEvent,render,screen,waitFor,cleanup} from '@testing-library/react'
import {afterEach,expect,it,vi} from 'vitest'
import {ResultsView} from './ResultsView'
import {request} from '../api'
import type {Artifact,Experiment,TimeSeriesDataset} from '../types'
vi.mock('../api',()=>({request:vi.fn()}))
afterEach(cleanup)
it('keeps replay execution-specific while exposing the aggregate heatmap under All executions',async()=>{
 const artifacts=['one','two'].map(id=>({id,operationId:'op',workspaceId:'ws',name:id,type:'TimeSeriesDataset',version:'v1alpha1',sensitive:false,verifiedAt:'2026-11-15T10:00:00Z'})) as Artifact[]
 const data=(id:string)=>({apiVersion:'artifacts.kubephos.dev/v1alpha1',kind:'TimeSeriesDataset',metadata:{name:id,version:'v1alpha1'},spec:{applicationRef:'app:test',namespace:'app',clusterServer:'https://cluster',start:'2026-11-15T09:59:45Z',end:'2026-11-15T10:00:00Z',stepSeconds:15,source:{kind:'prometheus-v1',version:'1',profile:'test'},summary:{metrics:1,series:1,samples:1},series:[{metric:'cpu.cores',unit:'cores',aggregation:'sum',labels:{},points:[{timestamp:'2026-11-15T10:00:00Z',value:1}]}],placement:{status:'available',source:'test',stepSeconds:15,nodes:[{name:`node-${id}`,cluster:'cluster'}],snapshots:[{timestamp:'2026-11-15T10:00:00Z',observed:true,pods:[{uid:id,name:id,service:'api',node:`node-${id}`,cluster:'cluster'}]}]}}}) as TimeSeriesDataset
 vi.mocked(request).mockImplementation(async(path)=>path.endsWith('/figures')?{items:[]} as never:data(path.includes('/one/')?'one':'two') as never)
 const experiment={id:'experiment',kind:'instance',name:'Test',status:'succeeded',variants:[{id:'variant',name:'Strategy',trials:[{id:'trial-one',position:1,status:'succeeded',resultArtifactId:'one'},{id:'trial-two',position:2,status:'succeeded',resultArtifactId:'two'}]}]} as Experiment
 render(<ResultsView artifacts={artifacts} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated:true}} changed={async()=>{}} openOperation={()=>{}} focusedExperiment={experiment}/>)
 await screen.findByRole('button',{name:'Plots'})
 expect(screen.queryByRole('button',{name:'Placement'})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Strategy · Execution 1'}))
 await screen.findByRole('button',{name:'Placement'})
 fireEvent.click(screen.getByRole('button',{name:'Plots'}))
 await screen.findByRole('img',{name:'Application placement by zone heatmap'})
 fireEvent.click(screen.getByRole('button',{name:'Placement'}))
 await screen.findByRole('heading',{name:'node-one'})
 expect(screen.queryByRole('heading',{name:'node-two'})).not.toBeInTheDocument()
 expect(screen.queryByLabelText('Placement run')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'All executions'}))
 await waitFor(()=>expect(screen.queryByRole('button',{name:'Placement'})).not.toBeInTheDocument())
 expect(screen.queryByLabelText('Placement replay')).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button',{name:'Plots'}))
 await screen.findByRole('img',{name:'Application placement by zone heatmap'})
 expect(screen.getByText('Mean replicas')).toBeInTheDocument()
})
