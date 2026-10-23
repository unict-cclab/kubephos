import {useState, type ReactNode} from 'react'
import {ActionIcon} from './ActionIcon'

export function FormDisclosure({title, children, defaultOpen = false, count, className = ''}: {title: string; children: ReactNode; defaultOpen?: boolean; count?: number; className?: string}) {
  const [expanded, setExpanded] = useState(defaultOpen)
  return <details className={`form-disclosure ${className}`} open={expanded} onToggle={event => setExpanded(event.currentTarget.open)}>
    <summary><ActionIcon name={expanded ? 'down' : 'right'} /><strong>{title}</strong>{count !== undefined && <span>{count}</span>}</summary>
    <div className="form-disclosure-body">{children}</div>
  </details>
}
