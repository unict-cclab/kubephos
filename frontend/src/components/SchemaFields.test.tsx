import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
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
})
