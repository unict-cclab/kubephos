import {fireEvent,render,screen,waitFor} from '@testing-library/react'
import {afterEach,describe,expect,it,vi} from 'vitest'
import {request} from '../api'
import {ProxmoxNodeSelect} from './ProxmoxNodeSelect'
vi.mock('../api',()=>({request:vi.fn()}))
afterEach(()=>vi.resetAllMocks())
describe('Proxmox server selection',()=>{
 it('offers actual nodes and disables offline servers',async()=>{
  vi.mocked(request).mockResolvedValue({items:[{name:'pve',online:true},{name:'pve-2',online:true},{name:'pve-3',online:false}]})
  const view=render(<ProxmoxNodeSelect connectionID="connection" defaultNode="pve"/>)
  await waitFor(()=>expect(screen.getByRole('combobox')).toHaveValue('pve'))
  expect(screen.getByRole('option',{name:'pve-3 · Offline'})).toBeDisabled()
  fireEvent.change(screen.getByRole('combobox'),{target:{value:'pve-2'}})
  view.rerender(<ProxmoxNodeSelect connectionID="connection" defaultNode="pve"/>)
  expect(screen.getByRole('combobox')).toHaveValue('pve-2')
 })
 it('allows manual entry when discovery fails and provides retry',async()=>{
  vi.mocked(request).mockRejectedValue(new Error('Unavailable'))
  render(<ProxmoxNodeSelect connectionID="connection" defaultNode="pve"/>)
  expect(await screen.findByRole('textbox',{name:'Proxmox server'})).toHaveValue('pve')
  expect(screen.getByRole('button',{name:'Retry'})).toBeInTheDocument()
 })
})
