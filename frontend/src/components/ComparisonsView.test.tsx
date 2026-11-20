import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeEach, expect, it, vi} from 'vitest'
import {refreshComparison, request} from '../api'
import {ComparisonDialog, ComparisonsView} from './ComparisonsView'
import type {Experiment, Session} from '../types'
import type {ResultComparison} from '../comparisons'
vi.mock('../api', () => ({request: vi.fn(), refreshComparison: vi.fn()}))
vi.mock('./LazyResultsView', () => ({ResultsView: ({focusedExperiment, comparison}: {focusedExperiment?: {name: string}; comparison?: {notice?: import('react').ReactNode}}) => <section aria-label="Comparison results">{comparison?.notice}{focusedExperiment?.name}</section>}))
const session = {authenticated: true, csrfToken: 'csrf', user: {id: 'alice', role: 'user'}} as Session
const experiments = ['first', 'second'].map((id, i) => ({id, name: `Experiment ${i + 1}`, ownerUserId: 'alice', ownerUsername: 'alice', kind: 'instance', status: 'succeeded', variants: [{id: `v-${id}`, name: 'NAS', alias: 'NAS', color: '#123456', configuration: {}, trials: [{id: `t-${id}`, position: 1, status: 'succeeded', resultArtifactId: `data-${id}`}]}]})) as Experiment[]
beforeEach(() => {vi.mocked(request).mockReset(); vi.mocked(refreshComparison).mockReset(); window.location.hash = '#comparisons'})
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

const withDeletedSource = {id: 'cmp', name: 'Paper', ownerUserId: 'alice', createdAt: '2026-09-17T00:00:00Z', updatedAt: '2026-09-17T00:00:00Z', series: [{label: 'Experiment 1', color: '#123456', runs: [{experimentId: 'first', trialId: 't-first'}]}, {label: 'Removed baseline', color: '#d55e00', runs: [{experimentId: 'gone', trialId: 't-gone'}]}], unavailableSources: [{experimentId: 'gone', label: 'Removed baseline', reason: 'deleted'}]} as ResultComparison

it('marks comparisons whose source experiments were deleted', async () => {
 vi.mocked(request).mockResolvedValue({items: [withDeletedSource]} as never)
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 expect(await screen.findByText('1 experiment deleted')).toBeInTheDocument()
 expect(screen.getByRole('status')).toHaveTextContent('1 comparison includes experiments that are no longer available.')
})

it('explains a deleted source on the comparison page and offers to edit it', async () => {
 window.location.hash = '#comparisons/comparison/cmp'
 vi.mocked(request).mockImplementation((async (path: string) => {
  if (path === '/comparisons') return {items: [withDeletedSource]}
  throw new Error('A source experiment is unavailable or is no longer shared with you. Edit the comparison to remove its runs.')
 }) as never)
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 const notice = await screen.findByRole('alert')
 expect(notice).toHaveTextContent('An experiment in this comparison was deleted')
 expect(notice).toHaveTextContent('Removed baseline')
 expect(screen.queryByText(/Edit the comparison to remove its runs/)).not.toBeInTheDocument()
 fireEvent.click(screen.getByRole('button', {name: 'Edit comparison'}))
 expect(screen.getByRole('button', {name: 'Remove Removed baseline'})).toBeInTheDocument()
})

const frozen = {id: 'frozen', name: 'Frozen paper', ownerUserId: 'alice', createdAt: '2026-09-17T00:00:00Z', updatedAt: '2026-09-17T00:00:00Z', snapshotCapturedAt: '2026-09-17T00:00:00Z', series: [{label: 'Experiment 1', color: '#123456', runs: [{experimentId: 'first', trialId: 't-first'}]}]} as ResultComparison
const live = {...frozen, id: 'live', name: 'Live paper', snapshotCapturedAt: undefined}

it('marks comparisons without a snapshot as live in the list', async () => {
 vi.mocked(request).mockResolvedValue({items: [frozen, live]} as never)
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 await screen.findByText('Live paper')
 const rows = screen.getAllByRole('row')
 expect(rows.find(row => row.textContent?.includes('Live paper'))).toHaveTextContent('Live')
 expect(rows.find(row => row.textContent?.includes('Frozen paper'))).not.toHaveTextContent(/Live/)
})

it('shows the integrity notice above a comparison and refreshes its snapshot', async () => {
 window.location.hash = '#comparisons/comparison/frozen'
 const view = {comparison: frozen, result: {...experiments[0], id: 'frozen', name: 'Frozen paper', kind: 'comparison'}, sources: experiments, artifacts: [], sourceConfigurations: {}, snapshot: {capturedAt: '2026-09-17T00:00:00Z'}, stale: true, staleReasons: ['Experiment 1: 1 newer completed run is not included.'], compatibility: {compatible: true, differences: []}}
 vi.mocked(request).mockImplementation((async (path: string) => path === '/comparisons' ? {items: [frozen]} : view) as never)
 const refreshedAt = '2026-11-02T10:00:00Z'
 vi.mocked(refreshComparison).mockResolvedValue({...view, comparison: {...frozen, updatedAt: refreshedAt, snapshotCapturedAt: refreshedAt}, snapshot: {capturedAt: refreshedAt}, stale: false, staleReasons: []} as never)
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 expect(await screen.findByRole('region', {name: 'Comparison results'})).toBeInTheDocument()
 const banner = await screen.findByRole('status', {name: 'The source experiments changed after this snapshot'})
 expect(banner).toHaveTextContent('1 newer completed run is not included.')
 fireEvent.click(screen.getByRole('button', {name: 'Refresh snapshot'}))
 const dialog = await screen.findByRole('dialog', {name: 'Refresh snapshot?'})
 fireEvent.click(within(dialog).getByRole('button', {name: 'Refresh snapshot'}))
 await waitFor(() => expect(refreshComparison).toHaveBeenCalledWith('frozen', 'csrf'))
 await waitFor(() => expect(screen.queryByRole('status', {name: 'The source experiments changed after this snapshot'})).not.toBeInTheDocument())
})

it('keeps the frozen comparison and explains a refresh conflict', async () => {
 window.location.hash = '#comparisons/comparison/frozen'
 const view = {comparison: frozen, result: {...experiments[0], id: 'frozen', name: 'Frozen paper', kind: 'comparison'}, sources: experiments, artifacts: [], sourceConfigurations: {}, snapshot: {capturedAt: '2026-09-17T00:00:00Z'}, stale: true, staleReasons: ['Experiment "Experiment 2" was deleted; its frozen results are still shown.']}
 vi.mocked(request).mockImplementation((async (path: string) => path === '/comparisons' ? {items: [frozen]} : view) as never)
 vi.mocked(refreshComparison).mockRejectedValue(Object.assign(new Error('Source unavailable.'), {status: 409}))
 render(<ComparisonsView experiments={experiments} artifacts={[]} operations={[]} workspaces={[]} session={session} changed={async () => {}} openOperation={() => {}} />)
 fireEvent.click(await screen.findByRole('button', {name: 'Refresh snapshot'}))
 fireEvent.click(within(await screen.findByRole('dialog', {name: 'Refresh snapshot?'})).getByRole('button', {name: 'Refresh snapshot'}))
 expect(await screen.findByRole('alert')).toHaveTextContent('cannot be refreshed')
 expect(screen.getByRole('region', {name: 'Comparison results'})).toBeInTheDocument()
})
