import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {createElement} from 'react'
import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest'
import type {Application, ExperimentConfiguration, ManagedResource, Plugin, Workspace} from '../types'
import {isTerminalExperiment} from '../lib'
import {applicationZones, configurableSchema, ExperimentConfigurationDialog, ExperimentConfigurationsView, experimentPlugins, primaryCapabilities} from './ExperimentConfigurationsView'

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

afterEach(() => vi.unstubAllGlobals())

const plugin = (id: string, capabilities: string[], inputs: string[] = []): Plugin => ({
  id,
  name: id,
  version: '1.0.0',
  description: '',
  schema: {type: 'object', required: ['clusterRef', 'policy'], properties: {clusterRef: {type: 'string', format: 'kubephos-artifact-ref'}, policy: {type: 'string', enum: ['default', 'custom']}}},
  capabilities,
  artifactInputs: inputs.map(type => ({type, version: 'v1alpha1'})),
  runtime: {kind: 'process'}
})

describe('experiment configuration helpers', () => {
  it('keeps only user-configurable plugin fields', () => {
    expect(configurableSchema(plugin('scheduler', []).schema)).toEqual({type: 'object', required: ['policy'], properties: {policy: {type: 'string', enum: ['default', 'custom']}}})
  })

  it('separates primary capabilities from lifecycle hooks', () => {
    expect(primaryCapabilities(plugin('scheduler', ['scheduler.install', 'scheduler.preflight', 'scheduler.cleanup', 'lifecycle.cleanup']))).toEqual(['scheduler.install'])
  })

  it('keeps infrastructure plugins out of the simple experiment form', () => {
    const values = [plugin('scheduler', ['scheduler.install']), plugin('monitoring', ['monitoring.mon-agent.configure']), plugin('custom', ['research.strategy'], ['TargetBinding']), plugin('infra', ['infrastructure.provision'])]
    expect(experimentPlugins(values).map(item => item.id)).toEqual(['scheduler', 'monitoring'])
  })

  it('selects a ready cluster when asynchronously loaded data becomes available', async () => {
    const base = {open: true, close: () => {}, items: [], experiments: [], clusters: [], workspaces: [], applications: [], plugins: [], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}
    const view = render(createElement(ExperimentConfigurationDialog, base))
    view.rerender(createElement(ExperimentConfigurationDialog, {...base, clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin]}))

    await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
    expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled()
  })

  it('saves an external destination with its Locust template and parameters', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({id: 'cfg_external'}), {status: 201, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const changed = vi.fn().mockResolvedValue(undefined)
    const close = vi.fn()
    render(createElement(ExperimentConfigurationDialog, {open: true, close, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed, openOperation: () => {}}))

    await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'External API'}})
    fireEvent.change(screen.getByRole('combobox', {name: 'Destination type'}), {target: {value: 'external'}})
    await goToStep('External destination')
    fireEvent.change(screen.getByRole('textbox', {name: /^External endpoint/}), {target: {value: 'https://api.example.test/v1'}})
    fireEvent.click(screen.getByRole('tab', {name: 'Variables'}))
    fireEvent.change(screen.getByRole('textbox', {name: /^Locustfile variables \(JSON\)/}), {target: {value: '{"tenant":"demo"}'}})
    await waitFor(() => expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled())
    await goToStep('Review')
    fireEvent.click(screen.getByRole('button', {name: 'Create configuration'}))

    await waitFor(() => expect(fetch).toHaveBeenCalledOnce())
    const body = JSON.parse(fetch.mock.calls[0][1].body)
    expect(body).toMatchObject({
      name: 'External API',
      applicationRef: '',
      definition: {
        destinationMode: 'external',
        externalDestination: {targetUrl: 'https://api.example.test/v1', runtimeImage: 'locustio/locust:2.42.6', parameters: {tenant: 'demo'}},
        applicationValues: {},
        podDisruptionBudgetMinAvailable: 0
      }
    })
    expect(body.definition.externalDestination.script).toContain('from locust import HttpUser')
    expect(body.definition.components.map((component: {capability: string}) => component.capability)).toEqual(['load.session.control', 'metrics.timeseries.collect'])
    expect(body.definition.components[0].configuration).toMatchObject({action: 'start', interactive: false, scenarioId: 'external-load'})
    expect(body.definition.components[0].configuration).not.toHaveProperty('geographicSteps')
    expect(close).toHaveBeenCalledOnce()
    expect(changed).toHaveBeenCalledOnce()
  })

  it('derives unique application zones from the selected cluster', () => {
    expect(applicationZones(cluster)).toEqual(['zone-a', 'zone-b'])
  })

  it('configures the built-in muBench application without creating catalog variants', async () => {
	render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [legacyMubenchApplication, mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
	expect(screen.getByRole('combobox', {name: 'Application'})).toHaveTextContent('muBench')
	expect(screen.getByRole('combobox', {name: 'Application'})).not.toHaveTextContent('Legacy muBench')
    await goToStep('Application')
    expect(screen.getByRole('spinbutton', {name: 'Services'})).toHaveValue(6)
    expect(screen.getByRole('combobox', {name: 'Request topology'})).toHaveValue('chain')
    expect(screen.getByRole('spinbutton', {name: 'Workers per service'})).toHaveValue(2)
    expect(screen.queryByRole('button', {name: 'Generate application'})).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled()
  })

  it('updates muBench workload targets when its topology values change', async () => {
	const workloadTargets = [...Array.from({length: 4}, (_, index) => ({id: `s${index}`, apiVersion: 'apps/v1', kind: 'Deployment', name: `s${index}`, traits: ['scalable', 'schedulable'], index, dependencies: index < 3 ? [`s${index + 1}`] : []})), {id: 'node-proxy', apiVersion: 'apps/v1', kind: 'DaemonSet', name: 'node-proxy', traits: ['traffic-entrypoint'], index: 0, dependencies: ['s0']}]
	const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({workloadTargets}), {status: 200, headers: {'Content-Type': 'application/json'}}))
	vi.stubGlobal('fetch', fetch)
	render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    await goToStep('Application')

	fireEvent.change(screen.getByRole('spinbutton', {name: 'Services'}), {target: {value: '4'}})
	expect(screen.getByRole('button', {name: 'Continue'})).toBeDisabled()
	await waitFor(() => expect(fetch).toHaveBeenCalledOnce())
	await waitFor(() => expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled())
	expect(screen.queryByText(/configurable services/)).not.toBeInTheDocument()
	expect(screen.queryByText('Checking application topology…')).not.toBeInTheDocument()
	expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({serviceCount: 4, topology: 'chain'})
	expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled()
  })

  it('removes obsolete resource overrides when a LocalAI service is removed', async () => {
    const localaiApplication: Application = {...mubenchApplication, id: 'dev.kubephos.localai', reference: 'app:dev.kubephos.localai', name: 'LocalAI', descriptor: {spec: {
      valuesSchema: {type: 'object', 'x-kubephos-dynamic-topology': true, 'x-kubephos-service-prefix': 'm', properties: {
        models: {type: 'array', title: 'Models per service', minItems: 1, items: {type: 'string', enum: ['Qwen-small', 'Qwen-coder']}, 'x-kubephos-item-prefix': 'm'},
        initialReplicas: {type: 'integer'}, replicaOverrides: {type: 'object'}, resourceOverrides: {type: 'object'},
        cpuRequest: {type: 'string'}, cpuLimit: {type: 'string'}, memoryRequest: {type: 'string'}, memoryLimit: {type: 'string'},
      }}, defaults: {models: ['Qwen-small', 'Qwen-coder'], initialReplicas: 1, replicaOverrides: {}, resourceOverrides: {m2: {cpuLimit: '1'}}},
      interface: {components: [{id: 'm1', traits: ['scalable', 'schedulable']}, {id: 'm2', traits: ['scalable', 'schedulable']}]},
    }}}
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({workloadTargets: [{id: 'm1', apiVersion: 'apps/v1', kind: 'Deployment', name: 'm1', traits: ['scalable', 'schedulable'], index: 2}]}), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [localaiApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    await goToStep('Application')
    fireEvent.click(screen.getByRole('tab', {name: 'Resources'}))
    expect(screen.getByRole('checkbox', {name: 'Override resources for m2'})).toBeChecked()
    fireEvent.click(screen.getByRole('tab', {name: 'Parameters'}))
    fireEvent.click(screen.getByRole('button', {name: 'Remove m2'}))
    await waitFor(() => expect(fetch).toHaveBeenCalledOnce())
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({models: ['Qwen-small'], resourceOverrides: {}})
    await waitFor(() => expect(screen.queryByRole('checkbox', {name: 'Override resources for m2'})).not.toBeInTheDocument())
  })

  it('merges catalog defaults with partial values saved by older configurations', async () => {
    const item: ExperimentConfiguration = {id: 'cfg_saved', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved configuration', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {workloadProfile: 'light'}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}, item}))
    await goToStep('Application')
    expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(1)
    expect(screen.getByRole('spinbutton', {name: 'Threads per worker'})).toHaveValue(4)
    expect(screen.getByRole('combobox', {name: 'Work per request'})).toHaveValue('light')
  })

  it('shows the complete effective application settings in configuration details', () => {
    const item: ExperimentConfiguration = {id: 'cfg_details', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved configuration', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {workloadProfile: 'light'}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    window.location.hash = `experiments/configuration/${item.id}`
    render(createElement(ExperimentConfigurationsView, {items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    fireEvent.click(screen.getByRole('tab', {name: 'Application'}))
    const section = screen.getByRole('tabpanel', {name: 'Application'})
    expect(within(section).getByText('Initial replicas')).toBeInTheDocument()
    expect(within(section).getByText('Threads per worker')).toBeInTheDocument()
    expect(within(section).getByText('light')).toBeInTheDocument()
    expect(screen.queryByText('Validation')).not.toBeInTheDocument()
    expect(screen.queryByText('Validated')).not.toBeInTheDocument()
    expect(screen.queryByText('Effective values used for every run')).not.toBeInTheDocument()
    window.location.hash = ''
  })

  it('shows the author and associated experiment count in dedicated list columns', () => {
    const item: ExperimentConfiguration = {id: 'cfg_list', workspaceId: workspace.id, clusterResourceId: cluster.id, ownerUserId: 'user_a', ownerUsername: 'alice', name: 'Baseline', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-13T00:00:00Z'}
    const other: ExperimentConfiguration = {...item, id: 'cfg_other', ownerUserId: 'user_b', ownerUsername: 'bruno', name: 'Alternative'}
    const experiment = {id: 'exp_list', configurationId: item.id, name: 'Run', status: 'succeeded'} as never
    render(createElement(ExperimentConfigurationsView, {items: [item, other], experiments: [experiment], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    const row = screen.getByRole('row', {name: /Baseline/})
    expect(within(row).getByRole('cell', {name: '1'})).toBeInTheDocument()
    expect(screen.getByRole('columnheader', {name: 'Created by'})).toBeInTheDocument()
    expect(within(row).getByRole('cell', {name: 'alice'})).toBeInTheDocument()
    expect(row).not.toHaveTextContent('Development')
    expect(row).not.toHaveTextContent('Online Boutique')
    expect(screen.getByRole('columnheader', {name: 'Last updated'})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Filter by user'}), {target: {value: 'user_b'}})
    expect(screen.queryByRole('heading', {name: 'Baseline'})).not.toBeInTheDocument()
    expect(screen.getByRole('heading', {name: 'Alternative'})).toBeInTheDocument()
  })

  it('shows the materialized application topology in dynamic configuration details', async () => {
    const workloadTargets = [
      {id: 's0', apiVersion: 'apps/v1', kind: 'Deployment', name: 's0', traits: ['scalable', 'schedulable'], index: 0, dependencies: ['s1']},
      {id: 's1', apiVersion: 'apps/v1', kind: 'Deployment', name: 's1', traits: ['scalable', 'schedulable'], index: 1, dependencies: []},
      {id: 'node-proxy', apiVersion: 'apps/v1', kind: 'DaemonSet', name: 'node-proxy', traits: ['traffic-entrypoint'], index: 0, dependencies: ['s0']}
    ]
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({workloadTargets}), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const item: ExperimentConfiguration = {id: 'cfg_dynamic', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Dynamic application', description: '', applicationRef: mubenchApplication.reference, applicationDigest: mubenchApplication.digest, definition: {applicationValues: {serviceCount: 2, topology: 'chain', workers: 3}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    window.location.hash = `experiments/configuration/${item.id}`
    render(createElement(ExperimentConfigurationsView, {items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    fireEvent.click(screen.getByRole('tab', {name: 'Application'}))
    fireEvent.click(screen.getByText('Topology', {exact: true}))
    const graph = await screen.findByRole('img', {name: 'Application component topology'})
    expect(graph.querySelectorAll('.application-topology-node')).toHaveLength(3)
    expect(fetch).toHaveBeenCalledWith('/api/v1/catalog/applications/dev.kubephos.mubench/resolve', expect.objectContaining({method: 'POST'}))
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({serviceCount: 2, topology: 'chain', workers: 3})
    window.location.hash = ''
  })

  it('keeps an unavailable saved strategy visible while editing', async () => {
    const item: ExperimentConfiguration = {id: 'cfg_strategy', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved strategy', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: [{id: 'component-1', pluginId: 'scheduler', capability: 'scheduler.install', strategyId: 'strategy_removed', configuration: {policy: 'custom'}, targets: {include: [], exclude: []}}]}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin, plugin('scheduler', ['scheduler.install'])], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}, item}))
    await goToStep('Scheduling and scaling')
    expect(screen.getByRole('combobox', {name: 'Image'})).toHaveValue('strategy_removed')
    expect(screen.getByRole('option', {name: 'strategy_removed · unavailable'})).toBeInTheDocument()
  })

  it('allows deletion only after an experiment reaches a terminal state', () => {
    expect(isTerminalExperiment('succeeded')).toBe(true)
    expect(isTerminalExperiment(' FAILED ')).toBe(true)
    expect(isTerminalExperiment('canceled')).toBe(true)
    expect(isTerminalExperiment('running')).toBe(false)
    expect(isTerminalExperiment('queued')).toBe(false)
  })
})

const workspace: Workspace = {id: 'ws_default', name: 'Default', description: '', status: 'ready', createdAt: '2026-09-12T00:00:00Z'}
const cluster: ManagedResource = {id: 'cluster_ready', workspaceId: workspace.id, name: 'Development', kind: 'kubernetes-cluster', provider: 'proxmox', status: 'ready', validation: {valid: true, issues: []}, spec: {applicationPools: [{name: 'apps-a', zones: ['zone-a']}, {name: 'apps-b', zones: ['zone-b', 'zone-a']}]}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
const application: Application = {id: 'online-boutique', reference: 'app:online-boutique', name: 'Online Boutique', description: '', origin: 'built-in', digest: 'sha256:test', descriptor: {spec: {valuesSchema: {type: 'object', properties: {replicas: {type: 'integer', title: 'Initial replicas', minimum: 1}, threads: {type: 'integer', title: 'Threads per worker', minimum: 1}, workloadProfile: {type: 'string', title: 'Work per request', enum: ['light', 'balanced', 'heavy']}}}, defaults: {replicas: 1, threads: 4, workloadProfile: 'balanced'}}}}
const loadPlugin = plugin('load', ['load.session.control'])
const metricsPlugin = plugin('metrics', ['metrics.timeseries.collect'])
const mubenchApplication: Application = {id: 'dev.kubephos.mubench', reference: 'app:dev.kubephos.mubench', name: 'muBench', description: '', origin: 'built-in', digest: 'sha256:mubench', descriptor: {spec: {valuesSchema: {type: 'object', 'x-kubephos-dynamic-topology': true, properties: {serviceCount: {type: 'integer', title: 'Services', minimum: 2, maximum: 100}, topology: {type: 'string', title: 'Request topology', enum: ['chain', 'fan-out', 'tree']}, workers: {type: 'integer', title: 'Workers per service', minimum: 1, maximum: 32}}}, defaults: {serviceCount: 6, topology: 'chain', workers: 2}}}}
const legacyMubenchApplication: Application = {...mubenchApplication, id: 'dev.kubephos.mubench.legacy', reference: 'app:dev.kubephos.mubench.legacy', name: 'Legacy muBench', origin: 'generated'}

it('keeps network injection available for native Redis while omitting HTTP geographic routing', async () => {
  const redis: Application = {...application, id: 'dev.kubephos.redis-cluster', reference: 'app:dev.kubephos.redis-cluster', name: 'Redis Cluster', descriptor: {spec: {...application.descriptor.spec, interface: {endpoints: [{id: 'redis', protocol: 'tcp', port: 6379, service: 'redis', component: 'redis-primary-1'}]}}}}
  render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [{...cluster, spec: {...cluster.spec, managedProfile: 'kubephos'}}], workspaces: [workspace], applications: [redis], plugins: [loadPlugin, metricsPlugin, plugin('network', ['chaos.network.zones'])], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    await goToStep('Application')
  expect(screen.getByRole('spinbutton', {name: 'Minimum available replicas'})).toHaveValue(0)
  expect(screen.getByRole('spinbutton', {name: 'Minimum available replicas'})).toHaveAttribute('min', '0')
  await goToStep('Monitoring')
  fireEvent.click(screen.getByText('Network between zones', {selector: 'strong'}))
  await waitFor(() => expect(screen.getByRole('checkbox', {name: 'Enable injection'})).toBeEnabled())
  await goToStep('Load profile')
  expect(screen.queryByText('Geographic timeline')).not.toBeInTheDocument()
  expect(screen.getByText('Total duration')).toBeInTheDocument()
})

async function goToStep(title: string) {
  const name = screen.queryByRole('textbox', {name: 'Name'}) as HTMLInputElement | null
  if (name && !name.value) fireEvent.change(name, {target: {value: 'Test configuration'}})
  for (let count = 0; count < 10; count++) {
    if (screen.queryByRole('heading', {name: title})) return
    await waitFor(() => expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled())
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
  }
  throw new Error(`Wizard did not reach ${title}`)
}

it('discards an obsolete topology failure when switching to an external destination', async () => {
  let rejectPreview: (reason: Error) => void = () => {}
  vi.stubGlobal('fetch', vi.fn(() => new Promise((_resolve, reject) => {rejectPreview = reject})))
  render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
  fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'External'}})
  await goToStep('Application')
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Services'}), {target: {value: '4'}})
  expect(screen.getByRole('button', {name: 'Continue'})).toBeDisabled()
  await waitFor(() => expect(vi.mocked(fetch)).toHaveBeenCalledOnce())
  fireEvent.click(screen.getByRole('button', {name: 'General'}))
  fireEvent.change(screen.getByRole('combobox', {name: 'Destination type'}), {target: {value: 'external'}})
  rejectPreview(new Error('Obsolete preview failure'))
  await waitFor(() => expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled())
  expect(screen.queryByText('Obsolete preview failure')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
  expect(screen.getByRole('textbox', {name: /^External endpoint/})).toBeVisible()
})

it('opens Bank of Anthos on Replicas instead of an empty Parameters tab and retains its fixed manifest value', async () => {
  const bank: Application = {...application, id: 'bank-of-anthos', reference: 'app:bank', name: 'Bank of Anthos', descriptor: {spec: {valuesSchema: {properties: {
    cloudTelemetryEnabled: {type: 'string', enum: ['false']},
    initialReplicas: {type: 'integer', title: 'Initial replicas', minimum: 1},
    replicaOverrides: {type: 'object'},
    ...Object.fromEntries(['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit'].map(name => [name, {type: 'string' as const}])),
    resourceOverrides: {type: 'object'}
  }}, defaults: {cloudTelemetryEnabled: 'false', initialReplicas: 1}}}}
  const view = render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [bank], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
  await goToStep('Application')
  expect(screen.queryByRole('tab', {name: 'Parameters'})).not.toBeInTheDocument()
  expect(screen.getByRole('tab', {name: 'Replicas'})).toHaveAttribute('aria-selected', 'true')
  fireEvent.change(screen.getByRole('spinbutton', {name: 'Initial replicas'}), {target: {value: '3'}})
  fireEvent.click(screen.getByRole('tab', {name: 'Resources'}))
  fireEvent.click(screen.getByRole('tab', {name: 'Replicas'}))
  expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(3)
  const values = new FormData(view.container.querySelector('form')!)
  expect(values.get('applicationValues.cloudTelemetryEnabled')).toBe('false')
  expect(values.get('applicationValues.initialReplicas')).toBe('3')
  expect(values.get('applicationValues.cpuRequest')).toBe('')
})

it('shows a removable unavailable component without rendering another plugin schema', async () => {
  const item: ExperimentConfiguration = {id: 'saved', kind: 'single', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved configuration', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: [{id: 'removed', pluginId: 'removed-plugin', capability: 'removed.placement.install', configuration: {saved: true}, targets: {include: [], exclude: []}}]}, validation: {valid: false, issues: []}, createdAt: '', updatedAt: ''}
  render(createElement(ExperimentConfigurationDialog, {open: true, item, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
  await goToStep('Scheduling and scaling')
  fireEvent.click(screen.getByRole('tab', {name: 'Other'}))
  expect(screen.getByText('This component is no longer available. Remove or replace it before saving.')).toBeInTheDocument()
  expect(screen.queryByRole('combobox', {name: 'policy'})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Remove component'}))
  expect(screen.queryByText('This component is no longer available. Remove or replace it before saving.')).not.toBeInTheDocument()
})
