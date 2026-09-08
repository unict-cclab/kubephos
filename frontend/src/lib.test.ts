import {describe, expect, it} from 'vitest'
import {formatBytes, humanize, shortID} from './lib'

describe('display helpers', () => {
  it('formats identifiers and labels', () => {
    expect(shortID('op_12345678901234567890')).toBe('op_123456789012345…')
    expect(humanize('applicationRef')).toBe('Application Ref')
    expect(formatBytes(2048)).toBe('2.0 KB')
  })
})
