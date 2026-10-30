import {afterEach, describe, expect, it, vi} from 'vitest'
import {ApiError, apiErrorMessage, request} from './api'

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

describe('request', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('keeps aborts distinguishable from connection failures', async () => {
    const abort = new DOMException('Aborted', 'AbortError')
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(abort))
    await expect(request('/test')).rejects.toBe(abort)
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')))
    await expect(request('/test')).rejects.toMatchObject({status: 0, message: 'Cannot reach KubePhos. Check your connection and try again.'})
  })

  it('does not treat malformed success responses as empty saved data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>proxy</html>', {status: 200})))
    await expect(request('/test')).rejects.toMatchObject({status: 200, body: {code: 'invalid_response'}})
  })

  it.each([401, 403, 502])('handles non-JSON errors with status %i', async status => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('<html>proxy</html>', {status})))
    await expect(request('/test')).rejects.toBeInstanceOf(ApiError)
  })

  it('handles null error payloads without a client-side exception', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('null', {status: 503})))
    await expect(request('/test')).rejects.toMatchObject({status: 503, message: 'KubePhos is temporarily unavailable. Try again shortly.'})
  })

  it('preserves supplied cancellation signals and explicit CSRF headers', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, {status: 204}))
    vi.stubGlobal('fetch', fetch)
    const signal = new AbortController().signal
    await expect(request('/test', {method: 'POST', signal}, 'csrf-token')).resolves.toBeUndefined()
    expect(fetch.mock.calls[0][1].signal).toBe(signal)
    expect(fetch.mock.calls[0][1].headers.get('X-CSRF-Token')).toBe('csrf-token')
  })

  it('ignores malformed optional validation details', () => {
    expect(apiErrorMessage({validation: {issues: 'invalid'}})).toBe('The request could not be completed.')
    expect(apiErrorMessage({issues: [null, {message: 4}, {message: 'Required field.'}]})).toBe('Required field.')
  })
})
