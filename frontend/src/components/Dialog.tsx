import {useEffect, useRef, type PropsWithChildren, type ReactNode} from 'react'

interface DialogProps extends PropsWithChildren {
  open: boolean
  title: string
  eyebrow: string
  onClose?: () => void
  footer?: ReactNode
  className?: string
}

export function Dialog({open, title, eyebrow, onClose, footer, className = '', children}: DialogProps) {
  const reference = useRef<HTMLDialogElement>(null)

  useEffect(() => {
    const dialog = reference.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])

  return <dialog ref={reference} className={`modal ${className}`} onCancel={event => {
    if (!onClose) event.preventDefault()
    else onClose()
  }}>
    <div className="modal-content">
      <div className="modal-header">
        <div><p className="eyebrow">{eyebrow}</p><h2>{title}</h2></div>
        {onClose && <button className="icon-button" type="button" onClick={onClose} aria-label="Close">×</button>}
      </div>
      {children}
      {footer && <div className="modal-actions">{footer}</div>}
    </div>
  </dialog>
}
