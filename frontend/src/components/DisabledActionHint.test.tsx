import {fireEvent,render,screen} from '@testing-library/react'
import {expect,it} from 'vitest'
import {DisabledActionHint} from './DisabledActionHint'
it('explains disabled actions on hover and keyboard focus without moving the button',()=>{
 render(<DisabledActionHint disabled reason="Create two configurations first."><button disabled>Comparison</button></DisabledActionHint>)
 const wrapper=screen.getByRole('button',{name:'Comparison'}).parentElement!
 expect(wrapper).toHaveAttribute('tabindex','0')
 expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
 fireEvent.mouseEnter(wrapper)
 expect(screen.getByRole('tooltip')).toHaveTextContent('Create two configurations first.')
 fireEvent.mouseLeave(wrapper)
 expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
 fireEvent.focus(wrapper)
 expect(screen.getByRole('tooltip')).toBeVisible()
 fireEvent.blur(wrapper)
 expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
})
it('leaves enabled actions unchanged',()=>{
 render(<DisabledActionHint disabled={false} reason="Unavailable"><button>Comparison</button></DisabledActionHint>)
 const button=screen.getByRole('button',{name:'Comparison'})
 expect(button).toBeEnabled()
 expect(button.parentElement).not.toHaveAttribute('tabindex')
 fireEvent.mouseEnter(button.parentElement!)
 expect(screen.queryByRole('tooltip')).not.toBeInTheDocument()
})
