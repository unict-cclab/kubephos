import {fireEvent, render, screen} from '@testing-library/react'
import {useState} from 'react'
import {describe, expect, it} from 'vitest'
import {ApplicationPlacementEditor} from './ApplicationPlacementEditor'
import {PoolPlacementFields} from './PlacementFields'
import type {ApplicationComponent, NodeTaint} from '../types'
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
  fireEvent.click(screen.getByRole('tab',{name:'Tolerations'}))
  fireEvent.click(screen.getByText('Add toleration'))
  fireEvent.change(screen.getByLabelText('Toleration key 1'),{target:{value:'gpu'}})
  fireEvent.change(screen.getByLabelText('Toleration operator 1'),{target:{value:'Exists'}})
  expect(screen.getByLabelText('Toleration value 1')).toBeDisabled()
  fireEvent.click(screen.getByRole('tab',{name:'Node selector'}))
  expect(screen.getByLabelText('Node selector value 1')).toHaveValue('gpu')
  expect(current()).toMatchObject({nodeSelector:{hardware:'gpu'},tolerations:[{key:'gpu',operator:'Exists'}]})
 })
 it('supports inherited, custom and absent values independently per service', () => {
  render(<ApplicationPlacementEditor services={services} initial={{nodeSelector:{disk:'ssd'},tolerations:[{key:'gpu',operator:'Exists'}]}} />)
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
