import {describe, expect, it} from 'vitest'
import type {Plugin} from '../types'
import {configurableSchema, experimentPlugins, primaryCapabilities} from './ExperimentConfigurationsView'

const plugin = (id: string, capabilities: string[], inputs: string[] = []): Plugin => ({
  id,
  name: id,
  version: '1.0.0',
  description: '',
  schema: {type: 'object', required: ['clusterRef', 'policy'], properties: {clusterRef: {type: 'string', format: 'kubephos-artifact-ref'}, policy: {type: 'string', enum: ['default', 'custom']}}},
  capabilities,
  artifactInputs: inputs.map(type => ({type, version: 'v1alpha1'})),
  runtime: {kind: 'process'}
})

describe('experiment configuration helpers', () => {
  it('keeps only user-configurable plugin fields', () => {
    expect(configurableSchema(plugin('scheduler', []).schema)).toEqual({type: 'object', required: ['policy'], properties: {policy: {type: 'string', enum: ['default', 'custom']}}})
  })

  it('separates primary capabilities from lifecycle hooks', () => {
    expect(primaryCapabilities(plugin('scheduler', ['scheduler.install', 'scheduler.preflight', 'scheduler.cleanup', 'lifecycle.cleanup']))).toEqual(['scheduler.install'])
  })

  it('discovers new experiment plugins from capability or typed inputs', () => {
    const values = [plugin('scheduler', ['scheduler.install']), plugin('custom', ['research.strategy'], ['TargetBinding']), plugin('infra', ['infrastructure.provision'])]
    expect(experimentPlugins(values).map(item => item.id)).toEqual(['scheduler', 'custom'])
  })
})
