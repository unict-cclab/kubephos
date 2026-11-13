import {fireEvent, render, screen, within} from '@testing-library/react'
import {expect, it, vi} from 'vitest'
import {ResultsView} from './ResultsView'
import type {Experiment} from '../types'
vi.mock('../usePreparedComparison',()=>({usePreparedComparison:()=>({result:null,progress:'Preparing comparison…',error:''})}))
it('opens Sources first and keeps it usable while aggregate results are preparing',()=>{
 const experiment={id:'cmp',name:'Comparison',kind:'comparison',status:'succeeded',variants:[]} as unknown as Experiment
 render(<ResultsView artifacts={[]} experiments={[]} operations={[]} workspaces={[]} session={{authenticated:true}} changed={async()=>{}} openOperation={()=>{}} focusedExperiment={experiment} onBack={()=>{}} comparison={{sourceConfigurations:{},sources:<table aria-label="Comparison source experiments"><tbody><tr><td>Selected source</td></tr></tbody></table>}}/>)
 const nav=within(screen.getByRole('navigation',{name:'Result sections'}))
 expect(nav.getAllByRole('button').map(button=>button.textContent)).toEqual(['Sources','Statistics','Plots','Export'])
 expect(nav.getByRole('button',{name:'Sources'})).toHaveAttribute('aria-current','page')
 expect(screen.getByRole('table',{name:'Comparison source experiments'})).toBeVisible()
 fireEvent.click(nav.getByRole('button',{name:'Statistics'}))
 expect(screen.queryByRole('table',{name:'Comparison source experiments'})).not.toBeInTheDocument()
 expect(screen.getByText('Preparing comparison…')).toBeVisible()
 fireEvent.click(nav.getByRole('button',{name:'Sources'}))
 expect(screen.getByText('Selected source')).toBeVisible()
 expect(screen.getByRole('button',{name:'Back to Comparisons'}).closest('.page-header')).toContainElement(screen.getByRole('heading',{name:'Comparison'}))
})
