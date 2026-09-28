import {render, screen} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import type {Operation} from '../types'
import {OperationDrawer, PipelineRunLogsPage} from './OperationDrawer'

afterEach(() => vi.unstubAllGlobals())

describe('OperationDrawer', () => {
  it('shows only execution progress and logs', async () => {
    const operation: Operation = {
      id: 'op_test', workspaceId: 'ws_test', pluginId: 'dev.example.console', pluginVersion: '1.0.0', title: 'Inspect', status: 'succeeded', planHash: 'hash',
      plan: {steps: []}, validation: {valid: true, issues: []}, steps: [], createdAt: '2026-01-01T00:00:00Z',
      artifacts: [
        {id: 'art_public', operationId: 'op_test', name: 'inventory', type: 'Inventory', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:public', sizeBytes: 20, sensitive: false, verifiedAt: '2026-01-01T00:00:00Z'},
        {id: 'art_secret', operationId: 'op_test', name: 'access', type: 'Access', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:secret', sizeBytes: 20, sensitive: true, verifiedAt: '2026-01-01T00:00:00Z'}
      ]
    }
    const fetch = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.endsWith('/logs')) return new Response(JSON.stringify({items: []}), {status: 200})
      return new Response(JSON.stringify(operation), {status: 200})
    })
    vi.stubGlobal('fetch', fetch)
    render(<OperationDrawer operationID="op_test" session={{authenticated: true}} plugins={[]} close={() => undefined} open={() => undefined} changed={async () => undefined} notify={() => undefined} />)

    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(await screen.findByRole('heading', {name: 'Logs'})).toBeInTheDocument()
    expect(screen.getByRole('heading', {name: 'Progress'})).toBeInTheDocument()
    expect(screen.queryByText('Validated plan')).not.toBeInTheDocument()
    expect(screen.queryByText('Verified artifacts')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Preview'})).not.toBeInTheDocument()
  })
  it('shows one chronological log stream across stages and cleanup', async () => {
    const run = {id: 'run_test', pipelineId: 'pipeline_test', workspaceId: 'ws_test', name: 'Experiment run', status: 'succeeded', pipelineHash: 'hash', resultType: 'Result', resultVersion: 'v1', cancelRequested: false, createdAt: '2026-01-01T00:00:00Z', stages: [{id: 'stage_one', runId: 'run_test', position: 1, stageId: 'deploy', pluginId: 'deploy', title: 'Deploy application', status: 'succeeded'}, {id: 'stage_two', runId: 'run_test', position: 2, stageId: 'load', pluginId: 'load', title: 'Run load', status: 'succeeded'}]}
    const logs = [{sequence: 1, level: 'info', source: 'worker', message: 'application ready', createdAt: '2026-01-01T00:00:01Z', stageId: 'deploy', stageTitle: 'Deploy application', stagePosition: 1, phase: 'execution'}, {sequence: 2, level: 'info', source: 'worker', message: 'resources removed', createdAt: '2026-01-01T00:00:02Z', stageId: 'load', stageTitle: 'Run load', stagePosition: 2, phase: 'cleanup'}]
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/strategy-logs')) return new Response(JSON.stringify({items: []}), {status: 200})
      return new Response(JSON.stringify(path.includes('/logs?') ? {items: logs} : run), {status: 200})
    }))
    const {container} = render(<PipelineRunLogsPage runID="run_test" close={() => undefined} notify={() => undefined} />)

    expect(await screen.findByText('application ready')).toBeInTheDocument()
    expect(screen.getByText('resources removed')).toBeInTheDocument()
    expect(screen.getByText('1. Deploy application')).toBeInTheDocument()
    expect(screen.getByText('2. Run load · cleanup')).toBeInTheDocument()
    expect(container.querySelectorAll('.log-console')).toHaveLength(1)
  })

  it('shows runtime logs only for strategies selected by the run', async () => {
    const run = {id: 'run_test', pipelineId: 'pipeline_test', workspaceId: 'ws_test', name: 'Scheduler run', status: 'running', pipelineHash: 'hash', resultType: 'Result', resultVersion: 'v1', cancelRequested: false, createdAt: '2026-01-01T00:00:00Z', stages: [{id: 'stage_scheduler', runId: 'run_test', position: 1, stageId: 'scheduler', pluginId: 'io.kubephos.scheduler.kubernetes.secondary', title: 'scheduler.kubernetes.install', status: 'succeeded'}]}
    const runtime = {items: [{kind: 'scheduler', title: 'Custom scheduler', namespace: 'kubephos-run', resource: 'deployment/scheduler', state: 'available', lines: ['scheduled pod frontend-1']}]}
    vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.includes('/strategy-logs')) return new Response(JSON.stringify(runtime), {status: 200})
      if (path.includes('/logs?')) return new Response(JSON.stringify({items: []}), {status: 200})
      return new Response(JSON.stringify(run), {status: 200})
    }))
    render(<PipelineRunLogsPage runID="run_test" close={() => undefined} notify={() => undefined} />)

    expect(await screen.findByRole('heading', {name: 'Custom scheduler logs'})).toBeInTheDocument()
    expect(screen.getByText('scheduled pod frontend-1')).toBeInTheDocument()
    expect(screen.queryByRole('heading', {name: 'Autoscaler logs'})).not.toBeInTheDocument()
    expect(screen.queryByRole('heading', {name: 'Descheduler logs'})).not.toBeInTheDocument()
  })
})
