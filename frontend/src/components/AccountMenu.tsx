import {useEffect, useRef, useState} from 'react'
import {ActionIcon} from './ActionIcon'
import {useTheme} from './ThemeToggle'

interface Props {
  username: string
  isAdmin: boolean
  version?: string
  refreshing: boolean
  refresh: () => void
  signOut: () => void
}

// Disclosure anchored to the account bar: identity, appearance and session actions.
export function AccountMenu({username, isAdmin, version, refreshing, refresh, signOut}: Props) {
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
      <button type="button" className="account-menu-item" aria-pressed={dark} onClick={toggle}><ActionIcon name="moon" /><span>Dark mode</span><span className="account-menu-switch" aria-hidden="true" /></button>
      <button type="button" className="account-menu-item" disabled={refreshing} onClick={run(refresh)}><ActionIcon name="refresh" /><span>{refreshing ? 'Refreshing…' : 'Refresh data'}</span></button>
      <button type="button" className="account-menu-item account-menu-sign-out" onClick={run(signOut)}><ActionIcon name="logout" /><span>Sign out</span></button>
    </div>}
    <p className="app-version" aria-label="KubePhos version"><span>Version</span><strong>{version || '—'}</strong></p>
    <div className="app-account">
      <span className="signed-user" title={username}><span className="avatar" aria-hidden="true">{username.charAt(0)}</span><span className="account-identity"><span className="signed-user-name">{username}</span><span className="account-role">{isAdmin ? 'Administrator' : 'User'}</span></span></span>
      <button ref={trigger} type="button" className="icon-button" aria-label="Account settings" title="Account settings" aria-expanded={open} aria-controls="account-menu-panel" onClick={() => setOpen(value => !value)}><ActionIcon name="settings" /></button>
    </div>
  </div>
}
