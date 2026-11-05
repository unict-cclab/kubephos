import {describe, expect, it} from 'vitest'
import {hashView, isNewExperimentRoute, parseDetailRoute} from './detailRoute'

it('recognizes the experiment launcher route', () => {
  expect(hashView('#results/new')).toBe('results')
  expect(isNewExperimentRoute('#results/new')).toBe(true)
  expect(isNewExperimentRoute('#results/instance/new')).toBe(false)
})

describe('detail routes', () => {
  it('keeps the parent view active on direct detail links', () => {
    expect(hashView('#kubernetes/resource/cluster_01')).toBe('kubernetes')
    expect(hashView('#experiments/instance/exp_01')).toBe('experiments')
    expect(hashView('#users')).toBe('users')
    expect(hashView('#administration')).toBe('administration')
    expect(hashView('#suites')).toBe('results')
    expect(hashView('#unknown')).toBe('overview')
  })

  it('decodes identifiers and rejects unrelated or malformed links', () => {
    expect(parseDetailRoute('#catalog/application/app%3Aonline-boutique', 'catalog')).toEqual({kind: 'application', id: 'app:online-boutique'})
    expect(parseDetailRoute('#kubernetes/resource/cluster_01', 'catalog')).toBeNull()
    expect(parseDetailRoute('#catalog/application/%ZZ', 'catalog')).toBeNull()
  })
})
