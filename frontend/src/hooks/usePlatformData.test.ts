import {act, renderHook} from '@testing-library/react'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {ApiError, request} from '../api'
import {usePlatformData} from './usePlatformData'
import type {Session} from '../types'

vi.mock('../api', async importOriginal => ({...await importOriginal<typeof import('../api')>(), request: vi.fn()}))
const session: Session = {authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'}}
const respond = async (path: string) => path === '/system' ? {status: 'healthy'} : path === '/plugin-runtime' ? null : {items: [], assignments: []}
const flush = async () => {await act(async () => {await Promise.resolve()})}

beforeEach(() => {vi.useFakeTimers(); vi.mocked(request).mockReset(); vi.mocked(request).mockImplementation(respond)})
afterEach(() => vi.useRealTimers())

describe('platform refresh', () => {
  it('polls live data while refreshing catalog and users only once a minute', async () => {
    const setSession = vi.fn(), notify = vi.fn()
    renderHook(() => usePlatformData(session, setSession, notify))
    await flush()
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/users')).toHaveLength(1)
    await act(async () => {await vi.advanceTimersByTimeAsync(5000)})
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/users')).toHaveLength(1)
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/operations')).toHaveLength(2)
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/connections')).toHaveLength(2)
    await act(async () => {await vi.advanceTimersByTimeAsync(55000)})
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/users')).toHaveLength(2)
  })

  it('does not start another refresh while an endpoint is slow', async () => {
    let resolve!: (value: unknown) => void
    vi.mocked(request).mockImplementation(path => path === '/system' ? new Promise(done => {resolve = done}) : respond(path))
    const setSession = vi.fn(), notify = vi.fn()
    renderHook(() => usePlatformData(session, setSession, notify))
    await flush()
    await act(async () => {await vi.advanceTimersByTimeAsync(15000)})
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/system')).toHaveLength(1)
    await act(async () => {resolve({status: 'healthy'})})
    expect(vi.mocked(request).mock.calls.filter(([path]) => path === '/system')).toHaveLength(1)
  })

  it('keeps successful endpoint data when another endpoint fails', async () => {
    vi.mocked(request).mockImplementation(async path => {
      if (path === '/users') throw new Error('Users unavailable')
      if (path === '/operations') return {items: [{id: 'op_new'}]}
      return respond(path)
    })
    const setSession = vi.fn(), notify = vi.fn()
    const {result} = renderHook(() => usePlatformData(session, setSession, notify))
    await flush()
    expect(result.current.data.operations).toEqual([{id: 'op_new'}])
    expect(result.current.connected).toBe(false)
  })

  it('clears the session on an unauthorized endpoint and aborts requests on unmount', async () => {
    const setSession = vi.fn(), notify = vi.fn()
    vi.mocked(request).mockImplementation(async path => {if (path === '/operations') throw new ApiError(401, {}); return respond(path)})
    const {unmount} = renderHook(() => usePlatformData(session, setSession, notify))
    await flush()
    expect(setSession).toHaveBeenCalledWith({authenticated: false, setupRequired: false})
    const signal = vi.mocked(request).mock.calls[0][3]!
    unmount()
    expect(signal.aborted).toBe(true)
  })
})

it('keeps the previous list when a refresh returns incomplete data', async () => {
  let malformed = false
  vi.mocked(request).mockImplementation(async path => path === '/operations' ? malformed ? {} : {items: [{id: 'op_saved'}]} : respond(path))
  const setSession = vi.fn(), notify = vi.fn()
  const {result} = renderHook(() => usePlatformData(session, setSession, notify))
  await flush()
  expect(result.current.data.operations).toEqual([{id: 'op_saved'}])
  malformed = true
  await act(async () => {await vi.advanceTimersByTimeAsync(5000)})
  expect(result.current.data.operations).toEqual([{id: 'op_saved'}])
  expect(result.current.connected).toBe(false)
})
