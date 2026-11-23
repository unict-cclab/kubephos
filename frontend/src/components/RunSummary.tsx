import {useEffect, useRef, useState} from 'react'
import type {ExperimentConfiguration} from '../types'
import {formatDuration, loadProfileSteps, loadSamples, type LoadProfileStep} from './LoadProfileEditor'
import type {ManualStartScope} from './TrialScheduleFields'
import './RunSummary.css'

// What one repetition of a configuration does, in the order it happens. Loaded lazily
// with the Review step and the New run dialog, so the editor's initial bundle stays small.

export interface LoadPlan {
  steps: LoadProfileStep[]
  warmupSeconds: number
  drainSeconds: number
  workers: number
  profiling?: {secondsPerTask: number; tasks?: number}
}

export interface RepetitionTiming {profiling: number; warmup: number; measured: number; drain: number; total: number}

const count = (value: unknown, fallback = 0) => {
  const number = Number(value)
  return Number.isFinite(number) && number >= 0 ? number : fallback
}

export function repetitionTiming(plan: LoadPlan): RepetitionTiming {
  const measured = plan.steps.reduce((sum, step) => sum + step.durationSeconds, 0)
  const profiling = plan.profiling ? plan.profiling.secondsPerTask * (plan.profiling.tasks ?? 1) : 0
  return {profiling, warmup: plan.warmupSeconds, measured, drain: plan.drainSeconds, total: profiling + plan.warmupSeconds + measured + plan.drainSeconds}
}

// Peak, mean and total requests of the measured profile (the warmup is not measured).
export function loadVolume(steps: LoadProfileStep[]): {peak: number; mean: number; requests: number} {
  const samples = loadSamples(steps)
  let peak = 0, requests = 0
  samples.forEach((sample, index) => {
    peak = Math.max(peak, sample.rps)
    const previous = samples[index - 1]
    if (previous) requests += (sample.seconds - previous.seconds) * (sample.rps + previous.rps) / 2
  })
  const duration = samples.length ? samples[samples.length - 1].seconds : 0
  return {peak, mean: duration ? requests / duration : 0, requests}
}

// The load plan saved in a configuration (single configurations only).
export function savedLoadPlan(configuration?: ExperimentConfiguration): LoadPlan | undefined {
  const load = configuration?.definition.components?.find(component => component.capability.startsWith('load.'))?.configuration
  if (!load) return undefined
  const weights = load.taskWeights && typeof load.taskWeights === 'object' ? Object.values(load.taskWeights).filter(value => Number(value) > 0).length : 0
  return {steps: loadProfileSteps(load.steps), warmupSeconds: count(load.warmupSeconds), drainSeconds: count(load.drainSeconds, 30), workers: count(load.replicas, 1) || 1,
    profiling: load.profileCallChain === true ? {secondsPerTask: count(load.profileSecondsPerTask, 60) || 60, tasks: weights || undefined} : undefined}
}

const rate = (value: number) => value.toLocaleString('en-US', {maximumFractionDigits: value < 10 ? 1 : 0})
const total = (value: number) => value >= 1e6 ? `${(value / 1e6).toLocaleString('en-US', {maximumFractionDigits: 1})} M` : value >= 1e4 ? `${Math.round(value / 1e3).toLocaleString('en-US')} k` : Math.round(value).toLocaleString('en-US')
const plural = (value: number, word: string) => `${value} ${word}${value === 1 ? '' : 's'}`

// How manual start behaves over several repetitions, in plain words.
export function manualStartText(scope: ManualStartScope, repetitions?: number): string {
  if (repetitions === 1) return 'The run waits for your Start after setup.'
  return scope === 'every'
    ? 'Every repetition waits for your Start after its setup; waiting time is not included in the estimate.'
    : 'Only the first repetition waits for your Start; later repetitions start by themselves after the rest interval.'
}

