import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import type {ApplicationComponent, JsonSchema} from '../types'
import {SchemaFields} from './SchemaFields'
import {readSchemaValues} from '../lib'

const fields = ['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit']
const schema: JsonSchema = {type: 'object', properties: {...Object.fromEntries(fields.map(field => [field, {type: 'string'}])), resourceOverrides: {type: 'object', additionalProperties: true}}}
const services: ApplicationComponent[] = [{id: 'm1', traits: ['scalable', 'schedulable']}, {id: 'm2', traits: ['scalable']}, {id: 'inference-router', traits: ['schedulable']}, {id: 'node-proxy', traits: ['traffic-entrypoint']}]

describe('Service resources', () => {
  it('orders request and limit fields by resource and omits empty defaults', () => {
    const {container} = render(<form><SchemaFields schema={schema} serviceTargets={services} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const form = container.querySelector('form')!
    expect(screen.getAllByRole('textbox').map(input => input.getAttribute('aria-label'))).toEqual(['CPU request per service (optional)', 'CPU limit per service (optional)', 'Memory request per service (optional)', 'Memory limit per service (optional)'])
    for (const input of screen.getAllByRole('textbox')) {
      expect(input).toHaveValue('')
      expect(input).toHaveAttribute('placeholder', 'Not set')
    }
    expect(readSchemaValues(form, schema)).toEqual({resourceOverrides: {}})
    expect(screen.queryByRole('checkbox', {name: 'Override resources for node-proxy'})).not.toBeInTheDocument()
  })
  it('submits partial per-service overrides and keeps inherited fields absent', () => {
    const changed = vi.fn()
    const {container} = render(<form><SchemaFields schema={schema} serviceTargets={services} values={{cpuRequest: '250m', memoryLimit: '2Gi'}} applications={[]} artifacts={[]} connections={[]} credentials={[]} onValuesChange={changed} /></form>)
    const form = container.querySelector('form')!
    fireEvent.click(screen.getByText('Resource overrides by service').closest('summary')!)
    fireEvent.click(screen.getByRole('checkbox', {name: 'Override resources for m1'}))
    expect(screen.getByRole('textbox', {name: 'CPU request for m1'})).toHaveAttribute('placeholder', 'Inherited: 250m')
    fireEvent.change(screen.getByRole('textbox', {name: 'CPU request for m1'}), {target: {value: '500m'}})
    expect(readSchemaValues(form, schema)).toEqual({cpuRequest: '250m', memoryLimit: '2Gi', resourceOverrides: {m1: {cpuRequest: '500m'}}})
    fireEvent.change(screen.getByRole('textbox', {name: 'CPU request for m1'}), {target: {value: ''}})
    expect(readSchemaValues(form, schema)).toMatchObject({resourceOverrides: {m1: {}}})
    fireEvent.click(screen.getByRole('checkbox', {name: 'Override resources for m1'}))
    expect(readSchemaValues(form, schema)).toMatchObject({resourceOverrides: {}})
    expect(changed).toHaveBeenCalledTimes(4)
  })
  it('preserves resource overrides saved for MuBench services', () => {
    const {container} = render(<form><SchemaFields schema={schema} serviceTargets={[{id: 's0', traits: ['scalable']}, {id: 's1', traits: ['scalable']}]} values={{resourceOverrides: {s1: {memoryLimit: '1Gi'}}}} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    expect(screen.getByRole('checkbox', {name: 'Override resources for s1'})).toBeChecked()
    expect(screen.getByRole('textbox', {name: 'Memory limit for s1'})).toHaveValue('1Gi')
    expect(readSchemaValues(container.querySelector('form')!, schema)).toEqual({resourceOverrides: {s1: {memoryLimit: '1Gi'}}})
  })
})
