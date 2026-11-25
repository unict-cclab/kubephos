import {useEffect, useId, useMemo, useRef, useState} from 'react'
import {loadScenarioPreview} from '../scenarioScripts'
import type {ScenarioContext} from './useRateEquivalents'
import {mixOverrides, mixRunnable, parseLocustfile, requestsPerExecution, taskShares, type LocustParseResult, type LocustTask} from '../locustParser'
import {findCallModel, modelRequestsPerExecution, predictChain, type FoundCallModel} from '../callChain'
import type {ApplicationComponent} from '../types'
import {NumericInput} from './NumericInput'
import {CallChainGraph, componentForService, formatRate, ServiceRankingTable, type NodeRate, type RankingRow} from './CallChainGraph'
import './RequestMix.css'

// Request mix of the load profile: Locust tasks read from the scenario's Locust
// file, editable weights, call-chain profiling settings and the predicted call
// chain at the profile's peak. Loaded lazily with the load profile tabs.

export interface RequestMixContext extends ScenarioContext {
  applicationRef: string
  components: ApplicationComponent[]
}

type Script = {status: 'loading'} | {status: 'error'; message: string} | {status: 'ready'; result: LocustParseResult; digest?: string}
type ModelState = {status: 'loading'} | {status: 'error'; message: string} | {status: 'ready'; found: FoundCallModel | null}

// Names the load generator accepts in taskWeights (Python function names).
const validTaskName = /^[A-Za-z_]\w{0,127}$/
const round = (value: number, digits = 4) => Number(value.toFixed(digits))
const percent = (value: number) => `${(value * 100).toLocaleString('en-US', {maximumFractionDigits: value < .1 ? 2 : 1})}%`

function numericRecord(value: unknown): Record<string, number> | undefined {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return undefined
  const entries = Object.entries(value).filter(([, item]) => typeof item === 'number' && Number.isFinite(item) && item >= 0) as Array<[string, number]>
  return entries.length ? Object.fromEntries(entries) : undefined
}

function formatSeconds(seconds: number): string {
  const minutes = Math.floor(seconds / 60), rest = Math.round(seconds % 60)
  return [minutes ? `${minutes} min` : '', rest ? `${rest} s` : ''].filter(Boolean).join(' ') || '0 s'
}

function methodSummary(tasks: LocustTask[], shares: Map<string, number>): string {
  const totals = new Map<string, number>()
  let total = 0
  for (const task of tasks) for (const call of task.calls) {
    const share = shares.get(task.name) ?? 0
    totals.set(call.method, (totals.get(call.method) ?? 0) + share)
    total += share
  }
  if (!(total > 0)) return 'unknown'
  return [...totals].sort((left, right) => right[1] - left[1]).map(([method, value]) => `${method} ${percent(value / total)}`).join(' · ')
}

// Initial replicas configured in the application step of the same form.
function readReplicas(form: HTMLFormElement | null): {initial: number; overrides: Record<string, number>} {
  const initialField = form?.elements.namedItem('applicationValues.initialReplicas')
  const overridesField = form?.elements.namedItem('applicationValues.replicaOverrides')
  const initial = initialField instanceof HTMLInputElement && Number(initialField.value) > 0 ? Number(initialField.value) : 1
  let overrides: Record<string, number> = {}
  if (overridesField instanceof HTMLInputElement && overridesField.value) {
    try { overrides = numericRecord(JSON.parse(overridesField.value)) ?? {} } catch { /* Keep the default replicas. */ }
  }
  return {initial, overrides}
}

