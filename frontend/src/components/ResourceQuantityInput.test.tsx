import {fireEvent,render,screen} from '@testing-library/react'
import {useState} from 'react'
import {expect,it} from 'vitest'
import {ResourceQuantityInput,convertResourceUnit} from './ResourceQuantityInput'

it('converts units without changing the requested amount',()=>{
 expect(convertResourceUnit('500m','')).toBe('0.5')
 expect(convertResourceUnit('1.25','m')).toBe('1250m')
 expect(convertResourceUnit('1536Mi','Gi')).toBe('1.5Gi')
 expect(convertResourceUnit('0','m')).toBe('0m')
 expect(convertResourceUnit('1Gi','M')).toBe('1073.741824M')
})
function Editor(){const [value,setValue]=useState('500m');return <form><ResourceQuantityInput name="cpuRequest" label="CPU request" kind="cpu" value={value} onChange={setValue}/></form>}
it('keeps the Kubernetes quantity in submitted values and leaves blank fields absent',()=>{
 render(<Editor/>);expect(screen.getByRole('textbox',{name:'CPU request'})).toHaveValue('500')
 fireEvent.change(screen.getByRole('combobox',{name:'CPU request unit'}),{target:{value:''}})
 expect(screen.getByRole('textbox',{name:'CPU request'})).toHaveValue('0.5')
 expect(new FormData(document.querySelector('form')!).get('cpuRequest')).toBe('0.5')
 fireEvent.change(screen.getByRole('textbox',{name:'CPU request'}),{target:{value:''}})
 expect(new FormData(document.querySelector('form')!).get('cpuRequest')).toBe('')
 fireEvent.change(screen.getByRole('textbox',{name:'CPU request'}),{target:{value:'0.'}})
 expect(screen.getByRole('textbox',{name:'CPU request'})).toHaveValue('0.')
 fireEvent.change(screen.getByRole('textbox',{name:'CPU request'}),{target:{value:'0.75'}})
 expect(new FormData(document.querySelector('form')!).get('cpuRequest')).toBe('0.75')
})
