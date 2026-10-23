import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {AutoscalerTargetEditor} from './AutoscalerTargetEditor'
import {LoadProfileEditor, LoadProfilePreview, loadProfileSteps} from './LoadProfileEditor'
import {svgToPDF} from './ResultsView'

describe('load profile preview', () => {
  it.each([900, 2700, 2460, 73])('ends the time axis and completed profile at the configured duration (%s seconds)', durationSeconds => {
    const view = render(<LoadProfilePreview steps={loadProfileSteps([{type: 'constant', durationSeconds, rps: 10}])} progressSeconds={durationSeconds} />)
    const svg = view.container.querySelector('svg')!
    const frame = svg.querySelector('.axis')!
    const right = Number(frame.getAttribute('x')) + Number(frame.getAttribute('width'))
    const path = svg.querySelector('path')!.getAttribute('d')!
    const endpoint = path.match(/L([\d.]+) [\d.]+$/)!
    expect(Number(endpoint[1])).toBeCloseTo(right)
    expect(svg.querySelector('.load-progress-marker line')).toHaveAttribute('x1', String(right))
    const timeTicks = [...svg.querySelectorAll('text.tick[text-anchor="middle"]')]
    expect(Number(timeTicks.at(-1)!.getAttribute('x'))).toBeCloseTo(right)
    expect(timeTicks.at(-1)).toHaveTextContent((durationSeconds / 60).toLocaleString('en-US', {maximumFractionDigits: 2}))
    const positions = timeTicks.map(tick => Number(tick.getAttribute('x')))
    expect(positions.every((position, index) => index === 0 || position - positions[index - 1] >= (right - Number(frame.getAttribute('x'))) * .08)).toBe(true)
  })

  it('restores saved workload and geographic phases for editing', () => {
    const view = render(<form><LoadProfileEditor prefix="component-1" zones={['zone-a', 'zone-b']} values={{steps: [{type: 'constant', durationSeconds: 120, rps: 77}], geographicSteps: [{type: 'constant', durationSeconds: 120, weights: {'zone-a': 3, 'zone-b': 1}}]}} /></form>)

    expect(screen.getByRole('spinbutton', {name: 'Requests / second'})).toHaveValue(77)
    expect(screen.getAllByRole('spinbutton', {name: 'Duration (minutes)'})[0]).toHaveValue(2)
    expect(view.container.querySelector<HTMLInputElement>('input[name="component-1.steps"]')?.value).toContain('"rps":77')
    expect(view.container.querySelector<HTMLInputElement>('input[name="component-1.geographicSteps"]')?.value).toContain('"zone-a":3')
    const phase = screen.getByRole('combobox', {name: 'Workload phase 1 type'}).closest('.timeline-card')
    const preview = screen.getByLabelText('Input load preview')
    expect(phase).not.toBeNull()
    expect(Boolean(phase!.compareDocumentPosition(preview) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true)
  })

  it('replaces a zero workload value without forcing it back while typing', () => {
    const view = render(<form><LoadProfileEditor prefix="component-1" zones={[]} values={{steps: [{type: 'constant', durationSeconds: 120, rps: 0}]}} /></form>)
    const field = screen.getByRole('spinbutton', {name: 'Requests / second'})

    fireEvent.change(field, {target: {value: ''}})
    expect(field).toHaveValue(null)
    fireEvent.change(field, {target: {value: '25'}})
    expect(field).toHaveValue(25)
    expect(view.container.querySelector<HTMLInputElement>('input[name="component-1.steps"]')?.value).toContain('"rps":25')
  })

  it('accepts valid saved phases and ignores malformed values', () => {
    const steps = loadProfileSteps([{type: 'constant', durationSeconds: 60, rps: 20}, {type: 'unknown', durationSeconds: 10}])
    expect(steps).toHaveLength(1)
    const view = render(<LoadProfilePreview steps={steps} />)
    expect(screen.getByRole('img')).toHaveAccessibleName(/lasting 1m with a peak of 20/)
    expect(screen.getByText('Input load profile')).toBeInTheDocument()
    expect(screen.getByText('Time (min)')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PDF'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PNG'})).toBeInTheDocument()
    const svg = view.container.querySelector('svg')!
    const pdf = new TextDecoder().decode(svgToPDF(svg))
    expect(pdf).toMatch(/^%PDF-1\.4/)
    expect(pdf).toContain('Requests / second')
  })
})

describe('autoscaler targets', () => {
  it('selects scalable services by default and serializes an override', () => {
    const view = render(<form><AutoscalerTargetEditor prefix="component-7" resetKey="strategy-1" defaults={{intervalSeconds: 15, minReplicas: 1, maxReplicas: 10, parameters: '{}'}} components={[{id: 'frontend', traits: ['scalable']}, {id: 'redis', traits: []}]} /></form>)
    expect(screen.getByText('1 / 1')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox', {name: 'Custom values'}))
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Min replicas'}), {target: {value: '3'}})
    const overrides = view.container.querySelector<HTMLInputElement>('input[name="component-7.targetOverrides"]')
    expect(JSON.parse(overrides?.value ?? '{}').frontend.minReplicas).toBe(3)
  })
})

it('keeps workload, traffic distribution and preview in separate tabs without losing edits', () => {
  const {container} = render(<form><LoadProfileEditor prefix="load" zones={['zone-a', 'zone-b']} /></form>)
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Requests / second'}), {target: {value: '42'}})
  fireEvent.click(screen.getByRole('tab', {name: 'Traffic distribution'}))
  fireEvent.change(screen.getByRole('spinbutton', {name: 'zone-a'}), {target: {value: '3'}})
  fireEvent.click(screen.getByRole('tab', {name: 'Preview'}))
  expect(screen.getByRole('img', {name: /Load profile lasting/})).toBeVisible()
  fireEvent.click(screen.getByRole('tab', {name: 'Workload'}))
  expect(screen.getByRole('spinbutton', {name: 'Requests / second'})).toHaveValue(42)
  expect(JSON.parse((container.querySelector('input[name="load.steps"]') as HTMLInputElement).value)[0].rps).toBe(42)
  expect(JSON.parse((container.querySelector('input[name="load.geographicSteps"]') as HTMLInputElement).value)[0].weights['zone-a']).toBe(3)
})

it('preserves generator settings while switching between workload and generator tabs', () => {
  const {container} = render(<form><LoadProfileEditor prefix="load" zones={[]} settings={<label>Load workers<input name="workers" defaultValue="1" /></label>} /></form>)
  expect(screen.queryByRole('textbox', {name: 'Load workers'})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('tab', {name: 'Generator'}))
  fireEvent.change(screen.getByRole('textbox', {name: 'Load workers'}), {target: {value: '3'}})
  fireEvent.click(screen.getByRole('tab', {name: 'Workload'}))
  expect(new FormData(container.querySelector('form')!).get('workers')).toBe('3')
})
