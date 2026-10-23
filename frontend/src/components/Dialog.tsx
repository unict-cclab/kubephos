import {ActionIcon} from './ActionIcon'
import {EditorPage} from './EditorPage'
import {useEffect, useId, useRef, type PropsWithChildren, type ReactNode} from 'react'

interface DialogProps extends PropsWithChildren {
  open: boolean
  title: string
  eyebrow?: string
  onClose?: () => void
  footer?: ReactNode
  className?: string
  describedBy?: string
  presentation?: 'dialog' | 'page'
}

export function Dialog({open, title, eyebrow, onClose, footer, className = '', describedBy, children, presentation = 'dialog'}: DialogProps) {
  const reference = useRef<HTMLDialogElement>(null)
  const titleId = useId()
  const page = presentation === 'page'

  useEffect(() => {
    if (page) return
    const dialog = reference.current
    if (!dialog) return
    if (!open) return
    const previous = document.activeElement instanceof HTMLElement ? document.activeElement : null
    if (!dialog.open) dialog.showModal()
    dialog.querySelector<HTMLElement>('.modal-body input:not([type="hidden"]):not([disabled]):not([readonly]), .modal-body select:not([disabled]), .modal-body textarea:not([disabled]):not([readonly])')?.focus({preventScroll: true})
    return () => {
      if (dialog.open) dialog.close()
      if (previous?.isConnected && !dialog.contains(previous)) previous.focus({preventScroll: true})
    }
  }, [open, page])

  if (page) return open ? <EditorPage title={title} onClose={onClose} className={className}>{children}{footer && <div className="modal-actions">{footer}</div>}</EditorPage> : null

  return <dialog ref={reference} className={`modal ${className}`} aria-labelledby={titleId} aria-describedby={describedBy} onCancel={event => {
    if (!onClose) event.preventDefault()
    else onClose()
  }}>
    <div className="modal-content">
      <div className="modal-header">
        <div>{eyebrow && <span className="eyebrow">{eyebrow}</span>}<h2 id={titleId}>{title}</h2></div>
        {onClose && <button className="icon-button" type="button" onClick={onClose} aria-label="Close"><ActionIcon name="close" /></button>}
      </div>
      <div className="modal-body">{children}</div>
      {footer && <div className="modal-actions">{footer}</div>}
    </div>
  </dialog>
}
