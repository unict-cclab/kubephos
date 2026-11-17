import {describe, expect, it} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {IstioSettingsEditor} from './IstioSettingsEditor'
import type {ApplicationComponent} from '../types'

const services = [{id: 'frontend'}, {id: 'cart'}] as ApplicationComponent[]

function renderEditor(props: Partial<Parameters<typeof IstioSettingsEditor>[0]> = {}) {
  const {container} = render(<form><IstioSettingsEditor services={services} {...props}><input name="istioInjection" defaultValue="true" /></IstioSettingsEditor></form>)
  return () => new FormData(container.querySelector('form')!)
}
const enableIstio = () => screen.getByRole('checkbox', {name: 'Enable Istio'})
const mode = (name: 'Sidecar' | 'Ambient') => screen.getByRole('radio', {name: new RegExp(`^${name}`)})
const waypoint = (values: () => FormData) => JSON.parse(String(values().get('istioWaypoint')))

describe('Istio settings editor', () => {
  it('asks first whether to enable Istio and keeps sidecar settings by default', () => {
    const values = renderEditor()
    expect(enableIstio()).toBeChecked()
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioInjection')).toBe('true')
  })

  it('submits no Istio settings when Istio is not enabled', () => {
    const values = renderEditor()
    fireEvent.click(enableIstio())
    expect(screen.queryByRole('group', {name: 'Mode'})).not.toBeInTheDocument()
    expect(screen.queryByText(/runs without a service mesh/)).not.toBeInTheDocument()
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioInjection')).toBeNull()
    expect(values().get('istioWaypoint')).toBeNull()

    fireEvent.click(enableIstio())
    expect(values().get('istioInjection')).toBe('true')
  })

  it('opens a configuration without Istio unchecked', () => {
    const values = renderEditor({enabled: false})
    expect(enableIstio()).not.toBeChecked()
    expect(values().get('istioInjection')).toBeNull()
  })

  it('drops sidecar settings in ambient mode and restores them on the way back', () => {
    const values = renderEditor()
    expect(mode('Sidecar')).toBeChecked()
    fireEvent.click(mode('Ambient'))
    expect(values().get('istioDataPlane')).toBe('ambient')
    expect(values().get('istioInjection')).toBeNull()
    expect(screen.queryByText(/whole namespace joins the ambient mesh/)).not.toBeInTheDocument()

    fireEvent.click(mode('Sidecar'))
    expect(values().get('istioInjection')).toBe('true')
    expect(values().get('istioWaypoint')).toBeNull()
    expect(values().get('istioModeChoice')).toBe('sidecar')
  })

  it('sizes the ambient waypoint with replicas and per-replica resources', () => {
    const values = renderEditor({dataPlane: 'ambient'})
    expect(values().get('istioWaypoint')).toBe('null')
    fireEvent.change(screen.getByRole('spinbutton', {name: 'Replicas'}), {target: {value: '3'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Waypoint cpu request per replica'}), {target: {value: '250'}})
    fireEvent.change(screen.getByRole('textbox', {name: 'Waypoint memory limit per replica'}), {target: {value: '256'}})
    expect(waypoint(values)).toEqual({replicas: 3, cpuRequest: '250m', memoryLimit: '256Mi'})
  })

  it('excludes services from the waypoint, or includes only some of them', () => {
    const values = renderEditor({dataPlane: 'ambient', waypoint: {serviceOverrides: {cart: false}}})
    fireEvent.click(screen.getByRole('tab', {name: 'Service overrides'}))
    expect(screen.queryByText(/Turn this off to use the waypoint only for some services/)).not.toBeInTheDocument()
    expect(screen.getByRole('checkbox', {name: 'Route every service through the waypoint'})).toBeChecked()
    expect(screen.getByRole('combobox', {name: 'Waypoint use for cart'})).toHaveValue('false')
    expect(waypoint(values)).toEqual({serviceOverrides: {cart: false}})

    fireEvent.click(screen.getByRole('checkbox', {name: 'Route every service through the waypoint'}))
    fireEvent.change(screen.getByRole('combobox', {name: 'Waypoint use for cart'}), {target: {value: 'true'}})
    expect(waypoint(values)).toEqual({allServices: false, serviceOverrides: {cart: true}})

    fireEvent.click(screen.getByRole('button', {name: 'Remove waypoint override for cart'}))
    expect(waypoint(values)).toEqual({allServices: false})
  })

  it('opens a saved ambient configuration with its waypoint settings', () => {
    const values = renderEditor({dataPlane: 'ambient', enabled: false, waypoint: {replicas: 2, cpuLimit: '1'}})
    expect(enableIstio()).toBeChecked()
    expect(mode('Ambient')).toBeChecked()
    expect(screen.getByRole('spinbutton', {name: 'Replicas'})).toHaveValue(2)
    expect(waypoint(values)).toEqual({replicas: 2, cpuLimit: '1'})
    expect(values().get('istioInjection')).toBeNull()
  })

  it('stays without Istio where Istio settings are unavailable', () => {
    const values = renderEditor({dataPlane: 'ambient', disabled: true})
    expect(enableIstio()).toBeDisabled()
    expect(enableIstio()).not.toBeChecked()
    expect(enableIstio()).toHaveAttribute('title','Multi-cluster uses Cilium.')
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioWaypoint')).toBeNull()
  })
})

it('places the ambient waypoint with a node selector and tolerations', () => {
  const values = renderEditor({dataPlane: 'ambient'})
  fireEvent.click(screen.getByRole('tab', {name: 'Placement'}))
  fireEvent.click(screen.getByRole('button', {name: 'Add node selector'}))
  fireEvent.change(screen.getByLabelText('Node selector key 1'), {target: {value: 'kubernetes.io/hostname'}})
  fireEvent.change(screen.getByLabelText('Node selector value 1'), {target: {value: 'fg-01-control-plane-1'}})
  fireEvent.click(screen.getByRole('button', {name: 'Add toleration'}))
  fireEvent.change(screen.getByLabelText('Toleration key 1'), {target: {value: 'node-role.kubernetes.io/control-plane'}})
  fireEvent.change(screen.getByLabelText('Toleration operator 1'), {target: {value: 'Exists'}})
  fireEvent.change(screen.getByLabelText('Toleration effect 1'), {target: {value: 'NoSchedule'}})
  expect(waypoint(values)).toMatchObject({
    nodeSelector: {'kubernetes.io/hostname': 'fg-01-control-plane-1'},
    tolerations: [{key: 'node-role.kubernetes.io/control-plane', operator: 'Exists', effect: 'NoSchedule'}],
  })
  expect(waypoint(values).replicas).toBeUndefined()
})

it('reopens a placed waypoint with its node selector and tolerations', () => {
  const values = renderEditor({dataPlane: 'ambient', waypoint: {nodeSelector: {'kubephos.dev/role': 'management'}, tolerations: [{key: 'dedicated', operator: 'Equal', value: 'mesh', effect: 'NoSchedule'}]}})
  expect(waypoint(values)).toEqual({nodeSelector: {'kubephos.dev/role': 'management'}, tolerations: [{key: 'dedicated', operator: 'Equal', value: 'mesh', effect: 'NoSchedule'}]})
})
