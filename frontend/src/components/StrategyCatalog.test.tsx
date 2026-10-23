import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import type {CatalogStrategy} from '../types'
import {StrategyCatalog} from './StrategyCatalog'

const strategy = (kind: CatalogStrategy['kind'], name: string): CatalogStrategy => ({id: name, workspaceId: 'workspace', harborResourceId: 'harbor', name, kind, sourceImage: `example.org/${name}:v1`, defaultConfiguration: {}, operationId: 'operation', status: 'ready', createdAt: '', updatedAt: ''})
const items = [strategy('scheduler', 'sample-scheduler'), strategy('descheduler', 'sample-descheduler'), strategy('autoscaler', 'sample-autoscaler')]

describe('catalog categories', () => {
  beforeEach(() => {window.location.hash = '#catalog'; HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }; HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }})


  it.each([['scheduler', 'sample-scheduler'], ['descheduler', 'sample-descheduler'], ['autoscaler', 'sample-autoscaler']] as const)('shows only %s entries in its tab', (kind, name) => {
    render(<StrategyCatalog kind={kind} items={items} workspaces={[]} session={{authenticated: true}} isAdmin={false} changed={async () => {}} openOperation={() => {}} />)
    expect(screen.getByText(name)).toBeInTheDocument()
    for (const item of items.filter(item => item.kind !== kind)) expect(screen.queryByText(item.name)).not.toBeInTheDocument()
    expect(screen.queryByText(/strategy images/i)).not.toBeInTheDocument()
  })

  it('offers view, settings edit and guarded delete actions on an imported image', () => {
    render(<StrategyCatalog kind="scheduler" items={[{...items[0], status: 'succeeded'}]} workspaces={[]} session={{authenticated: true}} isAdmin changed={async () => {}} openOperation={() => {}} />)
    expect(screen.getByRole('button', {name: 'View sample-scheduler details'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Delete sample-scheduler'})).toBeEnabled()
    expect(screen.getByText('Ready')).toBeInTheDocument()
    expect(screen.queryByText('succeeded')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Activity'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'View sample-scheduler details'}))
    expect(screen.getByText('Ready')).toBeInTheDocument()
  })

  it('keeps the public image form free of an explicit Harbor selection', () => {
    render(<StrategyCatalog kind="scheduler" items={[]} workspaces={[]} session={{authenticated: true}} isAdmin changed={async () => {}} openOperation={() => {}} />)

    fireEvent.click(screen.getByRole('button', {name: 'Add scheduler'}))

    expect(screen.getByRole('textbox', {name: 'Name'})).toBeInTheDocument()
    expect(screen.getByRole('textbox', {name: 'Image'})).toHaveAttribute('placeholder', 'ghcr.io/team/scheduler:v1.2.0')
    expect(screen.getByRole('textbox', {name: 'Configuration'})).toBeInTheDocument()
    expect(screen.queryByRole('combobox', {name: 'Harbor registry'})).not.toBeInTheDocument()
  })

  it('enables NetworkAware without storing experiment-specific selectors', () => {
    render(<StrategyCatalog kind="descheduler" items={[]} workspaces={[]} session={{authenticated: true}} isAdmin changed={async () => {}} openOperation={() => {}} />)
    fireEvent.click(screen.getByRole('button', {name: 'Add descheduler'}))
    const policy = (screen.getByRole('textbox', {name: 'Configuration'}) as HTMLTextAreaElement).value
    expect(policy).toContain('name: NetworkAware')
    expect(policy).toContain('maxPodsToEvict: 1')
    expect(policy).not.toContain('namespaces:')
    expect(policy).not.toContain('labelSelector:')
  })
  it.each(['scheduler', 'descheduler', 'autoscaler'] as const)('updates the %s image while preserving the catalog entry and defaults', async kind => {
    const item = {...strategy(kind, `sample-${kind}`), status: 'succeeded', defaultConfiguration: kind === 'autoscaler' ? {intervalSeconds: 30, minReplicas: 2, maxReplicas: 20, parameters: '{\"threshold\":42}'} : kind === 'descheduler' ? {configFile: 'saved policy', intervalSeconds: 90} : {configFile: 'saved configuration'}}
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(item), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<StrategyCatalog kind={kind} items={[item]} workspaces={[]} session={{authenticated: true, csrfToken: 'csrf'}} isAdmin changed={changed} openOperation={() => {}} />)
    fireEvent.click(screen.getByRole('button', {name: `Edit ${item.name} settings`}))
    const image = screen.getByRole('textbox', {name: 'Image'})
    expect(image).toHaveValue(item.sourceImage)
    fireEvent.change(image, {target: {value: `example.org/sample-${kind}:v2`}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate and save'}))
    await waitFor(() => expect(changed).toHaveBeenCalledOnce())
    expect(fetch).toHaveBeenCalledWith(`/api/v1/catalog/strategies/${item.id}`, expect.objectContaining({method: 'PUT', body: JSON.stringify({workspaceId: '', name: item.name, kind, sourceImage: `example.org/sample-${kind}:v2`, defaultConfiguration: kind === 'autoscaler' ? {...item.defaultConfiguration, parameters: JSON.stringify({threshold: 42}, null, 2)} : item.defaultConfiguration})}))
    vi.unstubAllGlobals()
  })

  it.each(['scheduler', 'descheduler', 'autoscaler'] as const)('saves edited %s settings', async kind => {
    const defaults = kind === 'autoscaler' ? {intervalSeconds: 30, minReplicas: 2, maxReplicas: 20, parameters: '{"threshold":42}'} : kind === 'descheduler' ? {configFile: 'saved policy', intervalSeconds: 90} : {configFile: 'saved configuration'}
    const item = {...strategy(kind, `sample-${kind}`), status: 'succeeded', defaultConfiguration: defaults}
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(item), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<StrategyCatalog kind={kind} items={[item]} workspaces={[]} session={{authenticated: true}} isAdmin changed={changed} openOperation={() => {}} />)
    fireEvent.click(screen.getByRole('button', {name: `Edit ${item.name} settings`}))
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue(item.name)
    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'renamed-strategy'}})
    let expected: Record<string, unknown>
    if (kind === 'autoscaler') {
      expect(screen.getByRole('spinbutton', {name: 'Minimum replicas'})).toHaveValue(2)
      fireEvent.change(screen.getByRole('spinbutton', {name: 'Minimum replicas'}), {target: {value: '4'}})
      fireEvent.change(screen.getByRole('textbox', {name: 'Configuration'}), {target: {value: '{"threshold":70}'}})
      expected = {intervalSeconds: 30, minReplicas: 4, maxReplicas: 20, parameters: JSON.stringify({threshold: 70}, null, 2)}
    } else {
      const config = screen.getByRole('textbox', {name: 'Configuration'})
      expect(config).toHaveValue(defaults.configFile)
      fireEvent.change(config, {target: {value: 'updated configuration'}})
      expected = {configFile: 'updated configuration'}
      if (kind === 'descheduler') {
        fireEvent.change(screen.getByRole('spinbutton', {name: 'Interval (s)'}), {target: {value: '120'}})
        expected.intervalSeconds = 120
      }
    }
    fireEvent.click(screen.getByRole('button', {name: 'Validate and save'}))
    await waitFor(() => expect(changed).toHaveBeenCalledOnce())
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toMatchObject({name: 'renamed-strategy', sourceImage: item.sourceImage, defaultConfiguration: expected})
    vi.unstubAllGlobals()
  })

})
