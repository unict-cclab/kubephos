import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeAll, beforeEach, describe, expect, it, vi} from 'vitest'
import type {Experiment, ExperimentConfiguration} from '../types'
import {request} from '../api'
import {ExperimentsHistoryView} from './ExperimentsHistoryView'

vi.mock('../api', () => ({request: vi.fn()}))

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
  HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
})

const configuration = (id: string, name: string, valid = true): ExperimentConfiguration => ({id, workspaceId: 'ws_1', clusterResourceId: 'cluster_1', name, description: '', applicationRef: 'app:test', applicationDigest: 'sha256:test', definition: {applicationValues: {}, components: []}, validation: {valid, issues: []}, createdAt: '2026-09-17T00:00:00Z', updatedAt: '2026-09-17T00:00:00Z'})

describe('experiments history creation', () => {
  beforeEach(() => {
    window.location.hash = '#results'
    vi.mocked(request).mockReset()
  })

  it.each(['instance', 'suite'] as const)('explains complete deletion from the list for %s', kind => {
    const experiment = {id: 'exp_delete', kind, name: 'Test run', status: 'failed', createdAt: '2026-09-17T00:00:00Z', variants: []} as unknown as Experiment
    render(<ExperimentsHistoryView experiments={[experiment]} configurations={[]} artifacts={[]} operations={[]} workspaces={[]} session={{authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'}}} changed={async () => {}} openOperation={() => {}} />)
    fireEvent.click(screen.getByRole('button', {name: 'Delete Test run'}))
    expect(screen.getByText(`Delete ${kind === 'suite' ? 'suite' : 'experiment'} Test run and its runs, logs and collected artifacts?`)).toBeInTheDocument()
    expect(screen.queryByText(/will be retained/)).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Cancel'}))
    expect(request).not.toHaveBeenCalled()
  })

  it('creates an experiment from a selected validated configuration', async () => {
    vi.mocked(request).mockResolvedValue({id: 'exp_1'} as never)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<ExperimentsHistoryView experiments={[]} configurations={[configuration('cfg_1', 'Baseline'), configuration('cfg_2', 'Scheduler plugin'), configuration('cfg_bad', 'Invalid', false)]} artifacts={[]} operations={[]} workspaces={[]} session={{authenticated: true, csrfToken: 'token', user: {id: 'admin', username: 'admin', role: 'admin'}}} changed={changed} openOperation={() => {}} />)

    fireEvent.click(screen.getByRole('button', {name: 'New experiment'}))
    const select = screen.getByRole('combobox', {name: 'Configuration'})
    expect(select).toHaveValue('cfg_1')
    expect(screen.queryByRole('option', {name: 'Invalid'})).not.toBeInTheDocument()
    fireEvent.change(select, {target: {value: 'cfg_2'}})
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('Scheduler plugin run')
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Number of runs'}), {target: {value: '2'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate and run'}))

    await waitFor(() => expect(request).toHaveBeenCalledWith('/experiments/start', expect.objectContaining({method: 'POST'}), 'token'))
    const body = JSON.parse((vi.mocked(request).mock.calls[0][1] as RequestInit).body as string)
    expect(body).toMatchObject({configurationId: 'cfg_2', name: 'Scheduler plugin run', runs: 2})
    expect(changed).toHaveBeenCalled()
  })

  it('filters experiments by creator', () => {
    const experiments = [
      {id: 'exp_a', kind: 'instance', name: 'Alice experiment', ownerUserId: 'user_a', ownerUsername: 'alice', status: 'succeeded', createdAt: '2026-09-17T00:00:00Z', variants: []},
      {id: 'exp_b', kind: 'instance', name: 'Bruno experiment', ownerUserId: 'user_b', ownerUsername: 'bruno', status: 'succeeded', createdAt: '2026-09-17T00:00:00Z', variants: []}
    ] as never[]
    render(<ExperimentsHistoryView experiments={experiments} configurations={[]} artifacts={[]} operations={[]} workspaces={[]} session={{authenticated: true, user: {id: 'user_a', username: 'alice', role: 'user'}}} changed={async () => {}} openOperation={() => {}} />)

    expect(screen.getByRole('columnheader', {name: 'Created by'})).toBeInTheDocument()
    expect(screen.getByRole('cell', {name: 'alice'})).toBeInTheDocument()
    expect(screen.getByRole('cell', {name: 'bruno'})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Filter by user'}), {target: {value: 'user_b'}})
    expect(screen.queryByText('Alice experiment')).not.toBeInTheDocument()
    expect(screen.getByText('Bruno experiment')).toBeInTheDocument()
  })

  it('shows stop requests as stopping until completion and keeps them in the active filter', () => {
    const experiment = {id: 'exp_stop', name: 'Stop requested', status: 'running', cancelRequested: true, createdAt: '2026-09-17T00:00:00Z', variants: []} as unknown as Experiment
    const props = {experiments: [experiment], configurations: [], artifacts: [], operations: [], workspaces: [], session: {authenticated: true}, changed: async () => {}, openOperation: () => {}}
    const view = render(<ExperimentsHistoryView {...props} />)
    expect(screen.getByText('stopping')).toHaveClass('status', 'stopping')
    expect(screen.queryByText('running')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'In progress'}))
    expect(screen.getByText('stopping')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('searchbox'), {target: {value: 'stopping'}})
    expect(screen.getByText('Stop requested')).toBeInTheDocument()
    fireEvent.change(screen.getByRole('searchbox'), {target: {value: ''}})
    view.rerender(<ExperimentsHistoryView {...props} experiments={[{...experiment, status: 'canceled'}]} />)
    expect(screen.queryByText('Stop requested')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Completed'}))
    expect(screen.getByText('canceled')).toBeInTheDocument()
    expect(screen.queryByText('stopping')).not.toBeInTheDocument()
  })
})
