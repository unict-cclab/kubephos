import {ReadoutTable, type ReadoutTableData} from './ReadoutTable'
import type {LoadProfileStep} from './LoadProfileEditor'

export function WorkloadReadout({phases}: {phases: LoadProfileStep[]}) {
  return <ReadoutTable data={workloadTable(phases)} label="Workload phases" />
}

export function TrafficDistributionReadout({phases, clusterMode = false}: {phases: Record<string, unknown>[]; clusterMode?: boolean}) {
  return <ReadoutTable data={trafficTable(phases, clusterMode)} label={clusterMode ? 'Traffic phases by cluster' : 'Traffic phases by zone'} />
}

function intervals(phases: {durationSeconds?: unknown}[]): string[] {
  let elapsed = 0
  return phases.map(phase => {
    const duration = Number(phase.durationSeconds)
    const start = elapsed
    elapsed += Number.isFinite(duration) && duration > 0 ? duration : 0
    return Number.isFinite(duration) && duration > 0 ? `${minutes(start)}–${minutes(elapsed)} min` : '—'
  })
}
function minutes(seconds: number) {return Number((seconds / 60).toFixed(2)).toLocaleString('en-US')}

export function workloadTable(phases: LoadProfileStep[]): ReadoutTableData {
  const fields: Array<{title: string; show: boolean; value: (phase: LoadProfileStep) => string}> = [
    {title: 'Rate (req/s)', show: phases.some(phase => phase.type === 'constant'), value: (phase: LoadProfileStep) => phase.type === 'constant' ? String(phase.rps) : '—'},
    {title: 'Baseline (req/s)', show: phases.some(phase => phase.type === 'sinusoidal'), value: (phase: LoadProfileStep) => phase.type === 'sinusoidal' ? String(phase.baselineRps) : '—'},
    {title: 'Amplitude (req/s)', show: phases.some(phase => phase.type === 'sinusoidal'), value: (phase: LoadProfileStep) => phase.type === 'sinusoidal' ? String(phase.amplitudeRps) : '—'},
    {title: 'Period (min)', show: phases.some(phase => phase.type === 'sinusoidal'), value: (phase: LoadProfileStep) => phase.type === 'sinusoidal' ? minutes(phase.periodSeconds) : '—'},
    {title: 'Phase offset (min)', show: phases.some(phase => phase.type === 'sinusoidal'), value: (phase: LoadProfileStep) => phase.type === 'sinusoidal' ? minutes(phase.phaseSeconds) : '—'},
    {title: 'Start (req/s)', show: phases.some(phase => phase.type === 'exponential'), value: (phase: LoadProfileStep) => phase.type === 'exponential' ? String(phase.startRps) : '—'},
    {title: 'End (req/s)', show: phases.some(phase => phase.type === 'exponential'), value: (phase: LoadProfileStep) => phase.type === 'exponential' ? String(phase.endRps) : '—'},
    {title: 'Curve', show: phases.some(phase => phase.type === 'exponential'), value: (phase: LoadProfileStep) => phase.type === 'exponential' ? String(phase.curve) : '—'}
  ].filter(field => field.show)
  const ranges = intervals(phases)
  return {headers: ['Phase', 'Time interval', 'Load', ...fields.map(field => field.title)], rows: phases.map((phase, index) => [`Phase ${index + 1}`, ranges[index], phase.type === 'sinusoidal' ? 'Sinusoidal' : phase.type === 'exponential' ? 'Exponential' : 'Constant', ...fields.map(field => field.value(phase))])}
}

export function trafficTable(phases: Record<string, unknown>[], clusterMode = false): ReadoutTableData {
  const zones = [...new Set(phases.flatMap(phase => Object.keys({...weights(phase.weights), ...weights(phase.startWeights), ...weights(phase.endWeights)})))].sort((a, b) => a.localeCompare(b, undefined, {numeric: true}))
  const ranges = intervals(phases)
  return {headers: ['Phase', 'Time interval', 'Distribution', ...zones.map(zone => `${zone} (%)`)], rows: phases.map((phase, index) => {
    const transition = phase.type === 'linear'
    const start = weights(transition ? phase.startWeights : phase.weights)
    const end = weights(phase.endWeights)
    return [`Phase ${index + 1}`, ranges[index], transition ? 'Linear transition (start → end)' : `Fixed ${clusterMode ? 'cluster' : 'zone'} shares`, ...zones.map(zone => transition ? `${share(start, zone)} → ${share(end, zone)}` : share(start, zone))]
  })}
}
function weights(value: unknown): Record<string, number> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return {}
  return Object.fromEntries(Object.entries(value).filter(([, weight]) => typeof weight === 'number' && Number.isFinite(weight) && weight >= 0))
}
function share(values: Record<string, number>, zone: string): string {
  const total = Object.values(values).reduce((sum, value) => sum + value, 0)
  return total > 0 ? `${Number(((values[zone] ?? 0) / total * 100).toFixed(2))}` : '—'
}
