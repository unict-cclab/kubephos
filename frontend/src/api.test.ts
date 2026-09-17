import {describe, expect, it} from 'vitest'
import {ApiError, apiErrorMessage} from './api'

describe('apiErrorMessage', () => {
  it('shows validation details when the API has no top-level message', () => {
    const body = {code: 'preflight_failed', validation: {issues: [{message: 'Kubernetes API is unreachable.'}, {message: 'Ownership validation failed.'}]}}
    expect(apiErrorMessage(body)).toBe('Kubernetes API is unreachable. Ownership validation failed.')
    expect(new ApiError(422, body).message).toBe('Kubernetes API is unreachable. Ownership validation failed.')
  })

  it('prefers the top-level API message', () => {
    expect(apiErrorMessage({message: 'Cluster is in use.'})).toBe('Cluster is in use.')
  })
})
