import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {readSchemaValues} from '../lib'
import type {Application, JsonSchema} from '../types'
import {SchemaFields} from './SchemaFields'

describe('SchemaFields', () => {
  it('renders catalog references without application-specific fields', () => {
    render(<form><SchemaFields
      schema={{type: 'object', required: ['applicationRef'], properties: {applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application'}}}}
      applications={[{id: 'dev.example.app', reference: 'app:dev.example.app@1.0.0', name: 'Example', version: '1.0.0', description: '', origin: 'imported', digest: 'sha256:test', descriptor: {}}]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    const field = screen.getByRole('combobox', {name: 'Application'})
    expect(field).toHaveValue('app:dev.example.app@1.0.0')
    expect(screen.getByRole('option')).toHaveTextContent('Example · 1.0.0')
  })

  it('filters application references by a trait declared in the schema', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application', 'x-kubephos-required-trait': 'scalable'}}}}
      applications={[
        {id: 'dev.example.scalable', reference: 'app:dev.example.scalable@1.0.0', name: 'Scalable', version: '1.0.0', description: '', origin: 'imported', digest: 'sha256:a', descriptor: {spec: {interface: {components: [{id: 'api', traits: ['scalable']}]}}}},
        {id: 'dev.example.fixed', reference: 'app:dev.example.fixed@1.0.0', name: 'Fixed', version: '1.0.0', description: '', origin: 'imported', digest: 'sha256:b', descriptor: {spec: {interface: {components: [{id: 'api'}]}}}},
      ]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('option')).toHaveTextContent('Scalable · 1.0.0')
    expect(screen.queryByText('Fixed · 1.0.0')).not.toBeInTheDocument()
  })

  it('offers only artifacts matching the declared contract', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {machines: {type: 'string', format: 'kubephos-artifact-ref', title: 'Machines', 'x-kubephos-artifact-type': 'MachineSet', 'x-kubephos-artifact-version': 'v1alpha1'}}}}
      applications={[]}
      artifacts={[
        {id: 'art_machines', operationId: 'op_topology', workspaceId: 'ws_one', name: 'machine-set', type: 'MachineSet', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:a', sizeBytes: 10, sensitive: false, verifiedAt: '2026-01-01T00:00:00Z'},
        {id: 'art_result', operationId: 'op_topology', workspaceId: 'ws_one', name: 'result', type: 'RunResult', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:b', sizeBytes: 10, sensitive: false, verifiedAt: '2026-01-01T00:00:00Z'},
      ]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('combobox', {name: 'Machines'})).toHaveValue('art_machines')
    expect(screen.getAllByRole('option')).toHaveLength(1)
  })

  it('prefills schema fields from a generic action preset', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {action: {type: 'string', title: 'Action', enum: ['start', 'stop'], default: 'start'}, replicas: {type: 'integer', title: 'Replicas', default: 1}}}}
      values={{action: 'stop', replicas: 4}}
      applications={[]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('combobox', {name: 'Action'})).toHaveValue('stop')
    expect(screen.getByRole('spinbutton', {name: 'Replicas'})).toHaveValue(4)
  })

  it('accepts whole and decimal values for number fields with a decimal minimum', () => {
    render(<form><SchemaFields
      schema={{type: 'object', required: ['spawnRate'], properties: {spawnRate: {type: 'number', title: 'Users started per second', minimum: 0.1, maximum: 100000, default: 10}}}}
      applications={[]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    const field = screen.getByRole('spinbutton', {name: 'Users started per second'})
    expect(field).toHaveAttribute('step', 'any')
    expect(field).toHaveValue(10)
    expect(field).toBeValid()
    fireEvent.change(field, {target: {value: '0.5'}})
    expect(field).toBeValid()
  })

  it('renders multiline strings declared by a plugin', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {command: {type: 'string', title: 'Command', 'x-kubephos-multiline': true}}}}
      values={{command: 'one\ntwo'}}
      applications={[]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('textbox', {name: 'Command'}).tagName).toBe('TEXTAREA')
    expect(screen.getByRole('textbox', {name: 'Command'})).toHaveValue('one\ntwo')
  })

  it('shows fields only for the selected schema action', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {
        action: {type: 'string', title: 'Action', enum: ['list', 'write'], default: 'list'},
        content: {type: 'string', title: 'Content', 'x-kubephos-visible-when': {property: 'action', values: ['write']}},
      }}}
      applications={[]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.queryByRole('textbox', {name: 'Content'})).not.toBeInTheDocument()
    fireEvent.change(screen.getByRole('combobox', {name: 'Action'}), {target: {value: 'write'}})
    expect(screen.getByRole('textbox', {name: 'Content'})).toBeInTheDocument()
  })

  it('renders and reads settings from the selected application contract', () => {
    const schema: JsonSchema = {type: 'object', required: ['applicationRef'], properties: {
      applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application'},
      values: {type: 'object', title: 'Application settings', 'x-kubephos-schema-from-application': 'applicationRef'},
    }}
    const applications: Application[] = [{
      id: 'dev.example.app', reference: 'app:dev.example.app@1.0.0', name: 'Example', version: '1.0.0', description: '', origin: 'imported', digest: 'sha256:test',
      descriptor: {spec: {valuesSchema: {type: 'object', required: ['replicas'], properties: {replicas: {type: 'integer', title: 'Initial replicas', minimum: 1, maximum: 10}}}, defaults: {replicas: 2}}},
    }]
    const {container} = render(<form><SchemaFields schema={schema} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const replicas = screen.getByRole('spinbutton', {name: 'Initial replicas'})
    expect(replicas).toHaveValue(2)
    fireEvent.change(replicas, {target: {value: '4'}})
    const form = container.querySelector('form')!
    expect(readSchemaValues(form, schema, 'schema', applications)).toEqual({applicationRef: applications[0].reference, values: {replicas: 4}})
  })
})
