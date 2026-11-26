import {afterEach, describe, expect, it} from 'vitest'
import {applyChristmasSeason, isChristmasSeason} from '.'

afterEach(() => {delete document.documentElement.dataset.season; delete document.documentElement.dataset.seasonDay})

describe('Christmas season', () => {
  it('runs from 8 December to 5 January', () => {
    expect(isChristmasSeason(new Date(2026, 11, 7))).toBe(false)
    expect(isChristmasSeason(new Date(2026, 11, 8))).toBe(true)
    expect(isChristmasSeason(new Date(2026, 11, 31))).toBe(true)
    expect(isChristmasSeason(new Date(2027, 0, 5, 23, 59))).toBe(true)
    expect(isChristmasSeason(new Date(2027, 0, 6))).toBe(false)
  })

  it('marks the page in season, or when asked for with ?season=christmas', () => {
    applyChristmasSeason(new Date(2026, 10, 26), '')
    expect(document.documentElement.dataset.season).toBeUndefined()
    applyChristmasSeason(new Date(2026, 10, 26), '?season=christmas')
    expect(document.documentElement.dataset.season).toBe('christmas')
    expect(document.documentElement.dataset.seasonDay).toBeUndefined()
  })

  it('adds Santa on 25 December only, or when asked for with ?season=christmas-day', () => {
    applyChristmasSeason(new Date(2026, 11, 24), '')
    expect(document.documentElement.dataset.seasonDay).toBeUndefined()
    applyChristmasSeason(new Date(2026, 11, 25), '')
    expect(document.documentElement.dataset).toMatchObject({season: 'christmas', seasonDay: 'christmas-day'})
    delete document.documentElement.dataset.seasonDay
    applyChristmasSeason(new Date(2026, 10, 26), '?season=christmas-day')
    expect(document.documentElement.dataset.seasonDay).toBe('christmas-day')
  })
})
