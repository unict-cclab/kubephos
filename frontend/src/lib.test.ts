import {describe, expect, it} from 'vitest'
import {experimentDisplayStatus, formatBytes, humanize} from './lib'

describe('display helpers', () => {
  it.each(['queued', 'running'])('shows an active %s experiment as stopping only after a stop request', status => {
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
