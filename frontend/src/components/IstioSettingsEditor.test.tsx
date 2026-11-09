import {describe, expect, it} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {IstioSettingsEditor} from './IstioSettingsEditor'

function renderEditor(props: Partial<Parameters<typeof IstioSettingsEditor>[0]> = {}) {
  const {container} = render(<form><IstioSettingsEditor {...props}><input name="istioInjection" defaultValue="true" /></IstioSettingsEditor></form>)
  return () => new FormData(container.querySelector('form')!)
}

describe('Istio settings editor', () => {
  it('keeps sidecar settings by default', () => {
    const values = renderEditor()
    expect(values().get('istioDataPlane')).toBe('sidecar')
    expect(values().get('istioInjection')).toBe('true')
  })

  it('drops sidecar settings in ambient mode and restores them on the way back', () => {
    const values = renderEditor()
    fireEvent.change(screen.getByRole('combobox', {name: 'Data plane'}), {target: {value: 'ambient'}})
    expect(values().get('istioDataPlane')).toBe('ambient')
    expect(values().get('istioInjection')).toBeNull()
    expect(screen.getByText(/whole namespace joins the ambient mesh/)).toBeInTheDocument()

    fireEvent.change(screen.getByRole('combobox', {name: 'Data plane'}), {target: {value: 'sidecar'}})
    expect(values().get('istioInjection')).toBe('true')
  })

  it('opens a saved ambient configuration in ambient mode', () => {
    const values = renderEditor({dataPlane: 'ambient'})
    expect(screen.getByRole('combobox', {name: 'Data plane'})).toHaveValue('ambient')
    expect(values().get('istioInjection')).toBeNull()
  })

  it('stays on sidecar where Istio settings are unavailable', () => {
    const values = renderEditor({dataPlane: 'ambient', disabled: true})
    expect(screen.getByRole('combobox', {name: 'Data plane'})).toBeDisabled()
    expect(values().get('istioDataPlane')).toBe('sidecar')
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
