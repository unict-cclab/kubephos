import {fireEvent, render, screen} from '@testing-library/react'
import {beforeAll, describe, expect, it, vi} from 'vitest'
import type {Artifact, Operation, Plugin} from '../types'
import {buildFlow, WorkspaceFlowDialog} from './WorkspaceFlowDialog'

const producer: Plugin = {id: 'producer', name: 'Producer', version: '1.0.0', description: '', schema: {}, runtime: {kind: 'bundled'}, artifactOutputs: [{type: 'Cluster', version: 'v1'}]}
const consumer: Plugin = {id: 'consumer', name: 'Consumer', version: '1.0.0', description: '', schema: {}, runtime: {kind: 'bundled'}, artifactInputs: [{type: 'Cluster', version: 'v1'}], artifactOutputs: [{type: 'Deployment', version: 'v1'}]}
const artifact: Artifact = {id: 'art_one', operationId: 'op_one', workspaceId: 'ws_one', name: 'cluster', type: 'Cluster', version: 'v1', mediaType: 'application/json', digest: 'sha256:value', sizeBytes: 10, sensitive: true, verifiedAt: '2026-01-01T00:00:00Z'}
const operation: Operation = {id: 'op_one', workspaceId: 'ws_one', pluginId: 'producer', pluginVersion: '1.0.0', title: 'Produce', status: 'succeeded', planHash: 'hash', plan: {steps: []}, validation: {valid: true, issues: []}, steps: [], createdAt: '2026-01-01T00:00:00Z'}

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

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

  it('keeps generic interactive capabilities ready after prior runs', () => {
    const controller = {...consumer, id: 'controller', capabilities: ['session.control']}
    const completed = {...operation, id: 'op_control', pluginId: controller.id}
    expect(buildFlow('ws_one', [controller], [artifact], [completed])[0]).toMatchObject({state: 'available', interactive: true})
  })

  it('renders schema-declared primary actions without knowing the controller', () => {
    const controller = {
      ...consumer,
      id: 'controller',
      capabilities: ['session.control'],
      schema: {properties: {transition: {type: 'string' as const, enum: ['engage', 'hold'], 'x-kubephos-primary-action': true}}}
    }
    const configure = vi.fn()
    render(<WorkspaceFlowDialog open workspace={{id: 'ws_one', name: 'Development', description: '', status: 'active', createdAt: '2026-01-01T00:00:00Z'}} plugins={[controller]} artifacts={[artifact]} operations={[]} close={() => undefined} configure={configure} />)

    fireEvent.click(screen.getByRole('button', {name: 'hold'}))

    expect(configure).toHaveBeenCalledWith('controller', {transition: 'hold'})
  })
})
