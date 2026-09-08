import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {SchemaFields} from './SchemaFields'

describe('SchemaFields', () => {
  it('renders catalog references without application-specific fields', () => {
    render(<form><SchemaFields
      schema={{type: 'object', required: ['applicationRef'], properties: {applicationRef: {type: 'string', format: 'kubephos-application-ref', title: 'Application'}}}}
      applications={[{id: 'dev.example.app', reference: 'app:dev.example.app@1.0.0', name: 'Example', version: '1.0.0', description: '', origin: 'imported', digest: 'sha256:test', descriptor: {}}]}
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
      connections={[]}
      credentials={[]}
    /></form>)
    expect(screen.getByRole('option')).toHaveTextContent('Scalable · 1.0.0')
    expect(screen.queryByText('Fixed · 1.0.0')).not.toBeInTheDocument()
  })
})
