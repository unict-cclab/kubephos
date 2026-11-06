import type {View} from './types'

export interface NavigationItem {
  view: View
  label: string
  description: string
  adminOnly?: boolean
  hash?: string
}
export interface SidebarItem extends NavigationItem { views: View[] }
export interface SidebarSection { id: string; label: string; items: SidebarItem[] }

const destinations: NavigationItem[] = [
  {view: 'overview', label: 'Overview', description: 'Setup progress and recent activity'},
  {view: 'infrastructure', label: 'Infrastructure', description: 'Proxmox connections, VM templates, Harbor and NFS', adminOnly: true},
  {view: 'kubernetes', label: 'Kubernetes', description: 'Provision infrastructure, create clusters and deploy applications'},
  {view: 'deployments', label: 'Deployments', description: 'Applications installed on clusters'},
  {view: 'catalog', label: 'Catalog', description: 'Applications, strategies and AI models'},
  {view: 'experiments', label: 'Configurations', description: 'Reusable experiment setups'},
  {view: 'results', label: 'Runs', description: 'Configure an experiment, run it and compare the results'},
  {view: 'comparisons', label: 'Comparisons', description: 'Saved comparisons of results'},
  {view: 'users', label: 'Users', description: 'Accounts and cluster access', adminOnly: true},
  {view: 'administration', label: 'Import and export', description: 'Export and import platform state', adminOnly: true},
]

export function navigationTargets(admin: boolean): NavigationItem[] {
  return destinations.filter(item => admin || !item.adminOnly)
}

export function visibleNavigation(admin: boolean): SidebarSection[] {
  const targets=navigationTargets(admin)
  const item=(view:View,label?:string,hash?:string):SidebarItem=>({...targets.find(item=>item.view===view)!,...(label?{label}:{}),...(hash?{hash}:{}),views:[view]})
  return [
    {id:'overview',label:'',items:[item('overview')]},
    {id:'infrastructure',label:'Infrastructure',items:[...(admin?[
      item('infrastructure','Proxmox connections','infrastructure/connections'),item('infrastructure','VM templates','infrastructure/templates'),item('infrastructure','Harbor','infrastructure/harbor'),item('infrastructure','NFS','infrastructure/nfs')
    ]:[]),item('kubernetes','Kubernetes clusters')]},
    {id:'deployments',label:'',items:[item('deployments')]},
    {id:'catalog',label:'',items:[item('catalog')]},
    {id:'experiments',label:'Experiments',items:[item('experiments'),item('results'),item('comparisons')]},
    ...(admin?[{id:'administration',label:'Administration',items:[item('users'),item('administration')]}]:[])
  ]
}

export interface CurrentSection { group?: string; description: string }
export function currentSection(view: View): CurrentSection | null {
  const item = destinations.find(item => item.view === view)
  return item ? {description: item.description} : null
}
export function isNavigableView(view: string): view is View {
  return destinations.some(item => item.view === view)
}
