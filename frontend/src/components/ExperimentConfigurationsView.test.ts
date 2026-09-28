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

  it('keeps orchestration plugins out of the simple experiment form', () => {
    const values = [plugin('scheduler', ['scheduler.install']), plugin('monitoring', ['monitoring.mon-agent.configure']), plugin('custom', ['research.strategy'], ['TargetBinding']), plugin('infra', ['infrastructure.provision'])]
    expect(experimentPlugins(values).map(item => item.id)).toEqual(['scheduler', 'monitoring'])
  })

  it('selects a ready cluster when asynchronously loaded data becomes available', async () => {
    const base = {open: true, close: () => {}, items: [], experiments: [], clusters: [], workspaces: [], applications: [], plugins: [], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}
    const view = render(createElement(ExperimentConfigurationDialog, base))
    view.rerender(createElement(ExperimentConfigurationDialog, {...base, clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin]}))

    await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
    expect(screen.getByRole('button', {name: 'Validate and save'})).toBeEnabled()
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
    fireEvent.change(screen.getByRole('textbox', {name: /^External endpoint/}), {target: {value: 'https://api.example.test/v1'}})
    fireEvent.change(screen.getByRole('textbox', {name: /^Locust parameters \(JSON\)/}), {target: {value: '{"tenant":"demo"}'}})
    await waitFor(() => expect(screen.getByRole('button', {name: 'Validate and save'})).toBeEnabled())
    fireEvent.click(screen.getByRole('button', {name: 'Validate and save'}))

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

  it('configures the built-in muBench application without creating catalog variants', () => {
	render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [legacyMubenchApplication, mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))

	expect(screen.getByRole('combobox', {name: 'Application'})).toHaveTextContent('muBench · 1.0.0-kubephos.1')
	expect(screen.getByRole('combobox', {name: 'Application'})).not.toHaveTextContent('Legacy muBench')
    expect(screen.getByRole('spinbutton', {name: 'Services'})).toHaveValue(6)
    expect(screen.getByRole('combobox', {name: 'Request topology'})).toHaveValue('chain')
    expect(screen.getByRole('spinbutton', {name: 'Workers per service'})).toHaveValue(2)
    expect(screen.queryByRole('button', {name: 'Generate application'})).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Validate and save'})).toBeEnabled()
  })

  it('updates muBench workload targets when its topology values change', async () => {
	const workloadTargets = [...Array.from({length: 4}, (_, index) => ({id: `s${index}`, apiVersion: 'apps/v1', kind: 'Deployment', name: `s${index}`, traits: ['scalable', 'schedulable'], index, dependencies: index < 3 ? [`s${index + 1}`] : []})), {id: 'node-proxy', apiVersion: 'apps/v1', kind: 'DaemonSet', name: 'node-proxy', traits: ['traffic-entrypoint'], index: 0, dependencies: ['s0']}]
	const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({workloadTargets}), {status: 200, headers: {'Content-Type': 'application/json'}}))
	vi.stubGlobal('fetch', fetch)
	render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [mubenchApplication], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true, csrfToken: 'csrf'}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))

	fireEvent.change(screen.getByRole('spinbutton', {name: 'Services'}), {target: {value: '4'}})
	expect(screen.getByRole('button', {name: 'Validate and save'})).toBeDisabled()
	await waitFor(() => expect(fetch).toHaveBeenCalledOnce())
	await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('4 configurable services'))
	expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({serviceCount: 4, topology: 'chain'})
	expect(screen.getByRole('button', {name: 'Validate and save'})).toBeEnabled()
  })

  it('merges catalog defaults with partial values saved by older configurations', () => {
    const item: ExperimentConfiguration = {id: 'cfg_saved', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved configuration', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {workloadProfile: 'light'}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}, item}))
    expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(1)
    expect(screen.getByRole('spinbutton', {name: 'Threads per worker'})).toHaveValue(4)
    expect(screen.getByRole('combobox', {name: 'Work per request'})).toHaveValue('light')
  })

  it('shows the complete effective application settings in configuration details', () => {
    const item: ExperimentConfiguration = {id: 'cfg_details', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved configuration', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {workloadProfile: 'light'}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    window.location.hash = `experiments/configuration/${item.id}`
    render(createElement(ExperimentConfigurationsView, {items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    const section = screen.getByText('Application settings').closest('details')!
    expect(section).toHaveAttribute('open')
    expect(within(section).getByText('Replicas')).toBeInTheDocument()
    expect(within(section).getByText('Threads')).toBeInTheDocument()
    expect(within(section).getByText('light')).toBeInTheDocument()
    expect(screen.queryByText('Validation')).not.toBeInTheDocument()
    expect(screen.queryByText('Validated')).not.toBeInTheDocument()
    expect(screen.queryByText('Effective values used for every run')).not.toBeInTheDocument()
    window.location.hash = ''
  })

  it('shows only the configuration name and associated experiment count in the list', () => {
    const item: ExperimentConfiguration = {id: 'cfg_list', workspaceId: workspace.id, clusterResourceId: cluster.id, ownerUserId: 'user_a', ownerUsername: 'alice', name: 'Baseline', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-13T00:00:00Z'}
    const other: ExperimentConfiguration = {...item, id: 'cfg_other', ownerUserId: 'user_b', ownerUsername: 'bruno', name: 'Alternative'}
    const experiment = {id: 'exp_list', configurationId: item.id, name: 'Run', status: 'succeeded'} as never
    render(createElement(ExperimentConfigurationsView, {items: [item, other], experiments: [experiment], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}))
    const card = screen.getByRole('heading', {name: 'Baseline'}).closest('article')!
    expect(card).toHaveTextContent('1 experiment')
    expect(card).toHaveTextContent('Created by alice')
    expect(card).not.toHaveTextContent('Development')
    expect(card).not.toHaveTextContent('Online Boutique')
    expect(card).not.toHaveTextContent('Updated')
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
    const graph = await screen.findByRole('img', {name: 'Application component topology'})
    expect(graph.querySelectorAll('.application-topology-node')).toHaveLength(3)
    expect(fetch).toHaveBeenCalledWith('/api/v1/catalog/applications/dev.kubephos.mubench/1.0.0-kubephos.1/resolve', expect.objectContaining({method: 'POST'}))
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({serviceCount: 2, topology: 'chain', workers: 3})
    window.location.hash = ''
  })

  it('keeps an unavailable saved strategy visible while editing', () => {
    const item: ExperimentConfiguration = {id: 'cfg_strategy', workspaceId: workspace.id, clusterResourceId: cluster.id, name: 'Saved strategy', description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: [{id: 'component-1', pluginId: 'scheduler', capability: 'scheduler.install', strategyId: 'strategy_removed', configuration: {policy: 'custom'}, targets: {include: [], exclude: []}}]}, validation: {valid: true, issues: []}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
    render(createElement(ExperimentConfigurationDialog, {open: true, close: () => {}, items: [item], experiments: [], clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin, plugin('scheduler', ['scheduler.install'])], strategies: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}, item}))
    expect(screen.getByRole('combobox', {name: 'Scheduler image'})).toHaveValue('strategy_removed')
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
const application: Application = {id: 'online-boutique', reference: 'online-boutique@1.0.0', name: 'Online Boutique', version: '1.0.0', description: '', origin: 'built-in', digest: 'sha256:test', descriptor: {spec: {valuesSchema: {type: 'object', properties: {replicas: {type: 'integer', title: 'Initial replicas', minimum: 1}, threads: {type: 'integer', title: 'Threads per worker', minimum: 1}, workloadProfile: {type: 'string', title: 'Work per request', enum: ['light', 'balanced', 'heavy']}}}, defaults: {replicas: 1, threads: 4, workloadProfile: 'balanced'}}}}
const loadPlugin = plugin('load', ['load.session.control'])
const metricsPlugin = plugin('metrics', ['metrics.timeseries.collect'])
const mubenchApplication: Application = {id: 'dev.kubephos.mubench', reference: 'app:dev.kubephos.mubench@1.0.0-kubephos.1', name: 'muBench', version: '1.0.0-kubephos.1', description: '', origin: 'built-in', digest: 'sha256:mubench', descriptor: {spec: {valuesSchema: {type: 'object', 'x-kubephos-dynamic-topology': true, properties: {serviceCount: {type: 'integer', title: 'Services', minimum: 2, maximum: 100}, topology: {type: 'string', title: 'Request topology', enum: ['chain', 'fan-out', 'tree']}, workers: {type: 'integer', title: 'Workers per service', minimum: 1, maximum: 32}}}, defaults: {serviceCount: 6, topology: 'chain', workers: 2}}}}
const legacyMubenchApplication: Application = {...mubenchApplication, id: 'dev.kubephos.mubench.legacy', reference: 'app:dev.kubephos.mubench.legacy@176c8f14-test', name: 'Legacy muBench', version: '176c8f14-test', origin: 'generated'}
