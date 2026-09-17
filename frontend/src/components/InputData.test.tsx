import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {InputData, safeInput} from './InputData'

describe('entered data', () => {
  it('shows every nested creation value with readable labels and resolved references', () => {
    render(<InputData value={{vmid: 120, gateway: '192.0.2.1', managementPool: {count: 2, zones: ['east', 'west']}, harborId: 'svc_123', enabled: false}} references={{svc_123: 'Managed Harbor'}} />)
    expect(screen.getByText('VMID')).toBeInTheDocument()
    expect(screen.getByText('120')).toBeInTheDocument()
    expect(screen.getByText('Management pool')).toBeInTheDocument()
    expect(screen.getByText('east')).toBeInTheDocument()
    expect(screen.getByText('west')).toBeInTheDocument()
    expect(screen.getByText('Managed Harbor')).toBeInTheDocument()
    expect(screen.getByText('No')).toBeInTheDocument()
  })

  it('hides secrets but preserves credential references', () => {
    expect(safeInput({credentialRef: 'cred_123', password: 'not-visible', nested: {apiToken: 'not-visible'}})).toEqual({credentialRef: 'cred_123', password: '••••••••', nested: {apiToken: '••••••••'}})
  })
})
