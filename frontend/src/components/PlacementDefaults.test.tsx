import {fireEvent,render,screen,within} from '@testing-library/react'
import {it,expect,vi} from 'vitest'
import {defaultPoolLabels,defaultPoolTaints,ApplicationDefaultFields} from './PlacementDefaults'
import {ApplicationPlacementEditor} from './ApplicationPlacementEditor'
import {PoolPlacementFields} from './PlacementFields'
import type {Application,ApplicationComponent} from '../types'

it('shows managed pool labels and zone values without a redundant node scope',()=>{
 const rows=defaultPoolLabels({clusterName:'test',name:'edge',role:'application',zones:['a','b'],count:3})
 expect(rows).toContainEqual(['kubephos.dev/pool','edge'])
 expect(rows).toContainEqual(['topology.kubernetes.io/zone','a'])
 expect(rows).toContainEqual(['topology.kubernetes.io/zone','b'])
 const mesh=defaultPoolLabels({clusterName:'mesh-west',name:'gpu',networking:'cilium',count:2})
 expect(mesh).toContainEqual(['kubephos.dev/cluster','mesh-west'])
 expect(mesh.some(row=>row[0]==='topology.kubernetes.io/zone')).toBe(false)
 expect(defaultPoolTaints({name:'control-plane',role:'control-plane'})).toEqual([{key:'node-role.kubernetes.io/control-plane',effect:'NoSchedule'}])
 expect(defaultPoolTaints({name:'management',role:'management'})).toEqual([])
})
it('keeps managed pool labels read-only and out of custom fields',()=>{
 const onChange=vi.fn()
 render(<PoolPlacementFields name="gpu" defaults={{name:'gpu',role:'application',zones:['edge'],count:1}} onChange={onChange}/>)
 const table=screen.getByRole('table',{name:'Default labels',hidden:true})
 expect(within(table).getByText('kubephos.dev/pool')).toBeInTheDocument()
 expect(table.querySelectorAll('input')).toHaveLength(0)
 expect(onChange).not.toHaveBeenCalled()
})
it('groups identical manifest defaults without mixing custom values',()=>{
 render(<ApplicationDefaultFields kind="nodeSelector" defaults={[{id:'api',nodeSelector:{'kubephos.dev/role':'application'}},{id:'db',nodeSelector:{'kubephos.dev/role':'application'}}]}/>)
 expect(screen.getAllByText('kubephos.dev/role')).toHaveLength(1)
 expect(screen.getByText('All workloads')).toBeInTheDocument()
})
it('loads the actual manifest defaults without persisting them as overrides',async()=>{
 const fetch=vi.fn().mockResolvedValue(new Response(JSON.stringify({placementDefaults:[{id:'api',nodeSelector:{'kubephos.dev/role':'application'},tolerations:[{key:'catalog-taint',operator:'Exists',effect:'NoSchedule'}]}]}),{status:200}))
 vi.stubGlobal('fetch',fetch)
 const application={id:'app',digest:'digest',descriptor:{spec:{defaults:{},valuesSchema:{type:'object'}}}} as Application
 render(<ApplicationPlacementEditor services={[{id:'api'}] as ApplicationComponent[]} application={application} session={{authenticated:true,csrfToken:'csrf'}} enabled/>)
 await screen.findByText('kubephos.dev/role')
 await screen.findByText('catalog-taint')
 expect(JSON.parse((document.querySelector('[name="applicationPlacement"]') as HTMLInputElement).value)).toBeNull()
 expect(fetch).toHaveBeenCalledTimes(1)
})

it('validates against actual manifest selectors and excludes services using Defaults only',async()=>{
 vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({placementDefaults:[{id:'api',nodeSelector:{hardware:'gpu','kubephos.dev/role':'application'}}]}),{status:200})))
 const application={id:'app-conflict',digest:'conflict',descriptor:{spec:{defaults:{},valuesSchema:{type:'object'}}}} as Application
 render(<form><ApplicationPlacementEditor services={[{id:'api'}] as ApplicationComponent[]} application={application} session={{authenticated:true,csrfToken:'csrf'}} initial={{nodeSelector:{hardware:'cpu'}}}/></form>)
 expect(await screen.findByText('api requires hardware=gpu.')).toBeVisible()
 expect((document.querySelector('form') as HTMLFormElement).checkValidity()).toBe(false)
 fireEvent.click(screen.getByRole('tab',{name:'Service overrides'}))
 fireEvent.click(screen.getByRole('button',{name:'Add service override'}))
 fireEvent.click(within(screen.getByRole('dialog',{name:'Choose a service'})).getByRole('button',{name:'api'}))
 fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'none'}})
 expect((document.querySelector('form') as HTMLFormElement).checkValidity()).toBe(true)
 fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'custom'}})
 expect(screen.getByText('api requires hardware=gpu.')).toBeVisible()
 fireEvent.change(screen.getByLabelText('api node selector value 1'),{target:{value:'gpu'}})
 expect((document.querySelector('form') as HTMLFormElement).checkValidity()).toBe(true)
})
