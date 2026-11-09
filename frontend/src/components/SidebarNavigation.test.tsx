import {expect,it,vi} from 'vitest'
import {fireEvent,render,screen} from '@testing-library/react'
import {visibleNavigation} from '../navigation'
import {SidebarNavigation} from './SidebarNavigation'
it('opens each infrastructure submenu directly and preserves experiment route identities',()=>{
 window.location.hash='#infrastructure/templates'
 const navigate=vi.fn()
 render(<SidebarNavigation sections={visibleNavigation(true)} view="infrastructure" navigate={navigate}/>)
 expect(screen.getByRole('button',{name:'VM templates'})).toHaveAttribute('aria-current','page')
 expect(screen.getByRole('button',{name:'Infrastructure'})).toHaveAttribute('aria-expanded','true')
 fireEvent.click(screen.getByRole('button',{name:'Harbor'}))
 expect(navigate).toHaveBeenCalledWith('infrastructure','infrastructure/harbor')
 fireEvent.click(screen.getByRole('button',{name:'Experiments'}))
 fireEvent.click(screen.getByRole('button',{name:'Runs'}))
 expect(navigate).toHaveBeenCalledWith('results')
})

it('keeps grouped destinations collapsed until expanded and gives direct destinations the same row style',()=>{
 const navigate=vi.fn();render(<SidebarNavigation sections={visibleNavigation(true)} view="catalog" navigate={navigate}/>)
 const infra=screen.getByRole('button',{name:'Infrastructure'})
 expect(infra).toHaveAttribute('aria-expanded','false')
 expect(screen.queryByRole('button',{name:'VM templates'})).not.toBeInTheDocument()
 fireEvent.click(infra)
 expect(screen.getByRole('button',{name:'VM templates'})).toBeVisible()
 expect(navigate).not.toHaveBeenCalled()
 fireEvent.click(infra)
 expect(screen.queryByRole('button',{name:'VM templates'})).not.toBeInTheDocument()
 expect(screen.getByRole('button',{name:'Catalog'})).toHaveClass('app-nav-section-link')
})

it('keeps only the group of the current page open after navigation',()=>{
 const navigate=vi.fn()
 const {rerender}=render(<SidebarNavigation sections={visibleNavigation(true)} view="kubernetes" navigate={navigate}/>)
 fireEvent.click(screen.getByRole('button',{name:'Experiments'}))
 expect(screen.getByRole('button',{name:'Infrastructure'})).toHaveAttribute('aria-expanded','true')
 expect(screen.getByRole('button',{name:'Experiments'})).toHaveAttribute('aria-expanded','true')
 rerender(<SidebarNavigation sections={visibleNavigation(true)} view="users" navigate={navigate}/>)
 expect(screen.getByRole('button',{name:'Infrastructure'})).toHaveAttribute('aria-expanded','false')
 expect(screen.getByRole('button',{name:'Experiments'})).toHaveAttribute('aria-expanded','false')
 expect(screen.getByRole('button',{name:'Administration'})).toHaveAttribute('aria-expanded','true')
})
