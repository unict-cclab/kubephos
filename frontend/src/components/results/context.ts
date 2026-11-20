import {isControlMetric, metricProvenance, type LoadedDataset} from '../../resultsData'
import type {Experiment, RunContext} from '../../types'
import {formatSeconds} from './format'
import {isPilot} from './runs'

export interface RunContextSummary {
  kubernetes: string[]
  istio: string[]
  prometheus: string[]
  nodes: NonNullable<RunContext['nodes']>
  concurrentLoad: Array<{run: string; namespaces: Array<{namespace: string; maxPods: number}>}>
  legacyClock: string[]
  withoutContext: string[]
  warmupSeconds: number[]
  drainSeconds: number[]
  workers: number[]
  rateWindowSeconds: number[]
  scrapeIntervalSeconds: number[]
  stepSeconds: number[]
  measuredSeconds: number[]
}

const unique = <T>(values: Array<T | undefined>): T[] => [...new Set(values.filter((value): value is T => value !== undefined && value !== null && value !== ''))]
const numbers = (values: Array<number | undefined>) => unique(values.filter(value => value === undefined || Number.isFinite(value))).sort((a, b) => a - b)

// Measured-window duration of one run: the Locust summary window, else workload start → end.
export function measuredSeconds(item: LoadedDataset): number | undefined {
  const summary = item.dataset.spec.locustSummary
  const from = Date.parse(summary?.measuredFrom ?? item.dataset.spec.workloadStartedAt ?? item.dataset.spec.start)
  const to = Date.parse(summary?.measuredTo ?? item.dataset.spec.end)
  return Number.isFinite(from) && Number.isFinite(to) && to > from ? Math.round((to - from) / 1000) : undefined
}

export function runContextSummary(datasets: LoadedDataset[]): RunContextSummary {
  const nodes = new Map<string, NonNullable<RunContext['nodes']>[number]>()
  for (const item of datasets) for (const node of item.dataset.spec.runContext?.nodes ?? []) if (!nodes.has(node.name)) nodes.set(node.name, node)
  return {
    kubernetes: unique(datasets.map(item => item.dataset.spec.runContext?.kubernetes?.serverVersion)),
    istio: unique(datasets.map(item => item.dataset.spec.runContext?.istio?.version)),
    prometheus: unique(datasets.map(item => item.dataset.spec.runContext?.prometheus?.version)),
    nodes: [...nodes.values()].sort((a, b) => a.name.localeCompare(b.name, undefined, {numeric: true})),
    concurrentLoad: datasets.flatMap(item => item.dataset.spec.runContext?.concurrentLoad?.length ? [{run: item.label, namespaces: item.dataset.spec.runContext.concurrentLoad}] : []),
    legacyClock: datasets.filter(item => item.dataset.spec.runContext && !item.dataset.spec.runContext.loadSession?.clock).map(item => item.label),
    withoutContext: datasets.filter(item => !item.dataset.spec.runContext).map(item => item.label),
    warmupSeconds: numbers(datasets.map(item => item.dataset.spec.runContext?.loadSession?.warmupSeconds)),
    drainSeconds: numbers(datasets.map(item => item.dataset.spec.runContext?.loadSession?.drainSeconds)),
    workers: numbers(datasets.map(item => item.dataset.spec.runContext?.loadSession?.workers ?? item.dataset.spec.locustSummary?.workers)),
    rateWindowSeconds: numbers(datasets.map(item => item.dataset.spec.rateWindowSeconds)),
    scrapeIntervalSeconds: numbers(datasets.map(item => item.dataset.spec.scrapeIntervalSeconds ?? item.dataset.spec.runContext?.prometheus?.scrapeIntervalSeconds)),
    stepSeconds: numbers(datasets.map(item => item.dataset.spec.stepSeconds)),
    measuredSeconds: numbers(datasets.map(measuredSeconds))
  }
}

export function concurrentLoadWarning(entry: RunContextSummary['concurrentLoad'][number]): string {
  return `Other KubePhos load generators were active in namespace${entry.namespaces.length === 1 ? '' : 's'} ${entry.namespaces.map(item => `${item.namespace} (up to ${item.maxPods} pod${item.maxPods === 1 ? '' : 's'})`).join(', ')} during ${entry.run}; shared-cluster interference is possible.`
}

export const legacyClockNote = 'The measured window of these runs has no recorded clock source (datasets collected before the shared-start clock): window boundaries were reconstructed from the collection time and can be off by up to one scrape interval.'

// "15 s", "15–30 s" (ranges when runs differ).
export function describeValues(values: number[], format = formatSeconds): string | undefined {
  if (!values.length) return undefined
  return values.length === 1 ? format(values[0]) : `${format(values[0])}–${format(values[values.length - 1])}`
}

