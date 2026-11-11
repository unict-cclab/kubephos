import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, expect, it, vi} from 'vitest'
import {request} from '../api'
import {ComparisonDialog, ComparisonsView} from './ComparisonsView'
import type {Experiment, Session} from '../types'
import type {ResultComparison} from '../comparisons'
vi.mock('../api', () => ({request: vi.fn()}))
const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'alice', role: 'user'}} as Session
const experiments = ['first', 'second'].map((id, i) => ({id, name: `Experiment ${i + 1}`, ownerUserId: 'alice', ownerUsername: 'alice', kind: 'instance', status: 'succeeded', variants: [{id: `v-${id}`, name: 'NAS', alias: 'NAS', color: '#123456', configuration: {}, trials: [{id: `t-${id}`, position: 1, status: 'succeeded', resultArtifactId: `data-${id}`}]}]})) as Experiment[]
beforeEach(() => {vi.mocked(request).mockReset(); window.location.hash = '#comparisons'})
it('selects whole experiments with separate colors even when aliases match and execution counts differ', async () => {
 const saved = vi.fn(); vi.mocked(request).mockResolvedValue({id: 'comparison'} as never)
 const repeated = {...experiments[1], variants: experiments[1].variants.map(variant => ({...variant, trials: [variant.trials[0], {...variant.trials[0], id: 'extra', position: 2, resultArtifactId: 'extra-data'}]}))}
 render(<ComparisonDialog experiments={[experiments[0], repeated]} session={session} close={() => {}} saved={saved} />)
 fireEvent.change(screen.getByLabelText('Name'), {target: {value: 'Paper'}})
 fireEvent.click(screen.getByRole('button', {name: 'Add experiments'}))
 fireEvent.click(screen.getByRole('checkbox', {name: 'Select Experiment 1'}))
 fireEvent.click(screen.getByRole('checkbox', {name: 'Select Experiment 2'}))
 fireEvent.click(screen.getByRole('button', {name: 'Add selected'}))
 fireEvent.change(screen.getByLabelText('Experiment 1 plot label'), {target: {value: 'Baseline'}})
 fireEvent.change(screen.getByLabelText('Experiment 2 plot color'), {target: {value: '#009e73'}})
 expect(screen.queryByRole('button', {name: 'Series'})).not.toBeInTheDocument()
 expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
 expect(screen.queryByRole('searchbox')).not.toBeInTheDocument()
 expect(screen.queryByText(/execution/i)).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button', {name: 'Create comparison'}))
 await waitFor(() => expect(saved).toHaveBeenCalled())
 expect(request).toHaveBeenCalledTimes(1)
 const [path, options, token] = vi.mocked(request).mock.calls[0]
 expect(path).toBe('/comparisons'); expect(token).toBe('csrf')
 expect(JSON.parse(String(options?.body))).toEqual({name: 'Paper', experiments: [{experimentId: 'first', color: '#1f77b4', label: 'Baseline'}, {experimentId: 'second', color: '#009e73'}]})
})
it('restores selected experiments and colors from legacy saved series', () => {
 const item = {id: 'old', name: 'Old', series: [{label: 'NAS', color: '#123456', runs: [{experimentId: 'first', trialId: 't-first'}, {experimentId: 'second', trialId: 't-second'}]}]} as ResultComparison
 render(<ComparisonDialog item={item} experiments={experiments} session={session} close={() => {}} saved={() => {}} />)
 expect(screen.getByLabelText('Experiment 1 plot label')).toHaveValue('')
 expect(screen.getByLabelText('Experiment 2 plot color')).toHaveValue('#123456')
})
it('allows removing unavailable sources when editing a comparison', () => {
 const item = {id: 'old', name: 'Old', series: [{label: 'NAS', color: '#123456', runs: [{experimentId: 'deleted', trialId: 'missing'}]}]} as ResultComparison
 render(<ComparisonDialog item={item} experiments={experiments} session={session} close={() => {}} saved={() => {}} />)
 fireEvent.click(screen.getByRole('button', {name: 'Remove Unavailable experiment'}))
 expect(screen.queryByText('Unavailable run')).not.toBeInTheDocument()
})
it('loads saved comparisons independently from experiment execution', async () => {
 vi.mocked(request).mockResolvedValue({items: []} as never)
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 await screen.findByText('No comparisons yet')
 expect(screen.getByRole('button', {name: 'New comparison'})).toBeEnabled()
 expect(request).toHaveBeenCalledWith('/comparisons', {}, undefined, expect.any(AbortSignal))
})
it.each([{sources:[]},{sources:[experiments[0]]},{sources:[{...experiments[0],variants:experiments[0].variants.map(variant=>({...variant,trials:[variant.trials[0],{...variant.trials[0],id:'repeat',position:2}]}))}]}])('disables New comparison until two experiments have usable results',async ({sources})=>{
 vi.mocked(request).mockResolvedValue({items:[]} as never)
 render(<ComparisonsView experiments={sources as Experiment[]} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async()=>{}} openOperation={()=>{}}/>)
 await screen.findByText('No comparisons yet')
 const button=screen.getByRole('button',{name:'New comparison'})
 expect(button).toBeDisabled()
 expect(button.parentElement).toHaveAttribute('title','Requires two experiments with results.')
 fireEvent.click(button)
 expect(screen.queryByRole('heading',{name:'Create comparison'})).not.toBeInTheDocument()
})
it('does not count an experiment whose executions have no collected results',async()=>{
 vi.mocked(request).mockResolvedValue({items:[]} as never)
 const missing={...experiments[1],variants:experiments[1].variants.map(variant=>({...variant,trials:variant.trials.map(trial=>({...trial,resultArtifactId:''}))}))}
 render(<ComparisonsView experiments={[experiments[0],missing]} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async()=>{}} openOperation={()=>{}}/>)
 await screen.findByText('No comparisons yet')
 expect(screen.getByRole('button',{name:'New comparison'})).toBeDisabled()
})
