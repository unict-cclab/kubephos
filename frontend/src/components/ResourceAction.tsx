import {ActionIcon} from './ActionIcon'

type Action = 'view' | 'delete' | 'create' | 'add' | 'remove' | 'edit' | 'archive'

export function ResourceAction({action, label, onClick, disabled, title}: {action: Action; label: string; onClick: () => void; disabled?: boolean; title?: string}) {
  const text = action === 'create' || action === 'add'
  const icon = action === 'add' ? 'create' : action === 'remove' ? 'close' : action
  return <button type="button" className={`button ${action === 'create' ? 'primary' : text ? 'secondary' : 'secondary icon-button'} resource-action action-${action}`} aria-label={label} title={title ?? label} onClick={onClick} disabled={disabled}><ActionIcon name={icon} />{text && <span>{label}</span>}</button>
}
