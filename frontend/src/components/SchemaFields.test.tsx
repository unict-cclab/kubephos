import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {readSchemaValues} from '../lib'
import type {Application, JsonSchema} from '../types'
import {SchemaFields, hasConfigurableFields} from './SchemaFields'

describe('SchemaFields', () => {
  it('merges asynchronous suggestions without resetting edits or unrelated fields', () => {
    const schema: JsonSchema = {type: 'object', properties: {vmid: {type: 'integer', title: 'VMID'}, address: {type: 'string', title: 'IP'}, storage: {type: 'string', title: 'Storage'}}}
    const props = {schema, applications: [], artifacts: [], connections: [], credentials: []}
    const view = render(<SchemaFields {...props} />)
    fireEvent.change(screen.getByLabelText('VMID'), {target: {value: '450'}})
    fireEvent.change(screen.getByLabelText('Storage'), {target: {value: 'local-lvm'}})
    view.rerender(<SchemaFields {...props} suggestions={{vmid: 300, address: '10.0.0.30'}} />)
    expect(screen.getByLabelText('VMID')).toHaveValue(450)
    expect(screen.getByLabelText('IP')).toHaveValue('10.0.0.30')
    expect(screen.getByLabelText('Storage')).toHaveValue('local-lvm')
  })

  it('keeps advanced settings collapsed and submits saved values and resource overrides', () => {
    const schema: JsonSchema = {type: 'object', properties: {
      primaries: {type: 'integer', title: 'Primary shards'},
      timeoutSeconds: {type: 'integer', title: 'Timeout', 'x-kubephos-advanced': true},
      appendOnly: {type: 'boolean', title: 'Persistence', 'x-kubephos-advanced': true},
      ...Object.fromEntries(['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit'].map(name => [name, {type: 'string', 'x-kubephos-advanced': true}])),
      resourceOverrides: {type: 'object', 'x-kubephos-advanced': true},
    }}
    const values = {primaries: 3, timeoutSeconds: 12, appendOnly: true, cpuRequest: '200m', memoryRequest: '256Mi', resourceOverrides: {'redis-primary-1': {cpuLimit: '1'}}}
    const {container} = render(<form><SchemaFields schema={schema} values={values} serviceTargets={[{id: 'redis-primary-1', traits: ['schedulable']}]} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const advanced = screen.getByText('Advanced settings').closest('details')!
    expect(advanced).not.toHaveAttribute('open')
    expect(screen.getByLabelText('Primary shards').closest('details')).toBeNull()
    expect(advanced).toContainElement(screen.getByLabelText('Timeout'))
    expect(advanced).toContainElement(screen.getByText('Service resources'))
    const form = container.querySelector('form')!
    expect(readSchemaValues(form, schema)).toEqual(values)
    fireEvent.click(screen.getByText('Advanced settings'))
    fireEvent.change(screen.getByLabelText('Timeout'), {target: {value: '20'}})
    fireEvent.click(screen.getByText('Advanced settings'))
    expect(readSchemaValues(form, schema)).toEqual({...values, timeoutSeconds: 20})
  })

  it('keeps hidden defaults in the submitted form without showing a control', () => {
    const schema: JsonSchema = {type: 'object', required: ['window'], properties: {window: {type: 'string', title: 'Metrics fallback window', default: '15m', 'x-kubephos-hidden': true}}}
    const {container} = render(<form><SchemaFields schema={schema} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    expect(screen.queryByText('Metrics fallback window')).not.toBeInTheDocument()
    expect(container.querySelector('input[name="schema.window"]')).toHaveValue('15m')
  })

  it('renders catalog references without application-specific fields', () => {
    render(<form><SchemaFields
      schema={{type: 'object', required: ['applicationRef'], properties: {applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application'}}}}
      applications={[{id: 'dev.example.app', reference: 'app:dev.example.app', name: 'Example', description: '', origin: 'imported', digest: 'sha256:test', descriptor: {}}]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    const field = screen.getByRole('combobox', {name: 'Application'})
    expect(field).toHaveValue('app:dev.example.app')
    expect(screen.getByRole('option')).toHaveTextContent('Example')
  })

  it('filters application references by a trait declared in the schema', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application', 'x-kubephos-required-trait': 'scalable'}}}}
      applications={[
        {id: 'dev.example.scalable', reference: 'app:dev.example.scalable', name: 'Scalable', description: '', origin: 'imported', digest: 'sha256:a', descriptor: {spec: {interface: {components: [{id: 'api', traits: ['scalable']}]}}}},
        {id: 'dev.example.fixed', reference: 'app:dev.example.fixed', name: 'Fixed', description: '', origin: 'imported', digest: 'sha256:b', descriptor: {spec: {interface: {components: [{id: 'api'}]}}}},
      ]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('option')).toHaveTextContent('Scalable')
    expect(screen.queryByText('Fixed')).not.toBeInTheDocument()
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

  it('keeps a saved select value visible when the current catalog no longer offers it', () => {
    render(<form><SchemaFields
      schema={{type: 'object', properties: {policy: {type: 'string', title: 'Policy', enum: ['default', 'balanced']}}}}
      values={{policy: 'legacy-policy'}}
      applications={[]}
      artifacts={[]}
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('combobox', {name: 'Policy'})).toHaveValue('legacy-policy')
    expect(screen.getByRole('option', {name: 'legacy-policy · saved value'})).toBeInTheDocument()
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

  it('leaves an optional numeric objective empty and omits it from submitted values', () => {
    const schema: JsonSchema = {type: 'object', properties: {sloMilliseconds: {type: 'integer', title: 'Response time SLO (optional, ms)', description: 'Leave empty to disable SLO plots.', minimum: 1}}}
    const {container} = render(<form><SchemaFields schema={schema} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const field = screen.getByRole('spinbutton', {name: 'Response time SLO (optional, ms)'})
    expect(field).toHaveValue(null)
    expect(screen.getByText('Leave empty to disable SLO plots.')).toBeInTheDocument()
    expect(readSchemaValues(container.querySelector('form')!, schema, 'schema', [])).toEqual({})
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
      id: 'dev.example.app', reference: 'app:dev.example.app', name: 'Example', description: '', origin: 'imported', digest: 'sha256:test',
      descriptor: {spec: {valuesSchema: {type: 'object', required: ['replicas'], properties: {replicas: {type: 'integer', title: 'Initial replicas', minimum: 1, maximum: 10}}}, defaults: {replicas: 2}}},
    }]
    const {container} = render(<form><SchemaFields schema={schema} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const replicas = screen.getByRole('spinbutton', {name: 'Initial replicas'})
    expect(replicas).toHaveValue(2)
    fireEvent.change(replicas, {target: {value: '4'}})
    const form = container.querySelector('form')!
    expect(readSchemaValues(form, schema, 'schema', applications)).toEqual({applicationRef: applications[0].reference, values: {replicas: 4}})
  })

  it('merges saved dynamic application settings with catalog defaults', () => {
    const schema: JsonSchema = {type: 'object', required: ['applicationRef'], properties: {
      applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application'},
      values: {type: 'object', title: 'Application settings', 'x-kubephos-schema-from-application': 'applicationRef'},
    }}
    const applications: Application[] = [{
      id: 'dev.example.app', reference: 'app:dev.example.app', name: 'Example', description: '', origin: 'imported', digest: 'sha256:test',
      descriptor: {spec: {valuesSchema: {type: 'object', properties: {replicas: {type: 'integer', title: 'Initial replicas'}, workers: {type: 'integer', title: 'Workers'}}}, defaults: {replicas: 2, workers: 3}}},
    }]
    render(<form><SchemaFields schema={schema} values={{applicationRef: applications[0].reference, values: {replicas: 7}}} applications={applications} artifacts={[]} connections={[]} credentials={[]} /></form>)
    expect(screen.getByRole('spinbutton', {name: 'Initial replicas'})).toHaveValue(7)
    expect(screen.getByRole('spinbutton', {name: 'Workers'})).toHaveValue(3)
  })
})

it('submits fixed boolean settings without presenting an unusable selector', () => {
  const schema: JsonSchema = {type: 'object', properties: {telemetry: {type: 'boolean', enum: [false]}, enabled: {type: 'boolean', enum: [true]}}}
  const {container} = render(<form><SchemaFields schema={schema} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  expect(readSchemaValues(container.querySelector('form')!,schema)).toEqual({telemetry: false, enabled: true})
})

it('uses declared field order independently of JSON property order and preserves submitted values', () => {
  const schema: JsonSchema = {type: 'object', properties: {
    responseSize: {type: 'integer', title: 'Response size', default: 10, 'x-kubephos-order': 3},
    services: {type: 'integer', title: 'Services', default: 6, 'x-kubephos-order': 1},
    topology: {type: 'string', title: 'Topology', default: 'tree', 'x-kubephos-order': 2}
  }}
  const {container} = render(<form><SchemaFields schema={schema} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
  expect(screen.getAllByRole('spinbutton').map(input => input.getAttribute('aria-label'))).toEqual(['Services', 'Response size'])
  expect([...container.querySelectorAll('label')].map(label => label.firstChild?.textContent)).toEqual(['Services', 'Topology', 'Response size'])
  expect(readSchemaValues(container.querySelector('form')!, schema)).toEqual({services: 6, topology: 'tree', responseSize: 10})
})

it('does not count fixed, hidden or conditionally invisible fields as configurable parameters', () => {
  expect(hasConfigurableFields({properties: {cloudTelemetryEnabled: {type: 'string', enum: ['false']}, internal: {type: 'integer', 'x-kubephos-hidden': true}}}, {cloudTelemetryEnabled: 'false'})).toBe(false)
  expect(hasConfigurableFields({properties: {telemetry: {type: 'string', enum: ['false']}}}, {telemetry: 'legacy'})).toBe(true)
  expect(hasConfigurableFields({properties: {nested: {type: 'object', properties: {fixed: {enum: ['fixed']}}}}})).toBe(false)
  expect(hasConfigurableFields({properties: {serviceCount: {type: 'integer'}}})).toBe(true)
  expect(hasConfigurableFields({properties: {advanced: {type: 'integer', 'x-kubephos-advanced': true}}})).toBe(true)
})
