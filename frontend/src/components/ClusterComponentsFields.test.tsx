import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import {ClusterComponentsFields} from './ClusterComponentsFields'

vi.mock('../api', () => ({request: vi.fn()}))

const catalog = {
  items: [
    {id: 'prometheus', title: 'Prometheus', description: 'Metrics.', core: true},
    {id: 'istio-sidecar', title: 'Istio (sidecar)', description: 'Sidecars.', core: false, unsupportedNetworking: ['cilium']},
    {id: 'kiali', title: 'Kiali', description: 'Mesh console.', core: false, requiresAny: ['istio-sidecar'], unsupportedNetworking: ['cilium']},
    {id: 'logs', title: 'Logs (Loki and Alloy)', description: 'Logs.', core: false},
  ],
  defaults: {flannel: ['prometheus', 'istio-sidecar', 'kiali', 'logs'], cilium: ['prometheus', 'logs']},
}

function renderFields(networking: 'flannel' | 'cilium' = 'flannel') {
  vi.mocked(request).mockResolvedValue(catalog as never)
  const {container} = render(<form><ClusterComponentsFields networking={networking} /></form>)
  return () => new FormData(container.querySelector('form')!).getAll('components')
}

describe('cluster components fields', () => {
  it('selects the default set and keeps core components locked', async () => {
    const values = renderFields()
    expect(await screen.findByRole('checkbox', {name: /Prometheus/})).toBeDisabled()
    await waitFor(() => expect(values()).toEqual(catalog.defaults.flannel))
  })

  it('switches to custom when a component changes and drops components whose dependency is removed', async () => {
    const values = renderFields()
    fireEvent.click(await screen.findByRole('checkbox', {name: /Istio \(sidecar\)/}))
    expect(values()).toEqual(['prometheus', 'logs'])
    expect(screen.getByRole('checkbox', {name: /Kiali/})).toBeDisabled()
    expect(screen.getByRole('combobox', {name: 'Preset'})).toHaveValue('custom')
    fireEvent.change(screen.getByRole('combobox', {name: 'Preset'}), {target: {value: 'default'}})
    expect(values()).toEqual(catalog.defaults.flannel)
  })

  it('hides components the networking mode cannot run', async () => {
    const values = renderFields('cilium')
    await screen.findByRole('checkbox', {name: /Prometheus/})
    expect(screen.queryByRole('checkbox', {name: /Istio|Kiali/})).not.toBeInTheDocument()
    await waitFor(() => expect(values()).toEqual(catalog.defaults.cilium))
  })
})
