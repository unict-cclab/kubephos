import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Application, ExperimentConfiguration, ManagedResource, Session} from '../types'
import {SuitesView} from './SuitesView'

vi.mock('../api', () => ({request: vi.fn()}))

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

const application = {id: 'app', reference: 'app:app@1', name: 'Application', version: '1', descriptor: {spec: {}}, origin: 'built-in', digest: 'sha256:test', description: ''} as Application
const cluster = {id: 'cluster', name: 'Cluster'} as ManagedResource
const configurations: ExperimentConfiguration[] = ['Default scheduler', 'Custom scheduler'].map((name, index) => ({id: `cfg_${index}`, workspaceId: 'ws', clusterResourceId: cluster.id, name, description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '', updatedAt: ''}))

describe('suite presentation', () => {
  it('submits a plot alias and line color for each selected configuration', async () => {
    vi.mocked(request).mockResolvedValue({id: 'suite'} as never)
    render(<SuitesView configurations={configurations} applications={[application]} experiments={[]} clusters={[cluster]} session={{authenticated: true, csrfToken: 'csrf'} as Session} isAdmin changed={async () => {}} navigate={() => {}} />)
    fireEvent.click(screen.getByRole('button', {name: 'New suite'}))
    fireEvent.change(screen.getByLabelText('Name'), {target: {value: 'Scheduler comparison'}})
    const choices = screen.getAllByRole('checkbox')
    fireEvent.click(choices[0])
    fireEvent.click(choices[1])
    const aliases = screen.getAllByLabelText('Plot alias')
    fireEvent.change(aliases[0], {target: {value: 'Baseline'}})
    fireEvent.change(screen.getByLabelText('Custom scheduler plot color'), {target: {value: '#123456'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate and run'}))
    await waitFor(() => expect(request).toHaveBeenCalled())
    const body = JSON.parse(String(vi.mocked(request).mock.calls[0][1]?.body))
    expect(body.variants).toEqual([
      {configurationId: 'cfg_0', alias: 'Baseline', color: '#1f77b4'},
      {configurationId: 'cfg_1', alias: 'Custom scheduler', color: '#123456'}
    ])
  })

  it('filters suites by creator', () => {
    const experiments = [
      {id: 'suite_a', kind: 'suite', name: 'Alice suite', ownerUserId: 'user_a', ownerUsername: 'alice', status: 'succeeded', variants: []},
      {id: 'suite_b', kind: 'suite', name: 'Bruno suite', ownerUserId: 'user_b', ownerUsername: 'bruno', status: 'succeeded', variants: []}
    ] as never[]
    render(<SuitesView configurations={configurations} applications={[application]} experiments={experiments} clusters={[cluster]} session={{authenticated: true} as Session} isAdmin={false} changed={async () => {}} navigate={() => {}} />)

    expect(screen.getByRole('heading', {name: 'Alice suite'})).toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Filter by user'}), {target: {value: 'user_b'}})
    expect(screen.queryByRole('heading', {name: 'Alice suite'})).not.toBeInTheDocument()
    expect(screen.getByRole('heading', {name: 'Bruno suite'})).toBeInTheDocument()
  })
})
