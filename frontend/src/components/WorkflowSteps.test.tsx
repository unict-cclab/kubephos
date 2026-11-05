import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {WorkflowSteps, workflowFor, type WorkflowCounts} from './WorkflowSteps'

const counts: WorkflowCounts = {isAdmin: true, infrastructure: 4, clusters: 2, deployments: 1, configurations: 3, runs: 5}

describe('workflowFor', () => {
  it('finds the workflow that hosts a page', () => {
    expect(workflowFor('comparisons', counts)?.label).toBe('Experiment steps')
    expect(workflowFor('deployments', counts)?.label).toBe('Platform steps')
    expect(workflowFor('catalog', counts)).toBeUndefined()
  })

  it('starts other users at clusters because infrastructure is administrator-only', () => {
    expect(workflowFor('kubernetes', {...counts, isAdmin: false})?.steps.map(step => step.view)).toEqual(['kubernetes', 'deployments'])
    expect(workflowFor('infrastructure', {...counts, isAdmin: false})).toBeUndefined()
  })
})

it('presents configure, run and compare as one ordered workflow', () => {
  const navigate = vi.fn()
  render(<WorkflowSteps workflow={workflowFor('results', counts)!} view="results" navigate={navigate} />)

  const steps = within(screen.getByRole('navigation', {name: 'Experiment steps'})).getAllByRole('button')
  expect(steps.map(step => step.textContent)).toEqual(['1ConfigureConfigurations · 3', '2RunRuns · 5', '3CompareComparisons'])
  expect(steps[1]).toHaveAttribute('aria-current', 'step')

  fireEvent.click(steps[0])
  expect(navigate).toHaveBeenCalledWith('experiments')
  fireEvent.click(steps[2])
  expect(navigate).toHaveBeenCalledWith('comparisons')
})
