// Call-chain prediction from a measured call model (dataset spec.callModel).
//
// Locust's rate R counts HTTP requests. A task t with share s_t (probability of
// being picked) sends r_t requests per execution, so tasks execute at
//   E = R / Σ_t s_t·r_t
// and a workload w receives E · Σ_t s_t · services_t[w] requests per second.
// When a model has no execution counts (older load generator) the per-request
// multipliers are used instead: R · Σ_t q_t · servicesPerRequest_t[w], with q_t the
// task's share of HTTP requests (s_t·r_t / Σ s·r).
import {request} from './api'
import type {CallModel, CallModelTask, Experiment, TimeSeriesDataset} from './types'

export interface MixTask {
  name: string
  share: number
  // Requests per execution read from the Locust file (static count).
  parsedRequests?: number
}

export interface ServicePrediction {
  service: string
  rps: number
  // Server-side requests per task execution, mix-weighted (Σ s_t·services_t).
  perExecution?: number
  proxy: boolean
}

export interface ChainPrediction {
  // Task executions per second.
  taskRate: number
  requestsPerExecution: Record<string, number>
  requestShare: Record<string, number>
  services: ServicePrediction[]
  edges: Array<{source: string; destination: string; rps: number}>
  // Tasks with a positive share that the model did not measure.
  unmeasured: string[]
  // Tasks whose requests per execution come from the Locust file, not the model.
  parsedOnly: string[]
}

export function modelRequestsPerExecution(task: CallModelTask | undefined): number | undefined {
  return task && task.executions && task.executions > 0 && task.entryRequests >= 0 ? task.entryRequests / task.executions : undefined
}

export function isProxy(model: Pick<CallModel, 'proxies'> | undefined, service: string): boolean {
  return Boolean(model?.proxies?.includes(service)) || /^(?:node-proxy|gateway-.*|waypoint|.*-waypoint)$/.test(service)
}

export function predictChain(tasks: MixTask[], model: CallModel, rate: number): ChainPrediction {
  const byTask = new Map(model.tasks.map(task => [task.task, task]))
  const requestsPerExecution: Record<string, number> = {}
  const parsedOnly: string[] = []
  const active = tasks.filter(task => task.share > 0)
  for (const task of active) {
    const measured = modelRequestsPerExecution(byTask.get(task.name))
    if (measured === undefined) parsedOnly.push(task.name)
    requestsPerExecution[task.name] = measured ?? Math.max(1, task.parsedRequests ?? 1)
  }
  const perMix = active.reduce((sum, task) => sum + task.share * requestsPerExecution[task.name], 0)
  const taskRate = perMix > 0 ? rate / perMix : 0
  const requestShare = Object.fromEntries(active.map(task => [task.name, perMix > 0 ? task.share * requestsPerExecution[task.name] / perMix : 0]))
  const services = new Map<string, {rps: number; perExecution: number; complete: boolean}>()
  const edges = new Map<string, {source: string; destination: string; rps: number}>()
  const unmeasured: string[] = []
  for (const task of active) {
    const measured = byTask.get(task.name)
    if (!measured || !measured.services && !measured.servicesPerRequest) { unmeasured.push(task.name); continue }
    const perExecution = measured.services
    for (const [service, value] of Object.entries(perExecution ?? measured.servicesPerRequest ?? {})) {
      const entry = services.get(service) ?? {rps: 0, perExecution: 0, complete: true}
      if (perExecution) {
        entry.rps += taskRate * task.share * value
        entry.perExecution += task.share * value
      } else {
        entry.rps += rate * requestShare[task.name] * value
        entry.perExecution += task.share * value * requestsPerExecution[task.name]
        entry.complete = false
      }
      services.set(service, entry)
    }
    for (const edge of measured.edges ?? []) {
      const value = edge.perExecution !== undefined ? taskRate * task.share * edge.perExecution : edge.perRequest !== undefined ? rate * requestShare[task.name] * edge.perRequest : undefined
      if (value === undefined) continue
      const key = `${edge.source}>${edge.destination}`
      const entry = edges.get(key) ?? {source: edge.source, destination: edge.destination, rps: 0}
      entry.rps += value
      edges.set(key, entry)
    }
  }
  return {
    taskRate, requestsPerExecution, requestShare, unmeasured, parsedOnly,
    services: [...services].map(([service, value]) => ({service, rps: value.rps, perExecution: value.perExecution, proxy: isProxy(model, service)})).sort((left, right) => right.rps - left.rps || left.service.localeCompare(right.service)),
    edges: [...edges.values()].sort((left, right) => right.rps - left.rps)
  }
}

export interface FoundCallModel {
  model: CallModel
  artifactId: string
  experimentName: string
  completedAt?: string
}

const cache = new Map<string, Promise<FoundCallModel | null>>()
const maxDownloads = 8

function loadConfiguration(configuration: Record<string, unknown> | undefined): Record<string, unknown> | undefined {
  const components = Array.isArray(configuration?.components) ? configuration.components as unknown[] : []
  for (const value of components) {
    if (!value || typeof value !== 'object') continue
    const component = value as Record<string, unknown>
    if (String(component.capability ?? '').startsWith('load.') && component.configuration && typeof component.configuration === 'object') return component.configuration as Record<string, unknown>
  }
  return undefined
}

// Most recent call model measured for this application and scenario among the
// experiments the user can see. Only runs configured with call-chain profiling are
// downloaded, newest first and at most a few, and the answer is cached per page.
export function findCallModel(applicationRef: string, scenarioId: string, refresh = false): Promise<FoundCallModel | null> {
  const key = `${applicationRef}\n${scenarioId}`
  if (!refresh && cache.has(key)) return cache.get(key)!
  const pending = (async () => {
    const experiments = (await request<{items?: Experiment[]}>('/experiments')).items ?? []
    const candidates = experiments.flatMap(experiment => experiment.variants.flatMap(variant => {
      const load = loadConfiguration(variant.configuration)
      if (load?.profileCallChain !== true || load.scenarioId !== undefined && load.scenarioId !== scenarioId) return []
      return variant.trials.filter(trial => trial.resultArtifactId && trial.status === 'succeeded').map(trial => ({artifactId: trial.resultArtifactId, experimentName: experiment.name, completedAt: trial.completedAt ?? experiment.updatedAt}))
    })).sort((left, right) => (right.completedAt ?? '').localeCompare(left.completedAt ?? ''))
    for (const candidate of candidates.slice(0, maxDownloads)) {
      let dataset: TimeSeriesDataset
      try {
        dataset = await request<TimeSeriesDataset>(`/artifacts/${encodeURIComponent(candidate.artifactId)}/download`)
      } catch {
        continue
      }
      const model = dataset?.spec?.callModel
      if (!model?.tasks?.length) continue
      if ((model.applicationRef || dataset.spec.applicationRef) !== applicationRef || model.scenarioId && model.scenarioId !== scenarioId) continue
      return {...candidate, model}
    }
    return null
  })()
  cache.set(key, pending)
  pending.catch(() => cache.delete(key))
  return pending
}

export function clearCallModelCache() {
  cache.clear()
}
