import {legacyMetricProvenance, plotLabel, type MetricProvenance} from '../../resultsData'

export function formatAxis(value: number): string {
  if (Math.abs(value) < 1) return Number(value.toPrecision(3)).toString()
  return value >= 100 ? value.toFixed(0) : value >= 10 ? value.toFixed(1).replace(/\.0$/, '') : value.toFixed(2).replace(/\.0+$/, '').replace(/(\.\d)0$/, '$1')
}

export function formatAxisValue(value: number, unit: string): string {
  if (unit === 'bytes' || unit === 'bytes_per_second') return formatAxis(value / 1024 ** 2)
  return formatAxis(value)
}

export function formatMetric(value: number | undefined, unit: string): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (unit === 'bytes') {
    const divisor = Math.abs(value) >= 1024 ** 3 ? 1024 ** 3 : Math.abs(value) >= 1024 ** 2 ? 1024 ** 2 : Math.abs(value) >= 1024 ? 1024 : 1
    const suffix = divisor === 1024 ** 3 ? 'GiB' : divisor === 1024 ** 2 ? 'MiB' : divisor === 1024 ? 'KiB' : 'B'
    return `${(value / divisor).toFixed(divisor === 1 ? 0 : 1)} ${suffix}`
  }
  if (unit === 'cores') return Math.abs(value) < 1 ? `${(value * 1000).toFixed(0)} mCPU` : `${value.toFixed(2)} cores`
  if (unit === 'requests_per_second') return `${value.toFixed(2)} req/s`
  if (unit === 'milliseconds') return `${value.toFixed(1)} ms`
  if (unit === 'percent') return `${value.toFixed(2)}%`
  if (unit === 'seconds') return `${value.toFixed(3)} s`
  if (unit === 'bytes_per_second') return `${formatMetric(value, 'bytes')}/s`
  return value.toLocaleString('en-US', {maximumFractionDigits: 2})
}

// Signed difference in the metric's unit.
export function formatDifference(value: number | undefined, unit: string): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  return `${value > 0 ? '+' : value < 0 ? '−' : ''}${formatMetric(Math.abs(value), unit)}`
}

export function formatInterval(low: number | undefined, high: number | undefined, unit: string): string {
  if (low === undefined || high === undefined || !Number.isFinite(low) || !Number.isFinite(high)) return 'n/a'
  return `${formatSignedPlain(low, unit)} to ${formatSignedPlain(high, unit)}`
}

function formatSignedPlain(value: number, unit: string): string {
  return value < 0 ? `−${formatMetric(-value, unit)}` : formatMetric(value, unit)
}

export function formatPValue(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return 'n/a'
  if (value < .001) return '< 0.001'
  return value.toFixed(3)
}

export function formatNumber(value: number | undefined, digits = 2): string {
  if (value === undefined || !Number.isFinite(value)) return 'n/a'
  return value.toLocaleString('en-US', {minimumFractionDigits: digits, maximumFractionDigits: digits}).replace('-', '−')
}

// Cohen's conventional labels for |g|.
export function effectMagnitude(value: number | undefined): string {
  if (value === undefined || !Number.isFinite(value)) return 'n/a'
  const absolute = Math.abs(value)
  if (absolute < .2) return 'Negligible'
  if (absolute < .5) return 'Small'
  if (absolute < .8) return 'Medium'
  return 'Large'
}

export function formatDuration(milliseconds: number): string {
  const seconds = Math.max(0, Math.round(milliseconds / 1000))
  if (seconds >= 3600) return `${(seconds / 3600).toFixed(1)}h`
  if (seconds >= 60) return `${Math.round(seconds / 60)}m`
  return `${seconds}s`
}

export function formatSeconds(seconds: number): string {
  if (!Number.isFinite(seconds)) return '—'
  if (seconds >= 3600 && seconds % 60 === 0) return `${+(seconds / 3600).toFixed(2)} h`
  if (seconds >= 120 && seconds % 60 === 0) return `${seconds / 60} min`
  return `${+seconds.toFixed(1)} s`
}

export function compactLabel(value: string): string {
  const label = plotLabel(value)
  return label.length <= 34 ? label : `${label.slice(0, 31)}...`
}

const sourceNames: Record<string, string> = {locust: 'Locust', istio: 'Istio', cadvisor: 'cAdvisor', 'kube-state-metrics': 'kube-state-metrics', mentat: 'Mentat', cilium: 'Cilium', kubephos: 'KubePhos', 'kube-scheduler': 'kube-scheduler', descheduler: 'Descheduler', unknown: 'Unknown source'}
const vantageNames: Record<string, string> = {client: 'client side', server: 'server side', configuration: 'configured'}

export function sourceName(source: string): string {
  return sourceNames[source] ?? source
}

