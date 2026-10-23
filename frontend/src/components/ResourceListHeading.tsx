import {createContext, useContext, useState, type ReactNode} from 'react'
import {createPortal} from 'react-dom'
import {SectionIcon, type SectionIconKind} from './SectionIcon'

const ActionTarget = createContext<HTMLElement | null | undefined>(undefined)

export function ResourceListHeading({title, icon, action}: {title: string; icon?: SectionIconKind; action?: ReactNode}) {
  return <div className="section-heading resource-list-heading"><div>{icon && <SectionIcon kind={icon} />}<h2>{title}</h2></div>{action && <div className="resource-list-heading-actions">{action}</div>}</div>
}

// Catalog editors own their dialogs; their creation actions share the page heading.
export function ResourceListPage({title, icon, children}: {title: string; icon?: SectionIconKind; children: ReactNode}) {
  const [target, setTarget] = useState<HTMLDivElement | null>(null)
  return <ActionTarget.Provider value={target}><div className="resource-list-page"><ResourceListHeading title={title} icon={icon} action={<div ref={setTarget} className="resource-list-action-slot" />} />{children}</div></ActionTarget.Provider>
}

export function ResourceListAction({children}: {children: ReactNode}) {
  const target = useContext(ActionTarget)
  return target ? createPortal(children, target) : target === undefined ? <div className="resource-list-standalone-actions">{children}</div> : null
}
