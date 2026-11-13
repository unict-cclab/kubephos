import {expect,it} from 'vitest'
import {currentSection,isNavigableView,navigationTargets,visibleNavigation} from './navigation'
it('groups resource destinations without changing their base routes',()=>{
 expect(visibleNavigation(true).map(group=>[group.label,group.items.map(item=>item.label)])).toEqual([
 ['', ['Overview']],['Infrastructure',['Proxmox connections','VM templates','Harbor','NFS','Kubernetes clusters']],['',['Deployments']],['',['Catalog']],['Experiments',['Configurations','Runs','Comparisons']],['Administration',['Users','Requests','Notifications','Import and export']]
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

it('offers requests to everyone with wording for each role and import and export to administrators',()=>{
 const memberRequests=visibleNavigation(false).find(group=>group.id==='requests')!
 expect(memberRequests.items.map(item=>item.label)).toEqual(['Ask an admin'])
 expect(currentSection('requests',false)).toEqual({description:'Send a message to the administrators'})
 expect(currentSection('requests',true)).toEqual({description:'Messages from users to the administrators'})
 expect(visibleNavigation(true).flatMap(group=>group.items.map(item=>item.view))).toContain('administration')
 expect(visibleNavigation(false).flatMap(group=>group.items.map(item=>item.view))).not.toContain('administration')
 expect(navigationTargets(true).map(item=>item.view)).toContain('administration')
})
