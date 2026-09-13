import {render, screen, waitFor} from '@testing-library/react'
import {createElement} from 'react'
import {beforeAll, describe, expect, it} from 'vitest'
import type {Application, ManagedResource, Plugin, Workspace} from '../types'
import {applicationZones, configurableSchema, ExperimentConfigurationDialog, experimentPlugins, primaryCapabilities} from './ExperimentConfigurationsView'

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

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
    const base = {open: true, close: () => {}, items: [], experiments: [], clusters: [], workspaces: [], applications: [], plugins: [], session: {authenticated: true}, isAdmin: true, changed: async () => {}, openOperation: () => {}}
    const view = render(createElement(ExperimentConfigurationDialog, base))
    view.rerender(createElement(ExperimentConfigurationDialog, {...base, clusters: [cluster], workspaces: [workspace], applications: [application], plugins: [loadPlugin, metricsPlugin]}))

    await waitFor(() => expect(screen.getByRole('combobox', {name: 'Kubernetes cluster'})).toHaveValue(cluster.id))
    expect(screen.getByRole('button', {name: 'Validate and save'})).toBeEnabled()
  })

  it('derives unique application zones from the selected cluster', () => {
    expect(applicationZones(cluster)).toEqual(['zone-a', 'zone-b'])
  })
})

const workspace: Workspace = {id: 'ws_default', name: 'Default', description: '', status: 'ready', createdAt: '2026-09-12T00:00:00Z'}
const cluster: ManagedResource = {id: 'cluster_ready', workspaceId: workspace.id, name: 'Development', kind: 'kubernetes-cluster', provider: 'proxmox', status: 'ready', validation: {valid: true, issues: []}, spec: {applicationPools: [{name: 'apps-a', zones: ['zone-a']}, {name: 'apps-b', zones: ['zone-b', 'zone-a']}]}, createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-12T00:00:00Z'}
const application: Application = {id: 'online-boutique', reference: 'online-boutique@1.0.0', name: 'Online Boutique', version: '1.0.0', description: '', origin: 'built-in', digest: 'sha256:test', descriptor: {spec: {valuesSchema: {type: 'object', properties: {}}, defaults: {}}}}
const loadPlugin = plugin('load', ['load.session.control'])
const metricsPlugin = plugin('metrics', ['metrics.timeseries.collect'])