interface LoadStep {type: string; durationSeconds: number; rps?: number; baselineRps?: number; amplitudeRps?: number; periodSeconds?: number; startRps?: number; endRps?: number}

function loadSteps(configuration: Record<string, unknown> | undefined): LoadStep[] {
  const components = Array.isArray(configuration?.components) ? configuration.components : []
  for (const value of components) {
    if (!value || typeof value !== 'object') continue
    const component = value as Record<string, unknown>
    if (!String(component.capability ?? '').startsWith('load.')) continue
    const settings = component.configuration as Record<string, unknown> | undefined
    const steps = Array.isArray(settings?.steps) ? settings.steps : []
    return steps.flatMap(step => step && typeof step === 'object' && Number((step as LoadStep).durationSeconds) > 0 ? [step as LoadStep] : [])
  }
  return []
}

function describeStep(step: LoadStep): string {
  const rate = (value?: number) => `${+Number(value ?? 0).toFixed(2)} req/s`
  const duration = formatSeconds(Number(step.durationSeconds))
  if (step.type === 'sinusoidal') return `a sinusoidal rate around ${rate(step.baselineRps)} (amplitude ${rate(step.amplitudeRps)}, period ${formatSeconds(Number(step.periodSeconds))}) for ${duration}`
  if (step.type === 'exponential') return `an exponential ramp from ${rate(step.startRps)} to ${rate(step.endRps)} over ${duration}`
  return `a constant ${rate(step.rps)} for ${duration}`
}

// Load profile for figure annotations: warm-up and the phases of the measured window.
export function loadProfileOf(configuration: Record<string, unknown> | undefined): {warmupSeconds: number; steps: Array<{label: string; durationSeconds: number}>} | undefined {
  const steps = loadSteps(configuration)
  if (!steps.length) return undefined
  const components = Array.isArray(configuration?.components) ? configuration.components as Array<Record<string, unknown>> : []
  const load = components.find(item => item && String(item.capability ?? '').startsWith('load.'))?.configuration as Record<string, unknown> | undefined
  const rate = (value?: number) => `${+Number(value ?? 0).toFixed(1)}`
  const label = (step: LoadStep) => step.type === 'sinusoidal' ? `sine ${rate(step.baselineRps)}±${rate(step.amplitudeRps)} req/s` : step.type === 'ramp' || step.type === 'exponential' ? `${step.type} ${rate(step.startRps)}→${rate(step.endRps)} req/s` : `${rate(step.rps)} req/s`
  return {warmupSeconds: Math.max(0, Number(load?.warmupSeconds) || 0), steps: steps.map(step => ({label: label(step), durationSeconds: Number(step.durationSeconds)}))}
}

export function describeLoadProfile(configuration: Record<string, unknown> | undefined): string | undefined {
  const steps = loadSteps(configuration)
  if (!steps.length) return undefined
  return steps.length === 1 ? describeStep(steps[0]) : `${steps.length} phases (${steps.map(describeStep).join('; then ')})`
}

export interface MethodsInput {
  experiment?: Experiment
  datasets: LoadedDataset[]
  metrics: string[]
  // Configuration per artifact (saved comparisons keep each source run's configuration).
  configurations?: Record<string, Record<string, unknown>>
  // Holm family size and comparison mode, when a comparison table is shown.
  comparison?: {familySize: number; mode: 'baseline' | 'all-pairs'; baseline?: string}
}

const list = (items: string[]) => items.length <= 1 ? items.join('') : `${items.slice(0, -1).join(', ')} and ${items[items.length - 1]}`
const plural = (count: number, word: string) => `${count} ${word}${count === 1 ? '' : 's'}`

