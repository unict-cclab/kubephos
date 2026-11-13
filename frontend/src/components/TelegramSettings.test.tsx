import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {TelegramAdministration, TelegramPreferences} from './TelegramSettings'
import {request} from '../api'
vi.mock('../api',()=>({request:vi.fn()}))
const session={authenticated:true,csrfToken:'csrf'}
const preferences={available:true,connected:true,enabled:true,completed:true,failed:true,name:'Alice'}
afterEach(()=>vi.resetAllMocks())
it('lets a connected user disconnect even when the administrator disables Telegram',async()=>{
 vi.mocked(request).mockResolvedValueOnce({...preferences,available:false}).mockResolvedValueOnce({...preferences,available:false,connected:false})
 render(<TelegramPreferences session={session}/>)
 expect(await screen.findByText('Alice',{exact:false})).toBeVisible()
 expect(screen.getByRole('button',{name:'Send test'})).toBeDisabled()
 fireEvent.click(screen.getByRole('button',{name:'Disconnect'}))
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/account/notifications/telegram/disconnect',{method:'POST'},'csrf'))
 await waitFor(()=>expect(screen.queryByRole('button',{name:'Disconnect'})).not.toBeInTheDocument())
})
it('updates only the current account preferences without sending its identity',async()=>{
 vi.mocked(request).mockResolvedValueOnce(preferences).mockResolvedValueOnce({...preferences,completed:false})
 render(<TelegramPreferences session={session}/>)
 fireEvent.click(await screen.findByLabelText('Experiment completed'))
 await waitFor(()=>expect(request).toHaveBeenCalledWith('/account/notifications/telegram',{method:'PUT',body:JSON.stringify({enabled:true,completed:false,failed:true})},'csrf'))
 expect(screen.getByLabelText('Experiment completed')).not.toBeChecked()
})
it('opens an explicit Telegram connection link and allows cancellation',async()=>{
 const until=new Date(Date.now()+600000).toISOString()
 vi.mocked(request).mockResolvedValueOnce({...preferences,connected:false}).mockResolvedValueOnce({url:'https://t.me/KubePhosTestBot?start=opaque'}).mockResolvedValueOnce({...preferences,connected:false,pendingUntil:until})
 render(<TelegramPreferences session={session}/>)
 fireEvent.click(await screen.findByRole('button',{name:'Connect Telegram'}))
 expect(await screen.findByRole('link',{name:'Open Telegram'})).toHaveAttribute('href','https://t.me/KubePhosTestBot?start=opaque')
 expect(screen.getByRole('button',{name:'Cancel'})).toBeEnabled()
})
it('keeps the saved bot token hidden and clears a replacement after saving',async()=>{
 vi.mocked(request).mockResolvedValueOnce({configured:true,enabled:true,username:'KubePhosTestBot'}).mockResolvedValueOnce({configured:true,enabled:true,username:'KubePhosTestBot'})
 render(<TelegramAdministration session={session}/>)
 const token=await screen.findByLabelText('Bot token')
 expect(token).toHaveAttribute('type','password')
 expect(token).toHaveValue('')
 fireEvent.change(token,{target:{value:'12345:abcdefghijklmnopqrstuvwxyz'}})
 fireEvent.click(screen.getByRole('button',{name:'Save'}))
 await waitFor(()=>expect(token).toHaveValue(''))
 expect(request).toHaveBeenCalledWith('/administration/telegram',{method:'PUT',body:JSON.stringify({token:'12345:abcdefghijklmnopqrstuvwxyz',enabled:true})},'csrf')
})
