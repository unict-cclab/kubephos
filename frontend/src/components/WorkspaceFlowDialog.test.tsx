import {describe, expect, it} from 'vitest'
import type {Artifact, Operation, Plugin} from '../types'
import {buildFlow} from './WorkspaceFlowDialog'

const producer: Plugin = {id: 'producer', name: 'Producer', version: '1.0.0', description: '', schema: {}, artifactOutputs: [{type: 'Cluster', version: 'v1'}]}
const consumer: Plugin = {id: 'consumer', name: 'Consumer', version: '1.0.0', description: '', schema: {}, artifactInputs: [{type: 'Cluster', version: 'v1'}], artifactOutputs: [{type: 'Deployment', version: 'v1'}]}
const artifact: Artifact = {id: 'art_one', operationId: 'op_one', workspaceId: 'ws_one', name: 'cluster', type: 'Cluster', version: 'v1', mediaType: 'application/json', digest: 'sha256:value', sizeBytes: 10, sensitive: true, verifiedAt: '2026-01-01T00:00:00Z'}
const operation: Operation = {id: 'op_one', workspaceId: 'ws_one', pluginId: 'producer', title: 'Produce', status: 'succeeded', planHash: 'hash', plan: {steps: []}, validation: {valid: true, issues: []}, steps: [], createdAt: '2026-01-01T00:00:00Z'}

describe('buildFlow', () => {
  it('unlocks consumers only from artifacts in the same workspace', () => {
    const empty = buildFlow('ws_one', [producer, consumer], [], [])
    expect(empty.find(item => item.plugin.id === 'producer')?.state).toBe('available')
    expect(empty.find(item => item.plugin.id === 'consumer')?.state).toBe('waiting')

    const ready = buildFlow('ws_one', [producer, consumer], [artifact], [operation])
    expect(ready.find(item => item.plugin.id === 'producer')?.state).toBe('executed')
    expect(ready.find(item => item.plugin.id === 'consumer')?.state).toBe('available')

    const isolated = buildFlow('ws_two', [producer, consumer], [artifact], [operation])
    expect(isolated.find(item => item.plugin.id === 'consumer')?.state).toBe('waiting')
  })

  it('prioritizes active operations', () => {
    const running = {...operation, status: 'verifying'}
    expect(buildFlow('ws_one', [producer], [], [running])[0].state).toBe('active')
  })
})
