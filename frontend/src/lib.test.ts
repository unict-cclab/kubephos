import {describe, expect, it} from 'vitest'
import {experimentDisplayStatus, formatBytes, formatRelativeTime, humanize} from './lib'

describe('formatRelativeTime', () => {
  const now = Date.parse('2026-11-05T12:00:00Z')

  it('describes recent and older moments in words', () => {
    expect(formatRelativeTime('2026-11-05T11:59:40Z', now)).toBe('just now')
    expect(formatRelativeTime('2026-11-05T11:55:00Z', now)).toBe('5 minutes ago')
    expect(formatRelativeTime('2026-11-04T12:00:00Z', now)).toBe('yesterday')
    expect(formatRelativeTime('2026-10-20T12:00:00Z', now)).toBe('2 weeks ago')
  })

  it('keeps invalid dates visible as a dash', () => {
    expect(formatRelativeTime('not a date', now)).toBe('—')
  })
})

describe('display helpers', () => {
  it.each(['queued', 'running', 'initializing', 'initialized'])('shows an active %s experiment as stopping only after a stop request', status => {
    expect(experimentDisplayStatus({status, cancelRequested: true})).toBe('stopping')
    expect(experimentDisplayStatus({status})).toBe(status)
  })

  it.each(['succeeded', 'failed', 'canceled'])('preserves the final %s status after a stop request', status => {
    expect(experimentDisplayStatus({status, cancelRequested: true})).toBe(status)
  })
  it('formats identifiers and labels', () => {
    expect(humanize('applicationRef')).toBe('Application Ref')
    expect(formatBytes(2048)).toBe('2.0 KB')
  })
})
