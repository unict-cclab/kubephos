import {cleanup, fireEvent, render, screen, within} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {ResultsView} from './ResultsView'
import {request} from '../api'
import type {Artifact, Experiment, TimeSeriesDataset} from '../types'

vi.mock('../api', () => ({request: vi.fn()}))
afterEach(cleanup)

const dataset = {apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: 'one', version: 'v1alpha1'}, spec: {
  applicationRef: 'app:test', namespace: 'app', clusterServer: 'https://cluster', start: '2026-11-15T09:59:45Z', end: '2026-11-15T10:00:00Z', stepSeconds: 15,
  source: {kind: 'prometheus-v1', version: '1', profile: 'test'}, summary: {metrics: 1, series: 1, samples: 2},
  series: [{metric: 'cpu.cores', unit: 'cores', aggregation: 'sum', labels: {}, points: [{timestamp: '2026-11-15T09:59:45Z', value: 1}, {timestamp: '2026-11-15T10:00:00Z', value: 2}]}]
}} as TimeSeriesDataset
const artifact = (id: string) => ({id, operationId: 'op', workspaceId: 'ws', name: id, type: 'TimeSeriesDataset', version: 'v1alpha1', sensitive: false, verifiedAt: '2026-11-15T10:00:00Z'}) as Artifact
const trials = (prefix: string, count: number) => Array.from({length: count}, (_, index) => ({id: `${prefix}-${index + 1}`, position: index + 1, status: 'succeeded', resultArtifactId: `${prefix}-${index + 1}`}))
const show = (experiment: Experiment) => {
  vi.mocked(request).mockImplementation(async path => path.startsWith('/account/result-layouts') ? {layout: null} as never : path.endsWith('/figures') ? {items: []} as never : dataset as never)
  const artifacts = experiment.variants.flatMap(variant => variant.trials.map(trial => artifact(trial.resultArtifactId!)))
  render(<ResultsView artifacts={artifacts} experiments={[experiment]} operations={[]} workspaces={[]} session={{authenticated: true}} changed={async () => {}} openOperation={() => {}} focusedExperiment={experiment} />)
  return within(screen.getByRole('navigation', {name: 'Run executions'}))
}

it('labels the runs of one variant without repeating its name, which stays in the accessible name and tooltip', async () => {
  const tabs = show({id: 'e', kind: 'suite', name: 'Suite', status: 'succeeded', variants: [{id: 'v', name: 'fg-01', trials: trials('r', 5)}]} as unknown as Experiment)
  const first = tabs.getByRole('button', {name: 'fg-01 · Execution 1'})
  expect(first).toHaveTextContent(/^Execution 1$/)
  expect(first).toHaveAttribute('title', 'fg-01 · Execution 1')
  expect(tabs.getAllByRole('button').map(button => button.textContent)).toEqual(['All executions', 'Execution 1', 'Execution 2', 'Execution 3', 'Execution 4', 'Execution 5'])
  // Customize view follows the tabs in the focus order, on the same row.
  const customize = await screen.findByRole('button', {name: /^Customize view/})
  expect([...document.querySelector('.results-view-bar')!.querySelectorAll('button')].at(-1)).toBe(customize)
})

it('keeps the variant with several variants, compactable to "what tells it apart · #n"', () => {
  const tabs = show({id: 'e', kind: 'suite', name: 'Suite', status: 'succeeded', variants: [{id: 'a', name: 'CPU limit 200m', trials: trials('a', 3)}, {id: 'b', name: 'CPU limit 500m', trials: trials('b', 3)}]} as unknown as Experiment)
  const tab = tabs.getByRole('button', {name: 'CPU limit 500m · Execution 2'})
  expect(tab.querySelector('.run-tab-variant')).toHaveTextContent('CPU limit 500m')
  expect(tab.querySelector('.run-tab-run')).toHaveTextContent('Execution 2')
  expect(tab.querySelector('.run-tab-run-short')).toHaveTextContent('#2')
  // Compact: only what tells the variants apart.
  expect(tab.querySelector('.run-tab-variant-short')).toHaveTextContent(/^500m$/)
})

it('shows the first runs as tabs and the rest in a "+k more" menu; a run picked there becomes a visible tab', () => {
  const tabs = show({id: 'e', kind: 'suite', name: 'Suite', status: 'succeeded', variants: [{id: 'v', name: 'fg-01', trials: trials('r', 20)}]} as unknown as Experiment)
  expect(tabs.getAllByRole('button').map(button => button.textContent)).toEqual(['All executions', ...Array.from({length: 8}, (_, index) => `Execution ${index + 1}`), '+12 more'])
  fireEvent.click(tabs.getByRole('button', {name: '12 more executions'}))
  const menu = within(screen.getByRole('menu', {name: 'More executions'}))
  expect(menu.getAllByRole('menuitem')).toHaveLength(12)
  fireEvent.click(menu.getByRole('menuitem', {name: /^Execution 17/}))
  expect(tabs.getByRole('button', {name: 'fg-01 · Execution 17'})).toHaveAttribute('aria-current', 'page')
  expect(tabs.queryByRole('button', {name: 'fg-01 · Execution 8'})).not.toBeInTheDocument()
  expect(tabs.getByRole('button', {name: '12 more executions'})).toBeInTheDocument()
})
