import {useEffect, useRef, useState} from 'react'
import {ActionIcon} from './ActionIcon'
import {useTheme} from './ThemeToggle'

// Keycloak serves self-service account pages under the realm used for sign-in.
const accountConsole = '/auth/realms/kubephos/account'
export const accountLinks = {details: `${accountConsole}/`, password: `${accountConsole}/account-security/signing-in`}

interface Props {
  username: string
  isAdmin: boolean
  version?: string
  refreshing: boolean
  refresh: () => void
  openBackup: () => void
  signOut: () => void
}

// Disclosure anchored to the account bar: identity, security, appearance and session actions.
export function AccountMenu({username, isAdmin, version, refreshing, refresh, openBackup, signOut}: Props) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const {dark, toggle} = useTheme()

  useEffect(() => {
    if (!open) return
    const closeOutside = (event: PointerEvent) => {if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false)}
    document.addEventListener('pointerdown', closeOutside)
    return () => document.removeEventListener('pointerdown', closeOutside)
  }, [open])

  const run = (action: () => void) => () => {
    setOpen(false)
    action()
  }

  return <div className="account-menu" ref={root} onKeyDown={event => {if (event.key === 'Escape' && open) {setOpen(false); trigger.current?.focus()}}}>
    {open && <div id="account-menu-panel" className="account-menu-panel" role="group" aria-label="Account">
      <div className="account-menu-identity">
        <span className="avatar" aria-hidden="true">{username.charAt(0)}</span>
        <span><strong>{username}</strong>{isAdmin && <small>Administrator</small>}</span>
      </div>
      <a className="account-menu-item" href={accountLinks.password} target="_blank" rel="noreferrer"><ActionIcon name="lock" /><span>Change password</span><ActionIcon name="external" /></a>
      <a className="account-menu-item" href={accountLinks.details} target="_blank" rel="noreferrer"><ActionIcon name="user" /><span>Account details</span><ActionIcon name="external" /></a>
      <button type="button" className="account-menu-item" aria-pressed={dark} onClick={toggle}><ActionIcon name="moon" /><span>Dark mode</span><span className="account-menu-switch" aria-hidden="true" /></button>
      <button type="button" className="account-menu-item" disabled={refreshing} onClick={run(refresh)}><ActionIcon name="refresh" /><span>{refreshing ? 'Refreshing…' : 'Refresh data'}</span></button>
      {isAdmin && <button type="button" className="account-menu-item" onClick={run(openBackup)}><ActionIcon name="archive" /><span>Backup and transfer</span></button>}
      <button type="button" className="account-menu-item account-menu-sign-out" onClick={run(signOut)}><ActionIcon name="logout" /><span>Sign out</span></button>
      {version && <p className="account-menu-version">KubePhos {version}</p>}
    </div>}
    <div className="app-account">
      <span className="signed-user" title={username}><span className="avatar" aria-hidden="true">{username.charAt(0)}</span><span className="signed-user-name">{username}</span>{isAdmin && <span className="role-badge">Admin</span>}</span>
      <button ref={trigger} type="button" className="icon-button" aria-label="Account settings" title="Account settings" aria-expanded={open} aria-controls="account-menu-panel" onClick={() => setOpen(value => !value)}><ActionIcon name="settings" /></button>
    </div>
  </div>
}
