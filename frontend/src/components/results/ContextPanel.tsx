import {useState} from 'react'
import {copyText} from '../../clipboard'
import {ResponsiveTable} from '../ResponsiveTable'
import {concurrentLoadWarning, describeValues, legacyClockNote, type RunContextSummary} from './context'

function Fact({label, value}: {label: string; value?: string}) {
  return value ? <div><dt>{label}</dt><dd>{value}</dd></div> : null
}

export function RunContextPanel({context}: {context: RunContextSummary}) {
  const versions = (values: string[]) => values.length ? values.join(', ') : undefined
  return <section className="statistics-card run-context" aria-label="Run context">
    <h3>Run context</h3>
    {context.concurrentLoad.map(entry => <div key={entry.run} className="validation-item warning">{concurrentLoadWarning(entry)}</div>)}
    {context.legacyClock.length > 0 && <div className="validation-item note">{legacyClockNote}</div>}
    {context.withoutContext.length > 0 && <p className="table-lead">{context.withoutContext.length} run{context.withoutContext.length === 1 ? '' : 's'} recorded no run context (collected before run context was captured): versions, nodes and concurrent load are unknown for {context.withoutContext.length === 1 ? 'it' : 'them'}.</p>}
    <dl className="run-context-facts">
      <Fact label="Kubernetes" value={versions(context.kubernetes)} />
      <Fact label="Istio" value={versions(context.istio)} />
      <Fact label="Prometheus" value={versions(context.prometheus)} />
      <Fact label="Scrape interval" value={describeValues(context.scrapeIntervalSeconds)} />
      <Fact label="Rate window" value={describeValues(context.rateWindowSeconds) ?? '60 s (collector default, not recorded)'} />
      <Fact label="Data resolution" value={describeValues(context.stepSeconds)} />
      <Fact label="Warm-up" value={describeValues(context.warmupSeconds)} />
      <Fact label="Measured window" value={describeValues(context.measuredSeconds)} />
      <Fact label="Drain" value={describeValues(context.drainSeconds)} />
      <Fact label="Locust workers" value={describeValues(context.workers, value => String(value))} />
      <Fact label="Concurrent KubePhos load" value={context.concurrentLoad.length ? `${context.concurrentLoad.length} run${context.concurrentLoad.length === 1 ? '' : 's'} affected` : context.withoutContext.length === 0 && context.nodes.length ? 'None observed' : undefined} />
    </dl>
    {context.nodes.length > 0 && <div className="statistics-table-wrap"><ResponsiveTable aria-label="Cluster nodes">
      <thead><tr><th>Node</th><th>Zone</th><th>Region</th><th>Instance type</th><th>Kubelet</th><th>Container runtime</th><th>Kernel</th></tr></thead>
      <tbody>{context.nodes.map(node => <tr key={node.name}><td>{node.name}</td><td>{node.zone ?? '—'}</td><td>{node.region ?? '—'}</td><td>{node.instanceType ?? '—'}</td><td>{node.kubelet ?? '—'}</td><td>{node.containerRuntime ?? '—'}</td><td>{node.kernel ?? '—'}</td></tr>)}</tbody>
    </ResponsiveTable></div>}
  </section>
}

export function MethodsPanel({text}: {text: string}) {
  const [status, setStatus] = useState('')
  const copy = async () => {
    try {await copyText(text); setStatus('Methods paragraph copied.')}
    catch {setStatus('Could not copy. Select the text and copy it manually.')}
  }
  return <section className="statistics-card methods-panel" aria-label="Methods">
    <div className="methods-head"><h3>Methods</h3><button type="button" className="button secondary compact" onClick={() => void copy()}>Copy methods</button></div>
    <p className="methods-text">{text}</p>
    <p className="table-footnote">Generated from the recorded data only; review before publication. Unknown settings are omitted rather than assumed.</p>
    <span className="visually-hidden" role="status">{status}</span>
  </section>
}
