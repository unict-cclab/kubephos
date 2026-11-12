import {fireEvent,render,screen,waitFor,within} from '@testing-library/react'
import {expect,it,vi} from 'vitest'
import {NetworkInjectionEditor} from './NetworkInjectionEditor'
import {IstioInjectionEditor} from './IstioInjectionEditor'

it('keeps static network values unchanged until a phase is added and deep clones link settings',async()=>{
 const {container}=render(<form><NetworkInjectionEditor prefix="network" zones={['cloud','edge']} durationSeconds={120} values={{enableLatency:false,linkOverrides:[{sourceZone:'cloud',targetZone:'edge',latencyMilliseconds:50}]}}/></form>)
 expect(screen.getByRole('tab',{name:'Phase 1'})).toHaveAttribute('aria-selected','true')
 expect(container.querySelector('[name="network.phases"]')).not.toBeInTheDocument()
 fireEvent.change(screen.getByRole('spinbutton',{name:'One-way latency (ms)'}),{target:{value:'70'}})
 fireEvent.click(screen.getByRole('button',{name:'Clone'}))
 const phaseValue=()=>JSON.parse((container.querySelector('[name="network.phases"]') as HTMLInputElement).value)
 await waitFor(()=>expect(phaseValue()).toHaveLength(2))
 expect(phaseValue().map((p:Record<string,any>)=>p.linkOverrides[0].latencyMilliseconds)).toEqual([70,70])
 fireEvent.click(screen.getByRole('tab',{name:'Phase 1'}))
 fireEvent.change(within(screen.getByRole('tabpanel')).getByRole('spinbutton',{name:'One-way latency (ms)'}),{target:{value:'90'}})
 await waitFor(()=>expect(phaseValue()[0].linkOverrides[0].latencyMilliseconds).toBe(90))
 expect(phaseValue()[1].linkOverrides[0].latencyMilliseconds).toBe(70)
 fireEvent.click(within(screen.getByRole('tabpanel')).getByRole('button',{name:'Remove override'}))
 await waitFor(()=>expect(phaseValue()[0].linkOverrides).toEqual([]))
 expect(phaseValue()[1].linkOverrides).toHaveLength(1)
})
it('validates total durations and preserves profiles while switching tabs',async()=>{
 const valid=vi.fn()
 const phase={durationSeconds:60,enableLatency:true,latencyMilliseconds:20,jitterMilliseconds:0,correlationPercent:0,enableBandwidth:false,bandwidthBytesPerSecond:100,enablePacketLoss:false,packetLossPercent:0,linkOverrides:[]}
 const {container}=render(<form><NetworkInjectionEditor prefix="network" zones={['cloud','edge']} durationSeconds={120} onValidityChange={valid} values={{phases:[phase,{...phase,latencyMilliseconds:80}]}}/></form>)
 await waitFor(()=>expect(valid).toHaveBeenLastCalledWith(true))
 fireEvent.change(within(screen.getByRole('tabpanel')).getByRole('spinbutton',{name:'Duration (min)'}),{target:{value:'0.5'}})
 await waitFor(()=>expect(valid).toHaveBeenLastCalledWith(false))
 fireEvent.click(screen.getByRole('tab',{name:'Phase 2'}))
 expect(within(screen.getByRole('tabpanel')).getByRole('spinbutton',{name:'Duration (min)'})).toHaveValue(1)
 expect(within(screen.getByRole('tabpanel')).getByRole('spinbutton',{name:'One-way latency (ms)'})).toHaveValue(80)
 fireEvent.change(within(screen.getByRole('tabpanel')).getByRole('spinbutton',{name:'Duration (min)'}),{target:{value:'1.5'}})
 await waitFor(()=>expect(valid).toHaveBeenLastCalledWith(true))
 expect(JSON.parse((container.querySelector('[name="network.phases"]') as HTMLInputElement).value).map((p:any)=>p.durationSeconds)).toEqual([30,90])
})
it('defaults Istio on and supports independently enabling a service when globally disabled',()=>{
 const {container}=render(<form><IstioInjectionEditor enabled services={[{id:'api'},{id:'web'}] as any}/></form>)
 expect(screen.getByRole('checkbox',{name:'Enable Istio injection'})).toBeChecked()
 fireEvent.click(screen.getByRole('checkbox',{name:'Enable Istio injection'}))
 fireEvent.click(screen.getByText('Service overrides'))
 fireEvent.click(screen.getByRole('button',{name:'Add service override'}))
 fireEvent.click(within(screen.getByRole('dialog',{name:'Choose a service'})).getByRole('button',{name:'api'}))
 expect(screen.getByLabelText('Istio injection for api')).toHaveValue('true')
 expect((container.querySelector('[name="istioInjection"]') as HTMLInputElement).value).toBe('false')
 expect(JSON.parse((container.querySelector('[name="istioInjectionOverrides"]') as HTMLInputElement).value)).toEqual({api:true})
})


it('keeps a saved neutral phase empty and makes Istio injection enabled by default',()=>{
 const phase={durationSeconds:60,enableLatency:false,enableBandwidth:false,enablePacketLoss:false,linkOverrides:[]}
 const {container}=render(<form><IstioInjectionEditor services={[]}/><NetworkInjectionEditor prefix="network" zones={['a','b']} durationSeconds={120} values={{phases:[{...phase,enableLatency:true,latencyMilliseconds:10},phase]}}/></form>)
 expect(screen.getByRole('checkbox',{name:'Enable Istio injection'})).toBeChecked()
 fireEvent.click(screen.getByRole('tab',{name:'Phase 2'}))
 expect(within(screen.getByRole('tabpanel',{name:'Phase 2'})).queryByRole('table')).not.toBeInTheDocument()
 expect(within(screen.getByRole('tabpanel',{name:'Phase 2'})).getByLabelText('Latency and jitter')).not.toBeChecked()
 const values=JSON.parse((container.querySelector('[name="network.phases"]') as HTMLInputElement).value)
 expect(values[1].linkOverrides).toEqual([])
 expect(values[1].enableLatency).toBe(false)
 expect((container.querySelector('form') as HTMLFormElement).checkValidity()).toBe(true)
})

it('preserves independent Istio overrides when switching service tabs and reassigning services',()=>{
 const {container}=render(<form><IstioInjectionEditor services={[{id:'api'},{id:'web'},{id:'worker'}] as any} overrides={{api:false,web:true}}/></form>)
 expect(screen.getByRole('heading',{name:'Service overrides'})).toBeInTheDocument()
 expect(screen.getByLabelText('Istio injection for api')).toHaveValue('false')
 fireEvent.click(screen.getByRole('tab',{name:'web'}))
 fireEvent.change(screen.getByLabelText('Istio service web'),{target:{value:'worker'}})
 expect(screen.getByRole('tab',{name:'worker'})).toHaveAttribute('aria-selected','true')
 fireEvent.click(screen.getByRole('tab',{name:'api'}))
 expect(screen.getByLabelText('Istio injection for api')).toHaveValue('false')
 expect(JSON.parse((container.querySelector('[name="istioInjectionOverrides"]') as HTMLInputElement).value)).toEqual({api:false,worker:true})
 fireEvent.click(screen.getByRole('button',{name:'Remove Istio override for api'}))
 expect(screen.getByLabelText('Istio injection for worker')).toHaveValue('true')
})
