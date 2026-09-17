import {useEffect} from 'react'

export interface ToastMessage {
  id: number
  message: string
  error: boolean
}

export function ToastRegion({items, dismiss}: {items: ToastMessage[]; dismiss: (id: number) => void}) {
  return <div className="toast-region" aria-live="polite" aria-atomic="false">
    {items.map(item => <Toast key={item.id} item={item} dismiss={dismiss} />)}
  </div>
}

function Toast({item, dismiss}: {item: ToastMessage; dismiss: (id: number) => void}) {
  useEffect(() => {
    const timer = window.setTimeout(() => dismiss(item.id), item.error ? 12000 : 5000)
    return () => window.clearTimeout(timer)
  }, [dismiss, item.error, item.id])
  return <button className={`toast ${item.error ? 'error' : ''}`} role={item.error ? 'alert' : 'status'} onClick={() => dismiss(item.id)}>{item.message}</button>
}