function Timeline({timing}: {timing: RepetitionTiming}) {
  const parts = ([['profiling', 'Call-chain profiling'], ['warmup', 'Warmup'], ['measured', 'Measured profile'], ['drain', 'Drain']] as const).filter(([key]) => timing[key] > 0)
  return <div className="run-timeline">
    <div className="run-timeline-bar" aria-hidden="true">{parts.map(([key]) => <span key={key} className={`run-timeline-${key}`} style={{flexGrow: timing[key]}} />)}</div>
    <ol className="run-timeline-legend">{parts.map(([key, label]) => <li key={key}><span className={`run-timeline-swatch run-timeline-${key}`} aria-hidden="true" />{label} <strong>{formatDuration(timing[key])}</strong></li>)}</ol>
  </div>
}

function LoadRows({plan}: {plan: LoadPlan}) {
  const timing = repetitionTiming(plan)
  const volume = loadVolume(plan.steps)
  return <>
    <div><dt>Each repetition</dt><dd><strong>{formatDuration(timing.total)}</strong> of traffic{plan.profiling && !plan.profiling.tasks ? ` (call-chain profiling counted as one task of ${plan.profiling.secondsPerTask} s)` : ''}<Timeline timing={timing} /></dd></div>
    <div><dt>Measured load</dt><dd>peak <strong>{rate(volume.peak)} req/s</strong> · mean {rate(volume.mean)} req/s · ≈ {total(volume.requests)} requests · {plural(plan.workers, 'Locust worker')}</dd></div>
  </>
}

export interface StrategyPlan {title: string; setting: string; targets: string[]; excluded: string[]}

// Reads the wizard's mounted controls: what the review step shows is what will be saved.
export function readWizardPlan(form: HTMLFormElement): {load?: LoadPlan; strategies: StrategyPlan[]; network?: boolean; manualStart?: ManualStartScope} {
  const field = (name: string) => form.querySelector<HTMLInputElement>(`[name="${name}"]`)
  const stepsInput = [...form.querySelectorAll<HTMLInputElement>('input[name$=".steps"]')].find(input => input.name.startsWith('component-'))
  let load: LoadPlan | undefined
  if (stepsInput) {
    const prefix = stepsInput.name.slice(0, -'.steps'.length)
    let steps: LoadProfileStep[] = []
    try { steps = loadProfileSteps(JSON.parse(stepsInput.value)) } catch { /* An invalid draft shows no load. */ }
    const profilingInput = field(`${prefix}.profileCallChain`)
    const profiling = profilingInput ? (profilingInput.type === 'checkbox' ? profilingInput.checked : profilingInput.value === 'true') : false
    let tasks = 0
    try { tasks = Object.values(JSON.parse(field(`${prefix}.taskWeights`)?.value || '{}') as Record<string, number>).filter(value => value > 0).length } catch { /* Unknown task count. */ }
    tasks ||= form.querySelectorAll('.request-mix-table tbody tr').length
    load = {steps, warmupSeconds: count(field(`${prefix}.warmupSeconds`)?.value), drainSeconds: count(field(`${prefix}.drainSeconds`)?.value ?? 30, 30), workers: count(field(`${prefix}.replicas`)?.value, 1) || 1,
      profiling: profiling ? {secondsPerTask: count(field(`${prefix}.profileSecondsPerTask`)?.value, 60) || 60, tasks: tasks || undefined} : undefined}
  }
  const strategies = [...form.querySelectorAll<HTMLElement>('.experiment-component-list .experiment-component')].map(section => {
    const select = section.querySelector('select')
    const rows = [...section.querySelectorAll<HTMLElement>('.target-editor-list article')].map(article => {
      const box = article.querySelector<HTMLInputElement>('.target-editor-row input[type=checkbox]')
      return {id: article.querySelector('.target-editor-row span')?.textContent ?? '', on: Boolean(box?.checked)}
    })
    return {title: section.querySelector('.experiment-component-head strong')?.textContent ?? 'Component', setting: select?.selectedOptions[0]?.textContent ?? '', targets: rows.filter(row => row.on).map(row => row.id), excluded: rows.filter(row => !row.on).map(row => row.id)}
  })
  const network = form.querySelector<HTMLInputElement>('.network-section-heading input[type=checkbox]')
  const manualStart = form.querySelector<HTMLSelectElement>('select[name="startMode"]')?.value === 'manual' ? form.querySelector<HTMLSelectElement>('select[name="manualStartScope"]')?.value === 'every' ? 'every' : 'first' : undefined
  return {load, strategies, network: network ? network.checked : undefined, manualStart}
}

