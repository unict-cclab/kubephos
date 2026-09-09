import {describe, expect, it} from 'vitest'
import type {Pipeline} from '../types'
import {compatibleInWorkspace, hasCompatiblePair} from './PipelinesView'

function pipeline(id: string, workspaceId: string, type: string, version = 'v1alpha1'): Pipeline {
  return {id, workspaceId, name: id, description: '', definition: {stages: [], result: {stage: ''}}, resolution: {stages: [], result: {type, version}}, validation: {valid: true, issues: []}, hash: id, createdAt: '2026-01-01T00:00:00Z', updatedAt: '2026-01-01T00:00:00Z'}
}

describe('pipeline comparison compatibility', () => {
  it('finds flows with the same result contract in one workspace', () => {
    const pipelines = [pipeline('a', 'one', 'Result'), pipeline('b', 'one', 'Result'), pipeline('c', 'one', 'Other')]
    expect(compatibleInWorkspace(pipelines, 'one').map(item => item.id)).toEqual(['a', 'b'])
    expect(hasCompatiblePair(pipelines)).toBe(true)
  })

  it('does not combine contracts or workspaces', () => {
    const pipelines = [pipeline('a', 'one', 'Result'), pipeline('b', 'two', 'Result'), pipeline('c', 'one', 'Other')]
    expect(compatibleInWorkspace(pipelines, 'one')).toEqual([])
    expect(hasCompatiblePair(pipelines)).toBe(false)
  })
})
