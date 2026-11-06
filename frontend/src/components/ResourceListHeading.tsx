import {createContext, useContext, useState, type ReactNode} from 'react'
import {createPortal} from 'react-dom'
import type {CurrentSection} from '../navigation'
import {SectionIcon, type SectionIconKind} from './SectionIcon'

export interface SectionHeadingContext extends CurrentSection {
  // Page-level navigation shown under the heading, such as workflow steps.
  subnav?: ReactNode
}

const ActionTarget = createContext<HTMLElement | null | undefined>(undefined)

// Provided by the application shell so every section heading names its group and purpose.
export const SectionContext = createContext<SectionHeadingContext | null>(null)

export function ResourceListHeading({title, icon, action}: {title: string; icon?: SectionIconKind; action?: ReactNode}) {
  const section = useContext(SectionContext)
  return <><div className="section-heading resource-list-heading">
    <div>
      {icon && <SectionIcon kind={icon} />}
      {section?.group && <p className="page-eyebrow">{section.group}</p>}
      <h2>{title}</h2>
    </div>
    {action && <div className="resource-list-heading-actions">{action}</div>}
  </div>{section?.subnav}</>
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