function StrategyRow({plan}: {plan: StrategyPlan}) {
  const all = plan.targets.length + plan.excluded.length
  return <div><dt>{plan.title}</dt><dd>{plan.setting && <>{plan.setting} · </>}{all ? <>{plan.targets.length} of {plural(all, 'service')}{plan.excluded.length ? <> · not managed: {plan.excluded.join(', ')}</> : ' (all)'}</> : 'no compatible services'}</dd></div>
}

// Review step: a plain-language account of what a run of this configuration does.
export function RunSummary() {
  const root = useRef<HTMLElement>(null)
  const [plan, setPlan] = useState<ReturnType<typeof readWizardPlan>>()
  useEffect(() => {
    const form = root.current?.closest('form')
    const panel = root.current?.closest<HTMLElement>('[data-wizard-panel]')
    if (!form || !panel) return
    const read = () => { if (!panel.hidden) setPlan(readWizardPlan(form)) }
    read()
    // The wizard keeps every step mounted: refresh whenever the review becomes visible.
    const observer = new MutationObserver(read)
    observer.observe(panel, {attributes: true, attributeFilter: ['hidden']})
    return () => observer.disconnect()
  }, [])
  return <section ref={root} className="run-summary" aria-labelledby="run-summary-title">
    <h4 id="run-summary-title">What a run does</h4>
    {plan && <dl>
      {plan.load && <LoadRows plan={plan.load} />}
      {plan.strategies.map((strategy, index) => <StrategyRow key={index} plan={strategy} />)}
      {!plan.strategies.length && plan.load && <div><dt>Scheduling and scaling</dt><dd>Default Kubernetes scheduler, no descheduler, fixed replica counts.</dd></div>}
      {plan.network !== undefined && <div><dt>Network injection</dt><dd>{plan.network ? 'On, as configured in Monitoring → Network' : 'Off'}</dd></div>}
      {plan.manualStart && <div><dt>Manual start</dt><dd>{manualStartText(plan.manualStart)}</dd></div>}
      <div><dt>Repetitions</dt><dd>Chosen when you start a run, together with the rest between repetitions and an optional warm-up repetition.</dd></div>
    </dl>}
  </section>
}

// New run dialog: duration of the whole series from the saved load plan.
export function RunEstimate({configuration, runs, restSeconds, pilot, manualStart}: {configuration?: ExperimentConfiguration; runs: number; restSeconds: number; pilot: boolean; manualStart?: ManualStartScope}) {
  const plan = savedLoadPlan(configuration)
  if (!plan || !(runs >= 1)) return null
  const timing = repetitionTiming(plan)
  const repetitions = Math.floor(runs) + (pilot ? 1 : 0)
  const series = repetitions * timing.total + Math.max(0, repetitions - 1) * restSeconds
  return <section className="run-summary run-estimate" aria-label="Run estimate">
    <dl>
      <div><dt>Repetitions</dt><dd><strong>{repetitions}</strong>{pilot ? ` (1 warm-up + ${Math.floor(runs)} measured)` : ' measured'}</dd></div>
      <div><dt>Each repetition</dt><dd><strong>{formatDuration(timing.total)}</strong> of traffic<Timeline timing={timing} /></dd></div>
      <div><dt>Whole run</dt><dd>≈ <strong>{formatDuration(series)}</strong> of traffic and rest, plus application setup and cleanup.</dd></div>
      {manualStart && <div><dt>Manual start</dt><dd>{manualStartText(manualStart, repetitions)}</dd></div>}
    </dl>
  </section>
}
