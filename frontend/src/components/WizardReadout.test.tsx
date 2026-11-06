import {fireEvent,render,screen} from '@testing-library/react'
import {expect,it} from 'vitest'
import {WizardReadout} from './WizardReadout'
import {FormTabs} from './FormTabs'
it('allows direct section navigation and preserves the selected subsection without exposing save actions',()=>{
 render(<WizardReadout sections={[{id:'general',title:'General',content:<p>Cluster A</p>},{id:'application',title:'Application',content:<FormTabs label="Application settings" items={[{id:'resources',title:'Resources',content:<p>100m</p>},{id:'placement',title:'Placement',content:<p>hardware=gpu</p>}]}/>},{id:'monitoring',title:'Monitoring',content:<p>15s</p>}]}/>)
 fireEvent.click(screen.getByRole('button',{name:'Application'}))
 fireEvent.click(screen.getByRole('tab',{name:'Placement'}))
 fireEvent.click(screen.getByRole('button',{name:'Monitoring'}))
 expect(screen.getByText('15s')).toBeVisible()
 fireEvent.click(screen.getByRole('button',{name:'Application'}))
 expect(screen.getByRole('tab',{name:'Placement'})).toHaveAttribute('aria-selected','true')
 expect(screen.getByText('hardware=gpu')).toBeVisible()
 expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'Previous'})).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:'Next'})).not.toBeInTheDocument()
 expect(screen.queryByRole('button',{name:/save|create/i})).not.toBeInTheDocument()
})
