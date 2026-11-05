import {describe, expect, it} from 'vitest'
import {currentSection, isNavigableView, navigationTargets, visibleNavigation} from './navigation'

describe('visibleNavigation', () => {
  it('groups every page into four sections for administrators', () => {
    expect(visibleNavigation(true).map(section => [section.label, section.items.map(item => item.label)])).toEqual([
      ['Home', ['Home']],
      ['Platform', ['Clusters']],
      ['Lab', ['Library', 'Experiments']],
      ['Team', ['Members']]
    ])
  })

  it('keeps configurations and comparisons as steps of the experiments page', () => {
    const experiments = visibleNavigation(false).find(section => section.id === 'lab')!.items.find(item => item.view === 'results')!
    expect(experiments.views).toEqual(['results', 'experiments', 'comparisons'])
  })

  it('keeps steps and account pages out of the sidebar but reachable as targets', () => {
    expect(navigationTargets(true).map(item => item.label)).toEqual(expect.arrayContaining(['Configurations', 'Comparisons', 'Backup and transfer']))
    expect(navigationTargets(false).map(item => item.view)).not.toContain('administration')
  })

  it('hides administrator-only pages and empty sections from other users', () => {
    const sections = visibleNavigation(false)
    expect(sections.map(section => section.id)).toEqual(['home', 'platform', 'lab'])
    expect(sections[1].items.map(item => item.views)).toEqual([['kubernetes', 'deployments']])
  })
})

it('describes a page by its section and by the page that hosts it', () => {
  expect(currentSection('infrastructure')).toEqual({group: undefined, description: 'Provision infrastructure, create clusters and deploy applications'})
  expect(currentSection('catalog')).toEqual({group: 'Lab', description: 'Applications, strategies and AI models'})
  expect(currentSection('experiments')).toEqual(currentSection('results'))
  expect(currentSection('overview')).toEqual({group: undefined, description: 'Setup progress and recent activity'})
})

it('recognizes navigable views', () => {
  expect(isNavigableView('administration')).toBe(true)
  expect(isNavigableView('unknown')).toBe(false)
})
