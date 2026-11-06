import {WizardForm,WizardStep} from './WizardForm'
import {TolerationFields} from './PlacementFields'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {useState} from 'react'
import {describe, expect, it, vi} from 'vitest'
import {ApplicationPlacementEditor} from './ApplicationPlacementEditor'
import {PoolPlacementFields} from './PlacementFields'
import type {ApplicationComponent, NodeTaint, PodToleration} from '../types'
const services=[{id:'api'},{id:'db'}] as ApplicationComponent[]
const current = () => JSON.parse((document.querySelector('[name="applicationPlacement"]') as HTMLInputElement).value)

describe('Application placement', () => {
 it('keeps legacy settings absent until configured and retains drafts across tabs', () => {
  render(<ApplicationPlacementEditor services={services} />)
  expect(current()).toBeNull()
  fireEvent.click(screen.getByText('Add node selector'))
  const key=screen.getByLabelText('Node selector key 1') as HTMLInputElement
  expect(key.checkValidity()).toBe(false)
  fireEvent.change(key,{target:{value:'hardware'}})
  fireEvent.change(screen.getByLabelText('Node selector value 1'),{target:{value:'gpu'}})
  fireEvent.click(screen.getByText('Add toleration'))
  fireEvent.change(screen.getByLabelText('Toleration key 1'),{target:{value:'gpu'}})
  fireEvent.change(screen.getByLabelText('Toleration operator 1'),{target:{value:'Exists'}})
  expect(screen.getByLabelText('Toleration value 1')).toBeDisabled()
  expect(screen.getByLabelText('Node selector value 1')).toHaveValue('gpu')
  expect(current()).toMatchObject({nodeSelector:{hardware:'gpu'},tolerations:[{key:'gpu',operator:'Exists'}]})
 })
 it('supports inherited, custom and absent values independently per service', () => {
  render(<ApplicationPlacementEditor services={services} initial={{nodeSelector:{disk:'ssd'},tolerations:[{key:'gpu',operator:'Exists'}]}} />)
  fireEvent.click(screen.getByRole('tab',{name:'Service overrides'}))
  fireEvent.click(screen.getByText('Add service override'))
  fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'custom'}})
  fireEvent.change(screen.getByLabelText('api node selector value 1'),{target:{value:'hdd'}})
  fireEvent.change(screen.getByLabelText('Tolerations mode for api'),{target:{value:'none'}})
  expect(current()).toMatchObject({nodeSelector:{disk:'ssd'},serviceOverrides:{api:{nodeSelectorMode:'custom',nodeSelector:{disk:'hdd'},tolerationsMode:'none'}}})
  fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'none'}})
  expect(current().nodeSelector).toEqual({disk:'ssd'})
  fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'inherit'}})
  expect(current().serviceOverrides.api.nodeSelectorMode).toBe('inherit')
 })
 it('rejects duplicate keys and excludes overrides for removed services', () => {
  render(<ApplicationPlacementEditor services={services} initial={{nodeSelector:{disk:'ssd'},serviceOverrides:{removed:{nodeSelectorMode:'none'}}}} />)
  expect(current().serviceOverrides).toEqual({})
  fireEvent.click(screen.getByText('Add node selector'))
  fireEvent.change(screen.getByLabelText('Node selector key 2'),{target:{value:'disk'}})
  expect((screen.getByLabelText('Node selector key 2') as HTMLInputElement).checkValidity()).toBe(false)
 })
})

function Pool() {
 const [value,setValue]=useState<{labels?:Record<string,string>;taints?:NodeTaint[]}>({})
 return <><PoolPlacementFields {...value} onChange={change=>setValue(current=>({...current,...change}))}/><output>{JSON.stringify(value)}</output></>
}
it('protects managed pool labels and keeps taint values while changing tabs', () => {
 render(<Pool />)
 fireEvent.click(screen.getByText('Add label'))
 fireEvent.change(screen.getByLabelText('Label key 1'),{target:{value:'kubephos.dev/role'}})
 expect((screen.getByLabelText('Label key 1') as HTMLInputElement).checkValidity()).toBe(false)
 fireEvent.change(screen.getByLabelText('Label key 1'),{target:{value:'hardware'}})
 fireEvent.change(screen.getByLabelText('Label value 1'),{target:{value:'gpu'}})
 fireEvent.click(screen.getByRole('tab',{name:'Taints'}))
 fireEvent.click(screen.getByText('Add taint'))
 fireEvent.change(screen.getByLabelText('Taint key 1'),{target:{value:'gpu'}})
 fireEvent.change(screen.getByLabelText('Taint effect 1'),{target:{value:'NoExecute'}})
 fireEvent.click(screen.getByRole('tab',{name:'Labels'}))
 expect(screen.getByLabelText('Label value 1')).toHaveValue('gpu')
 expect(JSON.parse(screen.getByRole('status').textContent??'{}')).toEqual({labels:{hardware:'gpu'},taints:[{key:'gpu',effect:'NoExecute'}]})
})

