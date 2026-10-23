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

it('shows inference token settings while masking authentication tokens', () => {
  const value = {maxTokens: 128, max_tokens: 64, maxCompletionTokens: 32, inputTokens: 0, apiToken: 'secret-value', access_token: 'secret-value', token: 'secret-value'}
  render(<InputData value={value} />)
  expect(screen.getByText('128')).toBeInTheDocument()
  expect(screen.getByText('64')).toBeInTheDocument()
  expect(screen.getByText('32')).toBeInTheDocument()
  expect(screen.getByText('0')).toBeInTheDocument()
  expect(screen.queryByText('secret-value')).not.toBeInTheDocument()
  expect(screen.getAllByText('••••••••')).toHaveLength(3)
})

it('omits empty fields and nested groups but keeps false and zero', () => {
  const view = render(<InputData value={{nothing: {}, overrides: {cpu: '', memory: null}, missing: undefined, blank: '  ', passwords: '', list: [null, {}], enabled: false, count: 0}} />)
  expect(screen.queryByText('Nothing')).not.toBeInTheDocument()
  expect(screen.queryByText('Overrides')).not.toBeInTheDocument()
  expect(screen.queryByText('Passwords')).not.toBeInTheDocument()
  expect(screen.queryByText('—')).not.toBeInTheDocument()
  expect(screen.getByText('No')).toBeInTheDocument()
  expect(screen.getByText('0')).toBeInTheDocument()
  view.rerender(<InputData value={{nested: {empty: []}}} />)
  expect(view.container).toBeEmptyDOMElement()
})

it('uses schema titles and order, omits internal fields, and masks write-only values', () => {
  render(<InputData value={{latencyMilliseconds: 25, custom: 7, internal: 'omit-me', credential: 'never-show'}} schema={{properties: {custom: {title: 'Samples', 'x-kubephos-order': 0}, latencyMilliseconds: {title: 'Latency (ms)', 'x-kubephos-order': 1}, internal: {'x-kubephos-hidden': true}, credential: {title: 'Credential', writeOnly: true}}}} />)
  expect(screen.getByText('Latency (ms)')).toBeInTheDocument()
  expect(screen.getByText('Samples').compareDocumentPosition(screen.getByText('Latency (ms)')) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(screen.queryByText('omit-me')).not.toBeInTheDocument()
  expect(screen.queryByText('never-show')).not.toBeInTheDocument()
  expect(screen.getByText('••••••••')).toBeInTheDocument()
})

it('gives structured values their own row and keeps multiline configuration readable', () => {
  const {container} = render(<InputData value={{latencyMilliseconds: 20, nested: {bandwidthBytesPerSecond: 1250}, configFile: 'kind: Policy\ninterval: 10'}} />)
  expect(screen.getByText('Latency (ms)')).toBeInTheDocument()
  expect(screen.getByText('Bandwidth (B/s)')).toBeInTheDocument()
  expect(container.querySelectorAll('.input-data-field-wide')).toHaveLength(2)
  expect(container.querySelector('pre')).toHaveTextContent('kind: Policy')
})
