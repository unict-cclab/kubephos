import {fireEvent,render,screen,waitFor} from '@testing-library/react'
import {beforeEach,expect,it,vi} from 'vitest'
import {request} from '../api'
import {ProxyResourcesEditor} from './ProxyResourcesEditor'
vi.mock('../api',()=>({request:vi.fn()}))
beforeEach(()=>vi.mocked(request).mockReset())
it('reads the selected cluster injector only when Defaults is opened',async()=>{
 vi.mocked(request).mockResolvedValue({available:true,resources:{cpuRequest:'125m',memoryRequest:'96Mi',memoryLimit:'768Mi'}})
 render(<ProxyResourcesEditor services={[{id:'api'}]} clusterId="cluster" session={{authenticated:true,csrfToken:'csrf'}}/>)
 expect(request).not.toHaveBeenCalled()
 fireEvent.click(screen.getByRole('tab',{name:'Defaults'}))
 await waitFor(()=>expect(screen.getByRole('table',{name:'Default proxy resources'})).toHaveTextContent('125m'))
 expect(screen.getByRole('table',{name:'Default proxy resources'})).toHaveTextContent('768Mi')
 expect(screen.getByRole('table',{name:'Default proxy resources'})).toHaveTextContent('None')
 expect(request).toHaveBeenCalledWith('/kubernetes-clusters/cluster/istio-proxy-defaults',{},undefined,expect.any(AbortSignal))
})
it('does not query Istio on multi clusters',()=>{
 render(<ProxyResourcesEditor services={[]} clusterId="mesh" session={{authenticated:true}} disabled/>)
 fireEvent.click(screen.getByRole('tab',{name:'Defaults'}))
 expect(screen.getByText('Istio is disabled on this cluster.')).toBeInTheDocument()
 expect(request).not.toHaveBeenCalled()
})
