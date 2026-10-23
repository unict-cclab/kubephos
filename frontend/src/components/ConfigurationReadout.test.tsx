import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {Application, ExperimentConfiguration, Plugin} from '../types'
import {ConfigurationReadout} from './ConfigurationReadout'

const item: ExperimentConfiguration = {workspaceId: 'ws', description: '', applicationDigest: 'sha256:test', validation: {valid: true, issues: []}, createdAt: '', updatedAt: '', id: 'cfg', name: 'Baseline', clusterResourceId: 'cluster', applicationRef: 'app', definition: {applicationValues: {serviceCount: 15}, components: [
  {id: 'network', pluginId: 'network-plugin', capability: 'chaos.network.zones', configuration: {enableLatency: false, latencyMilliseconds: 50, linkOverrides: [{sourceZone: 'a', targetZone: 'b', latencyMilliseconds: 100}]}, targets: {include: [], exclude: []}},
  {id: 'load', pluginId: 'load-plugin', capability: 'load.session.control', configuration: {steps: [{type: 'constant', durationSeconds: 900, rps: 10, baselineRps: 100, periodSeconds: 300}]}, targets: {include: [], exclude: []}},
  {id: 'scheduler', pluginId: 'scheduler-plugin', strategyId: 'strategy', capability: 'scheduler.kubernetes.install', configuration: {configFile: 'kind: Policy\ninterval: 10'}, targets: {include: ['s0'], exclude: []}}
]}}
const application: Application = {id: 'app', description: '', origin: 'built-in', digest: 'sha256:test', reference: 'app', name: 'muBench', descriptor: {spec: {valuesSchema: {properties: {serviceCount: {title: 'Services'}}}}}}

describe('configuration readout', () => {
  it('separates application, network, load and strategy settings without editable controls', () => {
    render(<ConfigurationReadout item={item} clusters={[]} applications={[application]} plugins={[]} strategies={[]} />)
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', {name: 'Application'}))
    expect(within(screen.getByRole('tabpanel', {name: 'Application'})).getByText('Services')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', {name: 'Network'}))
    const network = within(screen.getByRole('tabpanel', {name: 'Network'}))
    expect(network.getByRole('columnheader', {name: 'Jitter (ms)'})).toBeInTheDocument()
    expect(network.getByRole('columnheader', {name: 'Bandwidth (B/s)'})).toBeInTheDocument()
    expect(network.getByRole('columnheader', {name: 'Packet loss (%)'})).toBeInTheDocument()
    expect(network.queryByRole('columnheader', {name: 'Correlation (%)'})).not.toBeInTheDocument()
    expect(network.getAllByRole('cell', {name: '—'})).toHaveLength(3)
    expect(network.getByRole('columnheader', {name: 'Latency (ms)'})).toBeInTheDocument()
    expect(network.getByText('100')).toBeInTheDocument()
    expect(network.queryByText('50')).not.toBeInTheDocument()
    expect(network.queryByText('Shared profile')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', {name: 'Load profile'}))
    const load = within(screen.getByRole('tabpanel', {name: 'Load profile'}))
    expect(load.getByRole('columnheader', {name: 'Rate (req/s)'})).toBeInTheDocument()
    expect(load.queryByText('Baseline (requests/s)')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('tab', {name: 'Scheduling'}))
    expect(screen.getByText('s0')).toBeVisible()
  })

  it('shows external destinations without exposing secrets', () => {
    render(<ConfigurationReadout item={{...item, definition: {applicationValues: {}, destinationMode: 'external', externalDestination: {targetUrl: 'https://example.test', runtimeImage: 'locust', script: 'class User:\n    pass', parameters: {apiToken: 'private'}}, components: []}}} clusters={[]} applications={[]} plugins={[] as Plugin[]} strategies={[]} />)
    fireEvent.click(screen.getByRole('tab', {name: 'External destination'}))
    expect(screen.getByText('Locustfile')).toBeVisible()
    expect(screen.queryByText('private')).not.toBeInTheDocument()
    expect(screen.queryByRole('tab', {name: 'Network'})).not.toBeInTheDocument()
  })
})


it('keeps scheduling, descheduling and scaling in distinct detail tabs', () => {
  const components = ['scheduler', 'descheduler', 'autoscaler'].map(kind => ({id: kind, pluginId: kind, capability: `${kind}.kubernetes.install`, configuration: {configFile: `${kind} settings`}, targets: {include: [], exclude: []}}))
  render(<ConfigurationReadout item={{...item, definition: {...item.definition, components}}} clusters={[]} applications={[]} plugins={[]} strategies={[]} />)
  expect(screen.queryByRole('tab', {name: 'Scheduling and scaling'})).not.toBeInTheDocument()
  for (const [title, kind] of [['Scheduling', 'scheduler'], ['Descheduling', 'descheduler'], ['Scaling', 'autoscaler']]) {
    fireEvent.click(screen.getByRole('tab', {name: title}))
    expect(screen.getByRole('tabpanel', {name: title})).toHaveTextContent(`${kind}.kubernetes.install`)
  }
})
