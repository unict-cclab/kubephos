import {fireEvent, render, screen} from '@testing-library/react'
import {beforeAll, beforeEach, describe, expect, it} from 'vitest'
import type {ExperimentConfiguration} from '../types'
import {ExperimentConfigurationsView} from './ExperimentConfigurationsView'

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
  HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
})

const item: ExperimentConfiguration = {id: 'cfg_1', workspaceId: 'ws_1', clusterResourceId: 'cluster_1', name: 'Simple load', description: '', applicationRef: 'app@1', applicationDigest: 'sha256:test', definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-15T00:00:00Z', updatedAt: '2026-09-15T00:00:00Z'}
const props = {items: [item], experiments: [], clusters: [], workspaces: [], applications: [], plugins: [], strategies: [], session: {authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'} as const}, isAdmin: true, changed: async () => {}, openOperation: () => {}}

describe('experiment configuration actions', () => {
  beforeEach(() => {window.location.hash = '#experiments'})

  it('keeps only eye and trash in the list and launches runs from detail', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    expect(screen.getByRole('button', {name: 'View Simple load details'}).textContent).toBe('')
    expect(screen.getByRole('button', {name: 'Delete Simple load'}).textContent).toBe('')
    expect(screen.queryByRole('button', {name: 'Run'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))
    expect(screen.getByRole('button', {name: 'Run experiment'})).toBeInTheDocument()
    expect(screen.getByText('Input load')).toBeInTheDocument()
    expect(screen.getByText('Setup')).toBeInTheDocument()
    expect(screen.queryByText('All entered data')).not.toBeInTheDocument()
    expect(screen.queryByText('Exact saved configuration')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Run experiment'}))
    expect(view.container.querySelector('dialog[open]')).toBeInTheDocument()
    expect(screen.getByRole('spinbutton', {name: 'Number of runs'})).toBeInTheDocument()
  })

  it('opens configuration editing as a page with the saved values', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))
    fireEvent.click(screen.getByRole('button', {name: 'Edit configuration'}))

    expect(screen.getByRole('heading', {name: 'Edit configuration'})).toBeInTheDocument()
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('Simple load')
    expect(screen.getByRole('button', {name: 'Validate and update'})).toBeInTheDocument()
    expect(view.container.querySelector('dialog[open]')).not.toBeInTheDocument()
    expect(screen.getByText('Changes apply only to experiments created after this update.')).toBeInTheDocument()
  })

  it('opens the complete saved configuration in read-only mode', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))
    fireEvent.click(screen.getByRole('button', {name: 'View configuration'}))

    expect(screen.getByRole('heading', {name: 'View configuration'})).toBeInTheDocument()
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('Simple load')
    expect(screen.getByRole('textbox', {name: 'Name'})).toBeDisabled()
    expect(screen.queryByRole('button', {name: 'Validate and update'})).not.toBeInTheDocument()
    expect(view.container.querySelector('dialog[open]')).not.toBeInTheDocument()
    expect(screen.getByText('Complete saved configuration in read-only mode.')).toBeInTheDocument()
  })
})
