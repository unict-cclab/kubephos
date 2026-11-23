import type {LoadedDataset} from '../../resultsData'
import {measuredSeconds} from './context'
import {formatSignificant, formatSpan} from './display'
import {fidelityThresholds, loadFidelity} from './loadFidelity'

// Key facts of one execution, above its summary: when the measured window ran, how long,
// the warm-up and drain around it, the load generator and offered vs achieved load.

const dateTime = (value: number) => new Date(value).toLocaleString('en-US', {month: 'short', day: 'numeric', year: 'numeric', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false})
const timeOnly = (value: number) => new Date(value).toLocaleTimeString('en-US', {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false})
const sameDay = (left: number, right: number) => new Date(left).toDateString() === new Date(right).toDateString()
const seconds = (value: number) => value >= 120 && value % 60 === 0 ? `${value / 60} min` : `${value} s`

export interface RunFact {label: string; value: string; detail?: string; tone?: 'critical'}

export function runFacts(item: LoadedDataset): RunFact[] {
  const spec = item.dataset.spec
  const summary = spec.locustSummary
  const session = spec.runContext?.loadSession
  const from = Date.parse(summary?.measuredFrom ?? spec.workloadStartedAt ?? spec.start)
  const to = Date.parse(summary?.measuredTo ?? spec.end)
  const facts: RunFact[] = []
  if (Number.isFinite(from)) facts.push({label: 'Started', value: dateTime(from)})
  if (Number.isFinite(to)) facts.push({label: 'Ended', value: Number.isFinite(from) && sameDay(from, to) ? timeOnly(to) : dateTime(to)})
  const measured = measuredSeconds(item)
  if (measured !== undefined) facts.push({label: 'Measured window', value: formatSpan(measured * 1000)})
  if (session?.warmupSeconds !== undefined || session?.drainSeconds !== undefined) facts.push({label: 'Warm-up / drain', value: `${session?.warmupSeconds !== undefined ? seconds(session.warmupSeconds) : '—'} / ${session?.drainSeconds !== undefined ? seconds(session.drainSeconds) : '—'}`})
  const workers = session?.workers ?? summary?.workers
  if (workers !== undefined) facts.push({label: 'Load generator', value: `${workers} Locust worker${workers === 1 ? '' : 's'}`})
  const fidelity = loadFidelity(item)
  if (fidelity.offeredMean !== undefined && fidelity.achievedMean !== undefined) {
    const short = fidelity.ratio !== undefined && fidelity.ratio < fidelityThresholds.achievedRatio
    facts.push({label: fidelity.offeredSource === 'configured' ? 'Configured → achieved' : 'Offered → achieved', value: `${formatSignificant(fidelity.offeredMean)} → ${formatSignificant(fidelity.achievedMean)} req/s`, detail: fidelity.ratio === undefined ? undefined : `${formatSignificant(fidelity.ratio * 100)} % achieved`, tone: short ? 'critical' : undefined})
  }
  return facts
}

export function RunFacts({item, name}: {item: LoadedDataset; name?: string}) {
  const facts = runFacts(item)
  if (!facts.length) return null
  return <section className="run-facts" aria-label={name ? `Execution facts: ${name}` : 'Execution facts'}>
    <dl>
      {facts.map(fact => <div key={fact.label} className={fact.tone ? `tone-${fact.tone}` : undefined}>
        <dt>{fact.label}</dt>
        <dd>{fact.value}{fact.detail && <small>{fact.detail}{fact.tone === 'critical' && <span className="visually-hidden"> (below the {fidelityThresholds.achievedRatio * 100} % threshold)</span>}</small>}</dd>
      </div>)}
    </dl>
  </section>
}
