import {useEffect, useId, useRef, useState} from 'react'
import {Dialog} from './Dialog'

export function useDeleteConfirmation() {
  const [message, setMessage] = useState<string | null>(null)
  const [options, setOptions] = useState({title: 'Confirm deletion', label: 'Delete'})
  const resolver = useRef<((confirmed: boolean) => void) | null>(null)
  const descriptionId = useId()

  useEffect(() => () => {
    resolver.current?.(false)
    resolver.current = null
  }, [])

  const settle = (confirmed: boolean) => {
    const resolve = resolver.current
    resolver.current = null
    setMessage(null)
    resolve?.(confirmed)
  }

  const confirmAction = (description: string, presentation = {title: 'Confirm deletion', label: 'Delete'}) => new Promise<boolean>(resolve => {
    if (resolver.current) {
      resolve(false)
      return
    }
    resolver.current = resolve
    setOptions(presentation)
    setMessage(description)
  })

  const confirmationDialog = <Dialog open={message !== null} title={options.title} onClose={() => settle(false)} className="confirmation-modal" describedBy={descriptionId} footer={<>
    <button type="button" className="button secondary" autoFocus onClick={() => settle(false)}>Cancel</button>
    <button type="button" className="button danger" onClick={() => settle(true)}>{options.label}</button>
  </>}>
    <p id={descriptionId}>{message}</p>
  </Dialog>

  return {confirmDelete: (description: string) => confirmAction(description), confirmAction, confirmationDialog}
}
