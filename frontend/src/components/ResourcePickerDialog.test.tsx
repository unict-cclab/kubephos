import {fireEvent, render, screen} from '@testing-library/react'
import {expect, it, vi} from 'vitest'
import {ResourcePickerDialog} from './ResourcePickerDialog'
import type {Session} from '../types'
const session = {authenticated: true, user: {id: 'alice', username: 'alice'}} as Session
const items = [{id:'a',name:'Alpha',ownerUserId:'alice'}, {id:'b',name:'Beta',ownerUserId:'alice'}, {id:'c',name:'Gamma',ownerUserId:'bob'}]
it('excludes already added items and retains pending choices across searches and owners', () => {
 const add = vi.fn()
 render(<ResourcePickerDialog resource="experiments" items={items} selectedIds={['a']} session={session} close={()=>{}} add={add}/>)
 expect(screen.getByRole('dialog',{name:'Add experiments'}).parentElement).toBe(document.body)
 expect(screen.getByLabelText('Filter by user')).toHaveValue('alice')
 expect(screen.queryByRole('checkbox',{name:'Select Alpha'})).not.toBeInTheDocument()
 expect(screen.queryByRole('checkbox',{name:'Select Gamma'})).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('checkbox',{name:'Select Beta'}))
 fireEvent.change(screen.getByLabelText('Filter by user'),{target:{value:''}})
 fireEvent.change(screen.getByRole('searchbox'),{target:{value:'Gamma'}})
 fireEvent.click(screen.getByRole('checkbox',{name:'Select Gamma'}))
 fireEvent.change(screen.getByRole('searchbox'),{target:{value:''}})
 expect(screen.getByRole('checkbox',{name:'Select Beta'})).toBeChecked()
 fireEvent.click(screen.getByRole('button',{name:'Add selected'}))
 expect(add).toHaveBeenCalledWith([items[1],items[2]])
})
it('limits selection to eight total and permits deselection', () => {
 render(<ResourcePickerDialog resource="configurations" items={items} selectedIds={['1','2','3','4','5','6','7']} session={session} close={()=>{}} add={()=>{}}/>)
 fireEvent.click(screen.getByRole('checkbox',{name:'Select Alpha'}))
 expect(screen.getByRole('checkbox',{name:'Select Beta'})).toBeDisabled()
 fireEvent.click(screen.getByRole('checkbox',{name:'Select Alpha'}))
 expect(screen.getByRole('checkbox',{name:'Select Beta'})).toBeEnabled()
})
it('paginates large lists without dropping search matches', () => {
 const many = Array.from({length:100},(_,i)=>({id:String(i),name:`Config ${i}`}))
 render(<ResourcePickerDialog resource="configurations" items={many} selectedIds={[]} session={{authenticated:true}} close={()=>{}} add={()=>{}}/>)
 expect(screen.getAllByRole('checkbox')).toHaveLength(40)
 fireEvent.change(screen.getByRole('searchbox'),{target:{value:'Config 99'}})
 expect(screen.getByRole('checkbox',{name:'Select Config 99'})).toBeInTheDocument()
})
