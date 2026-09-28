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
