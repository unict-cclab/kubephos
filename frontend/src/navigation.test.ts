import {expect,it} from 'vitest'
import {currentSection,isNavigableView,navigationTargets,visibleNavigation} from './navigation'
it('groups resource destinations without changing their base routes',()=>{
 expect(visibleNavigation(true).map(group=>[group.label,group.items.map(item=>item.label)])).toEqual([
 ['', ['Overview']],['Infrastructure',['Proxmox connections','VM templates','Harbor','NFS','Kubernetes clusters']],['',['Deployments']],['',['Catalog']],['Experiments',['Configurations','Runs','Comparisons']],['Administration',['Users','Import and export']]
 ])
 const infrastructure=visibleNavigation(true)[1].items
 expect(infrastructure.slice(0,4).map(item=>item.hash)).toEqual(['infrastructure/connections','infrastructure/templates','infrastructure/harbor','infrastructure/nfs'])
})
it('keeps administrator-only sections inaccessible to normal users',()=>{
 for(const view of ['infrastructure','users','administration'])expect(navigationTargets(false).map(item=>item.view)).not.toContain(view)
 expect(visibleNavigation(false).flatMap(group=>group.items.map(item=>item.label))).toContain('Kubernetes clusters')
 expect(visibleNavigation(false).some(group=>group.label==='Administration')).toBe(false)
})
it('keeps existing deep-link view identities valid',()=>{
 expect(currentSection('catalog')).toEqual({description:'Applications, strategies and AI models'})
 expect(isNavigableView('results')).toBe(true)
 expect(isNavigableView('unknown')).toBe(false)
})
