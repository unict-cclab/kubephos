import {describe, expect, it} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {IstioSettingsEditor} from './IstioSettingsEditor'

function renderEditor(props: Partial<Parameters<typeof IstioSettingsEditor>[0]> = {}) {
  const {container} = render(<form><IstioSettingsEditor {...props}><input name="istioInjection" defaultValue="true" /></IstioSettingsEditor></form>)
  return () => new FormData(container.querySelector('form')!)
}
const useIstio = () => screen.getByRole('combobox', {name: 'Use Istio'})
const dataPlane = () => screen.getByRole('combobox', {name: 'Data plane'})

describe('Istio settings editor', () => {
  it('asks first whether to use Istio and keeps sidecar settings by default', () => {
    const values = renderEditor()
    expect(useIstio()).toHaveValue('yes')
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioInjection')).toBe('true')
  })

  it('submits no Istio settings when the run does not use Istio', () => {
    const values = renderEditor()
    fireEvent.change(useIstio(), {target: {value: 'no'}})
    expect(screen.queryByRole('combobox', {name: 'Data plane'})).not.toBeInTheDocument()
    expect(screen.getByText(/runs without a service mesh/)).toBeInTheDocument()
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioInjection')).toBeNull()
    expect(values().get('istioWaypoint')).toBeNull()

    fireEvent.change(useIstio(), {target: {value: 'yes'}})
    expect(values().get('istioInjection')).toBe('true')
  })

  it('opens a configuration without Istio on "No"', () => {
    const values = renderEditor({enabled: false})
    expect(useIstio()).toHaveValue('no')
    expect(values().get('istioInjection')).toBeNull()
  })

  it('drops sidecar settings in ambient mode and restores them on the way back', () => {
    const values = renderEditor()
    fireEvent.change(dataPlane(), {target: {value: 'ambient'}})
    expect(values().get('istioDataPlane')).toBe('ambient')
    expect(values().get('istioInjection')).toBeNull()
    expect(screen.getByText(/whole namespace joins the ambient mesh/)).toBeVisible()

    fireEvent.change(dataPlane(), {target: {value: 'sidecar'}})
    expect(values().get('istioInjection')).toBe('true')
    expect(values().get('istioWaypoint')).toBeNull()
  })

  it('sizes the ambient waypoint with replicas and per-replica resources', () => {
    const values = renderEditor({dataPlane: 'ambient'})
    expect(values().get('istioWaypoint')).toBe('null')
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Replicas'}), {target: {value: '3'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Waypoint cpu request per replica'}), {target: {value: '250'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Waypoint memory limit per replica'}), {target: {value: '256'}})
    expect(JSON.parse(String(values().get('istioWaypoint')))).toEqual({replicas: 3, cpuRequest: '250m', memoryLimit: '256Mi'})
  })

  it('opens a saved ambient configuration with its waypoint size', () => {
    const values = renderEditor({dataPlane: 'ambient', enabled: false, waypoint: {replicas: 2, cpuLimit: '1'}})
    expect(useIstio()).toHaveValue('yes')
    expect(dataPlane()).toHaveValue('ambient')
    expect(screen.getByRole('spinbutton', {name: 'Replicas'})).toHaveValue(2)
    expect(JSON.parse(String(values().get('istioWaypoint')))).toEqual({replicas: 2, cpuLimit: '1'})
    expect(values().get('istioInjection')).toBeNull()
  })

  it('stays without Istio where Istio settings are unavailable', () => {
    const values = renderEditor({dataPlane: 'ambient', disabled: true})
    expect(useIstio()).toBeDisabled()
    expect(useIstio()).toHaveValue('no')
    expect(screen.getByText(/Cilium networking/)).toBeInTheDocument()
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioWaypoint')).toBeNull()
  })
})

describe('Istio settings for application deployments', () => {
  it('offers no data plane choice and submits sidecar settings unchanged', () => {
    const values = renderEditor({ambientAvailable: false})
    expect(screen.queryByRole('combobox', {name: 'Data plane'})).not.toBeInTheDocument()
    expect(values().get('istioDataPlane')).toBeNull()
    expect(values().get('istioInjection')).toBe('true')
  })
})
