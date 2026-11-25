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

it('shows only the charts at first, starting with the heatmap, and adds sections from the Sections menu', async () => {
  vi.mocked(request).mockImplementation(async path => path === layoutPath ? {layout: null} as never : path.endsWith('/figures') ? {items: []} as never : dataset as never)
  render(<ResultsView artifacts={[artifact]} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'token'}} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  fireEvent.click(await screen.findByRole('button', {name: 'Strategy · Execution 1'}))
  const charts = within(await screen.findByRole('region', {name: 'Charts'}))
  await waitFor(() => expect(charts.getAllByRole('figure')[0]).toHaveAccessibleName('Placement heatmap'))
  expect(screen.queryByRole('button', {name: /^Statistics/})).not.toBeInTheDocument()
  expect(screen.queryByRole('heading', {name: /Throughput|latency/i})).not.toBeInTheDocument()
  fireEvent.click(charts.getByRole('button', {name: 'Sections'}))
  expect(screen.getAllByRole('menuitemcheckbox').map(item => item.querySelector('strong')!.textContent)).toEqual(['Summary', 'Load and latency', 'Services', 'Statistics', 'Environment & methods', 'Placement'])
  fireEvent.click(screen.getByRole('menuitemcheckbox', {name: /^Statistics/}))
  expect(await screen.findByRole('button', {name: /^Statistics/})).toHaveAttribute('aria-expanded', 'true')
  expect(request).toHaveBeenCalledWith(layoutPath, {method: 'PUT', body: JSON.stringify({layout: {version: 1, sections: ['statistics']}})}, 'token')
})