// Methods paragraph generated from the data. Pilots must already be excluded from
// `experiment` and `datasets`; their count is read from `pilots`.
export function methodsParagraph(input: MethodsInput & {pilots?: number}): string {
  const {experiment, datasets, metrics} = input
  const sentences: string[] = []
  const present = new Set(datasets.map(item => item.artifact.id))
  const variants = (experiment?.variants ?? []).map(variant => ({name: variant.alias || variant.name, runs: variant.trials.filter(trial => !isPilot(trial) && present.has(trial.resultArtifactId)).length, configuration: variant.configuration, trials: variant.trials})).filter(variant => variant.runs > 0)
  if (variants.length > 1) sentences.push(`We compared ${variants.length} configurations (${variants.map(variant => `${variant.name}: ${plural(variant.runs, 'repetition')}`).join('; ')}).`)
  else if (variants.length === 1) sentences.push(`The configuration ${variants[0].name} was measured in ${plural(variants[0].runs, 'independent repetition')}.`)
  else sentences.push(`The analysis covers ${plural(datasets.length, 'run')}.`)
  if (input.pilots) sentences.push(`${plural(input.pilots, 'pilot repetition')} ${input.pilots === 1 ? 'was' : 'were'} executed first and excluded from all statistics and figures.`)
  const context = runContextSummary(datasets)
  const window: string[] = []
  const warmup = describeValues(context.warmupSeconds)
  if (warmup && context.warmupSeconds.some(value => value > 0)) window.push(`a ${warmup} warm-up excluded from analysis`)
  const measured = describeValues(context.measuredSeconds)
  if (measured) window.push(`a measured window of ${measured}`)
  if (window.length) sentences.push(`Each repetition consisted of ${list(window)}.`)
  const profiles = [...new Set((variants.length ? variants.flatMap(variant => {
    const own = describeLoadProfile(variant.configuration)
    if (own) return [own]
    return variant.trials.map(trial => describeLoadProfile(input.configurations?.[trial.resultArtifactId])).filter((value): value is string => Boolean(value))
  }) : []))]
  if (profiles.length === 1) sentences.push(`The load generator (Locust${describeValues(context.workers, value => String(value)) ? `, ${describeValues(context.workers, value => String(value))} worker${context.workers.length === 1 && context.workers[0] === 1 ? '' : 's'}` : ''}) applied ${profiles[0]}.`)
  else if (profiles.length > 1) sentences.push(`Load profiles differed between configurations: ${profiles.join(' / ')}.`)
  const collection: string[] = []
  const scrape = describeValues(context.scrapeIntervalSeconds)
  const rate = describeValues(context.rateWindowSeconds)
  const step = describeValues(context.stepSeconds)
  if (scrape) collection.push(`a scrape interval of ${scrape}`)
  if (rate) collection.push(`a ${rate} window for rate() and histogram_quantile()`)
  if (step) collection.push(`a query resolution of ${step}`)
  sentences.push(`Metrics were collected from Prometheus${collection.length ? ` with ${list(collection)}` : ''}.`)
  if (!context.rateWindowSeconds.length && datasets.length) sentences.push('Datasets without a recorded rate window used the collector default of 60 s.')
  const provenance = (metric: string) => datasets.find(item => item.dataset.spec.series.some(series => series.metric === metric))
  const percentiles = (prefix: string) => ['p50', 'p90', 'p95', 'p99'].filter(level => provenance(`${prefix}.${level}_response_time`)).map(level => level.toUpperCase())
  const client = percentiles('load')
  const server = percentiles('application')
  const exact = datasets.some(item => item.dataset.spec.locustSummary)
  if (client.length) {
    const clientMetric = `load.${client[0].toLowerCase()}_response_time`
    const origin = metricProvenance(provenance(clientMetric)!.dataset, clientMetric)
    sentences.push(`Client-side latency percentiles (${client.join(', ')}) were measured by the ${origin.source === 'locust' ? 'Locust load generator' : origin.source} and estimated per ${rate ?? '60 s'} window from histogram buckets${exact ? '; exact end-to-end percentiles over the whole measured window were additionally computed by merging the Locust workers\' response-time histograms' : ''}.`)
  }
  if (server.length) sentences.push(`Server-side latency percentiles (${server.join(', ')}) were reported by the Istio data plane at the destination workloads and estimated from histogram buckets.`)
  const temporal = metrics.some(metric => !isControlMetric(metric))
  const repeated = variants.some(variant => variant.runs >= 2)
  const statistics: string[] = []
  if (temporal) statistics.push('For every metric, each repetition contributes one value: the mean over its measured window')
  if (repeated) statistics.push('we report the mean of per-repetition means with a 95% confidence interval based on the exact Student t quantile with n − 1 degrees of freedom')
  if (statistics.length) sentences.push(`${statistics.join('; ')}.`)
  if (temporal) sentences.push('Within a single run, confidence intervals of the time-averaged mean account for autocorrelation through the AR(1) effective sample size n_eff = n(1 − ρ₁)/(1 + ρ₁).')
  const comparison = comparisonMethodsSentence(input.comparison)
  if (comparison) sentences.push(comparison)
  return sentences.join(' ')
}

export function comparisonMethodsSentence(comparison: MethodsInput['comparison']): string {
  if (!comparison || comparison.familySize <= 0) return ''
  return `Configurations were compared ${comparison.mode === 'all-pairs' ? 'pairwise' : `against ${comparison.baseline ?? 'the baseline'}`} on run-level values with Welch's two-sided t-test; p-values were adjusted with the Holm–Bonferroni procedure over a family of ${plural(comparison.familySize, 'test')}. Effect sizes are reported as Hedges' g with an approximate 95% confidence interval, and the Mann–Whitney U test is reported as a non-parametric check.`
}
