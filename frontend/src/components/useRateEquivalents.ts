import {useEffect, useMemo, useState, type RefObject} from 'react'
import {loadScenarioPreview} from '../scenarioScripts'
import type {LocustParseResult} from '../locustParser'
import type {RateEquivalents, Redirect} from '../rateEquivalents'

export interface ScenarioContext {applicationId: string; scenarioId: string; redirects?: Redirect[]}

type Loaded = {result: LocustParseResult; compute: typeof import('../rateEquivalents').rateEquivalents}

// Virtual users and server-side rate of the scenario with the given request mix.
// The Locust file and its parser are loaded on demand, like the Request mix tab.
export function useRateEquivalents(context: ScenarioContext | undefined, weights: Record<string, number>): RateEquivalents | undefined {
  const [loaded, setLoaded] = useState<Loaded>()
  useEffect(() => {
    setLoaded(undefined)
    if (!context) return
    let active = true
    Promise.all([loadScenarioPreview(context.applicationId), import('../locustParser'), import('../rateEquivalents')]).then(([preview, parser, equivalents]) => {
      const script = preview.loadScenarios?.find(item => item.id === context.scenarioId)?.script
      if (active && script) setLoaded({result: parser.parseLocustfile(script), compute: equivalents.rateEquivalents})
    }, () => { /* Without the Locust file no equivalents are shown. */ })
    return () => {active = false}
  }, [context?.applicationId, context?.scenarioId])
  const redirects = context?.redirects
  return useMemo(() => loaded ? loaded.compute(loaded.result, weights, redirects) : undefined, [loaded, weights, redirects])
}

// The request mix currently in the form: the Request mix tab keeps
// `${prefix}.taskWeights` up to date (empty for the Locust file's own mix);
// until that field exists the saved weights apply.
export function useFormTaskWeights(prefix: string, saved: unknown, container: RefObject<HTMLElement | null>): Record<string, number> {
  const [weights, setWeights] = useState<Record<string, number>>(() => taskWeights(saved))
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
  return weights
}

// "≈ 1,363 virtual users · ≈ 365 req/s server side" for a Locust rate, or
// undefined when nothing is known. Kept here, not in rateEquivalents.ts, so
// callers do not pull the parser into their bundle.
export function rateEquivalentsText(rps: number, equivalents: RateEquivalents | undefined): string | undefined {
  const rate = Math.max(0, rps)
  const parts = [
    equivalents?.secondsPerRequest !== undefined && `≈ ${Math.round(rate * equivalents.secondsPerRequest).toLocaleString('en-US')} virtual users`,
    equivalents?.serverRequestsPerRequest !== undefined && `≈ ${Math.round(rate * equivalents.serverRequestsPerRequest).toLocaleString('en-US')} req/s server side`
  ].filter(Boolean)
  return parts.length ? parts.join(' · ') : undefined
}

// The redirects declared on the endpoint a scenario sends its load to.
export function scenarioRedirects(descriptor: {spec?: {interface?: {endpoints?: Array<{id?: string; redirects?: Redirect[]}>; loadScenarios?: Array<{id?: string; targetEndpoint?: string}>}}} | undefined, scenarioId: string): Redirect[] {
  const target = descriptor?.spec?.interface?.loadScenarios?.find(scenario => scenario.id === scenarioId)?.targetEndpoint
  return descriptor?.spec?.interface?.endpoints?.find(endpoint => endpoint.id === target)?.redirects ?? []
}

function formWeights(form: HTMLFormElement, prefix: string, saved: unknown): Record<string, number> {
  const field = form.elements.namedItem(`${prefix}.taskWeights`)
  if (!(field instanceof HTMLInputElement)) return taskWeights(saved)
  if (!field.value) return {}
  try { return taskWeights(JSON.parse(field.value)) } catch { return {} }
}

export function taskWeights(value: unknown): Record<string, number> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter((entry): entry is [string, number] => typeof entry[1] === 'number' && Number.isFinite(entry[1]) && entry[1] >= 0))
}
