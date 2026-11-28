import {beforeEach, expect, it, vi} from 'vitest'
import {request} from './api'
import {loadExperimentHistory} from './experimentHistory'
import type {Experiment} from './types'
vi.mock('./api', async importOriginal => ({...await importOriginal<typeof import('./api')>(), request: vi.fn()}))
beforeEach(() => vi.mocked(request).mockReset())
const experiment = (id: string, status = 'queued') => ({id, status} as Experiment)
it('includes older completed runs after pages of scheduled runs and deduplicates overlapping pages', async () => {
  vi.mocked(request).mockResolvedValueOnce({items: [experiment('future')], nextOffset: 1}).mockResolvedValueOnce({items: [experiment('future'), experiment('completed', 'succeeded')], nextOffset: null})
  const signal = new AbortController().signal
  expect((await loadExperimentHistory(signal)).items).toEqual([experiment('future'), experiment('completed', 'succeeded')])
  expect(vi.mocked(request).mock.calls.map(([path]) => path)).toEqual(['/experiments?limit=200&offset=0', '/experiments?limit=200&offset=1'])
  expect(vi.mocked(request).mock.calls.every(call => call[3] === signal)).toBe(true)
})
it('rejects a failed later page instead of returning a silently truncated history', async () => {
  vi.mocked(request).mockResolvedValueOnce({items: [experiment('future')], nextOffset: 1}).mockRejectedValueOnce(new Error('Unavailable'))
  await expect(loadExperimentHistory(new AbortController().signal)).rejects.toThrow('Unavailable')
})
it.each([0, -1, 0.5, 100001])('rejects an invalid next offset %s without looping', async nextOffset => {
  vi.mocked(request).mockResolvedValue({items: [experiment('future')], nextOffset})
  await expect(loadExperimentHistory(new AbortController().signal)).rejects.toThrow('Could not load experiment history.')
  expect(request).toHaveBeenCalledTimes(1)
})
