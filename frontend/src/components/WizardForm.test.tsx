import {FormDisclosure} from './FormDisclosure'
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {WizardForm, WizardReview, WizardStep} from './WizardForm'
import {FormTabs} from './FormTabs'

function next() {fireEvent.click(screen.getByRole('button', {name:'Continue'}))}

it('omits disabled and explicitly skipped groups from the review, including table previews', async () => {
  const table = JSON.stringify({headers: ['Name'], rows: [['Inactive preview']]})
  render(<WizardForm onCancel={() => {}} submitLabel="Save"><WizardStep title="Settings"><label>Name<input defaultValue="Active value" /></label><fieldset disabled><label>Disabled value<input defaultValue="Inactive setting" /></label><input type="hidden" data-review-title="Disabled table" data-review-table={table} /></fieldset><div data-review-skip="true"><input type="hidden" data-review-title="Skipped table" data-review-table={table} /></div></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  expect(await screen.findByText('Active value')).toBeVisible()
  expect(screen.queryByText('Inactive setting')).not.toBeInTheDocument()
  expect(screen.queryByText('Inactive preview')).not.toBeInTheDocument()
})

describe('WizardForm', () => {
 it('jumps directly across unvisited steps and validates skipped fields before saving', () => {
  const saved = vi.fn()
  render(<WizardForm onCancel={() => {}} submitLabel="Save" onSubmit={saved}><WizardStep title="Identity"><label>Name<input defaultValue="lab" /></label></WizardStep><WizardStep title="Network"><label>Address<input required /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  fireEvent.click(screen.getByRole('button', {name: 'Review'}))
  expect(screen.getByRole('heading', {name: 'Review'})).toBeVisible()
  fireEvent.click(screen.getByRole('button', {name: 'Save'}))
  expect(saved).not.toHaveBeenCalled()
  expect(screen.getByRole('heading', {name: 'Network'})).toBeVisible()
  fireEvent.change(screen.getByRole('textbox', {name: 'Address'}), {target: {value: '10.0.0.10'}})
  fireEvent.click(screen.getByRole('button', {name: 'Identity'}))
  fireEvent.change(screen.getByRole('combobox', {name: 'Setup step'}), {target: {value: '2'}})
  expect(screen.getByRole('heading', {name: 'Review'})).toBeVisible()
  fireEvent.click(screen.getByRole('button', {name: 'Save'}))
  expect(saved).toHaveBeenCalledOnce()
 })
 it('does not save while a skipped step is still blocked by asynchronous validation', () => {
  const saved = vi.fn()
  render(<WizardForm onCancel={() => {}} submitLabel="Save" onSubmit={saved}><WizardStep title="Identity"><input defaultValue="lab" /></WizardStep><WizardStep title="Application" blocked status={{message: 'Waiting for topology'}}><p>Application</p></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  fireEvent.click(screen.getByRole('button', {name: 'Review'}))
  fireEvent.click(screen.getByRole('button', {name: 'Save'}))
  expect(saved).not.toHaveBeenCalled()
  expect(screen.getByRole('heading', {name: 'Application'})).toBeVisible()
 })
 it('validates the current step and preserves values through navigation and final submission', async () => {
  const saved=vi.fn()
  render(<WizardForm onCancel={()=>{}} submitLabel="Create" onSubmit={event=>saved(Object.fromEntries(new FormData(event.currentTarget)))}><WizardStep title="Identity"><label>Name<input name="name" required /></label></WizardStep><WizardStep title="Network"><label>Address<input name="address" required /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  expect(screen.getByRole('heading',{name:'Identity'})).toBeVisible()
  expect(saved).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('Name'),{target:{value:'lab'}})
  next()
  expect(screen.queryByRole('textbox',{name:'Name'})).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Address'),{target:{value:'10.0.0.10'}})
  fireEvent.click(screen.getByRole('button',{name:'Previous'}))
  expect(screen.getByLabelText('Name')).toHaveValue('lab')
  next();next()
  expect(await screen.findByText('10.0.0.10')).toBeVisible()
  expect(saved).not.toHaveBeenCalled()
  fireEvent.click(screen.getByRole('button',{name:'Create'}))
  expect(saved).toHaveBeenCalledWith({name:'lab',address:'10.0.0.10'})
 })
 it('opens the tab containing an invalid field before continuing', async () => {
  render(<WizardForm onCancel={()=>{}} submitLabel="Create"><WizardStep title="Pools"><FormTabs label="Pools" items={[{id:'master',title:'Master',content:<label>Master count<input defaultValue="1" /></label>},{id:'app',title:'Applications',content:<label>Pool name<input required /></label>}]} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  expect(screen.getByRole('tab',{name:'Applications'})).toHaveAttribute('aria-selected','true')
  await waitFor(()=>expect(screen.getByRole('textbox',{name:'Pool name'})).toHaveFocus())
  expect(screen.queryByRole('heading',{name:'Review'})).not.toBeInTheDocument()
 })
 it('never exposes credentials in the review', async () => {
  render(<WizardForm onCancel={()=>{}} submitLabel="Create"><WizardStep title="Access"><label>Token secret<input type="password" defaultValue="sensitive-secret" /></label><label>API key<input defaultValue="api-secret-value" /></label><label>Name<input defaultValue="lab" /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  expect(await screen.findByText('lab')).toBeVisible()
  expect(screen.queryByText('sensitive-secret')).not.toBeInTheDocument()
  expect(screen.queryByText('api-secret-value')).not.toBeInTheDocument()
 })
 it('does not submit when Enter is pressed before the final review', () => {
  const submit=vi.fn()
  const {container}=render(<WizardForm onCancel={()=>{}} submitLabel="Create" onSubmit={submit}><WizardStep title="One"><input name="one" defaultValue="value" /></WizardStep><WizardStep title="Two"><input /></WizardStep></WizardForm>)
  fireEvent.submit(container.querySelector('form')!)
  expect(submit).not.toHaveBeenCalled()
  expect(screen.getByRole('heading',{name:'Two'})).toBeVisible()
 })
 it('returns to a previous step when its values become invalid', () => {
  const submit=vi.fn()
  render(<WizardForm onCancel={()=>{}} submitLabel="Create" onSubmit={submit}><WizardStep title="Identity"><label>Name<input name="name" defaultValue="lab" required /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  fireEvent.change(screen.getByLabelText('Name'),{target:{value:''}})
  fireEvent.click(screen.getByRole('button',{name:'Create'}))
  expect(submit).not.toHaveBeenCalled()
  expect(screen.getByRole('heading',{name:'Identity'})).toBeVisible()
 })
})

it('reveals every enclosing tab and disclosure when validating a deeply nested field', async () => {
  render(<WizardForm onCancel={() => {}} submitLabel="Create"><WizardStep title="Settings"><FormTabs label="Outer" items={[
    {id: 'general', title: 'General', content: <p>General settings</p>},
    {id: 'advanced', title: 'Advanced', content: <FormDisclosure title="Advanced settings"><FormTabs label="Inner" items={[
      {id: 'one', title: 'One', content: <p>One</p>},
      {id: 'two', title: 'Two', content: <FormDisclosure title="Connection"><label>Endpoint<input required /></label></FormDisclosure>}
    ]} /></FormDisclosure>}
  ]} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  expect(screen.getByRole('tab', {name: 'Advanced'})).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByRole('tab', {name: 'Two'})).toHaveAttribute('aria-selected', 'true')
  expect(screen.getByText('Advanced settings').closest('details')).toHaveAttribute('open')
  expect(screen.getByText('Connection').closest('details')).toHaveAttribute('open')
  await waitFor(() => expect(screen.getByRole('textbox', {name: 'Endpoint'})).toHaveFocus())
})

it('keeps asynchronous blocking errors visible outside inactive tabs', () => {
  render(<WizardForm onCancel={() => {}} submitLabel="Create"><WizardStep title="Application" blocked status={{message: 'Topology could not be resolved.', error: true}}><FormTabs label="Application" items={[{id: 'parameters', title: 'Parameters', content: <p>Parameters</p>}, {id: 'topology', title: 'Topology', content: <p>Graph</p>}]} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  expect(screen.getByRole('alert')).toHaveTextContent('Topology could not be resolved.')
  expect(screen.getByRole('button', {name: 'Continue'})).toBeDisabled()
})

it('reviews repeated workload and traffic phases as tables instead of flattening tab-prefixed fields', async () => {
  const {LoadProfileEditor} = await import('./LoadProfileEditor')
  render(<WizardForm onCancel={() => {}} submitLabel="Create"><WizardStep title="Load profile"><LoadProfileEditor prefix="load" zones={['cloud', 'edge-a', 'edge-b', 'edge-c']} settings={<label>Load workers<input name="workers" type="number" defaultValue="1" /></label>} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  const workload = await screen.findByRole('table', {name: 'Workload phases'})
  expect(workload).toHaveTextContent('Phase 1')
  expect(workload).toHaveTextContent('0–15 min')
  expect(workload).toHaveTextContent('30')
  const traffic = screen.getByRole('table', {name: 'Traffic phases by zone'})
  expect(traffic).toHaveTextContent('cloud (%)')
  expect(traffic).toHaveTextContent('25')
  expect(screen.queryByText('Workload · Duration')).not.toBeInTheDocument()
  expect(within(document.querySelector('.wizard-review') as HTMLElement).getByText('Generator')).toBeInTheDocument()
})

it('retains units across separate label text nodes without including helper descriptions', async () => {
  render(<WizardForm onCancel={() => {}} submitLabel="Create"><WizardStep title="Settings"><label>Timeout{' (ms)'}<input name="timeout" defaultValue="50" /><small>A description</small></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  next()
  const review = document.querySelector('.wizard-review')!
  await waitFor(() => expect(review).toHaveTextContent('Timeout (ms)'))
  expect(review).not.toHaveTextContent('A description')
})

it('replaces Continue with a distinct submit control before review can be saved', () => {
  const saved = vi.fn()
  render(<WizardForm onCancel={() => {}} submitLabel="Save" onSubmit={saved}><WizardStep title="Settings"><input defaultValue="example" /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  const continueButton = screen.getByRole('button', {name: 'Continue'})
  fireEvent.click(continueButton)
  expect(continueButton.isConnected).toBe(false)
  expect(screen.getByRole('button', {name: 'Save'})).toHaveAttribute('type', 'submit')
  expect(saved).not.toHaveBeenCalled()
})
