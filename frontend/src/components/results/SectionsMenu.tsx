import {useState} from 'react'
import {anchorAtButton, ContextMenu, type MenuAnchor, type MenuItem} from './kubestudio/ContextMenu'
import {pageSections, type PageSection} from './kubestudio/resultsLayout'

// "Display", in the page header next to Export: which parts of the results page are shown. One switch per
// section (menuitemcheckbox, aria-checked), the hidden ones dimmed, each with what it holds;
// the menu stays open while switching, and "Show all" turns every one on. Charts are always there.

const descriptions: Record<PageSection, string> = {
  summary: 'Verdict and key numbers',
  timeline: 'Request rate and latency over time',
  services: 'Health of each service',
  sources: 'The experiments compared',
  endpoints: 'Locust statistics per request',
  callchain: 'Calls between services',
  statistics: 'Tables per metric and comparisons',
  environment: 'Cluster, versions and methods text',
  placement: 'Where the pods ran, replayed'
}

export function SectionsMenu({available, shown, onToggle, onShowAll, details = {}}: {available: PageSection[]; shown: PageSection[]; onToggle: (id: PageSection, shown: boolean) => void; onShowAll: () => void; details?: Partial<Record<PageSection, string>>}) {
  const [menu, setMenu] = useState<MenuAnchor>()
  if (!available.length) return null
  const sections = pageSections.filter(section => available.includes(section.id))
  const hidden = sections.filter(section => !shown.includes(section.id))
  // Charts count as one, always shown.
  const count = sections.length - hidden.length + 1
  const items: MenuItem[] = [
    {label: 'Charts', hint: 'Always shown', checked: true, disabled: true, run: () => {}},
    ...sections.map((section): MenuItem => ({label: section.label, hint: details[section.id] ?? descriptions[section.id], checked: shown.includes(section.id), keepOpen: true, run: () => onToggle(section.id, !shown.includes(section.id))})),
    {label: 'Show all', icon: 'view', separated: true, disabled: !hidden.length, run: onShowAll}
  ]
  return <>
    <button type="button" className="button secondary display-menu" aria-haspopup="menu" aria-expanded={Boolean(menu)} title={`Choose what this page shows (${count} of ${available.length + 1} sections shown)`} onClick={event => setMenu(anchorAtButton(event.currentTarget))}>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d="M4 7h10M18 7h2M4 17h4M12 17h8" /><circle cx="16" cy="7" r="2" /><circle cx="10" cy="17" r="2" /></svg>
      <span className="display-menu-label">Display</span><span className="visually-hidden">: {count} of {available.length + 1} sections shown</span>
    </button>
    {menu && <ContextMenu at={menu} label="Sections of this page" className="sections-menu" title="Shown on this page" items={items} onClose={() => setMenu(undefined)} />}
  </>
}
