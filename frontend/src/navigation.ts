import type {View} from './types'

export type SectionID = 'home' | 'platform' | 'lab' | 'team'

export interface NavigationItem {
  view: View
  label: string
  description: string
  adminOnly?: boolean
  // Shown as a step inside another page instead of as its own sidebar entry.
  within?: View
  // Reached from the account menu and the command palette instead of the sidebar.
  accountMenu?: boolean
}

export interface NavigationSection {
  id: SectionID
  label: string
  items: NavigationItem[]
}

// A sidebar entry also stays current for the pages it hosts as steps.
export interface SidebarItem extends NavigationItem {
  views: View[]
}

export interface SidebarSection {
  id: SectionID
  label: string
  items: SidebarItem[]
}

// Four destinations; each section reveals its pages only while it is open.
const sections: NavigationSection[] = [
  {id: 'home', label: 'Home', items: [
    {view: 'overview', label: 'Home', description: 'Setup progress and recent activity'}
  ]},
  {id: 'platform', label: 'Platform', items: [
    {view: 'infrastructure', label: 'Infrastructure', description: 'Proxmox connections, VM templates, Harbor and NFS', adminOnly: true, within: 'kubernetes'},
    {view: 'kubernetes', label: 'Clusters', description: 'Provision infrastructure, create clusters and deploy applications'},
    {view: 'deployments', label: 'Deployments', description: 'Applications installed on clusters', within: 'kubernetes'}
  ]},
  {id: 'lab', label: 'Lab', items: [
    {view: 'catalog', label: 'Library', description: 'Applications, strategies and AI models'},
    {view: 'results', label: 'Experiments', description: 'Configure an experiment, run it and compare the results'},
    {view: 'experiments', label: 'Configurations', description: 'Reusable experiment setups', within: 'results'},
    {view: 'comparisons', label: 'Comparisons', description: 'Saved comparisons of results', within: 'results'}
  ]},
  {id: 'team', label: 'Team', items: [
    {view: 'users', label: 'Members', description: 'Accounts and cluster access', adminOnly: true},
    {view: 'administration', label: 'Backup and transfer', description: 'Export and import platform state', adminOnly: true, accountMenu: true}
  ]}
]

function permitted(item: NavigationItem, admin: boolean): boolean {
  return admin || !item.adminOnly
}

export function visibleNavigation(admin: boolean): SidebarSection[] {
  return sections
    .map(section => {
      const allowed = section.items.filter(item => permitted(item, admin))
      const items = allowed
        .filter(item => !item.within && !item.accountMenu)
        .map(item => ({...item, views: [item.view, ...allowed.filter(child => child.within === item.view).map(child => child.view)]}))
      return {id: section.id, label: section.label, items}
    })
    .filter(section => section.items.length > 0)
}

// Every page the user may open, including steps and account pages.
export function navigationTargets(admin: boolean): NavigationItem[] {
  return sections.flatMap(section => section.items.filter(item => permitted(item, admin)))
}

export interface CurrentSection {
  group?: string
  description: string
}

// Heading context for a view: its section name (only when the section lists more
// than one sidebar entry) and the purpose of the page that hosts it.
export function currentSection(view: View): CurrentSection | null {
  for (const section of sections) {
    const item = section.items.find(candidate => candidate.view === view)
    if (!item) continue
    const host = section.items.find(candidate => candidate.view === item.within) ?? item
    const entries = section.items.filter(candidate => !candidate.within && !candidate.accountMenu).length
    return {group: entries > 1 ? section.label : undefined, description: host.description}
  }
  return null
}

export function isNavigableView(view: string): view is View {
  return sections.some(section => section.items.some(item => item.view === view))
}
