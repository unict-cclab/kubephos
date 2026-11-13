import {useEffect, useState, type FormEvent} from 'react'
import {request} from '../api'
import type {Session} from '../types'
import {ErrorFeedback, useErrorState, useNotifications} from './ToastRegion'
import {ValidatedInput} from './ValidatedInput'
import {ResourceListHeading} from './ResourceListHeading'
import {useDeleteConfirmation} from './ConfirmationDialog'

interface BotSettings {configured: boolean; enabled?: boolean; username?: string; lastError?: string}
interface Preferences {available: boolean; connected: boolean; enabled: boolean; completed: boolean; failed: boolean; name?: string; pendingUntil?: string; lastError?: string}
const accountPath='/account/notifications/telegram'

export function TelegramAdministration({session}: {session: Session}) {
 const [bot,setBot]=useState<BotSettings|null>(null)
 const [token,setToken]=useState('')
 const [enabled,setEnabled]=useState(true)
 const [pending,setPending]=useState(false)
 const [error,setError]=useErrorState()
 const notifications=useNotifications()
 const {confirmAction,confirmationDialog}=useDeleteConfirmation()
 useEffect(()=>{
  const controller=new AbortController()
  void request<BotSettings>('/administration/telegram',{},undefined,controller.signal).then(value=>{setBot(value);setEnabled(value.enabled??true)}).catch(cause=>{if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:'Could not load Telegram settings.')})
  return ()=>controller.abort()
 },[])
 const save=async(event:FormEvent)=>{
  event.preventDefault();setPending(true);setError('')
  try{
   const value=await request<BotSettings>('/administration/telegram',{method:'PUT',body:JSON.stringify({token,enabled})},session.csrfToken)
   setBot(value);setToken('');notifications?.notify('Telegram settings saved.','success')
  }catch(cause){setError(cause instanceof Error?cause.message:'Could not save Telegram settings.')}finally{setPending(false)}
 }
 const remove=async()=>{
  if(!await confirmAction('Disconnect all Telegram accounts and remove the bot token?',{title:'Remove Telegram integration',label:'Remove'}))return
  setPending(true);setError('')
  try{await request('/administration/telegram',{method:'DELETE'},session.csrfToken);setBot({configured:false});setToken('');setEnabled(true);notifications?.notify('Telegram integration removed.','success')}
  catch(cause){setError(cause instanceof Error?cause.message:'Could not remove Telegram settings.')}finally{setPending(false)}
 }
 return <>{confirmationDialog}<ResourceListHeading title="Notifications" icon="notifications"/><article className="detail-page-card"><form className="form-stack" onSubmit={save}>
  <h3>Telegram</h3><ErrorFeedback message={error}/>
  {!bot&&!error&&<p role="status">Loading Telegram settings…</p>}
  {bot&&<>
   {bot.configured&&<p><strong>Bot</strong> <a href={`https://t.me/${bot.username}`} target="_blank" rel="noreferrer">@{bot.username}</a></p>}
   <label>Bot token<ValidatedInput aria-label="Bot token" type="password" autoComplete="new-password" value={token} onChange={event=>setToken(event.target.value)} required={!bot.configured} maxLength={181} pattern="[0-9]{5,20}:[A-Za-z0-9_-]{20,160}" placeholder={bot.configured?'Unchanged':undefined}/></label>
   <p>Create a dedicated bot with <a href="https://t.me/BotFather" target="_blank" rel="noreferrer">BotFather</a>.</p>
   <label className="checkbox-field"><input type="checkbox" checked={enabled} disabled={pending} onChange={event=>setEnabled(event.target.checked)}/>Enable Telegram</label>
   {bot.lastError&&<p role="status">{bot.lastError}</p>}
   <div className="dialog-actions">{bot.configured&&<button type="button" className="button danger" disabled={pending} onClick={()=>void remove()}>Remove integration</button>}<button className="button primary" disabled={pending}>{pending?'Saving…':'Save'}</button></div>
  </>}
 </form></article></>
}

