import {fireEvent, render, screen} from '@testing-library/react'
import {expect, it, vi} from 'vitest'
import {ResultsView} from './ResultsView'
import type {Experiment} from '../types'
vi.mock('./results/kubestudio/resultsLayout', async () => (await import('../test/everySection')).everySection())
vi.mock('../usePreparedComparison',()=>({usePreparedComparison:()=>({result:null,progress:'Preparing comparison…',error:''})}))
it('keeps Sources one click away while aggregate results are preparing',()=>{
 const experiment={id:'cmp',name:'Comparison',kind:'comparison',status:'succeeded',variants:[]} as unknown as Experiment
 render(<ResultsView artifacts={[]} experiments={[]} operations={[]} workspaces={[]} session={{authenticated:true}} changed={async()=>{}} openOperation={()=>{}} focusedExperiment={experiment} onBack={()=>{}} comparison={{sourceConfigurations:{},sources:<table aria-label="Comparison source experiments"><tbody><tr><td>Selected source</td></tr></tbody></table>}}/>)
 expect(screen.getByText('Preparing comparison…')).toBeVisible()
 const sources=screen.getByRole('button',{name:/^Sources/})
 expect(sources).toHaveAttribute('aria-expanded','false')
 expect(screen.queryByRole('table',{name:'Comparison source experiments'})).not.toBeInTheDocument()
 fireEvent.click(sources)
 expect(screen.getByRole('table',{name:'Comparison source experiments'})).toBeVisible()
 expect(screen.getByText('Preparing comparison…')).toBeVisible()
 fireEvent.click(sources)
 expect(screen.queryByText('Selected source')).not.toBeInTheDocument()
 expect(screen.getByRole('button',{name:'Back to Comparisons'}).closest('.page-header')).toContainElement(screen.getByRole('heading',{name:'Comparison'}))
})