// Compact badge text: "Locust · client side", "Istio · server side", "cAdvisor".
export function provenanceLabel(provenance: Pick<MetricProvenance, 'source' | 'vantage'>): string {
  const vantage = vantageNames[provenance.vantage]
  return vantage ? `${sourceName(provenance.source)} · ${vantage}` : sourceName(provenance.source)
}

// One-sentence provenance for captions, CSV and figure metadata.
export function provenanceSentence(metric: string, provenance: MetricProvenance): string {
  const parts = [`${metricTitle(metric, provenance)}: ${provenance.description || 'no description recorded'}`, `source ${provenanceLabel(provenance)}`]
  if (provenance.collector) parts.push(`collected via ${provenance.collector}`)
  if (provenance.rateWindowSeconds) parts.push(`rate window ${provenance.rateWindowSeconds} s`)
  if (provenance.query) parts.push(`query ${provenance.query}`)
  if (provenance.legacy) parts.push('provenance inferred from the metric catalog (dataset predates recorded provenance)')
  return `${parts.join('; ')}.`
}

const latencyStatistic: Record<string, string> = {p50: 'P50', p90: 'P90', p95: 'P95', p99: 'P99', mean: 'Mean'}

// "P95 latency — client (Locust)" vs "P95 latency — server (Istio)".
export function metricTitle(metric: string, provenance?: Pick<MetricProvenance, 'source' | 'vantage'>): string {
  const latency = metric.match(/^(load|application)\.(p50|p90|p95|p99|mean)_response_time$/)
  if (latency) {
    const origin = provenance ?? legacyMetricProvenance(metric)
    const vantage = origin.vantage === 'client' || origin.vantage === 'server' ? origin.vantage : origin.vantage.replace('-', ' ')
    return `${latencyStatistic[latency[2]]} latency — ${vantage} (${sourceName(origin.source)})`
  }
  const label = metricNames[metric]
  if (label) return label
  const acronyms: Record<string, string> = {cpu: 'CPU', rps: 'request rate', rtt: 'round-trip time', hpa: 'HPA', cpa: 'CPA', p95: 'P95', p99: 'P99'}
  return metric.split(/[._]/).filter(Boolean).map((word, index) => acronyms[word.toLowerCase()] ?? (index === 0 ? word.slice(0, 1).toUpperCase() + word.slice(1) : word)).join(' ')
}

const metricNames: Record<string, string> = {
  'load.configured_rps': 'Configured input rate',
  'load.offered_rps': 'Offered request rate',
  'load.achieved_rps': 'Achieved request rate',
  'load.schedule_lag': 'Schedule lag',
  'load.users': 'Concurrent users',
  'load.successful_rps': 'Throughput',
  'load.failed_rps': 'Failed request rate',
  'load.failure_percentage': 'Failure percentage',
  'load.slo_violation_percentage': 'SLO violation windows',
  'redis.operation_rps': 'Throughput per Redis operation',
  'redis.operation_p95_response_time': 'P95 per Redis operation',
  'application.request_rps': 'Internal request rate (server side)',
  'ingress.request_rps': 'Ingress request rate',
  'cpu.cores': 'CPU usage',
  'memory.working_set': 'Memory working set',
  'scheduler.pod_scheduling_latency': 'Scheduling latency',
  'scheduler.cycle_duration': 'Scheduling cycle duration',
  'descheduler.cycle_duration': 'Descheduling cycle duration',
  'descheduler.evictions': 'Descheduler evictions',
  'pod.restarts': 'Pod restarts during the run',
  'deployment.ready_replicas': 'Ready replicas',
  'network.rtt': 'Network RTT',
  'network.packet_loss': 'Packet loss',
  'network.bandwidth': 'Network bandwidth'
}

export function unitLabel(unit: string): string {
  return ({milliseconds: 'ms', requests_per_second: 'req/s', percent: '%', cores: 'cores', bytes: 'MiB', bytes_per_second: 'MiB/s', replicas: 'replicas', seconds: 's', count: 'count'} as Record<string, string>)[unit] ?? unit.replaceAll('_', ' ')
}

export function metricAxisLabel(metric: string, unit: string, provenance?: Pick<MetricProvenance, 'source' | 'vantage'>): string {
  const labels: Record<string, string> = {
    'load.configured_rps': 'Input rate (req/s)',
    'load.offered_rps': 'Offered rate (req/s)',
    'load.achieved_rps': 'Achieved rate (req/s)',
    'load.successful_rps': 'Successful requests (req/s)',
    'load.failed_rps': 'Failed requests (req/s)',
    'load.failure_percentage': 'Failed requests (%)',
    'load.slo_violation_percentage': 'SLO violation windows (%)',
    'application.request_rps': 'Service calls (req/s)',
    'ingress.request_rps': 'Ingress requests (req/s)',
    'cpu.cores': 'CPU usage (cores)',
    'memory.working_set': 'Working set (MiB)',
    'pod.restarts': 'Restarts since workload start',
    'deployment.ready_replicas': 'Replica count',
    'network.rtt': 'Network RTT (ms)',
    'network.packet_loss': 'Packet loss (%)',
    'network.bandwidth': 'Bandwidth (MiB/s)'
  }
  const latency = metric.match(/^(load|application)\.(p50|p90|p95|p99|mean)_response_time$/)
  if (latency) {
    const origin = provenance ?? legacyMetricProvenance(metric)
    return `${latencyStatistic[latency[2]]} latency, ${origin.vantage === 'server' ? 'server' : origin.vantage === 'client' ? 'client' : origin.vantage} (ms)`
  }
  return labels[metric] ?? `${metricTitle(metric)}${unit ? ` (${unitLabel(unit)})` : ''}`
}

