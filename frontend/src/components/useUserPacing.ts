import {useEffect, useMemo, useState, type RefObject} from 'react'
import {loadScenarioPreview} from '../scenarioScripts'
import type {LocustParseResult} from '../locustParser'
import type {UserPacing} from '../virtualUsers'
import type {RequestMixContext} from './RequestMixEditor'

type Loaded = {result: LocustParseResult; pacing: (result: LocustParseResult, overrides: Record<string, number>) => UserPacing | undefined}

// Pacing of the scenario's plain Locust users with the request mix currently in
// the form. The Request mix tab keeps `${prefix}.taskWeights` up to date (empty
// for the Locust file's own mix); until that field exists the saved weights
// apply. The parser is loaded on demand, like the Request mix tab.
export function useUserPacing(context: RequestMixContext | undefined, prefix: string, saved: unknown, container: RefObject<HTMLElement | null>): UserPacing | undefined {
  const [loaded, setLoaded] = useState<Loaded>()
  const [weights, setWeights] = useState<Record<string, number>>(() => numericRecord(saved))
  useEffect(() => {
    setLoaded(undefined)
    if (!context) return
    let active = true
    Promise.all([loadScenarioPreview(context.applicationId), import('../locustParser'), import('../virtualUsers')]).then(([preview, parser, virtualUsers]) => {
      const script = preview.loadScenarios?.find(item => item.id === context.scenarioId)?.script
      if (active && script) setLoaded({result: parser.parseLocustfile(script), pacing: virtualUsers.userPacing})
    }, () => { /* Without the Locust file no equivalent users are shown. */ })
    return () => {active = false}
  }, [context?.applicationId, context?.scenarioId])
  useEffect(() => {
    const form = container.current?.closest('form')
    if (!form) return
    let timer: number | undefined
    const read = () => setWeights(formWeights(form, prefix, saved))
    // React rewrites the hidden field when it re-renders, after the input event.
    const schedule = () => {window.clearTimeout(timer); timer = window.setTimeout(read, 0)}
    read()
    form.addEventListener('input', schedule)
    form.addEventListener('change', schedule)
    return () => {window.clearTimeout(timer); form.removeEventListener('input', schedule); form.removeEventListener('change', schedule)}
  }, [prefix])
  return useMemo(() => loaded ? loaded.pacing(loaded.result, weights) : undefined, [loaded, weights])
}

// Plain Locust users that send `rps` (kept here, not in virtualUsers.ts, so the
// editor does not pull the parser into its bundle).
export function equivalentUsers(rps: number, pacing: UserPacing): number {
  return Math.round(Math.max(0, rps) * pacing.secondsPerRequest)
}

function formWeights(form: HTMLFormElement, prefix: string, saved: unknown): Record<string, number> {
  const field = form.elements.namedItem(`${prefix}.taskWeights`)
  if (!(field instanceof HTMLInputElement)) return numericRecord(saved)
  if (!field.value) return {}
  try { return numericRecord(JSON.parse(field.value)) } catch { return {} }
}

function numericRecord(value: unknown): Record<string, number> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, number] => typeof entry[1] === 'number' && Number.isFinite(entry[1]) && entry[1] >= 0))
}
