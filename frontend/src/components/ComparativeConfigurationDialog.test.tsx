import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Application, ExperimentConfiguration, ManagedResource, Session} from '../types'
import {ComparativeConfigurationDialog} from './ComparativeConfigurationDialog'

vi.mock('../api', () => ({request: vi.fn()}))

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

const application = {id: 'app', reference: 'app:app', name: 'Application', descriptor: {spec: {}}, origin: 'built-in', digest: 'sha256:test', description: ''} as Application
const cluster = {id: 'cluster', name: 'Cluster'} as ManagedResource
const configurations: ExperimentConfiguration[] = ['Default scheduler', 'Custom scheduler'].map((name, index) => ({id: `cfg_${index}`, workspaceId: 'ws', clusterResourceId: cluster.id, name, description: '', applicationRef: application.reference, applicationDigest: application.digest, definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '', updatedAt: ''}))

describe('suite presentation', () => {
  it('submits a plot alias and line color for each selected configuration', async () => {
    vi.mocked(request).mockResolvedValue({id: 'suite'} as never)
    render(<ComparativeConfigurationDialog open close={() => {}} configurations={configurations} applications={[application]} clusters={[cluster]} session={{authenticated: true, csrfToken: 'csrf'} as Session} changed={async () => {}} />)
    fireEvent.change(screen.getByLabelText('Name'), {target: {value: 'Scheduler comparison'}})
    const choices = screen.getAllByRole('checkbox')
    fireEvent.click(choices[0])
    fireEvent.click(choices[1])
    const aliases = screen.getAllByLabelText('Plot label')
    fireEvent.change(aliases[0], {target: {value: 'Baseline'}})
    fireEvent.change(screen.getByLabelText('Custom scheduler plot color'), {target: {value: '#123456'}})
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    const review = within(screen.getByRole('region', {name: 'Review'}))
    const table = review.getByRole('table', {name: 'Configurations'})
    expect(within(table).getAllByRole('columnheader').map(cell => cell.textContent)).toEqual(['Order', 'Configuration', 'Plot label', 'Color'])
    expect(within(table).getAllByRole('row')).toHaveLength(3)
    expect(within(table).getByRole('img', {name: 'Plot color for Baseline'})).toHaveStyle({backgroundColor: '#1f77b4'})
    expect(within(table).getByRole('img', {name: 'Plot color for Custom scheduler'})).toHaveStyle({backgroundColor: '#123456'})
    expect(review.queryByText('Enabled')).not.toBeInTheDocument()
    expect(review.queryByText('#123456')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Create comparison'}))
    await waitFor(() => expect(request).toHaveBeenCalled())
    const body = JSON.parse(String(vi.mocked(request).mock.calls[0][1]?.body))
    expect(body.kind).toBe('suite')
    expect(vi.mocked(request).mock.calls[0][0]).toBe('/experiment-configurations')
    expect(body.definition.variants).toEqual([
      {configurationId: 'cfg_0', alias: 'Baseline', color: '#1f77b4'},
      {configurationId: 'cfg_1', alias: 'Custom scheduler', color: '#123456'}
    ])
  })

  it('does not offer other suites as members and requires two single configurations', () => {
    const suite = {...configurations[0], id: 'suite', kind: 'suite' as const, name: 'Existing suite'}
    render(<ComparativeConfigurationDialog open close={() => {}} configurations={[...configurations, suite]} applications={[application]} clusters={[cluster]} session={{authenticated: true}} changed={async () => {}} />)
    expect(screen.queryByText('Existing suite')).not.toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Continue'})).toBeDisabled()
    fireEvent.click(screen.getAllByRole('checkbox')[0])
    expect(screen.getByRole('button', {name: 'Continue'})).toBeDisabled()
    fireEvent.click(screen.getAllByRole('checkbox')[1])
    expect(screen.getByRole('button', {name: 'Continue'})).toBeEnabled()
  })
  it('preserves the editing revision while allowing the execution order to change', async () => {
    vi.mocked(request).mockResolvedValue({id: 'suite'} as never)
    const item: ExperimentConfiguration = {...configurations[0], id: 'suite', kind: 'suite', updatedAt: '2026-10-06T00:00:00Z', definition: {...configurations[0].definition, kind: 'suite', runs: 2, variants: [{configurationId: 'cfg_0', alias: 'Baseline', color: '#1f77b4'}, {configurationId: 'cfg_1', alias: 'Custom', color: '#123456'}]}}
    const props = {open: true, close: () => {}, configurations, applications: [application], clusters: [cluster], session: {authenticated: true}, changed: async () => {}}
    const view = render(<ComparativeConfigurationDialog {...props} item={item} />)
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    fireEvent.click(screen.getByRole('button', {name: 'Move configuration 2 up'}))
    view.rerender(<ComparativeConfigurationDialog {...props} item={{...item, updatedAt: '2026-10-06T01:00:00Z'}} />)
    fireEvent.click(screen.getByRole('button', {name: 'Continue'}))
    const rows = within(screen.getByRole('table', {name: 'Configurations'})).getAllByRole('row')
    expect(rows[1]).toHaveTextContent('Custom scheduler')
    expect(rows[2]).toHaveTextContent('Default scheduler')
    fireEvent.click(screen.getByRole('button', {name: 'Save comparison'}))
    await waitFor(() => expect(vi.mocked(request).mock.calls.at(-1)?.[0]).toBe('/experiment-configurations/suite'))
    const call = vi.mocked(request).mock.calls.at(-1)!
    expect(call[1]?.method).toBe('PUT')
    const body = JSON.parse(String(call[1]?.body))
    expect(body.updatedAt).toBe(item.updatedAt)
    expect(body.definition.variants.map((member: {configurationId: string}) => member.configurationId)).toEqual(['cfg_1', 'cfg_0'])
  })

})

describe('comparative configuration details', () => {
  const item: ExperimentConfiguration = {...configurations[0], id: 'suite', kind: 'suite', name: 'Scheduler comparison', description: 'Compare placement strategies.', definition: {applicationValues: {}, components: [], kind: 'suite', runs: 3, variants: [{configurationId: 'cfg_1', alias: 'Custom', color: '#123456'}, {configurationId: 'cfg_0', alias: 'Baseline', color: '#1f77b4'}]}}
  const props = {open: true, readOnly: true, configurations, applications: [application], clusters: [cluster], session: {authenticated: true}, changed: async () => {}}

  it('shows only selected configurations in saved order, with colors and run totals', () => {
    const close = vi.fn()
    render(<ComparativeConfigurationDialog {...props} configurations={[...configurations, {...configurations[0], id: 'unused', name: 'Not selected'}]} item={item} close={close} />)
    expect(screen.getByRole('heading', {name: 'Scheduler comparison'})).toBeInTheDocument()
    expect(screen.getAllByRole('heading', {level: 4}).map(node => node.textContent)).toEqual(['Custom', 'Baseline'])
    expect(screen.queryByText('Not selected')).not.toBeInTheDocument()
    expect(screen.getByText('Total runs').nextElementSibling).toHaveTextContent('6')
    expect(screen.getByRole('img', {name: 'Plot color for Custom: #123456'})).toHaveStyle({backgroundColor: '#123456'})
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /Move configuration|Validate and save/})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Back'}))
    expect(close).toHaveBeenCalledOnce()
  })

  it('preserves unavailable and invalid members in the comparison instead of hiding them', () => {
    render(<ComparativeConfigurationDialog {...props} configurations={[{...configurations[0], validation: {valid: false, issues: []}}]} item={item} close={() => {}} />)
    expect(screen.getAllByRole('heading', {level: 4}).map(node => node.textContent)).toEqual(['Custom', 'Baseline'])
    expect(screen.getByText('This configuration is no longer available.')).toBeInTheDocument()
    expect(screen.getByText('This configuration needs validation.')).toBeInTheDocument()
  })

  it('shows external targets and closes when the view is no longer open', () => {
    const external = {...configurations[1], definition: {...configurations[1].definition, destinationMode: 'external' as const, externalDestination: {targetUrl: 'https://inference.example/v1', runtimeImage: '', script: '', parameters: {}}}}
    const view = render(<ComparativeConfigurationDialog {...props} configurations={[external, configurations[0]]} item={item} close={() => {}} />)
    expect(screen.getByText('External destination').nextElementSibling).toHaveTextContent('https://inference.example/v1')
    view.rerender(<ComparativeConfigurationDialog {...props} open={false} item={item} close={() => {}} />)
    expect(screen.queryByRole('heading', {name: item.name})).not.toBeInTheDocument()
  })
})
