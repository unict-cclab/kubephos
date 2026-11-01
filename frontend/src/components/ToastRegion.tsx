import {createContext, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState, type PropsWithChildren, type SetStateAction} from 'react'
import {createPortal} from 'react-dom'
import {ActionIcon} from './ActionIcon'

export interface ToastMessage {
  id: number
  message: string
  error: boolean
}

const NotificationContext = createContext<{notify: (message: string, error?: boolean, renew?: boolean) => void; clearErrors: () => void} | null>(null)

export function useNotifications() { return useContext(NotificationContext) }

export function useErrorState({repeat = true}: {repeat?: boolean} = {}) {
  const [message, setMessage] = useState('')
  const current = useRef('')
  const mounted = useRef(true)
  const notify = useNotifications()?.notify
  useEffect(() => {mounted.current = true; return () => {mounted.current = false}}, [])
  const setError = useCallback((value: SetStateAction<string>) => {
    if (!mounted.current) return
    const next = typeof value === 'function' ? value(current.current) : value
    const changed = next !== current.current
    current.current = next
    setMessage(next)
    if (next && (repeat || changed)) notify?.(next, true, repeat)
  }, [notify, repeat])
  return [message, setError] as const
}

export function NotificationProvider({children}: PropsWithChildren) {
  const [items, setItems] = useState<ToastMessage[]>([])
  const nextId = useRef(0)
  const notify = useCallback((message: string, error = false, renew = false) => {
    if (!message.trim()) return
    const id = ++nextId.current
    setItems(current => {
      const matches = (item: ToastMessage) => item.message === message && item.error === error
      if (!renew && current.some(matches)) return current
      return [...current.filter(item => !matches(item)).slice(-2), {id, message, error}]
    })
  }, [])
  const dismiss = useCallback((id: number) => setItems(current => current.filter(item => item.id !== id)), [])
  const clear = useCallback(() => setItems([]), [])
  const clearErrors = useCallback(() => setItems(current => current.filter(item => !item.error)), [])
  useEffect(() => {
    const sectionChange = (event: Event) => {
      if (event.target instanceof Element && event.target.closest('[role="tab"], nav button, nav a[href]')) clear()
    }
    const keyboardSectionChange = (event: KeyboardEvent) => {
      if (['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown', 'Home', 'End'].includes(event.key)) sectionChange(event)
    }
    window.addEventListener('hashchange', clear)
    window.addEventListener('popstate', clear)
    document.addEventListener('click', sectionChange, true)
    document.addEventListener('keydown', keyboardSectionChange, true)
    return () => {
      window.removeEventListener('hashchange', clear)
      window.removeEventListener('popstate', clear)
      document.removeEventListener('click', sectionChange, true)
      document.removeEventListener('keydown', keyboardSectionChange, true)
    }
  }, [clear])
  const context = useMemo(() => ({notify, clearErrors}), [notify, clearErrors])
  return <NotificationContext.Provider value={context}>{children}<ToastRegion items={items} dismiss={dismiss} /></NotificationContext.Provider>
}

export function ErrorFeedback({message, persistent = false}: {message?: string; persistent?: boolean}) {
  const notifications = useNotifications()
  return (!notifications || persistent) && message ? <p className="form-error" role={notifications ? undefined : 'alert'}>{message}</p> : null
}

export function ToastRegion({items, dismiss}: {items: ToastMessage[]; dismiss: (id: number) => void}) {
  const reference = useRef<HTMLDivElement>(null)
  const timings = useRef(new Map<number, {remaining: number}>())
  useEffect(() => {
    for (const id of timings.current.keys()) if (!items.some(item => item.id === id)) timings.current.delete(id)
  }, [items])
  const [host, setHost] = useState<Element>(document.body)
  useLayoutEffect(() => {
    let opened: HTMLDialogElement[] = []
    let currentHost: Element = document.body
    const update = (records: MutationRecord[]) => {
      if (records.length && currentHost.isConnected && !records.some(record => record.type === 'attributes' && record.target instanceof HTMLDialogElement)) return
      const additions = records.filter(record => record.type === 'attributes').map(record => record.target).filter((target): target is HTMLDialogElement => target instanceof HTMLDialogElement && target.isConnected && target.open)
      opened = [...opened.filter(dialog => dialog.isConnected && dialog.open && !additions.includes(dialog)), ...additions]
      const dialogs = document.querySelectorAll<HTMLDialogElement>('dialog[open]')
      currentHost = opened.at(-1) ?? dialogs[dialogs.length - 1] ?? document.body
      setHost(currentHost)
    }
    update([])
    const observer = new MutationObserver(update)
    observer.observe(document.body, {subtree: true, childList: true, attributes: true, attributeFilter: ['open']})
    return () => observer.disconnect()
  }, [])
  useLayoutEffect(() => {
    const region = reference.current
    if (!region?.showPopover || !region.isConnected || host instanceof HTMLDialogElement && !host.open) return
    // Keep notifications inside the active modal's non-inert subtree, and
    // use the top layer for viewport placement without stealing focus.
    if (region.matches(':popover-open')) region.hidePopover()
    if (items.length) region.showPopover()
  }, [items, host])
  return createPortal(<div ref={reference} popover={typeof HTMLElement.prototype.showPopover === 'function' ? 'manual' : undefined} className="toast-region" role="region" aria-label="Notifications">
    {items.map(item => {
      let timing = timings.current.get(item.id)
      if (!timing) {timing = {remaining: item.error ? 4000 : 3000}; timings.current.set(item.id, timing)}
      return <Toast key={item.id} item={item} dismiss={dismiss} timing={timing} />
    })}
  </div>, host)
}

function Toast({item, dismiss, timing}: {item: ToastMessage; dismiss: (id: number) => void; timing: {remaining: number}}) {
  const [hovered, setHovered] = useState(false)
  const [focused, setFocused] = useState(false)
  useEffect(() => {
    if (hovered || focused) return
    const started = Date.now()
    const timer = window.setTimeout(() => dismiss(item.id), timing.remaining)
    return () => {
      timing.remaining = Math.max(0, timing.remaining - (Date.now() - started))
      window.clearTimeout(timer)
    }
  }, [dismiss, item.id, hovered, focused, timing])
  return <div className={`toast ${item.error ? 'error' : 'success'}`} onMouseEnter={() => setHovered(true)} onMouseLeave={() => setHovered(false)} onFocus={() => setFocused(true)} onBlur={event => {if (!event.currentTarget.contains(event.relatedTarget)) setFocused(false)}}>
    <span className="toast-message" role={item.error ? 'alert' : 'status'}>{item.message}</span>
    <button type="button" className="icon-button" aria-label="Dismiss notification" onClick={() => dismiss(item.id)}><ActionIcon name="close" /></button>
  </div>
}
