import {fireEvent, render, screen} from '@testing-library/react'
import {createElement} from 'react'
import {describe, expect, it, vi} from 'vitest'
import {ExecutionProgressGraph, highLevelExecutionNodes, type ProgressNode} from './ExecutionProgressGraph'

describe('execution progress graph', () => {
  it('shows the active step and its logs', () => {
    const openOperation = vi.fn()
    const nodes: ProgressNode[] = [
      {id: 'prepare', title: 'Prepare cluster', status: 'succeeded', operationId: 'op_prepare'},
      {id: 'install', title: 'Install platform', status: 'running', dependencies: ['Prepare cluster'], operationId: 'op_install'},
      {id: 'verify', title: 'Verify health', status: 'queued'}
    ]
    render(createElement(ExecutionProgressGraph, {nodes, label: 'Provisioning', openOperation}))

    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '2. Install platform: Running'})).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByRole('button', {name: '2. Install platform: Running'})).toHaveAttribute('aria-current', 'step')
    expect(screen.getByRole('progressbar', {name: 'Provisioning completed steps'})).toHaveAttribute('aria-valuenow', '1')
    expect(screen.queryByText('Depends on Prepare cluster')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Open logs'}))
    expect(openOperation).toHaveBeenCalledWith('op_install')
    fireEvent.click(screen.getByRole('button', {name: '1. Prepare cluster: Succeeded'}))
    expect(screen.getByRole('button', {name: '1. Prepare cluster: Succeeded'})).toHaveAttribute('aria-pressed', 'true')
  })

  it('updates the graph from a new execution state', () => {
    const nodes: ProgressNode[] = [{id: 'first', title: 'First', status: 'running'}, {id: 'second', title: 'Second', status: 'queued'}]
    const {rerender} = render(createElement(ExecutionProgressGraph, {nodes, label: 'Experiment'}))
    expect(screen.getByText('Step 1 of 2')).toBeInTheDocument()
    rerender(createElement(ExecutionProgressGraph, {nodes: [{...nodes[0], status: 'succeeded'}, {...nodes[1], status: 'running'}], label: 'Experiment'}))
    expect(screen.getByText('Step 2 of 2')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '2. Second: Running'})).toHaveAttribute('aria-pressed', 'true')
  })

  it('groups experiment internals into high-level phases', () => {
    const nodes: ProgressNode[] = [
      {id: 'application', title: 'Prepare application package', status: 'succeeded'},
      {id: 'prepare', title: 'Prepare isolated application namespace', status: 'succeeded'},
      {id: 'capability-1', title: 'chaos.network.zones', status: 'succeeded'},
      {id: 'targets-2', title: 'Select targets for component-2', status: 'succeeded'},
      {id: 'capability-2', title: 'descheduler.kubernetes.install', status: 'succeeded'},
      {id: 'targets-3', title: 'Select targets for component-3', status: 'succeeded'},
      {id: 'capability-3', title: 'scheduler.kubernetes.install', status: 'succeeded'},
      {id: 'deploy', title: 'Deploy isolated application', status: 'succeeded'},
      {id: 'capability-4', title: 'load.session.control', status: 'running'},
      {id: 'capability-5', title: 'metrics.timeseries.collect', status: 'pending'}
    ]

    expect(highLevelExecutionNodes(nodes).map(node => node.title)).toEqual(['Prepare application', 'Configure experiment', 'Deploy application', 'Run workload', 'Collect results'])
    render(createElement(ExecutionProgressGraph, {nodes, label: 'Experiment'}))
    expect(screen.getByText('Step 4 of 5')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '4. Run workload: Running'})).toHaveAttribute('aria-pressed', 'true')
    expect(screen.queryByText('chaos.network.zones')).not.toBeInTheDocument()
    expect(screen.queryByText('load.session.control')).not.toBeInTheDocument()
  })

  it('uses high-level names for an external destination pipeline', () => {
    const nodes: ProgressNode[] = [
      {id: 'external-destination', title: 'Prepare external destination', status: 'succeeded'},
      {id: 'capability-1', title: 'load.session.control', status: 'running'},
      {id: 'capability-2', title: 'metrics.timeseries.collect', status: 'pending'}
    ]

    expect(highLevelExecutionNodes(nodes).map(node => node.title)).toEqual(['Prepare external workload', 'Run workload', 'Collect results'])
  })

  it('distinguishes cancellation from failure and selects the stopped step', () => {
    render(createElement(ExecutionProgressGraph, {nodes: [{id: 'prepare', title: 'Prepare', status: 'succeeded'}, {id: 'load', title: 'Run workload', status: 'canceled'}, {id: 'metrics', title: 'Collect results', status: 'pending'}], label: 'Stopped experiment'}))
    const stopped = screen.getByRole('button', {name: '2. Run workload: Canceled'})
    expect(stopped).toHaveClass('canceled', 'selected')
    expect(stopped).not.toHaveClass('failed')
    expect(screen.getByRole('progressbar')).toHaveAttribute('aria-valuenow', '1')
  })
})
