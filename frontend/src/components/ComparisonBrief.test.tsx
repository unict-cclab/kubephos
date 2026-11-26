import {cleanup, fireEvent, render, screen} from '@testing-library/react'
import {afterEach, expect, it} from 'vitest'
import {formatDate} from '../lib'
import {ResourceAttribution} from './ResourceAttribution'
import {VerdictStrip} from './results/Overview'
import type {Insight} from './results/insights'

afterEach(cleanup)

// Saved comparisons keep their page short: findings first, explanations on demand.
it('shows only the findings of a brief summary until its details are asked for', () => {
  const insights = [{id: 'slo', severity: 'warning', title: 'The SLO was missed in 6.2 % of windows', detail: 'Share of measured windows whose client P95 exceeded 350 ms.', target: 'latency'}] as Insight[]
  render(<VerdictStrip insights={insights} notices={[{kind: 'warning', text: 'The load generator was saturated.'}]} onNavigate={() => {}} brief />)
  expect(screen.getByText('The SLO was missed in 6.2 % of windows.')).toBeInTheDocument()
  expect(screen.queryByText(/Share of measured windows/)).not.toBeInTheDocument()
  expect(screen.queryByText('The load generator was saturated.')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Show details'}))
  expect(screen.getByText(/Share of measured windows/)).toBeInTheDocument()
  expect(screen.getByText('The load generator was saturated.')).toBeInTheDocument()
  expect(screen.getByRole('button', {name: 'Hide details'})).toHaveAttribute('aria-expanded', 'true')
})

it('writes who created and updated a comparison on one line, the update only when it differs', () => {
  const created = '2026-11-26T12:42:00Z'
  const {rerender} = render(<ResourceAttribution resource={{createdAt: created, updatedAt: created, createdBy: 'fabio.gallone'}} editable compact />)
  expect(document.querySelector('.attribution-line')).toHaveTextContent(`Created ${formatDate(created)} by fabio.gallone`)
  expect(screen.queryByText(/updated/)).not.toBeInTheDocument()
  const updated = '2026-11-27T09:00:00Z'
  rerender(<ResourceAttribution resource={{createdAt: created, updatedAt: updated, createdBy: 'fabio.gallone', modifiedBy: 'alice'}} editable compact />)
  expect(document.querySelector('.attribution-line')).toHaveTextContent(`Created ${formatDate(created)} by fabio.gallone · updated ${formatDate(updated)} by alice`)
})
