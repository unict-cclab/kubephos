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

describe('repetition schedule', () => {
 const render_ = (configuration = single) => render(<ExperimentExecutionDialog open configurations={[configuration]} close={() => {}} session={{authenticated: true, csrfToken: 'csrf'}} changed={async () => {}} onCreated={() => {}} />)
 it('sends rest and warm-up settings and shows them in the execution readout', async () => {
  vi.mocked(request).mockReset().mockResolvedValue({id: 'experiment'} as never)
  render_()
  const rest = screen.getByLabelText('Rest between repetitions (s)')
  expect(rest).toHaveValue(0)
  expect(rest).toHaveAccessibleDescription(/Recommended ≥ 60 s for publication runs/)
  fireEvent.change(rest, {target: {value: '90'}})
  const warmup = screen.getByRole('checkbox', {name: 'First repetition is a warm-up (excluded from statistics)'})
  expect(warmup).toHaveAccessibleDescription(/image pulls and cold caches/)
  fireEvent.click(warmup)
  const readout = screen.getByRole('table', {name: 'Execution settings'})
  expect(readout).toHaveTextContent('Rest between repetitions90 s')
  expect(readout).toHaveTextContent('Warm-up repetitionYes · excluded from statistics')
  fireEvent.click(screen.getByRole('button', {name: 'Validate and run'}))
  await waitFor(() => expect(request).toHaveBeenCalled())
  expect(JSON.parse(String(vi.mocked(request).mock.calls[0][1]?.body))).toMatchObject({configurationId: 'single', restSecondsBetweenTrials: 90, firstTrialPilot: true})
 })
 it('starts from the saved suite settings', () => {
  render_({...suite, definition: {...suite.definition, restSecondsBetweenTrials: 120, firstTrialPilot: true}})
  expect(screen.getByLabelText('Rest between repetitions (s)')).toHaveValue(120)
  expect(screen.getByRole('checkbox', {name: /warm-up/})).toBeChecked()
 })
 it('rejects rest periods outside 0–3600 seconds before sending', () => {
  vi.mocked(request).mockReset()
  render_()
  fireEvent.change(screen.getByLabelText('Rest between repetitions (s)'), {target: {value: '4000'}})
  expect(screen.getByText('Rest between repetitions must be a whole number from 0 to 3600 seconds.')).toBeInTheDocument()
  fireEvent.submit(screen.getByRole('button', {name: 'Validate and run'}).closest('form')!)
  expect(request).not.toHaveBeenCalled()
 })
 it('shows a 422 invalid_trial_schedule rejection next to the field', async () => {
  vi.mocked(request).mockReset().mockRejectedValue(Object.assign(new Error('Rest between runs must be between 0 and 3600 seconds.'), {status: 422, body: {code: 'invalid_trial_schedule'}}))
  render_()
  fireEvent.click(screen.getByRole('button', {name: 'Validate and run'}))
  const rest = screen.getByLabelText('Rest between repetitions (s)')
  // The error, its description and the focus move are applied after the rejected request settles.
  await waitFor(() => {
    expect(rest).toHaveAttribute('aria-invalid', 'true')
    expect(rest).toHaveAccessibleDescription(/Rest between runs must be between 0 and 3600 seconds\./)
    expect(rest).toHaveFocus()
  })
  fireEvent.change(rest, {target: {value: '30'}})
  expect(rest).not.toHaveAttribute('aria-invalid')
 })
})

it('keeps the repetition schedule fields accessible, including a rejected value', async () => {
 const {TrialScheduleFields} = await import('./TrialScheduleFields')
 const axe = (await import('axe-core')).default
 render(<main><h1>Run</h1><form><TrialScheduleFields value={{restSecondsBetweenTrials: 0, firstTrialPilot: true}} error="Rest between runs must be between 0 and 3600 seconds." onChange={() => {}} /></form></main>)
 const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
 expect(results.violations.map(({id}) => id)).toEqual([])
})