export function TelegramPreferences({session}: {session: Session}) {
 const [preferences,setPreferences]=useState<Preferences|null>(null)
 const [link,setLink]=useState('')
 const [pending,setPending]=useState(false)
 const [error,setError]=useErrorState()
 const notifications=useNotifications()
 useEffect(()=>{
  const controller=new AbortController()
  void request<Preferences>(accountPath,{},undefined,controller.signal).then(value=>{if(!controller.signal.aborted)setPreferences(value)}).catch(cause=>{if(!controller.signal.aborted)setError(cause instanceof Error?cause.message:'Could not load notification preferences.')})
  return ()=>controller.abort()
 },[])
 useEffect(()=>{
  if(!preferences?.pendingUntil||pending||preferences.connected)return
  const controller=new AbortController()
  const timer=window.setInterval(()=>{
   void request<Preferences>(accountPath,{},undefined,controller.signal).then(value=>{if(controller.signal.aborted)return;setPreferences(value);if(value.connected||!value.pendingUntil)setLink('')}).catch(()=>{})
  },2500)
  return ()=>{window.clearInterval(timer);controller.abort()}
 },[preferences?.pendingUntil,preferences?.connected,pending])
 const action=async(work:()=>Promise<void>)=>{
  setPending(true);setError('')
  try{await work()}catch(cause){setError(cause instanceof Error?cause.message:'Could not update Telegram notifications.')}finally{setPending(false)}
 }
 const connect=()=>action(async()=>{
  const value=await request<{url:string}>(accountPath+'/connect',{method:'POST'},session.csrfToken)
  setLink(value.url);setPreferences(await request<Preferences>(accountPath))
 })
 const disconnect=()=>action(async()=>{
  setPreferences(await request<Preferences>(accountPath+'/disconnect',{method:'POST'},session.csrfToken));setLink('')
 })
 const save=(update:Partial<Preferences>)=>action(async()=>{
  const next={...preferences,...update}
  setPreferences(await request<Preferences>(accountPath,{method:'PUT',body:JSON.stringify({enabled:next.enabled,completed:next.completed,failed:next.failed})},session.csrfToken))
 })
 const test=()=>action(async()=>{
  await request(accountPath+'/test',{method:'POST'},session.csrfToken)
  notifications?.notify('Test notification queued.','success')
 })
 return <div className="form-stack"><h3>Telegram</h3><ErrorFeedback message={error}/>
  {!preferences&&!error&&<p role="status">Loading notification preferences…</p>}
  {preferences&&!preferences.available&&<p>Telegram is not enabled for this instance.</p>}
  {(preferences?.available||preferences?.connected)&&preferences&&<>
   {preferences.connected?<>
    <p><strong>Connected account</strong> {preferences.name||'Telegram user'}</p>
    {preferences.lastError&&<p role="status">{preferences.lastError}</p>}
    <label className="checkbox-field"><input type="checkbox" checked={preferences.enabled} disabled={pending||!preferences.available} onChange={event=>void save({enabled:event.target.checked})}/>Enable notifications</label>
    <div className="field-row">
     <label className="checkbox-field"><input type="checkbox" checked={preferences.completed} disabled={pending||!preferences.available||!preferences.enabled} onChange={event=>void save({completed:event.target.checked})}/>Experiment completed</label>
     <label className="checkbox-field"><input type="checkbox" checked={preferences.failed} disabled={pending||!preferences.available||!preferences.enabled} onChange={event=>void save({failed:event.target.checked})}/>Experiment failed</label>
    </div>
    <div className="dialog-actions"><button type="button" className="button secondary" disabled={pending} onClick={()=>void disconnect()}>Disconnect</button><button type="button" className="button primary" disabled={pending||!preferences.available||!preferences.enabled} onClick={()=>void test()}>Send test</button></div>
   </>:link?<>
    <p>Open Telegram and press Start to connect your account.</p>
    <div className="dialog-actions"><button type="button" className="button secondary" disabled={pending} onClick={()=>void disconnect()}>Cancel</button><a className="button primary" href={link} target="_blank" rel="noreferrer">Open Telegram</a></div>
   </>:<button type="button" className="button primary" disabled={pending} onClick={()=>void connect()}>{pending?'Connecting…':'Connect Telegram'}</button>}
  </>}
 </div>
}
