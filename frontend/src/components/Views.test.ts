import {describe, expect, it} from 'vitest'
import {buildRecentActivity} from './Views'
import type {Experiment, ManagedResource} from '../types'

describe('buildRecentActivity', () => {
  it('shows user resources and experiments instead of internal operations', () => {
    const experiment = {id: 'exp-1', name: 'Scheduler comparison', status: 'running', createdAt: '2026-09-17T10:00:00Z', updatedAt: '2026-09-17T12:00:00Z'} as Experiment
    const cluster = {id: 'cluster-1', name: 'research-cluster', kind: 'kubernetes-cluster', status: 'ready', createdAt: '2026-09-17T09:00:00Z', updatedAt: '2026-09-17T11:00:00Z'} as ManagedResource
    const items = buildRecentActivity({experiments: [experiment], configurations: [], connections: [], resources: [cluster], applications: []})

    expect(items.map(item => item.title)).toEqual(['Experiment in progress', 'Kubernetes cluster ready'])
    expect(items[0].detail).toBe('Scheduler comparison')
    expect(items[0].href).toBe('results/instance/exp-1')
  })
})
