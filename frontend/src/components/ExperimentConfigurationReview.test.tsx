import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {createElement} from 'react'
import {afterEach, expect, it, vi} from 'vitest'
import type {Application, ExperimentConfiguration, ManagedResource, Plugin, Workspace} from '../types'
import {ExperimentConfigurationDialog} from './ExperimentConfigurationsView'

// The editor explains what a run does and why a feature cannot be used.

afterEach(() => vi.unstubAllGlobals())

const loadSchema = {type: 'object' as const, required: ['scenarioId', 'action', 'replicas', 'maxUsers', 'spawnRate'], properties: {
  scenarioId: {type: 'string' as const}, action: {type: 'string' as const, enum: ['start', 'stop', 'resume'], default: 'start'}, interactive: {type: 'boolean' as const, default: false},
  replicas: {type: 'integer' as const, title: 'Load workers', minimum: 1, maximum: 100, default: 1}, maxUsers: {type: 'integer' as const, title: 'Maximum concurrent users per worker', minimum: 1, maximum: 100000, default: 1000},
  spawnRate: {type: 'number' as const, title: 'Users started per second', minimum: 0.1, maximum: 100000, default: 10},
  steps: {type: 'array' as const, items: {type: 'object' as const}}, geographicSteps: {type: 'array' as const, items: {type: 'object' as const}},
  taskWeights: {type: 'object' as const, 'x-kubephos-hidden': true}, profileCallChain: {type: 'boolean' as const, default: false},
  warmupSeconds: {type: 'integer' as const, title: 'Warmup seconds', minimum: 0, maximum: 3600, default: 0}, drainSeconds: {type: 'integer' as const, title: 'Drain seconds', minimum: 0, maximum: 600, default: 30}
}}
const plugin = (id: string, capabilities: string[], schema: Plugin['schema'] = {type: 'object', properties: {}}): Plugin => ({id, name: id, version: '1.0.0', description: '', schema, capabilities, artifactInputs: [], runtime: {kind: 'process'}})
const loadPlugin = plugin('load', ['load.session.control'], loadSchema)
const metricsPlugin = plugin('metrics', ['metrics.timeseries.collect'])
const workspace: Workspace = {id: 'ws_default', name: 'Default', description: '', status: 'ready', createdAt: '2026-09-12T00:00:00Z'}
const cluster: ManagedResource = {id: 'cluster_ready', workspaceId: workspace.id, name: 'Development', kind: 'kubernetes-cluster', provider: 'proxmox', status: 'ready', validation: {valid: true, issues: []}, spec: {applicationPools: [{name: 'apps', zones: ['zone-a', 'zone-b']}]}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
const application: Application = {id: 'shop', reference: 'app:shop', name: 'Shop', description: '', origin: 'built-in', digest: 'sha256:shop', descriptor: {spec: {valuesSchema: {type: 'object', properties: {threads: {type: 'integer', title: 'Threads per worker', minimum: 1}}}, defaults: {threads: 4}, interface: {loadScenarios: [{id: 'default'}]}}}}
const other: Application = {...application, id: 'bank', reference: 'app:bank', name: 'Bank', digest: 'sha256:bank', descriptor: {spec: {valuesSchema: {type: 'object', properties: {threads: {type: 'integer', title: 'Threads per worker', minimum: 1}}}, defaults: {threads: 2}, interface: {loadScenarios: [{id: 'default'}]}}}}

const item = {id: 'cfg_saved', kind: 'single', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: [
  {id: 'component-1', pluginId: 'load', capability: 'load.session.control', configuration: {scenarioId: 'default', replicas: 2, warmupSeconds: 60, drainSeconds: 30, steps: [{type: 'constant', durationSeconds: 600, rps: 100}]}, targets: {include: [], exclude: []}},
  {id: 'component-2', pluginId: 'metrics', capability: 'metrics.timeseries.collect', configuration: {}, targets: {include: [], exclude: []}}
]}, validation: {valid: true, issues: []}, createdAt: '', updatedAt: '2026-09-12T00:00:00Z'} as ExperimentConfiguration

async function open() {
  vi.stubGlobal('fetch', vi.fn(async () => new Response('{"items":[]}', {status: 200, headers: {'Content-Type': 'application/json'}})))
  render(createElement(ExperimentConfigurationDialog, {open: true, item, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application, other], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
  await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
}

it('summarises what one repetition does on the review step', async () => {
  await open()
  fireEvent.click(screen.getByRole('button', {name: 'Review'}))
  const summary = await screen.findByRole('region', {name: 'What a run does'})
  await waitFor(() => expect(summary).toHaveTextContent('Each repetition11m 30s of traffic'))
  expect(summary).toHaveTextContent('Warmup 1m')
  expect(summary).toHaveTextContent('Measured profile 10m')
  expect(summary).toHaveTextContent('peak 100 req/s · mean 100 req/s · ≈ 60 k requests · 2 Locust workers')
  expect(summary).toHaveTextContent('Default Kubernetes scheduler, no descheduler, fixed replica counts.')
  expect(summary).toHaveTextContent('Chosen when you start a run')
  const axe = (await import('axe-core')).default
  const results = await axe.run(summary, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
  expect(results.violations).toEqual([])
})

it('explains why network injection and the mon-agent cannot be enabled instead of showing an empty tab', async () => {
  await open()
  fireEvent.click(screen.getByRole('button', {name: 'Monitoring'}))
  fireEvent.click(screen.getByRole('tab', {name: 'Network'}))
  expect(screen.getByRole('checkbox', {name: 'Enable injection'})).toBeDisabled()
  expect(await screen.findByRole('note', {name: 'Network injection requirements'})).toHaveTextContent('No network injection plugin is installed.')
  fireEvent.click(screen.getByRole('tab', {name: 'Agent'}))
  expect(await screen.findByRole('note', {name: 'Mon-agent customization'})).toHaveTextContent('No mon-agent profile plugin is installed.')
})

it('says what an empty scheduling tab means', async () => {
  await open()
  fireEvent.click(screen.getByRole('button', {name: 'Scheduling and scaling'}))
  expect(await screen.findByText('Without a scheduler, pods are placed by the default Kubernetes scheduler.')).toBeInTheDocument()
})
