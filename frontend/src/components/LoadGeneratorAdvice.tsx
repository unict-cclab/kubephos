import {useEffect, useRef, useState, type ReactNode} from 'react'
import './LoadGeneratorAdvice.css'

// Sizing guidance for the Locust load generator of a configured profile.
//
// - One Locust worker is one Python process on one CPU core; with HttpUser it
//   reliably sustains roughly this many requests per second before CPU
//   saturation inflates the measured latency.
// - Users have no think time and follow the observed latency (rate x latency x
//   1.5, Little's law), so the per-worker user cap bounds the latency up to which
//   the target rate can be held.
export const workerCapacityRps = 150
const userHeadroom = 1.5
const supportedLatencySeconds = 2

export interface GeneratorSettings {replicas: number; maxUsers: number; spawnRate: number; warmupSeconds: number}
export interface GeneratorAdvice {
  peakRps: number
  perWorkerPeakRps: number
  recommended: GeneratorSettings
  issues: Array<{field: keyof GeneratorSettings; message: string}>
}

const defaults: GeneratorSettings = {replicas: 1, maxUsers: 1000, spawnRate: 10, warmupSeconds: 0}
const round = (value: number) => Number(value.toFixed(value < 10 ? 1 : 0)).toLocaleString('en-US')

export function loadGeneratorAdvice(samples: Array<{seconds: number; rps: number}>, current: GeneratorSettings): GeneratorAdvice | undefined {
  let peak = 0
  let ramp = 0
  for (let index = 0; index < samples.length; index++) {
    peak = Math.max(peak, samples[index].rps)
    const previous = samples[index - 1]
    if (previous && samples[index].seconds > previous.seconds) ramp = Math.max(ramp, (samples[index].rps - previous.rps) / (samples[index].seconds - previous.seconds))
  }
  if (!(peak > 0)) return undefined
  const replicas = Math.max(1, current.replicas || 1)
  const workers = Math.max(1, Math.ceil(peak / workerCapacityRps - 1e-9))
  const perWorkerPeak = peak / replicas
  // The other settings are per worker: size them for the recommended worker count.
  const sized = Math.max(replicas, workers)
  const sizedPeak = peak / sized
  const initial = (samples[0]?.rps ?? 0) / sized
  const neededUsers = Math.ceil(sizedPeak * userHeadroom * supportedLatencySeconds)
  const maxUsers = Math.max(defaults.maxUsers, neededUsers)
  // Users track the rate, so they must start at least as fast as the steepest ramp
  // (with latency headroom) and reach the initial rate within about ten seconds.
  const spawnRate = Math.max(defaults.spawnRate, Math.ceil(Math.max(ramp / sized * userHeadroom, initial / 10)))
  const warmupSeconds = Math.max(60, Math.ceil(initial / Math.max(current.spawnRate, spawnRate)) + 30)
  const issues: GeneratorAdvice['issues'] = []
  if (replicas < workers) issues.push({field: 'replicas', message: `A peak of ${round(peak)} RPS needs about ${workers} load workers: with ${replicas}, each sends ${round(perWorkerPeak)} RPS, above the ~${workerCapacityRps} RPS one Locust worker sustains on a CPU core. A saturated generator inflates the measured latency.`})
  if (current.maxUsers < neededUsers) {
    const latency = current.maxUsers / (sizedPeak * userHeadroom)
    issues.push({field: 'maxUsers', message: `With ${current.maxUsers} users per worker the target rate holds only while the mean latency stays below ${round(latency)} s; ${maxUsers} keeps it up to ${supportedLatencySeconds} s.`})
  }
  if (current.spawnRate < spawnRate) issues.push({field: 'spawnRate', message: `Users start at ${current.spawnRate}/s, slower than the profile needs (${spawnRate}/s): the offered load would lag behind its ramps.`})
  if (current.warmupSeconds < 60) issues.push({field: 'warmupSeconds', message: `A warmup of at least ${warmupSeconds} s lets users start, caches warm up and the first rate window fill before measuring.`})
  return {peakRps: peak, perWorkerPeakRps: perWorkerPeak, recommended: {replicas: sized, maxUsers: Math.max(current.maxUsers, maxUsers), spawnRate: Math.max(current.spawnRate, spawnRate), warmupSeconds: Math.max(current.warmupSeconds, warmupSeconds)}, issues}
}

const fields: Array<{key: keyof GeneratorSettings; label: string}> = [
  {key: 'replicas', label: 'Load workers'},
  {key: 'maxUsers', label: 'Maximum users per worker'},
  {key: 'spawnRate', label: 'Users started per second'},
  {key: 'warmupSeconds', label: 'Warmup seconds'}
]

function readSettings(container: HTMLElement | null, prefix: string, values: Record<string, unknown>): GeneratorSettings {
  return Object.fromEntries(fields.map(({key}) => {
    const input = container?.querySelector<HTMLInputElement>(`[name="${prefix}.${key}"]`)
    const value = Number(input ? input.value : values[key])
    return [key, input?.value !== '' && Number.isFinite(value) ? value : defaults[key]]
  })) as unknown as GeneratorSettings
}

// Wraps the generator settings form and shows sizing advice for the profile.
export function LoadGeneratorSettings({prefix, samples, values = {}, children}: {prefix: string; samples: Array<{seconds: number; rps: number}>; values?: Record<string, unknown>; children: ReactNode}) {
  const container = useRef<HTMLDivElement>(null)
  const [current, setCurrent] = useState<GeneratorSettings>(() => readSettings(null, prefix, values))
  useEffect(() => setCurrent(readSettings(container.current, prefix, values)), [prefix])
  const advice = loadGeneratorAdvice(samples, current)
  const apply = () => {
    if (!advice) return
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set
    for (const {key} of fields) {
      const input = container.current?.querySelector<HTMLInputElement>(`[name="${prefix}.${key}"]`)
      if (!input || !setter || Number(input.value) >= advice.recommended[key]) continue
      setter.call(input, String(advice.recommended[key]))
      input.dispatchEvent(new Event('input', {bubbles: true}))
    }
    setCurrent(readSettings(container.current, prefix, values))
  }
  return <div ref={container} className="form-stack" onChange={() => setCurrent(readSettings(container.current, prefix, values))}>
    {advice && <section className={`generator-advice${advice.issues.length ? ' needs-attention' : ''}`} aria-label="Generator sizing">
      <h4>Generator sizing</h4>
      <p>Peak {round(advice.peakRps)} RPS · {round(advice.perWorkerPeakRps)} RPS per worker. Locust users follow the observed latency automatically; these settings bound how far they can.</p>
      <table className="generator-advice-table"><thead><tr><th scope="col">Setting</th><th scope="col">Current</th><th scope="col">Recommended</th></tr></thead>
        <tbody>{fields.map(({key, label}) => <tr key={key} className={advice.issues.some(issue => issue.field === key) ? 'needs-attention' : undefined}><th scope="row">{label}</th><td>{current[key].toLocaleString('en-US')}</td><td>{advice.recommended[key].toLocaleString('en-US')}</td></tr>)}</tbody></table>
      {advice.issues.length ? <><ul>{advice.issues.map(issue => <li key={issue.field}>{issue.message}</li>)}</ul><button type="button" className="button secondary compact" onClick={apply}>Apply recommended values</button></> : <p role="status">The generator settings can sustain this profile.</p>}
      <p className="field-help">Confirm on a short pilot run: in the results, the Load generator tab must show the achieved rate at least 95% of the offered rate, no late requests and no CPU saturation warning.</p>
    </section>}
    {children}
  </div>
}
