import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {InitialReplicaEditor} from './InitialReplicaEditor'

const components = [
  {id: 'frontend', traits: ['scalable']},
  {id: 'checkout', traits: ['scalable']},
  {id: 'database', traits: ['stateful']}
]

describe('initial replica editor', () => {
  it('uses one default and stores only selected service overrides', () => {
    render(<form><InitialReplicaEditor prefix="applicationValues" components={components} values={{initialReplicas: 2, replicaOverrides: {checkout: 4}}} resetKey="test" /></form>)
    expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(2)
    expect(screen.getByRole('checkbox', {name: 'checkout'})).toBeChecked()
    expect(screen.getByRole('checkbox', {name: 'frontend'})).not.toBeChecked()
    const hidden = document.querySelector('input[name="applicationValues.replicaOverrides"]') as HTMLInputElement
    expect(JSON.parse(hidden.value)).toEqual({checkout: 4})
    fireEvent.click(screen.getByRole('checkbox', {name: 'frontend'}))
    expect(JSON.parse(hidden.value)).toEqual({frontend: 2, checkout: 4})
    expect(screen.queryByRole('checkbox', {name: 'database'})).not.toBeInTheDocument()
  })
})

it('starts with service overrides collapsed when none are configured and retains edits when collapsed', () => {
  const {container} = render(<form><InitialReplicaEditor prefix="app" components={components} values={{initialReplicas: 2}} resetKey="new" /></form>)
  const summary = screen.getByText('Initial replicas by service')
  expect(summary.closest('details')).not.toHaveAttribute('open')
  fireEvent.click(summary)
  fireEvent.click(screen.getByRole('checkbox', {name: 'frontend'}))
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Initial replicas for frontend'}), {target: {value: '3'}})
  fireEvent.click(summary)
  expect(JSON.parse((container.querySelector('input[name="app.replicaOverrides"]') as HTMLInputElement).value)).toEqual({frontend: 3})
})

it('preserves edited initial replica values when a topology refresh changes the service list', () => {
  const view = render(<form><InitialReplicaEditor prefix="app" components={components} values={{initialReplicas: 1}} resetKey="same-application" /></form>)
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Initial replicas'}), {target: {value: '3'}})
  fireEvent.click(screen.getByText('Initial replicas by service'))
  fireEvent.click(screen.getByRole('checkbox', {name: 'frontend'}))
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Initial replicas for frontend'}), {target: {value: '5'}})
  view.rerender(<form><InitialReplicaEditor prefix="app" components={[...components, {id: 'worker', traits: ['scalable']}]} values={{initialReplicas: 1}} resetKey="same-application" /></form>)
  expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(3)
  expect(screen.getByRole('spinbutton', {name: 'Initial replicas for frontend'})).toHaveValue(5)
  expect(screen.getByRole('checkbox', {name: 'worker'})).not.toBeChecked()
  expect(JSON.parse((view.container.querySelector('input[name="app.replicaOverrides"]') as HTMLInputElement).value)).toEqual({frontend: 5})
})