it('navigates placement overrides by service tab and keeps independent custom drafts',()=>{
 render(<ApplicationPlacementEditor services={services} initial={{nodeSelector:{disk:'ssd'}}}/>)
 fireEvent.click(screen.getByRole('tab',{name:'Service overrides'}))
 fireEvent.click(screen.getByRole('button',{name:'Add service override'}))
 fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'custom'}})
 fireEvent.change(screen.getByLabelText('api node selector value 1'),{target:{value:'hdd'}})
 fireEvent.click(screen.getByRole('button',{name:'Add service override'}))
 expect(screen.getByRole('tab',{name:'db'})).toHaveAttribute('aria-selected','true')
 fireEvent.change(screen.getByLabelText('Tolerations mode for db'),{target:{value:'none'}})
 fireEvent.click(screen.getByRole('tab',{name:'api'}))
 expect(screen.getByLabelText('api node selector value 1')).toHaveValue('hdd')
 fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'none'}})
 fireEvent.change(screen.getByLabelText('Node selector mode for api'),{target:{value:'custom'}})
 expect(screen.getByLabelText('api node selector value 1')).toHaveValue('hdd')
 expect(current().serviceOverrides).toMatchObject({api:{nodeSelector:{disk:'hdd'}},db:{tolerationsMode:'none'}})
 fireEvent.click(screen.getByRole('button',{name:'Remove placement override for api'}))
 expect(screen.queryByRole('tab',{name:'api'})).not.toBeInTheDocument()
 expect(screen.getByRole('tab',{name:'db'})).toHaveAttribute('aria-selected','true')
})

it('blocks saving an incomplete node selector from a hidden wizard section',async()=>{
 const save=vi.fn()
 render(<WizardForm submitLabel="Save" onCancel={()=>{}} onSubmit={save}><WizardStep title="Application"><ApplicationPlacementEditor services={services}/></WizardStep><WizardStep title="Review"><p>Review</p></WizardStep></WizardForm>)
 fireEvent.click(screen.getByRole('button',{name:'Add node selector'}))
 fireEvent.click(screen.getByRole('button',{name:'Review'}))
 fireEvent.click(screen.getByRole('button',{name:'Save'}))
 expect(save).not.toHaveBeenCalled()
 await waitFor(()=>expect(screen.getByLabelText('Node selector key 1')).toHaveFocus())
 expect((screen.getByLabelText('Node selector key 1') as HTMLInputElement).validity.valueMissing).toBe(true)
 fireEvent.change(screen.getByLabelText('Node selector key 1'),{target:{value:'disk'}})
 expect((screen.getByLabelText('Node selector value 1') as HTMLInputElement).checkValidity()).toBe(true)
 fireEvent.click(screen.getByRole('button',{name:'Review'}))
 fireEvent.click(screen.getByRole('button',{name:'Save'}))
 expect(save).toHaveBeenCalledOnce()
})
it('requires a toleration key for Equal, allows valid Exists and rejects duplicates',()=>{
 function Fields(){const [value,setValue]=useState<PodToleration[]>([]);return <TolerationFields value={value} onChange={setValue}/>}
 render(<Fields/>)
 fireEvent.click(screen.getByRole('button',{name:'Add toleration'}))
 expect((screen.getByLabelText('Toleration key 1') as HTMLInputElement).checkValidity()).toBe(false)
 fireEvent.change(screen.getByLabelText('Toleration operator 1'),{target:{value:'Exists'}})
 expect((screen.getByLabelText('Toleration key 1') as HTMLInputElement).checkValidity()).toBe(true)
 expect(screen.getByLabelText('Toleration value 1')).toBeDisabled()
 fireEvent.click(screen.getByRole('button',{name:'Add toleration'}))
 fireEvent.change(screen.getByLabelText('Toleration operator 2'),{target:{value:'Exists'}})
 expect((screen.getByLabelText('Toleration key 2') as HTMLInputElement).checkValidity()).toBe(false)
})

it('shows managed node label conflicts inline before submitting',()=>{
 render(<Pool/>)
 fireEvent.click(screen.getByText('Labels and taints'))
 fireEvent.click(screen.getByRole('button',{name:'Add label'}))
 fireEvent.change(screen.getByLabelText('Label key 1'),{target:{value:'kubephos.dev/pool'}})
 expect(screen.getByText('This key is reserved.')).toBeVisible()
 expect(screen.getByLabelText('Label key 1')).toHaveAttribute('aria-invalid','true')
 fireEvent.change(screen.getByLabelText('Label key 1'),{target:{value:'hardware'}})
 expect(screen.queryByText('This key is reserved.')).not.toBeInTheDocument()
})
it('shows a placement conflict immediately and clears it when corrected',()=>{
 render(<form><ApplicationPlacementEditor services={services}/></form>)
 fireEvent.click(screen.getByRole('button',{name:'Add node selector'}))
 fireEvent.change(screen.getByLabelText('Node selector key 1'),{target:{value:'kubephos.dev/role'}})
 fireEvent.change(screen.getByLabelText('Node selector value 1'),{target:{value:'management'}})
 expect(screen.getByText('api requires kubephos.dev/role=application.')).toBeVisible()
 expect((document.querySelector('form') as HTMLFormElement).checkValidity()).toBe(false)
 fireEvent.change(screen.getByLabelText('Node selector value 1'),{target:{value:'application'}})
 expect(screen.queryByText('api requires kubephos.dev/role=application.')).not.toBeInTheDocument()
 expect((document.querySelector('form') as HTMLFormElement).checkValidity()).toBe(true)
})
