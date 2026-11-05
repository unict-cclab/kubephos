import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {WorkflowSteps, workflowFor, type WorkflowCounts} from './WorkflowSteps'

const counts: WorkflowCounts = {isAdmin: true, infrastructure: 4, clusters: 2, deployments: 1, configurations: 3, runs: 5}

describe('workflowFor', () => {
  it('finds the workflow that hosts a step or a tool', () => {
    expect(workflowFor('comparisons', counts)?.label).toBe('Experiments')
    expect(workflowFor('deployments', counts)?.label).toBe('Platform')
    expect(workflowFor('catalog', counts)).toBeUndefined()
  })

  it('starts other users at clusters because infrastructure is administrator-only', () => {
    const platform = workflowFor('kubernetes', {...counts, isAdmin: false})!
    expect(platform.steps.map(step => step.view)).toEqual(['kubernetes'])
    expect(platform.tools.map(tool => tool.view)).toEqual(['deployments'])
    expect(workflowFor('infrastructure', {...counts, isAdmin: false})).toBeUndefined()
  })
})

it('numbers the ordered steps and keeps tools apart, without a number', () => {
  const navigate = vi.fn()
  render(<WorkflowSteps workflow={workflowFor('results', counts)!} view="results" navigate={navigate} />)

  const steps = within(screen.getByRole('list', {name: 'Experiments steps'})).getAllByRole('button')
  expect(steps.map(step => step.textContent)).toEqual(['1ConfigureConfigurations · 3', '2RunRuns · 5'])
  expect(steps[1]).toHaveAttribute('aria-current', 'step')

  const tools = within(screen.getByRole('list', {name: 'Experiments tools'})).getAllByRole('button')
  expect(tools.map(tool => tool.textContent)).toEqual(['ComparisonsCompare saved results'])

  fireEvent.click(steps[0])
  expect(navigate).toHaveBeenCalledWith('experiments')
  fireEvent.click(tools[0])
  expect(navigate).toHaveBeenCalledWith('comparisons')
})

it('drops the number when the sequence has a single step', () => {
  render(<WorkflowSteps workflow={workflowFor('deployments', {...counts, isAdmin: false})!} view="deployments" navigate={() => {}} />)
  expect(within(screen.getByRole('list', {name: 'Platform steps'})).getByRole('button')).toHaveTextContent(/^CreateClusters · 2$/)
  expect(screen.getByRole('button', {name: /Deployments/})).toHaveAttribute('aria-current', 'page')
})
