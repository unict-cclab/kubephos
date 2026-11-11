import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {ExperimentConfiguration} from '../types'
import {ExperimentExecutionDialog, suggestedRunName} from './ExperimentExecutionDialog'
vi.mock('../api', () => ({request: vi.fn()}))
beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})
const single: ExperimentConfiguration = {id: 'single', name: 'Baseline', kind: 'single', workspaceId: 'ws', clusterResourceId: 'cluster', description: '', applicationRef: 'app:test', applicationDigest: 'digest', definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '', updatedAt: ''}
const suite: ExperimentConfiguration = {...single, id: 'suite', kind: 'suite', name: 'Comparison', definition: {...single.definition, kind: 'suite', runs: 4, variants: [{configurationId: 'a', alias: 'A', color: '#123456'}, {configurationId: 'b', alias: 'B', color: '#654321'}]}}
describe('unified experiment execution', () => {
 it('executes a comparative configuration using its saved repetition settings', async () => {
  vi.mocked(request).mockResolvedValue({id: 'experiment'} as never)
  const onCreated = vi.fn()
  render(<ExperimentExecutionDialog open configurations={[single, suite]} initialConfigurationId="suite" close={() => {}} session={{authenticated: true, csrfToken: 'csrf'}} changed={async () => {}} onCreated={onCreated} />)
  expect(screen.queryByText(/runs per configuration ×/)).not.toBeInTheDocument()
  expect(screen.queryByLabelText('Number of executions')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Validate and run'}))
  await waitFor(() => expect(onCreated).toHaveBeenCalledWith('experiment'))
  const call = vi.mocked(request).mock.calls.at(-1)!
  expect(call[0]).toBe('/experiments/start')
  expect(JSON.parse(String(call[1]?.body))).toMatchObject({configurationId: 'suite', runs: 4})
 })
 it('keeps single repetition controls when switching from comparative to single', () => {
  render(<ExperimentExecutionDialog open configurations={[single, suite]} initialConfigurationId="suite" close={() => {}} session={{authenticated: true}} changed={async () => {}} onCreated={() => {}} />)
  fireEvent.change(screen.getByLabelText('Configuration'), {target: {value: 'single'}})
  expect(screen.getByLabelText('Number of executions')).toHaveValue(3)
  expect(screen.queryByText('4 runs per configuration × 2 configurations')).not.toBeInTheDocument()
 })
})

it('suggests local date and time and preserves a manually edited name', () => {
 expect(suggestedRunName('mubench-netmarks', new Date(2026,10,11,8,43))).toBe('mubench-netmarks-11/11/2026-08:43')
 expect(suggestedRunName('a'.repeat(200))).toHaveLength(120)
 render(<ExperimentExecutionDialog open configurations={[single,suite]} close={()=>{}} session={{authenticated:true}} changed={async()=>{}} onCreated={()=>{}}/>)
 fireEvent.change(screen.getByLabelText('Start later (optional)'),{target:{value:'2026-11-20T08:43'}})
 expect(screen.getByLabelText('Name')).toHaveValue('Baseline-20/11/2026-08:43')
 fireEvent.change(screen.getByLabelText('Name'),{target:{value:'My run'}})
 fireEvent.change(screen.getByLabelText('Configuration'),{target:{value:'suite'}})
 expect(screen.getByLabelText('Name')).toHaveValue('My run')
})
