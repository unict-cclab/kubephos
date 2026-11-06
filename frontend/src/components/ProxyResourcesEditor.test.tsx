import {fireEvent,render,screen} from '@testing-library/react'
import {expect,it} from 'vitest'
import {ProxyResourcesEditor} from './ProxyResourcesEditor'
const services=[{id:'api'},{id:'db'}]
const settings=()=>JSON.parse((document.querySelector('[name="istioProxyResources"]') as HTMLInputElement).value)
it('leaves unspecified proxy resources absent and keeps them separate from application inputs',()=>{
 render(<form><input name="applicationValues.cpuRequest" defaultValue="500m"/><ProxyResourcesEditor services={services}/></form>)
 expect(settings()).toBeNull()
 fireEvent.change(screen.getByRole('textbox',{name:'CPU request for proxy (optional)'}),{target:{value:'100m'}})
 expect(settings()).toEqual({cpuRequest:'100m'})
 expect((document.querySelector('[name="applicationValues.cpuRequest"]') as HTMLInputElement).value).toBe('500m')
 fireEvent.change(screen.getByRole('textbox',{name:'CPU request for proxy (optional)'}),{target:{value:''}})
 expect(settings()).toBeNull()
})
it('edits saved per-service proxy overrides directly and retains inherited values',()=>{
 render(<ProxyResourcesEditor services={services} initial={{cpuRequest:'100m',resourceOverrides:{api:{memoryLimit:'256Mi'}}}}/>)
 fireEvent.click(screen.getByRole('tab',{name:'Service overrides'}))
 expect(screen.getByRole('textbox',{name:'Memory limit for proxy on api'})).toHaveValue('256')
 expect(screen.getByRole('textbox',{name:'CPU request for proxy on api'})).toHaveAttribute('placeholder','Inherited: 100m')
 fireEvent.change(screen.getByRole('textbox',{name:'Memory limit for proxy on api'}),{target:{value:'512Mi'}})
 expect(settings()).toMatchObject({cpuRequest:'100m',resourceOverrides:{api:{memoryLimit:'512Mi'}}})
})
it('omits proxy customization when Istio is unavailable on a multi cluster',()=>{
 render(<ProxyResourcesEditor services={services} initial={{cpuRequest:'100m'}} disabled/>)
 expect(settings()).toBeNull()
 expect(screen.getByRole('textbox',{name:'CPU request for proxy (optional)'})).toBeDisabled()
})