export function RequestMixEditor({prefix, values = {}, samples, context}: {prefix: string; values?: Record<string, unknown>; samples: Array<{seconds: number; rps: number}>; context: RequestMixContext}) {
  const sameScenario = values.scenarioId === undefined || values.scenarioId === context.scenarioId
  const saved = sameScenario ? numericRecord(values.taskWeights) : undefined
  const [script, setScript] = useState<Script>({status: 'loading'})
  const [weights, setWeights] = useState<Record<string, number>>({})
  const [dirty, setDirty] = useState(false)
  const [profiling, setProfiling] = useState(values.profileCallChain === true)
  const [secondsPerTask, setSecondsPerTask] = useState(typeof values.profileSecondsPerTask === 'number' ? values.profileSecondsPerTask : 60)
  const peak = samples.reduce((maximum, point) => Math.max(maximum, point.rps), 0)
  const initialRate = samples[0]?.rps ?? 0
  const [profileRps, setProfileRps] = useState(typeof values.profileRps === 'number' && values.profileRps > 0 ? values.profileRps : round(Math.min(10, initialRate > 0 ? initialRate : 10), 2))
  const [model, setModel] = useState<ModelState>({status: 'loading'})
  const [revision, setRevision] = useState(0)
  const container = useRef<HTMLDivElement>(null)
  const [replicas, setReplicas] = useState<{initial: number; overrides: Record<string, number>}>({initial: 1, overrides: {}})
  const id = useId()

  useEffect(() => {
    let active = true
    setScript({status: 'loading'})
    loadScenarioPreview(context.applicationId, revision > 0).then(preview => {
      if (!active) return
      const scenario = preview.loadScenarios?.find(item => item.id === context.scenarioId)
      if (!scenario?.script) { setScript({status: 'error', message: 'The Locust file of this scenario is not available.'}); return }
      const result = parseLocustfile(scenario.script)
      setScript({status: 'ready', result, digest: scenario.scriptDigest})
      const initial = saved && result.tasks.length ? taskShares(result.users, saved) : undefined
      setWeights(Object.fromEntries(result.tasks.map(task => [task.name, round((initial ? initial.get(task.name) ?? 0 : task.share) * 100)])))
      setDirty(false)
    }, (cause: unknown) => {
      if (active) setScript({status: 'error', message: cause instanceof Error ? cause.message : 'The Locust file could not be loaded.'})
    })
    return () => {active = false}
  }, [context.applicationId, context.scenarioId, revision])

  useEffect(() => {
    let active = true
    setModel({status: 'loading'})
    findCallModel(context.applicationRef, context.scenarioId, revision > 0).then(found => {if (active) setModel({status: 'ready', found})}, (cause: unknown) => {
      if (active) setModel({status: 'error', message: cause instanceof Error ? cause.message : 'Measured call chains could not be loaded.'})
    })
    return () => {active = false}
  }, [context.applicationRef, context.scenarioId, revision])

  useEffect(() => {
    const form = container.current?.closest('form') ?? null
    const update = () => setReplicas(readReplicas(form))
    update()
    form?.addEventListener('input', update)
    form?.addEventListener('change', update)
    return () => {form?.removeEventListener('input', update); form?.removeEventListener('change', update)}
  }, [])

  const tasks = script.status === 'ready' ? script.result.tasks : []
  const users = script.status === 'ready' ? script.result.users : []
  const defaults = useMemo(() => Object.fromEntries(tasks.map(task => [task.name, round(task.share * 100)])), [tasks])
  // Percentages are shares of all executions; nested TaskSets get the sum of their tasks.
  const overrides = useMemo(() => mixOverrides(users, weights), [users, weights])
  const shares = useMemo(() => taskShares(users, overrides), [users, overrides])
  const total = tasks.reduce((sum, task) => sum + (weights[task.name] ?? 0), 0)
  const valid = !tasks.length || total > 0 && mixRunnable(users, overrides)
  const changed = tasks.some(task => Math.abs((weights[task.name] ?? 0) - defaults[task.name]) >= .005)
  const serialized = dirty && tasks.length ? changed ? JSON.stringify(Object.fromEntries(Object.entries(overrides).filter(([name]) => validTaskName.test(name)).map(([name, value]) => [name, round(value)]))) : '' : saved ? JSON.stringify(values.taskWeights) : ''
  const found = model.status === 'ready' ? model.found : null
  const measuredTask = (name: string) => found?.model.tasks.find(task => task.task === name)
  const perExecution = (task: LocustTask) => modelRequestsPerExecution(measuredTask(task.name)) ?? requestsPerExecution(task)
  const requestWeight = tasks.reduce((sum, task) => sum + (shares.get(task.name) ?? 0) * perExecution(task), 0)
  const activeTasks = tasks.filter(task => (shares.get(task.name) ?? 0) > 0).length
  const extraSeconds = activeTasks * secondsPerTask
  const set = (name: string, value: number) => {setWeights(current => ({...current, [name]: Math.max(0, value)})); setDirty(true)}
  const reset = () => {setWeights(defaults); setDirty(true)}

  const prediction = found && tasks.length && valid ? predictChain(tasks.map(task => ({name: task.name, share: shares.get(task.name) ?? 0, parsedRequests: requestsPerExecution(task)})), found.model, peak) : undefined
  const replicaCount = (service: string) => {
    const component = componentForService(context.components, service)
    if (!component || component.workload?.kind === 'DaemonSet') return undefined
    return replicas.overrides[component.id] ?? replicas.initial
  }
  const rows: RankingRow[] = (prediction?.services ?? []).map(service => {
    const count = replicaCount(service.service)
    return {...service, replicas: count, perReplica: count ? service.rps / count : undefined}
  })
  const rates: Record<string, NodeRate> | undefined = prediction ? Object.fromEntries(rows.flatMap(row => {
    const component = componentForService(context.components, row.service)
    return component ? [[component.id, {rps: row.rps, perReplica: row.perReplica}]] : []
  })) : undefined
  const executions = found?.model.tasks.map(task => task.executions).filter((value): value is number => value !== undefined) ?? []

  const reviewTable = serialized ? JSON.stringify({headers: ['Task', 'Weight (%)', 'Share'], rows: tasks.map(task => [task.name, String(round(weights[task.name] ?? 0, 2)), percent(shares.get(task.name) ?? 0)])}) : undefined

  return <div className="request-mix" ref={container}>
    <input type="hidden" name={`${prefix}.taskWeights`} value={serialized} data-review-title="Request mix" data-review-table={reviewTable} readOnly />
    <section className="request-mix-section" aria-labelledby={`${id}-mix`}>
      <h4 id={`${id}-mix`}>Request mix</h4>
      <p className="field-help">Locust picks the next task of each user at random in proportion to these weights; the profile's rate counts HTTP requests, so tasks that send several requests weigh more in the traffic.</p>
      {script.status === 'loading' && <p role="status">Reading the Locust file…</p>}
      {script.status === 'error' && <div className="request-mix-notice warning" role="alert">{script.message} The load generator keeps the mix defined in the Locust file{saved ? ' and the saved weights are kept unchanged' : ''}. <button type="button" className="button secondary compact" onClick={() => setRevision(value => value + 1)}>Retry</button></div>}
      {script.status === 'ready' && script.result.status === 'failed' && <div className="request-mix-notice warning" role="alert">The tasks of this Locust file could not be read: {script.result.warnings.join(' ')} The load generator uses the mix defined in the file{saved ? '; the saved weights are kept unchanged' : ''}.</div>}
      {script.status === 'ready' && script.result.status === 'partial' && <div className="request-mix-notice warning"><strong>Partly read Locust file.</strong><ul>{script.result.warnings.map(warning => <li key={warning}>{warning}</li>)}</ul></div>}
      {tasks.length > 0 && <>
        <div className="statistics-table-wrap" data-review-skip="true"><table className="request-mix-table" aria-label="Request mix by Locust task">
          <thead><tr><th scope="col">Task</th><th scope="col">HTTP calls per execution</th><th scope="col">Default</th><th scope="col">Weight (%)</th><th scope="col">Task share</th><th scope="col">Share of HTTP requests</th></tr></thead>
          <tbody>{tasks.map((task, index) => {
            const share = shares.get(task.name) ?? 0
            const weight = weights[task.name] ?? 0
            return <tr key={task.name}>
              <th scope="row">{task.name}{task.parents.length > 1 && <small className="field-help"> in {task.parents.at(-1)}</small>}</th>
              <td>{task.calls.length ? <ul className="request-mix-calls">{task.calls.map((call, position) => <li key={position}><code>{call.method} {call.path || '…'}{call.dynamic && call.path ? '…' : ''}</code></li>)}</ul> : <span className="field-help">unknown</span>}{measuredTask(task.name) && modelRequestsPerExecution(measuredTask(task.name)) !== undefined && <small className="field-help"> measured {modelRequestsPerExecution(measuredTask(task.name))!.toLocaleString('en-US', {maximumFractionDigits: 2})} requests per execution</small>}</td>
              <td>{percent(task.share)}</td>
              <td><div className="request-mix-weight">
                <NumericInput aria-label={`Weight of ${task.name} (%)`} value={round(weight, 2)} min={0} max={10000} step="any" required validationMessage={index === 0 && !valid ? 'At least one task needs a positive weight.' : undefined} onValueChange={value => set(task.name, value)} />
                <input type="range" aria-label={`Weight of ${task.name} slider`} min={0} max={Math.max(100, Math.ceil(weight))} step={.5} value={weight} onChange={event => set(task.name, event.currentTarget.valueAsNumber)} />
              </div></td>
              <td>{percent(share)}</td>
              <td>{requestWeight > 0 ? percent(share * perExecution(task) / requestWeight) : '—'}</td>
            </tr>
          })}</tbody>
          <tfoot><tr><th scope="row">Total</th><td /><td>100%</td><td>{round(total, 2).toLocaleString('en-US')}</td><td>{valid ? '100%' : '—'}</td><td>{valid ? '100%' : '—'}</td></tr></tfoot>
        </table></div>
        <div className="request-mix-summary">
          <span>Weights are normalized: a total of {round(total, 2).toLocaleString('en-US')} counts as 100%.</span>
          <span>HTTP methods: {methodSummary(tasks, shares)}</span>
          <span role="status">{!valid ? 'At least one task needs a positive weight.' : serialized ? 'Custom mix: applied by the load generator.' : 'Locust file defaults.'}</span>
          <button type="button" className="button secondary compact" disabled={!changed} onClick={reset}>Reset to Locust file defaults</button>
        </div>
      </>}
    </section>

    <section className="request-mix-section" aria-labelledby={`${id}-profile`}>
      <h4 id={`${id}-profile`}>Measure call chain</h4>
      <p className="field-help">Before warmup, each task runs alone for a short window while the mesh counts the requests every service receives. The resulting call model predicts the per-service load of later configurations of this application and scenario.</p>
      <div className="request-mix-profile">
        <label className="checkbox-field"><input type="checkbox" name={`${prefix}.profileCallChain`} checked={profiling} onChange={event => setProfiling(event.currentTarget.checked)} /><span>Measure call chain before warmup</span></label>
        {profiling && <>
          <label>Seconds per task<NumericInput name={`${prefix}.profileSecondsPerTask`} value={secondsPerTask} min={30} max={600} step={1} required onValueChange={value => setSecondsPerTask(Math.round(value))} /></label>
          <label>Profiling rate (req/s)<NumericInput name={`${prefix}.profileRps`} value={profileRps} min={0.01} max={100000} step="any" required onValueChange={setProfileRps} /></label>
        </>}
      </div>
      <p className="field-help" role="status">{profiling ? `Adds about ${formatSeconds(extraSeconds || secondsPerTask)} before warmup (${activeTasks || 'one window per'} task${activeTasks === 1 ? '' : 's'} × ${secondsPerTask} s).` : `Off. Enabling it adds ${activeTasks ? formatSeconds(activeTasks * secondsPerTask) : `${secondsPerTask} s per task`} to each run.`}</p>
    </section>

    <section className="request-mix-section" aria-labelledby={`${id}-prediction`}>
      <h4 id={`${id}-prediction`}>Predicted call chain at the profile peak</h4>
      {model.status === 'loading' && <p role="status">Looking for a measured call chain…</p>}
      {model.status === 'error' && <div className="request-mix-notice warning" role="alert">{model.message} <button type="button" className="button secondary compact" onClick={() => setRevision(value => value + 1)}>Retry</button></div>}
      {model.status === 'ready' && !found && <div className="request-mix-notice">Not measured yet: no run of this application and scenario has a call model. Turn on “Measure call chain” above for the next run (it adds {activeTasks ? formatSeconds(activeTasks * secondsPerTask) : `${secondsPerTask} s per task`}); later configurations reuse the measurement. Until then the graph only shows which services the application calls.</div>}
      {found && prediction && <>
        <p>{formatRate(peak)} HTTP req/s at the peak → {formatRate(prediction.taskRate)} task executions/s ({(peak > 0 ? peak / prediction.taskRate : 0).toLocaleString('en-US', {maximumFractionDigits: 2})} requests per execution on average).</p>
        <p className="field-help">Measured on {new Date(found.model.measuredAt).toLocaleString('en-US')} in “{found.experimentName}”{executions.length ? ` from ${executions.reduce((sum, value) => sum + value, 0).toLocaleString('en-US', {maximumFractionDigits: 0})} task executions (${Math.min(...executions).toLocaleString('en-US', {maximumFractionDigits: 0})}–${Math.max(...executions).toLocaleString('en-US', {maximumFractionDigits: 0})} per task)` : ' (task executions not recorded: per-request multipliers are used)'}{found.model.rps ? ` at ${formatRate(found.model.rps)} req/s` : ''}{found.model.secondsPerTask ? `, ${found.model.secondsPerTask} s per task` : ''}{found.model.windowTrimSeconds ? `, first ${found.model.windowTrimSeconds} s of each window discarded` : ''}. The prediction scales those counts linearly: caches, retries, timeouts and autoscaling can change them under load. The model does not record the Locust file version{script.status === 'ready' && script.digest ? ` (current file ${script.digest.slice(0, 19)}…)` : ''}; measure again after changing it. Per-replica rates use the initial replicas of this configuration.</p>
        {prediction.unmeasured.length > 0 && <div className="request-mix-notice warning">Not measured by this model: {prediction.unmeasured.join(', ')}. Their downstream requests are missing from the prediction.</div>}
      </>}
      <CallChainGraph components={context.components} rates={found && prediction ? rates : undefined} edges={prediction?.edges} chartID={`request-mix-graph-${id.replace(/[^\w-]/g, '')}`} heading="Service graph" label={prediction ? `Predicted requests per second by service at a peak of ${formatRate(peak)} HTTP requests per second` : 'Application service graph'} />
      {rows.length > 0 && <ServiceRankingTable rows={rows} label="Predicted requests per service at the profile peak" />}
    </section>
  </div>
}