export function groupMetrics(metrics: string[]): Map<string, string[]> {
  const values = new Map<string, string[]>()
  for (const metric of metrics) {
    const group = metric.startsWith('load.') ? 'Load' : metric.startsWith('application.') ? 'Application' : metric.startsWith('redis.') ? 'Redis' : metric.startsWith('network.') ? 'Network' : ['cpu', 'memory'].includes(metric.split('.')[0]) ? 'Resources' : 'Kubernetes'
    values.set(group, [...(values.get(group) ?? []), metric])
  }
  return new Map(['Load', 'Application', 'Redis', 'Kubernetes', 'Resources', 'Network'].flatMap(group => values.has(group) ? [[group, values.get(group)!] as [string, string[]]] : []))
}

export function controlMetricDescription(metric: string, scope: string, reason?: string, resolution?: number): string {
  const description = metric === 'scheduler.pod_scheduling_latency' ? 'Time from pod creation to node assignment; includes queueing and scheduling.' : metric === 'scheduler.cycle_duration' ? 'Time spent in the scheduler scheduling algorithm per attempt; excludes queueing and pod startup.' : metric === 'descheduler.cycle_duration' ? 'Time spent in a descheduler loop.' : 'Successful application pod evictions.'
  return [description, scope === 'cluster' ? 'Scope: entire cluster.' : 'Scope: experiment.', resolution ? `Timestamp resolution: ${resolution} s.` : '', reason ?? ''].filter(Boolean).join(' ')
}

// RFC 4180 CSV.
export function toCSV(rows: Array<Array<string | number | boolean | undefined>>): string {
  return rows.map(row => row.map(value => {const text = value === undefined || (typeof value === 'number' && !Number.isFinite(value)) ? '' : String(value); return /[",\r\n]/.test(text) ? `"${text.replaceAll('"', '""')}"` : text}).join(',')).join('\n')
}

// Statistics tables: one display unit and one precision per row, chosen from the row's
// typical value, so the unit is written once (Unit column) and every number of the row
// has the same number of decimals ("157–216" instead of "156.7 ms–216.4 ms").
export interface RowScale {unit: string; factor: number; digits: number}

export function rowScale(unit: string, reference: number | undefined): RowScale {
  const magnitude = reference === undefined || !Number.isFinite(reference) ? 0 : Math.abs(reference)
  let label = unitLabel(unit)
  let factor = 1
  if (unit === 'milliseconds' && magnitude >= 10000) {label = 's'; factor = 1 / 1000}
  else if (unit === 'seconds' && magnitude > 0 && magnitude < 1) {label = 'ms'; factor = 1000}
  else if (unit === 'cores' && magnitude < 1) {label = 'mCPU'; factor = 1000}
  else if (unit === 'bytes' || unit === 'bytes_per_second') {
    const steps: Array<[number, string]> = [[1024 ** 3, 'GiB'], [1024 ** 2, 'MiB'], [1024, 'KiB'], [1, 'B']]
    const [divisor, name] = steps.find(([size]) => magnitude >= size) ?? [1, 'B']
    label = unit === 'bytes' ? name : `${name}/s`
    factor = 1 / divisor
  } else if (unit === 'count') label = ''
  const scaled = magnitude * factor
  const integral = unit === 'count' || unit === 'replicas'
  const digits = scaled >= 100 || (integral && (scaled >= 10 || Number.isInteger(scaled))) ? 0 : scaled >= 10 ? 1 : scaled >= 1 || scaled === 0 ? 2 : 3
  return {unit: label, factor, digits}
}

export function scaledValue(value: number | undefined, scale: RowScale): string {
  if (value === undefined || !Number.isFinite(value)) return '—'
  const text = Math.abs(value * scale.factor).toLocaleString('en-US', {minimumFractionDigits: scale.digits, maximumFractionDigits: scale.digits})
  return value * scale.factor < 0 && Number(text.replace(/,/g, '')) !== 0 ? `−${text}` : text
}

export function scaledInterval(low: number | undefined, high: number | undefined, scale: RowScale): string {
  if (low === undefined || high === undefined || !Number.isFinite(low) || !Number.isFinite(high)) return 'n/a'
  return `${scaledValue(low, scale)}–${scaledValue(high, scale)}`
}
