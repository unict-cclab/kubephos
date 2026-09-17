import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {AutoscalerTargetEditor} from './AutoscalerTargetEditor'
import {LoadProfileEditor, LoadProfilePreview, loadProfileSteps} from './LoadProfileEditor'
import {svgToPDF} from './ResultsView'

describe('load profile preview', () => {
  it('restores saved workload and geographic phases for editing', () => {
    const view = render(<form><LoadProfileEditor prefix="component-1" zones={['zone-a', 'zone-b']} values={{steps: [{type: 'constant', durationSeconds: 120, rps: 77}], geographicSteps: [{type: 'constant', durationSeconds: 120, weights: {'zone-a': 3, 'zone-b': 1}}]}} /></form>)

    expect(screen.getByRole('spinbutton', {name: 'Requests / second'})).toHaveValue(77)
    expect(screen.getAllByRole('spinbutton', {name: 'Duration (minutes)'})[0]).toHaveValue(2)
    expect(view.container.querySelector<HTMLInputElement>('input[name="component-1.steps"]')?.value).toContain('"rps":77')
    expect(view.container.querySelector<HTMLInputElement>('input[name="component-1.geographicSteps"]')?.value).toContain('"zone-a":3')
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
    expect(screen.getByRole('button', {name: 'PNG 600 DPI'})).toBeInTheDocument()
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
