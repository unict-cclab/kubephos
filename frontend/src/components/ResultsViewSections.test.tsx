import {cleanup, fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {ResultsView} from './ResultsView'
import {request} from '../api'
import type {Artifact, Experiment, TimeSeriesDataset} from '../types'

vi.mock('../api', () => ({request: vi.fn()}))
afterEach(cleanup)

const artifact = {id: 'one', operationId: 'op', workspaceId: 'ws', name: 'one', type: 'TimeSeriesDataset', version: 'v1alpha1', sensitive: false, verifiedAt: '2026-11-15T10:00:00Z'} as Artifact
const dataset = {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: 'one', version: 'v1alpha1'}, spec: {
  applicationRef: 'app:test', namespace: 'app', clusterServer: 'https://cluster', start: '2026-11-15T09:59:45Z', end: '2026-11-15T10:00:00Z', stepSeconds: 15,
  source: {kind: 'prometheus-v1', version: '1', profile: 'test'}, summary: {metrics: 1, series: 1, samples: 2},
  series: [{metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {}, points: [{timestamp: '2026-11-15T09:59:45Z', value: 1}, {timestamp: '2026-11-15T10:00:00Z', value: 2}]}],
  placement: {status: 'available', source: 'test', stepSeconds: 15, nodes: [{name: 'node-one', cluster: 'cluster'}], snapshots: [{timestamp: '2026-11-15T10:00:00Z', observed: true, pods: [{uid: 'one', name: 'one', service: 'api', node: 'node-one', cluster: 'cluster'}]}]}
}} as TimeSeriesDataset
const experiment = {id: 'experiment', kind: 'instance', name: 'Test', status: 'succeeded', variants: [{id: 'variant', name: 'Strategy', trials: [{id: 'trial-one', position: 1, status: 'succeeded', resultArtifactId: 'one'}]}]} as Experiment
const layoutPath = '/account/result-layouts/experiments/experiment'

it('shows only the charts at first, starting with the heatmap, and adds sections from Display', async () => {
  vi.mocked(request).mockImplementation(async path => path === layoutPath ? {layout: null} as never : path.endsWith('/figures') ? {items: []} as never : dataset as never)
  render(<ResultsView artifacts={[artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'token'}} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  // One execution: no run tabs ("All executions" would show the same run), its own view directly.
  const charts = within(await screen.findByRole('region', {name: 'Charts'}))
  await waitFor(() => expect(charts.getAllByRole('figure')[0]).toHaveAccessibleName('Placement heatmap'))
  expect(screen.queryByRole('navigation', {name: 'Run executions'})).not.toBeInTheDocument()
  expect(screen.queryByRole('button', {name: 'All executions'})).not.toBeInTheDocument()
  expect(screen.queryByRole('button', {name: /^Statistics/})).not.toBeInTheDocument()
  expect(screen.queryByRole('heading', {name: /Throughput|latency/i})).not.toBeInTheDocument()
  // "Display" is a page command: in the page header, left of Export, not in the Charts header.
  expect(charts.queryByRole('button', {name: /^Display|Sections/})).not.toBeInTheDocument()
  const actions = within(document.querySelector('.results-detail-actions') as HTMLElement)
  expect(actions.getAllByRole('button').map(button => button.textContent).slice(0, 2)).toEqual([expect.stringMatching(/^Display: \d of \d sections shown$/), 'Export'])
  fireEvent.click(actions.getByRole('button', {name: /^Display/}))
  expect(screen.getAllByRole('menuitemcheckbox').map(item => item.querySelector('strong')!.textContent)).toEqual(['Charts', 'Summary', 'Load and latency', 'Services', 'Statistics', 'Environment & methods', 'Placement'])
  fireEvent.click(screen.getByRole('menuitemcheckbox', {name: /^Statistics/}))
  expect(await screen.findByRole('button', {name: /^Statistics/})).toHaveAttribute('aria-expanded', 'true')
  expect(request).toHaveBeenCalledWith(layoutPath, {method: 'PUT', body: JSON.stringify({layout: {version: 1, sections: ['statistics']}})}, 'token')
})

it('keeps the header to Display, Export, Create figure and ⋯ (visibility, reset), with Configuration in the meta line', async () => {
  const configured = {...experiment, configurationId: 'cfg'} as Experiment
  vi.mocked(request).mockImplementation(async path => path === layoutPath ? {layout: {version: 1, sections: ['statistics']}} as never : path.endsWith('/sharing') ? {visibility: 'everyone', userIds: []} as never : path === '/sharing/users' ? {items: []} as never : path.endsWith('/figures') ? {items: []} as never : dataset as never)
  render(<ResultsView artifacts={[artifact]} experiments={[configured]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'token', user: {id: 'u', username: 'u', role: 'admin'}} as never} changed={async () => {}} openOperation={() => {}} focusedExperiment={configured} />)
  await screen.findByRole('region', {name: 'Charts'})
  // The header: Display and Export, then Create figure as the one primary action, then ⋯; Configuration is a link in the meta line.
  const header = within(document.querySelector('.results-detail-actions') as HTMLElement)
  expect(header.getAllByRole('button').map(button => button.textContent)).toEqual([expect.stringMatching(/^Display/), 'Export', 'Create figure', 'More actions'])
  expect(header.getByRole('button', {name: 'Create figure'})).toHaveClass('primary')
  expect(document.querySelectorAll('.results-detail-actions .button.primary')).toHaveLength(1)
  expect(header.queryByRole('link', {name: 'Configuration'})).not.toBeInTheDocument()
  expect(within(document.querySelector('.results-meta') as HTMLElement).getByRole('link', {name: 'Configuration'})).toHaveAttribute('href', '#experiments/configuration/cfg')
  expect(screen.queryByRole('button', {name: /^Share/})).not.toBeInTheDocument()
  expect(screen.queryByRole('button', {name: 'Reset'})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'More actions'}))
  const visibility = await screen.findByRole('button', {name: /^Visibility…/})
  await waitFor(() => expect(visibility).toHaveTextContent('Visible to all users'))
  expect(screen.getByRole('button', {name: /^Reset charts and sections/})).toBeInTheDocument()
  fireEvent.click(visibility)
  const dialog = await screen.findByRole('dialog', {name: 'Visibility of Test'})
  expect(await within(dialog).findByRole('combobox', {name: 'Who can see it'})).toHaveValue('everyone')
  fireEvent.click(within(dialog).getByRole('button', {name: 'Cancel'}))
  fireEvent.click(screen.getByRole('button', {name: 'More actions'}))
  fireEvent.click(screen.getByRole('button', {name: /^Reset charts and sections/}))
  expect(request).toHaveBeenCalledWith(layoutPath, {method: 'DELETE'}, 'token')
  expect(screen.getByText(/Default charts and sections restored/)).toBeInTheDocument()
})
