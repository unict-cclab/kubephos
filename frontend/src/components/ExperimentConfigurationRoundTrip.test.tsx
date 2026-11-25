import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {createElement} from 'react'
import {afterEach, expect, it, vi} from 'vitest'
import type {Application, ExperimentConfiguration, ManagedResource, Plugin, Workspace} from '../types'
import {ExperimentConfigurationDialog} from './ExperimentConfigurationsView'

// Round trips of saved configurations through the editor: what is saved again
// must be what the run will execute.

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

function saved(configuration: Record<string, unknown>, applicationValues: Record<string, unknown> = {}): ExperimentConfiguration {
  return {id: 'cfg_saved', kind: 'single', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues, components: [
    {id: 'component-1', pluginId: 'load', capability: 'load.session.control', configuration, targets: {include: [], exclude: []}},
    {id: 'component-2', pluginId: 'metrics', capability: 'metrics.timeseries.collect', configuration: {}, targets: {include: [], exclude: []}}
  ]}, validation: {valid: true, issues: []}, createdAt: '', updatedAt: '2026-09-12T00:00:00Z'}
}

function stubFetch() {
  const fetch = vi.fn(async (_path: RequestInfo | URL, _options?: RequestInit) => new Response(JSON.stringify({id: 'cfg_saved'}), {status: 200, headers: {'Content-Type': 'application/json'}}))
  vi.stubGlobal('fetch', fetch)
  return () => fetch.mock.calls.filter(([path, options]) => String(path).includes('/experiment-configurations') && options?.method === 'PUT').map(([, options]) => JSON.parse(String(options!.body)))
}

async function save(item: ExperimentConfiguration, prepare?: () => Promise<void> | void) {
  const sent = stubFetch()
  const close = vi.fn()
  render(createElement(ExperimentConfigurationDialog, {open: true, item, close, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application, other], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
  await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
  await prepare?.()
  fireEvent.click(screen.getByRole('button', {name: 'Review'}))
  fireEvent.click(screen.getByRole('button', {name: 'Save changes'}))
  await waitFor(() => expect(close).toHaveBeenCalled())
  return sent()[0]
}

it('saves again workload phases whose durations are not multiples of six seconds', async () => {
  const steps = [{type: 'constant', durationSeconds: 45, rps: 10}, {type: 'sinusoidal', durationSeconds: 125, minRps: 5, maxRps: 20, periodSeconds: 100, phaseSeconds: 10}]
  const body = await save(saved({scenarioId: 'default', replicas: 2, warmupSeconds: 60, taskWeights: {browse: 3}, steps, geographicSteps: [{type: 'constant', durationSeconds: 170, weights: {'zone-a': 3, 'zone-b': 1}}]}, {threads: 16}))
  expect(body.definition.applicationValues).toEqual({threads: 16})
  const load = body.definition.components.find((component: {capability: string}) => component.capability.startsWith('load.')).configuration
  expect(load.steps).toMatchObject(steps)
  expect(load.geographicSteps).toEqual([{type: 'constant', durationSeconds: 170, weights: {'zone-a': 3, 'zone-b': 1}}])
  expect(load).toMatchObject({replicas: 2, warmupSeconds: 60, maxUsers: 1000, spawnRate: 10, drainSeconds: 30, taskWeights: {browse: 3}})
})

it('does not carry the saved values of one application into another', async () => {
  const body = await save(saved({scenarioId: 'default', taskWeights: {browse: 3}, steps: [{type: 'constant', durationSeconds: 600, rps: 10}]}, {threads: 16}), async () => {
    fireEvent.click(screen.getByRole('button', {name: 'General'}))
    fireEvent.change(screen.getByRole('combobox', {name: 'Application'}), {target: {value: other.reference}})
  })
  expect(body.applicationRef).toBe(other.reference)
  expect(body.definition.applicationValues).toEqual({threads: 2})
  // The saved request mix names the Locust tasks of the previous application.
  expect(body.definition.components[0].configuration.taskWeights).toBeUndefined()
})

it('keeps the saved repetition schedule when the configuration is edited', async () => {
  const item = saved({scenarioId: 'default', steps: [{type: 'constant', durationSeconds: 600, rps: 10}]})
  item.definition = {...item.definition, restSecondsBetweenTrials: 90, firstTrialPilot: true}
  const body = await save(item)
  expect(body.definition).toMatchObject({restSecondsBetweenTrials: 90, firstTrialPilot: true})
})
