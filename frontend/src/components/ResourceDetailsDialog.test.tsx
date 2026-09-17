import {fireEvent, render, screen} from '@testing-library/react'
import {createElement} from 'react'
import {describe, expect, it, vi} from 'vitest'
import type {ManagedResource, Pipeline, PipelineRun, Session} from '../types'
import {ResourceDetailsPage} from './ResourceDetailsDialog'

const resource: ManagedResource = {
  id: 'cluster_01',
  workspaceId: 'ws_01',
  name: 'Research cluster',
  kind: 'kubernetes-cluster',
  provider: 'proxmox',
  status: 'ready',
  validation: {valid: true, issues: []},
  spec: {
    managedProfile: 'managed', addressStart: '192.0.2.10', controlPlanes: 1,
    controlPlaneZones: ['zone-a'], controlPlaneCapacity: {cores: 4, memoryMiB: 8192, diskGiB: 80},
    managementPool: {name: 'management', count: 1, zones: ['zone-a'], capacity: {cores: 2, memoryMiB: 4096, diskGiB: 40}},
    applicationPools: [{name: 'apps', count: 2, zones: ['zone-b'], capacity: {cores: 4, memoryMiB: 8192, diskGiB: 80}}],
    customSetting: 'retained', kubeconfig: 'private'
  },
  createdAt: '2026-09-15T00:00:00Z',
  updatedAt: '2026-09-15T00:00:00Z'
}

describe('resource detail', () => {
  it('separates dashboards, access, activity and configuration', () => {
    render(createElement(ResourceDetailsPage, {resource, close: () => {}, connections: [], workspaces: [], resources: [resource], runs: [], session: {authenticated: true} as Session, openOperation: () => {}}))

    expect(screen.getByRole('link', {name: /Grafana/})).toBeInTheDocument()
    expect(screen.getByText('Topology')).toBeInTheDocument()
    expect(screen.queryByText('Entered data')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Access'}))
    expect(screen.queryByRole('link', {name: /Grafana/})).not.toBeInTheDocument()
    expect(screen.getByText('Administrator access required.')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Activity'}))
    expect(screen.getByText('No issues')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Configuration'}))
    expect(screen.getByText('Cluster settings')).toBeInTheDocument()
    expect(screen.getByText('Control plane')).toBeInTheDocument()
    expect(screen.getByText('Management pool')).toBeInTheDocument()
    expect(screen.getByText('Application pools')).toBeInTheDocument()
    expect(screen.getByText('retained')).toBeInTheDocument()
    expect(screen.getByText('Cluster settings')).toBeInTheDocument()
    expect(screen.getAllByText(/••••••••/)).toHaveLength(1)
  })

  it('keeps cluster recreation and kubeconfig in the detail page', () => {
    const recreate = vi.fn(async () => {})
    render(createElement(ResourceDetailsPage, {resource, close: () => {}, connections: [], workspaces: [], resources: [resource], runs: [], session: {authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'}} as Session, openOperation: () => {}, recreate}))
    fireEvent.click(screen.getByRole('button', {name: 'Recreate cluster'}))
    expect(recreate).toHaveBeenCalledOnce()
    fireEvent.click(screen.getByRole('button', {name: 'Access'}))
    expect(screen.getByRole('link', {name: 'Download kubeconfig'})).toHaveAttribute('href', `/api/v1/kubernetes-clusters/${resource.id}/kubeconfig`)
  })

  it('opens provisioning on the interactive lifecycle graph', () => {
    const provisioning = {...resource, status: 'provisioning', pipelineRunId: 'run_01'}
    const run: PipelineRun = {id: 'run_01', pipelineId: 'pipeline_01', workspaceId: resource.workspaceId, name: 'Provision', status: 'running', pipelineHash: 'hash', resultType: 'Cluster', resultVersion: '1', cancelRequested: false, createdAt: resource.createdAt, stages: [
      {id: 'stage_01', runId: 'run_01', position: 1, stageId: 'prepare', pluginId: 'p1', title: 'Prepare VM', status: 'succeeded', operationId: 'op_01'},
      {id: 'stage_02', runId: 'run_01', position: 2, stageId: 'install', pluginId: 'p2', title: 'Install Kubernetes', status: 'running', operationId: 'op_02'}
    ]}
    const pipeline: Pipeline = {id: 'pipeline_01', workspaceId: resource.workspaceId, name: 'Provision', description: '', definition: {stages: [{id: 'prepare', pluginId: 'p1', title: 'Prepare VM', spec: {}}, {id: 'install', pluginId: 'p2', title: 'Install Kubernetes', spec: {}, bindings: [{path: 'cluster', fromStage: 'prepare'}]}], result: {stage: 'install'}}, resolution: {stages: [], result: {type: 'Cluster', version: '1'}}, validation: {valid: true, issues: []}, hash: 'hash', createdAt: resource.createdAt, updatedAt: resource.updatedAt}
    const openOperation = vi.fn()
    render(createElement(ResourceDetailsPage, {resource: provisioning, close: () => {}, connections: [], workspaces: [], resources: [provisioning], runs: [run], pipelines: [pipeline], session: {authenticated: true} as Session, openOperation}))

    expect(screen.getByRole('button', {name: 'Activity'})).toHaveAttribute('aria-current', 'page')
    expect(screen.getByText('Step 2 of 2')).toBeInTheDocument()
    expect(screen.getByText('Depends on Prepare VM')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Open step logs'}))
    expect(openOperation).toHaveBeenCalledWith('op_02')
  })
})
