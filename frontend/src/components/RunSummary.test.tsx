import {render, screen} from '@testing-library/react'
import axe from 'axe-core'
import {describe, expect, it} from 'vitest'
import type {ExperimentConfiguration} from '../types'
import {loadVolume, manualStartText, readWizardPlan, repetitionTiming, RunEstimate, savedLoadPlan} from './RunSummary'
import {loadProfileSteps} from './LoadProfileEditor'

const steps = loadProfileSteps([{type: 'constant', durationSeconds: 600, rps: 100}, {type: 'constant', durationSeconds: 300, rps: 300}])
const configuration = {id: 'c', name: 'Baseline', kind: 'single', definition: {applicationValues: {}, components: [
  {id: 'component-1', pluginId: 'load', capability: 'load.session.control', configuration: {steps: [{type: 'constant', durationSeconds: 600, rps: 100}, {type: 'constant', durationSeconds: 300, rps: 300}], warmupSeconds: 60, drainSeconds: 30, replicas: 2, profileCallChain: true, profileSecondsPerTask: 45, taskWeights: {index: 1, cart: 2, checkout: 0}}}
]}} as unknown as ExperimentConfiguration

describe('run timing', () => {
  it('adds profiling, warmup and drain around the measured profile', () => {
    const plan = savedLoadPlan(configuration)!
    expect(plan).toMatchObject({warmupSeconds: 60, drainSeconds: 30, workers: 2, profiling: {secondsPerTask: 45, tasks: 2}})
    expect(repetitionTiming(plan)).toEqual({profiling: 90, warmup: 60, measured: 900, drain: 30, total: 1080})
  })
  it('reports the measured volume of the profile', () => {
    const volume = loadVolume(steps)
    expect(volume.peak).toBe(300)
    expect(Math.round(volume.requests)).toBe(150000)
    expect(Math.round(volume.mean)).toBe(167)
  })
  it('uses the load generator defaults when the saved plan omits them', () => {
    const plan = savedLoadPlan({...configuration, definition: {applicationValues: {}, components: [{id: 'l', pluginId: 'load', capability: 'load.session.control', configuration: {steps: []}}]}} as unknown as ExperimentConfiguration)
    expect(plan).toMatchObject({warmupSeconds: 0, drainSeconds: 30, workers: 1, profiling: undefined})
  })
})

it('estimates the whole series in the New run dialog', async () => {
  render(<RunEstimate configuration={configuration} runs={3} restSeconds={120} pilot />)
  const estimate = screen.getByRole('region', {name: 'Run estimate'})
  expect(estimate).toHaveTextContent('4 (1 warm-up + 3 measured)')
  expect(estimate).toHaveTextContent('18m of traffic')
  // 4 × 18 min + 3 × 2 min of rest.
  expect(estimate).toHaveTextContent('≈ 1h 18m of traffic and rest')
  const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
  expect(results.violations).toEqual([])
})

it('reads the load plan, strategy targets and network injection from the wizard form', () => {
  const form = document.createElement('form')
  form.innerHTML = `<input type="hidden" name="component-2.steps" value='${JSON.stringify([{type: 'constant', durationSeconds: 300, rps: 50}])}'>
    <input name="component-2.warmupSeconds" value="60"><input name="component-2.drainSeconds" value="15"><input name="component-2.replicas" value="3">
    <div class="experiment-component-list"><section class="experiment-component"><div class="experiment-component-head"><strong>Horizontal Pod Autoscaler</strong></div>
      <select><option selected>HPA CPU 70 %</option></select>
      <div class="target-editor-list"><article><div class="target-editor-row"><input type="checkbox" checked><span>frontend</span></div></article><article><div class="target-editor-row"><input type="checkbox"><span>redis-cart</span></div></article></div>
    </section></div>
    <div class="network-section-heading"><input type="checkbox"></div>`
  const plan = readWizardPlan(form)
  expect(plan.load).toMatchObject({warmupSeconds: 60, drainSeconds: 15, workers: 3, profiling: undefined})
  expect(repetitionTiming(plan.load!).total).toBe(375)
  expect(plan.strategies).toEqual([{title: 'Horizontal Pod Autoscaler', setting: 'HPA CPU 70 %', targets: ['frontend'], excluded: ['redis-cart']}])
  expect(plan.network).toBe(false)
})

it('explains which repetitions wait for a manual Start', () => {
  render(<RunEstimate configuration={configuration} runs={3} restSeconds={60} pilot={false} manualStart="first" />)
  expect(screen.getByRole('region', {name: 'Run estimate'})).toHaveTextContent('Only the first repetition waits for your Start; later repetitions start by themselves after the rest interval.')
  expect(manualStartText('every')).toMatch(/^Every repetition waits for your Start/)
  expect(manualStartText('first', 1)).toBe('The run waits for your Start after setup.')
  const form = document.createElement('form')
  form.innerHTML = '<select name="startMode"><option value="manual" selected>Manual</option></select><select name="manualStartScope"><option value="every" selected>Every</option></select>'
  expect(readWizardPlan(form).manualStart).toBe('every')
  form.querySelector<HTMLSelectElement>('[name="startMode"]')!.innerHTML = '<option value="automatic" selected>Automatic</option>'
  expect(readWizardPlan(form).manualStart).toBeUndefined()
})
