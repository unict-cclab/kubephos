import {cleanup, fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {ChartExportActions} from './ChartExportActions'
import {exportChart} from '../charts'

vi.mock('../charts',()=>({exportChart:vi.fn()}))
afterEach(()=>{cleanup();vi.resetAllMocks()})

it('shows export progress, prevents duplicate submissions and leaves unrelated controls enabled',async()=>{
 let finish!:()=>void
 vi.mocked(exportChart).mockReturnValue(new Promise<void>(resolve=>{finish=resolve}))
 const unrelated=vi.fn()
 render(<><ChartExportActions chartID="plot" metric="cpu"/><button onClick={unrelated}>Another action</button></>)
 fireEvent.click(screen.getByRole('button',{name:'PDF'}))
 expect(screen.getByRole('button',{name:'Preparing PDF'})).toBeDisabled()
 expect(screen.getByRole('button',{name:'PNG'})).toBeDisabled()
 expect(screen.getByRole('status')).toHaveTextContent('Preparing PDF…')
 expect(screen.getByRole('group',{name:'Export plot'})).toHaveAttribute('aria-busy','true')
 fireEvent.click(screen.getByRole('button',{name:'PNG'}))
 fireEvent.click(screen.getByRole('button',{name:'Another action'}))
 expect(unrelated).toHaveBeenCalledOnce()
 expect(exportChart).toHaveBeenCalledExactlyOnceWith('plot','cpu','pdf')
 finish()
 await waitFor(()=>expect(screen.getByRole('button',{name:'PDF'})).toBeEnabled())
 expect(screen.getByRole('status')).toBeEmptyDOMElement()
})

it('clears loading after a failure and permits retry in either format',async()=>{
 vi.mocked(exportChart).mockRejectedValueOnce(new Error('Could not render PNG figure.')).mockResolvedValueOnce(undefined)
 render(<ChartExportActions chartID="plot" metric="cpu"/>)
 fireEvent.click(screen.getByRole('button',{name:'PNG'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('Could not render PNG figure.')
 await waitFor(()=>expect(screen.getByRole('button',{name:'PNG'})).toBeEnabled())
 fireEvent.click(screen.getByRole('button',{name:'PDF'}))
 await waitFor(()=>expect(exportChart).toHaveBeenCalledTimes(2))
 await waitFor(()=>expect(screen.queryByRole('alert')).not.toBeInTheDocument())
})
