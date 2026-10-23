import {useState} from 'react'
import {render, screen, waitFor} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import {describe, expect, it, vi} from 'vitest'
import {WizardForm, WizardReview, WizardStep} from './WizardForm'
import {FormTabs} from './FormTabs'

// Task-based interaction tests: use the same keyboard and focus paths as a person.
describe('wizard task flows', () => {
  it('finds a missing field in a hidden tab and lets the user recover using the keyboard', async () => {
    const user = userEvent.setup()
    const save = vi.fn()
    render(<WizardForm onCancel={() => {}} submitLabel="Save" onSubmit={event => save(Object.fromEntries(new FormData(event.currentTarget)))}>
      <WizardStep title="Pools"><FormTabs label="Node pools" items={[
        {id: 'master', title: 'Master', content: <label>Master nodes<input name="master" defaultValue="1" /></label>},
        {id: 'applications', title: 'Applications', content: <label>Pool name<input name="pool" required /></label>}
      ]} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm>)
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    expect(screen.getByRole('tab', {name: 'Applications'})).toHaveAttribute('aria-selected', 'true')
    await waitFor(() => expect(screen.getByRole('textbox', {name: 'Pool name'})).toHaveFocus())
    await user.keyboard('workers{Enter}')
    expect(screen.getByRole('heading', {name: 'Review'})).toBeVisible()
    expect(save).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', {name: 'Previous'}))
    expect(screen.getByRole('textbox', {name: 'Pool name'})).toHaveValue('workers')
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await user.click(screen.getByRole('button', {name: 'Save'}))
    expect(save).toHaveBeenCalledExactlyOnceWith({master: '1', pool: 'workers'})
  })

  it('supports arrow keys, Home and End while keeping values in inactive tabs', async () => {
    const user = userEvent.setup()
    render(<FormTabs label="Pools" items={[
      {id: 'master', title: 'Master', content: <label>Master value<input defaultValue="1" /></label>},
      {id: 'management', title: 'Management', content: <label>Management value<input /></label>},
      {id: 'applications', title: 'Applications', content: <label>Application value<input /></label>}
    ]} />)
    await user.click(screen.getByRole('tab', {name: 'Master'}))
    await user.keyboard('{ArrowRight}')
    expect(screen.getByRole('tab', {name: 'Management'})).toHaveFocus()
    await user.type(screen.getByRole('textbox', {name: 'Management value'}), 'kept')
    await user.click(screen.getByRole('tab', {name: 'Management'}))
    await user.keyboard('{End}')
    expect(screen.getByRole('tab', {name: 'Applications'})).toHaveFocus()
    await user.keyboard('{Home}')
    expect(screen.getByRole('tab', {name: 'Master'})).toHaveFocus()
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('tab', {name: 'Applications'})).toHaveFocus()
    await user.keyboard('{ArrowLeft}')
    expect(screen.getByRole('textbox', {name: 'Management value'})).toHaveValue('kept')
  })

  it('allows direct navigation from the compact step selector without losing incomplete values', async () => {
    const user = userEvent.setup()
    render(<WizardForm onCancel={() => {}} submitLabel="Save"><WizardStep title="Identity"><label>Name<input name="name" required /></label></WizardStep><WizardStep title="Network"><label>IP address<input name="ip" /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
    const picker = screen.getByRole('combobox', {name: 'Setup step'})
    await user.selectOptions(picker, '1')
    expect(screen.getByRole('heading', {name: 'Network'})).toBeVisible()
    await user.selectOptions(picker, '0')
    await user.type(screen.getByRole('textbox', {name: 'Name'}), 'lab')
    await user.selectOptions(picker, '2')
    expect(screen.getByRole('heading', {name: 'Review'})).toBeVisible()
    await user.selectOptions(picker, '0')
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('lab')
  })

  it('prevents duplicate saves and retains values after a server error', async () => {
    const user = userEvent.setup()
    let rejectSave: (reason: Error) => void = () => {}
    const save = vi.fn(() => new Promise<void>((_resolve, reject) => {rejectSave = reject}))
    function Editor() {
      const [pending, setPending] = useState(false)
      const [error, setError] = useState('')
      return <WizardForm onCancel={() => {}} pending={pending} error={error} submitLabel="Save" onSubmit={async () => {
        setPending(true); setError('')
        try {await save()} catch {setError('Address already in use. Choose another address.')} finally {setPending(false)}
      }}><WizardStep title="Network"><label>Address<input name="address" defaultValue="10.0.0.10" /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>
    }
    render(<Editor />)
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await user.dblClick(screen.getByRole('button', {name: 'Save'}))
    expect(save).toHaveBeenCalledOnce()
    expect(screen.getByRole('button', {name: 'Saving…'})).toBeDisabled()
    rejectSave(new Error('conflict'))
    expect(await screen.findByRole('alert')).toHaveTextContent('Address already in use')
    await user.click(screen.getByRole('button', {name: 'Previous'}))
    expect(screen.getByRole('textbox', {name: 'Address'})).toHaveValue('10.0.0.10')
    await user.clear(screen.getByRole('textbox', {name: 'Address'}))
    await user.type(screen.getByRole('textbox', {name: 'Address'}), '10.0.0.11')
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    expect(await screen.findByText('10.0.0.11')).toBeVisible()
  })
})

it('keeps member edits when a multi cluster wizard gains another member', async () => {
  const user = userEvent.setup()
  const errors = vi.spyOn(console, 'error').mockImplementation(() => {})
  function Editor() {
    const [members, setMembers] = useState([1, 2])
    return <WizardForm onCancel={() => {}} submitLabel="Save"><WizardStep title="Clusters"><button type="button" onClick={() => setMembers(value => [...value, value.length + 1])}>Add member</button></WizardStep><WizardStep title="Network"><label>Gateway<input name="gateway" /></label></WizardStep>{members.map(member => <WizardStep key={member} title={`Cluster ${member}`}><label>Member name<input name={`member-${member}`} defaultValue={`cluster-${member}`} /></label></WizardStep>)}<WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>
  }
  try {
    render(<Editor />)
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await user.type(screen.getByRole('textbox', {name: 'Gateway'}), '10.0.0.1')
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await user.clear(screen.getByRole('textbox', {name: 'Member name'}))
    await user.type(screen.getByRole('textbox', {name: 'Member name'}), 'east')
    await user.click(screen.getByRole('button', {name: 'Clusters'}))
    await user.click(screen.getByRole('button', {name: 'Add member'}))
    await user.click(screen.getByRole('button', {name: 'Cluster 1'}))
    expect(screen.getByRole('textbox', {name: 'Member name'})).toHaveValue('east')
    await user.click(screen.getByRole('button', {name: 'Network'}))
    expect(screen.getByRole('textbox', {name: 'Gateway'})).toHaveValue('10.0.0.1')
    expect(errors).not.toHaveBeenCalled()
  } finally {errors.mockRestore()}
})

it('keeps Enter as a newline in multiline configuration fields', async () => {
  const user = userEvent.setup()
  const submit = vi.fn()
  render(<WizardForm onCancel={() => {}} submitLabel="Save" onSubmit={submit}><WizardStep title="Policy"><label>Policy<textarea name="policy" /></label></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  await user.type(screen.getByRole('textbox', {name: 'Policy'}), 'first{Enter}second')
  expect(screen.getByRole('textbox', {name: 'Policy'})).toHaveValue('first\nsecond')
  expect(screen.getByRole('heading', {name: 'Policy'})).toBeVisible()
  expect(submit).not.toHaveBeenCalled()
})
