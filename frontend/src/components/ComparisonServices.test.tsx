import {fireEvent, render, screen, within} from '@testing-library/react'
import {expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Artifact, Experiment, Session, TimeSeriesDataset} from '../types'
import {ResultsView} from './ResultsView'

vi.mock('../api', () => ({request: vi.fn(async () => ({items: []}))}))

const start = Date.parse('2026-11-11T00:00:00Z')
function run(id: string, p95: number, restarts: [number, number]): {artifact: Artifact; dataset: TimeSeriesDataset} {
  const points = (value: (index: number) => number) => Array.from({length: 8}, (_, index) => ({timestamp: new Date(start + index * 15000).toISOString(), value: value(index)}))
  const artifact = {id, operationId: '', workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: id, sizeBytes: 1, sensitive: false, verifiedAt: '2026-11-11T01:00:00Z'} as Artifact
  const dataset = {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: id, version: 'v1alpha1'}, spec: {applicationRef: 'app', clusterServer: 'https://cluster.example', namespace: 'shop', start: new Date(start).toISOString(), end: new Date(start + 7 * 15000).toISOString(), stepSeconds: 15, source: {kind: 'prometheus', version: 'v1', profile: 'application'}, summary: {metrics: 6, series: 7, samples: 56}, series: [
    {metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: 'recommendation'}, points: points(() => p95)},
    {metric: 'application.p95_response_time', unit: 'milliseconds', aggregation: 'mean', labels: {destination_workload: 'cart'}, points: points(() => 30)},
    {metric: 'application.request_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {destination_workload: 'recommendation'}, points: points(() => 42)},
    {metric: 'application.request_rps', unit: 'requests_per_second', aggregation: 'sum', labels: {destination_workload: 'cart'}, points: points(() => 17)},
    {metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {workload: 'recommendation', pod: 'recommendation-1'}, points: points(() => .2)},
    {metric: 'deployment.ready_replicas', unit: 'replicas', aggregation: 'sum', labels: {deployment: 'recommendation'}, points: points(() => 1)},
    {metric: 'pod.restarts', unit: 'count', aggregation: 'sum', labels: {workload: 'recommendation', pod: 'recommendation-1'}, points: points(index => index < 4 ? restarts[0] : restarts[1])}
  ]}} as TimeSeriesDataset
  return {artifact, dataset}
}

it('shows req/s, restarts and per-service charts for each series of a saved comparison', async () => {
  const runs = [run('a1', 1800, [4, 6]), run('a2', 2200, [0, 0]), run('b1', 60, [1, 1])]
  vi.mocked(request).mockImplementation(async path => runs.find(item => path.includes(item.artifact.id))?.dataset as never ?? {items: []} as never)
  const comparison = {id: 'cmp', ownerUserId: 'alice', name: 'Limits', kind: 'comparison', status: 'succeeded', variants: [
    {id: 'limited', name: 'Limited', alias: 'Limited', configuration: {}, trials: runs.slice(0, 2).map((item, index) => ({id: `t${index}`, position: index + 1, status: 'succeeded', resultArtifactId: item.artifact.id}))},
    {id: 'unlimited', name: 'Unlimited', alias: 'Unlimited', configuration: {}, trials: [{id: 't2', position: 1, status: 'succeeded', resultArtifactId: 'b1'}]}
  ]} as unknown as Experiment
  render(<ResultsView artifacts={runs.map(item => item.artifact)} experiments={[]} operations={[]} workspaces={[]} session={{authenticated: true, user: {id: 'alice', role: 'user'}} as Session} changed={async () => {}} openOperation={() => {}} focusedExperiment={comparison} onBack={() => {}} comparison={{sourceConfigurations: {}}} />)
  const table = await screen.findByRole('table', {name: 'Services ranked by bottleneck score'})
  // Experiment-grade summary: the bottleneck is named in a sentence.
  expect(screen.getByRole('region', {name: 'Verdict'})).toHaveTextContent(/recommendation is the bottleneck/)
  const row = within(table).getByRole('button', {name: 'recommendation'}).closest('tr')!
  expect(row).toHaveTextContent('Bottleneck')
  const cells = within(row).getAllByRole('cell')
  expect(cells[0]).toHaveTextContent('42')
  expect(cells[1]).toHaveTextContent('2')
  expect(cells[4]).toHaveTextContent('1')
  // The P95 sparkline and the expanded row's four charts come from the worker's curves.
  await vi.waitFor(() => expect(row.querySelector('.trend-cell .sparkline')).not.toBeNull())
  fireEvent.click(within(table).getByRole('button', {name: 'recommendation'}))
  for (const title of ['Server P95', 'Requests (req/s)', 'CPU (cores)', 'Ready replicas']) expect(within(table).getByRole('img', {name: `recommendation: ${title} over time`})).toBeInTheDocument()
  // The second series has its own ranking.
  fireEvent.click(screen.getByRole('button', {name: 'Unlimited'}))
  const other = within(screen.getByRole('table', {name: 'Services ranked by bottleneck score'})).getByRole('button', {name: 'recommendation'}).closest('tr')!
  expect(other).not.toHaveTextContent('Bottleneck')
  expect(within(other).getAllByRole('cell')[4]).toHaveTextContent('0')
})
