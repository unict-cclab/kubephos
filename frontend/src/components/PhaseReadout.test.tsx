import {render, screen, within} from '@testing-library/react'
import {expect, it} from 'vitest'
import {TrafficDistributionReadout, workloadTable} from './PhaseReadout'
import {loadProfileSteps} from './LoadProfileEditor'

it('makes phases distinct rows with cumulative time intervals and normalized zone shares', () => {
  render(<TrafficDistributionReadout phases={[{type: 'constant', durationSeconds: 300, weights: {cloud: 1, 'edge-a': 3}}, {type: 'linear', durationSeconds: 600, startWeights: {cloud: 1, 'edge-a': 0}, endWeights: {cloud: 0, 'edge-a': 1}}]} />)
  const rows = within(screen.getByRole('table', {name: 'Traffic phases by zone'})).getAllByRole('row')
  expect(rows).toHaveLength(3)
  expect(rows[1]).toHaveTextContent('Phase 1')
  expect(rows[1]).toHaveTextContent('0–5 min')
  expect(rows[1]).toHaveTextContent('25')
  expect(rows[1]).toHaveTextContent('75')
  expect(rows[2]).toHaveTextContent('5–15 min')
  expect(rows[2]).toHaveTextContent('100 → 0')
  expect(screen.getByRole('columnheader', {name: 'edge-a (%)'})).toBeInTheDocument()
})

it('keeps only parameters relevant to the actual workload phase types', () => {
  const table = workloadTable(loadProfileSteps([{type: 'constant', durationSeconds: 900, rps: 30, baselineRps: 999}]))
  expect(table.headers).toEqual(['Phase', 'Time interval', 'Load', 'Rate (req/s)'])
  expect(table.rows).toEqual([['Phase 1', '0–15 min', 'Constant', '30']])
})
