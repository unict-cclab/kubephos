import {fireEvent, render, screen} from '@testing-library/react'
import {createElement} from 'react'
import {describe, expect, it, vi} from 'vitest'
import {ExecutionProgressGraph, type ProgressNode} from './ExecutionProgressGraph'

describe('execution progress graph', () => {
  it('shows the active step, dependencies and its logs', () => {
    const openOperation = vi.fn()
    const nodes: ProgressNode[] = [
      {id: 'prepare', title: 'Prepare cluster', status: 'succeeded', operationId: 'op_prepare'},
      {id: 'install', title: 'Install platform', status: 'running', dependencies: ['Prepare cluster'], operationId: 'op_install'},
      {id: 'verify', title: 'Verify health', status: 'queued'}
    ]
    render(createElement(ExecutionProgressGraph, {nodes, label: 'Provisioning', openOperation}))

    expect(screen.getByText('Step 2 of 3')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: '2. Install platform: Running'})).toHaveAttribute('aria-pressed', 'true')
    expect(screen.getByText('Depends on Prepare cluster')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Open step logs'}))
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
})
