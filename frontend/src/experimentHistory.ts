import {ApiError, request} from './api'
import type {Experiment} from './types'

export async function loadExperimentHistory(signal: AbortSignal): Promise<{items: Experiment[]}> {
  const items = new Map<string, Experiment>()
  let offset = 0
  for (;;) {
    const page = await request<{items: Experiment[]; nextOffset?: number | null}>(`/experiments?limit=200&offset=${offset}`, {}, undefined, signal)
    if (!page || !Array.isArray(page.items) || page.items.some(item => !item || typeof item.id !== 'string')) throw new ApiError(200, {code: 'invalid_response', message: 'Could not load experiment history.'})
    for (const item of page.items) items.set(item.id, item)
    if (page.nextOffset == null) return {items: [...items.values()]}
    if (!Number.isInteger(page.nextOffset) || page.nextOffset <= offset || page.nextOffset > 100000) throw new ApiError(200, {code: 'invalid_response', message: 'Could not load experiment history.'})
    offset = page.nextOffset
  }
}
