import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import axe from 'axe-core'
import {describe, expect, it, vi} from 'vitest'
import type {ComparisonView} from '../comparisons'
import {formatDate} from '../lib'
import {ComparisonIntegrityNotice, compatibilityFieldLabel, compatibilityValue} from './ComparisonIntegrityNotice'

const base = {comparison: {id: 'cmp', name: 'Paper', ownerUserId: 'alice', series: [], createdAt: '', updatedAt: ''}, result: {}, sources: [], artifacts: [], sourceConfigurations: {}} as unknown as ComparisonView
const captured = '2026-11-02T10:15:00Z'

async function audit() {
  const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
  expect(results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))).toEqual([])
}

describe('comparison snapshot status', () => {
  it('reports when a snapshot was frozen and stays quiet when it is current and compatible', () => {
    render(<ComparisonIntegrityNotice view={{...base, snapshot: {capturedAt: captured}, stale: false, compatibility: {compatible: true, differences: [], sources: []}}} onRefresh={async () => {}} />)
    expect(screen.getByText(`Frozen on ${formatDate(captured)}`)).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
    expect(screen.queryByRole('table')).not.toBeInTheDocument()
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('marks legacy comparisons as live and offers to freeze them', async () => {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    render(<ComparisonIntegrityNotice view={base} onRefresh={onRefresh} />)
    expect(screen.getByText('Live comparison')).toBeInTheDocument()
    expect(screen.getByText(/results may change; save or refresh to freeze/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Freeze results'}))
    fireEvent.click(within(await screen.findByRole('dialog', {name: 'Freeze comparison?'})).getByRole('button', {name: 'Freeze results'}))
    await waitFor(() => expect(onRefresh).toHaveBeenCalledOnce())
  })
})

describe('stale snapshots', () => {
  const stale = {...base, snapshot: {capturedAt: captured}, stale: true, staleReasons: ['Experiment "Baseline" was renamed to "Baseline v2".', 'Baseline: 1 newer completed run is not included.']}

  it('lists why the snapshot is stale and refreshes only after confirmation', async () => {
    const onRefresh = vi.fn().mockResolvedValue(undefined)
    render(<main><ComparisonIntegrityNotice view={stale} onRefresh={onRefresh} /></main>)
    const banner = screen.getByRole('status', {name: 'The source experiments changed after this snapshot'})
    expect(within(banner).getByText('Baseline: 1 newer completed run is not included.')).toBeInTheDocument()
    await audit()
    fireEvent.click(within(banner).getByRole('button', {name: 'Refresh snapshot'}))
    const dialog = await screen.findByRole('dialog', {name: 'Refresh snapshot?'})
    expect(dialog).toHaveTextContent('Statistics, plots and exports of this comparison may change')
    fireEvent.click(within(dialog).getByRole('button', {name: 'Cancel'}))
    expect(onRefresh).not.toHaveBeenCalled()
    fireEvent.click(within(banner).getByRole('button', {name: 'Refresh snapshot'}))
    fireEvent.click(within(await screen.findByRole('dialog', {name: 'Refresh snapshot?'})).getByRole('button', {name: 'Refresh snapshot'}))
    await waitFor(() => expect(onRefresh).toHaveBeenCalledOnce())
    expect(await screen.findByText('Comparison snapshot updated.')).toBeInTheDocument()
  })

  it('explains a 409 when a source is gone and keeps the frozen results', async () => {
    const onRefresh = vi.fn().mockRejectedValue(Object.assign(new Error('A source experiment is unavailable.'), {status: 409}))
    render(<ComparisonIntegrityNotice view={stale} onRefresh={onRefresh} />)
    fireEvent.click(screen.getByRole('button', {name: 'Refresh snapshot'}))
    fireEvent.click(within(await screen.findByRole('dialog', {name: 'Refresh snapshot?'})).getByRole('button', {name: 'Refresh snapshot'}))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('deleted or is no longer shared with you')
    expect(alert).toHaveTextContent('The frozen results are still shown.')
  })

  it('shows other refresh failures as reported by the API', async () => {
    render(<ComparisonIntegrityNotice view={stale} onRefresh={vi.fn().mockRejectedValue(Object.assign(new Error('KubePhos is temporarily unavailable.'), {status: 503}))} />)
    fireEvent.click(screen.getByRole('button', {name: 'Refresh snapshot'}))
    fireEvent.click(within(await screen.findByRole('dialog', {name: 'Refresh snapshot?'})).getByRole('button', {name: 'Refresh snapshot'}))
    expect(await screen.findByRole('alert')).toHaveTextContent('KubePhos is temporarily unavailable.')
  })

  it('hides the refresh action without a handler', () => {
    render(<ComparisonIntegrityNotice view={stale} />)
    expect(screen.getByRole('status')).toBeInTheDocument()
    expect(screen.queryByRole('button')).not.toBeInTheDocument()
  })
})

describe('load profile compatibility', () => {
  it('tabulates every differing setting per source with readable labels', async () => {
    const view: ComparisonView = {...base, snapshot: {capturedAt: captured}, compatibility: {compatible: false, sources: [{label: 'Baseline', fingerprint: 'a'}, {label: 'Scheduler', fingerprint: 'b'}], differences: [
      {field: 'load.steps', values: {Baseline: [{durationSeconds: 60, users: 10}], Scheduler: [{durationSeconds: 120, users: 10}]}},
      {field: 'metrics.sloMilliseconds', values: {Baseline: 200, Scheduler: 250}},
      {field: 'custom.field', values: {Baseline: 'x'}}
    ]}}
    render(<main><ComparisonIntegrityNotice view={view} /></main>)
    expect(screen.getByRole('heading', {name: 'Sources were not measured alike'})).toBeInTheDocument()
    const table = screen.getByRole('table', {name: 'Measurement settings that differ'})
    expect(within(table).getAllByRole('columnheader').map(cell => cell.textContent)).toEqual(['Setting', 'Baseline', 'Scheduler'])
    const rows = within(table).getAllByRole('row').slice(1)
    expect(rows[0]).toHaveTextContent('Load profile steps[{"durationSeconds":60,"users":10}][{"durationSeconds":120,"users":10}]')
    expect(rows[1]).toHaveTextContent('Response time SLO (ms)200250')
    expect(rows[2]).toHaveTextContent('custom.fieldx—')
    await audit()
  })

  it('shows no warning when sources are compatible', () => {
    render(<ComparisonIntegrityNotice view={{...base, snapshot: {capturedAt: captured}, compatibility: {compatible: true, differences: []}}} />)
    expect(screen.queryByText('Sources were not measured alike')).not.toBeInTheDocument()
  })

  it('labels known fields and shortens long values', () => {
    expect(compatibilityFieldLabel('load.replicas')).toBe('Load generator workers')
    expect(compatibilityFieldLabel('network.phases')).toBe('Network phases')
    expect(compatibilityValue(null).text).toBe('—')
    expect(compatibilityValue(false).text).toBe('false')
    const long = compatibilityValue({phases: Array.from({length: 10}, (_, index) => ({latency: index}))})
    expect(long.text.length).toBeLessThanOrEqual(58)
    expect(long.text.endsWith('…')).toBe(true)
    expect(JSON.parse(long.full).phases).toHaveLength(10)
  })
})
