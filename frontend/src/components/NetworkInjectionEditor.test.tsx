import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {NetworkInjectionEditor} from './NetworkInjectionEditor'
import {WizardForm, WizardReview, WizardStep} from './WizardForm'

const labels = ['Latency (ms)', 'Jitter (ms)', 'Bandwidth (B/s)', 'Packet loss (%)']

describe('network injection units', () => {
  it.each(['zone', 'cluster'] as const)('labels directed %s links and retains backend units when editing', distribution => {
    const {container} = render(<form><NetworkInjectionEditor prefix="network" zones={['a', 'b']} distribution={distribution} /></form>)
    expect(screen.queryByRole('spinbutton', {name: 'Correlation (%)'})).not.toBeInTheDocument()
    for (const name of labels) expect(screen.getByRole('spinbutton', {name})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Bandwidth (B/s)'}), {target: {value: '12500000'}})
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Latency (ms)'}), {target: {value: '75'}})
    const data = new FormData(container.querySelector('form')!)
    expect(JSON.parse(String(data.get('network.linkOverrides')))).toEqual([{sourceZone: 'a', targetZone: 'b', latencyMilliseconds: 75, bandwidthBytesPerSecond: 12500000}])
    expect(screen.getByRole('combobox', {name: `Source ${distribution}`})).toHaveValue('a')
  })

  it('uses the same units in the shared profile without converting saved values', () => {
    const {container} = render(<form><NetworkInjectionEditor prefix="network" zones={['a', 'b']} values={{enableLatency: true, enableBandwidth: true, enablePacketLoss: true, latencyMilliseconds: 75, bandwidthBytesPerSecond: 12500000}} /></form>)
    const effects = within(container.querySelector('.effect-grid') as HTMLElement)
    for (const name of labels) expect(effects.getByRole('spinbutton', {name})).toBeInTheDocument()
    expect(effects.getByRole('spinbutton', {name: 'Latency (ms)'})).toHaveValue(75)
    expect(effects.getByRole('spinbutton', {name: 'Bandwidth (B/s)'})).toHaveValue(12500000)
    const data = new FormData(container.querySelector('form')!)
    expect(data.get('network.bandwidthBytesPerSecond')).toBe('12500000')
  })
})


it('does not invent a directed override when editing a shared-only profile', () => {
  const {container} = render(<form><NetworkInjectionEditor prefix="network" zones={['a', 'b']} values={{enableLatency: true, latencyMilliseconds: 100, linkOverrides: []}} /></form>)
  expect(screen.queryByRole('table', {name: 'Zone links'})).not.toBeInTheDocument()
  expect(new FormData(container.querySelector('form')!).get('network.linkOverrides')).toBe('[]')
})

it('preserves shared field edits across mode changes and submits inactive effects as off', () => {
  const {container} = render(<form><NetworkInjectionEditor prefix="network" zones={['a', 'b']} values={{enableLatency: true, latencyMilliseconds: 100}} /></form>)
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Latency (ms)'}), {target: {value: '175'}})
  fireEvent.click(screen.getByRole('checkbox', {name: 'Use one profile for every inter-zone link'}))
  expect(new FormData(container.querySelector('form')!).has('network.enableLatency')).toBe(false)
  fireEvent.click(screen.getByRole('checkbox', {name: 'Use one profile for every inter-zone link'}))
  expect(screen.getByRole('spinbutton', {name: 'Latency (ms)'})).toHaveValue(175)
  expect(screen.getByRole('checkbox', {name: 'Latency and jitter'})).toBeChecked()
})

it('adds unused directed pairs, prevents self-links and disables adding when all pairs exist', () => {
  const {container} = render(<form><NetworkInjectionEditor prefix="network" zones={['a', 'b']} /></form>)
  fireEvent.click(screen.getByRole('button', {name: 'Add zone link'}))
  const links = JSON.parse(String(new FormData(container.querySelector('form')!).get('network.linkOverrides')))
  expect(links.map((link: {sourceZone: string; targetZone: string}) => `${link.sourceZone}>${link.targetZone}`)).toEqual(['a>b', 'b>a'])
  expect(screen.getByRole('button', {name: 'Add zone link'})).toBeDisabled()
  const target = screen.getAllByRole('combobox', {name: 'Target zone'})[0]
  expect(within(target).getByRole('option', {name: 'a'})).toBeDisabled()
})

it('uses integer steps for integer backend fields and allows fractional packet loss', () => {
  render(<NetworkInjectionEditor prefix="network" zones={['a', 'b']} />)
  for (const name of ['Latency (ms)', 'Jitter (ms)', 'Bandwidth (B/s)']) expect(screen.getByRole('spinbutton', {name})).toHaveAttribute('step', '1')
  expect(screen.getByRole('spinbutton', {name: 'Packet loss (%)'})).toHaveAttribute('step', '0.1')
})

it('places the host-network toggle in the shared disclosure and preserves its saved value', () => {
  render(<NetworkInjectionEditor prefix="network" zones={['a', 'b']} values={{hostNetwork: true}} />)
  fireEvent.click(screen.getByText('Network target'))
  const toggle = screen.getByRole('checkbox', {name: 'Shape host-network traffic instead of Pod CIDRs'})
  expect(toggle).toBeChecked()
  expect(toggle.closest('.form-disclosure-body')).toBeInTheDocument()
  expect(toggle.closest('label')).toHaveClass('wide')
})

it.each(['zone', 'cluster'] as const)('reviews directed %s links without inactive shared defaults or duplicate fields', async distribution => {
  const {container} = render(<WizardForm onCancel={() => {}} submitLabel="Save"><WizardStep title="Network"><NetworkInjectionEditor prefix="network" zones={['a', 'b']} distribution={distribution} values={{linkOverrides: [{sourceZone: 'a', targetZone: 'b', latencyMilliseconds: 100}]}} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
  const review = within(container.querySelector('.wizard-review') as HTMLElement)
  const table = await review.findByRole('table', {name: distribution === 'zone' ? 'Zone links' : 'Cluster links'})
  expect(within(table).getByRole('row', {name: 'a b 100 — — —'})).toBeVisible()
  expect(review.queryByText('12500000')).not.toBeInTheDocument()
  expect(review.queryByText('50')).not.toBeInTheDocument()
  expect(review.queryByText(`Source ${distribution}`)).not.toBeInTheDocument()
  expect(new FormData(container.querySelector('form')!).get('network.linkOverrides')).toBe('[{"sourceZone":"a","targetZone":"b","latencyMilliseconds":100}]')
})

it('reviews only enabled shared effects while retaining inactive values for editing', async () => {
  const {container} = render(<WizardForm onCancel={() => {}} submitLabel="Save"><WizardStep title="Network"><NetworkInjectionEditor prefix="network" zones={['a', 'b']} values={{enableLatency: true, latencyMilliseconds: 75, bandwidthBytesPerSecond: 9000, linkOverrides: []}} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep></WizardForm>)
  fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
  const review = within(container.querySelector('.wizard-review') as HTMLElement)
  expect(await review.findByText('75')).toBeVisible()
  expect(review.queryByText('Bandwidth (B/s)')).not.toBeInTheDocument()
  expect(review.queryByText('9000')).not.toBeInTheDocument()
  expect(new FormData(container.querySelector('form')!).get('network.bandwidthBytesPerSecond')).toBe('9000')
})
