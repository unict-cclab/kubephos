type Action = 'view' | 'delete' | 'create'

export function ResourceAction({action, label, onClick, disabled, title}: {action: Action; label: string; onClick: () => void; disabled?: boolean; title?: string}) {
  const shapes = {
    view: <><path d="M2.5 12s3.5-5.5 9.5-5.5 9.5 5.5 9.5 5.5-3.5 5.5-9.5 5.5S2.5 12 2.5 12z"/><circle cx="12" cy="12" r="2.5"/></>,
    delete: <><path d="M4 7h16M9 7V4h6v3m-9 0 1 13h10l1-13M10 11v5m4-5v5"/></>,
    create: <><path d="M12 5v14M5 12h14"/></>
  }
  return <button type="button" className={`resource-action action-${action}`} aria-label={label} title={title ?? label} onClick={onClick} disabled={disabled}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{shapes[action]}</svg></button>
}
