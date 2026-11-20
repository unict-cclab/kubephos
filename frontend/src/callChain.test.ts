import {describe, expect, it} from 'vitest'
import {isProxy, modelRequestsPerExecution, predictChain} from './callChain'
import type {CallModel} from './types'

const base = {source: 'istio', measuredAt: '2026-11-18T10:00:00Z', applicationRef: 'app', scenarioId: 's', proxies: ['node-proxy']}

describe('call chain prediction', () => {
  it('converts the HTTP rate to task executions with measured requests per execution', () => {
    const model: CallModel = {...base, tasks: [
      {task: 'browse', executions: 50, entryRequests: 50, services: {frontend: 1, currency: 6}, edges: [{source: 'frontend', destination: 'currency', perExecution: 6}]},
      {task: 'checkout', executions: 10, entryRequests: 30, services: {frontend: 3, checkout: 1}}
    ]}
    const prediction = predictChain([{name: 'browse', share: .75}, {name: 'checkout', share: .25}], model, 120)
    // Σ s·r = 0.75 + 0.75 = 1.5 → 80 executions/s.
    expect(prediction.taskRate).toBeCloseTo(80)
    expect(prediction.requestShare).toEqual({browse: .5, checkout: .5})
    expect(Object.fromEntries(prediction.services.map(service => [service.service, service.rps]))).toEqual({frontend: 120, currency: 360, checkout: 20})
    expect(prediction.services[0].service).toBe('currency')
    expect(prediction.edges).toEqual([{source: 'frontend', destination: 'currency', rps: 360}])
    expect(prediction.unmeasured).toEqual([])
  })

  it('falls back to per-request multipliers and parsed request counts when executions are missing', () => {
    const model: CallModel = {...base, tasks: [
      {task: 'browse', entryRequests: 100, servicesPerRequest: {'node-proxy': 1, currency: 6}, edges: [{source: 'node-proxy', destination: 'currency', perRequest: 6}]},
      {task: 'checkout', entryRequests: 90, servicesPerRequest: {'node-proxy': 1, payment: 1 / 3}}
    ]}
    const prediction = predictChain([{name: 'browse', share: .5, parsedRequests: 1}, {name: 'checkout', share: .5, parsedRequests: 3}, {name: 'other', share: 0}], model, 100)
    expect(prediction.parsedOnly).toEqual(['browse', 'checkout'])
    expect(prediction.requestShare).toEqual({browse: .25, checkout: .75})
    const rates = Object.fromEntries(prediction.services.map(service => [service.service, service.rps]))
    expect(rates['node-proxy']).toBeCloseTo(100)
    expect(rates.currency).toBeCloseTo(150)
    expect(rates.payment).toBeCloseTo(25)
    expect(prediction.services.find(service => service.service === 'node-proxy')?.proxy).toBe(true)
    expect(prediction.edges[0].rps).toBeCloseTo(150)
  })

  it('reports tasks the model did not measure', () => {
    const prediction = predictChain([{name: 'a', share: .5}, {name: 'b', share: .5}], {...base, tasks: [{task: 'a', executions: 1, entryRequests: 1, services: {x: 1}}]}, 10)
    expect(prediction.unmeasured).toEqual(['b'])
    expect(modelRequestsPerExecution({task: 'a', executions: 0, entryRequests: 3})).toBeUndefined()
    expect(isProxy(undefined, 'istio-waypoint')).toBe(true)
  })
})
