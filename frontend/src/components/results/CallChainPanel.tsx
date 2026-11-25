import {analysisSeries, type LoadedDataset} from '../../resultsData'
import {modelRequestsPerExecution, isProxy, predictChain} from '../../callChain'
import type {ApplicationComponent, CallModel, TimeSeriesDataset} from '../../types'
import {ResponsiveTable} from '../ResponsiveTable'
import {ChartExportActions} from '../ChartExportActions'
import {CallChainGraph, formatRate, ServiceRankingTable, type RankingRow} from '../CallChainGraph'

// Results: the call model measured during call-chain profiling, the task mix the
// load generator applied and, when both exist, predicted vs measured service rates.

export function hasCallChainData(dataset: TimeSeriesDataset): boolean {
  return Boolean(dataset.spec.callModel?.tasks?.length || dataset.spec.taskMix?.length)
}

// Graph nodes and dependencies from the measured edges (the dataset does not carry
// the catalog topology).
export function modelComponents(model: CallModel): ApplicationComponent[] {
  const services = new Set<string>()
  const dependencies = new Map<string, Set<string>>()
  for (const task of model.tasks) {
    Object.keys(task.services ?? task.servicesPerRequest ?? {}).forEach(service => services.add(service))
    for (const edge of task.edges ?? []) {
      services.add(edge.source); services.add(edge.destination)
      if (edge.source !== edge.destination) dependencies.set(edge.source, (dependencies.get(edge.source) ?? new Set()).add(edge.destination))
    }
  }
  return [...services].map(service => ({id: service, ...(isProxy(model, service) ? {traits: ['traffic-entrypoint']} : {}), dependencies: [...dependencies.get(service) ?? []]}))
}

// Mean HTTP request rate Locust sent during the measured window.
export function measuredHttpRate(dataset: TimeSeriesDataset): number | undefined {
  const summary = dataset.spec.locustSummary
  const from = Date.parse(summary?.measuredFrom ?? ''), to = Date.parse(summary?.measuredTo ?? '')
  if (summary && to > from) return summary.requests / ((to - from) / 1000)
  const series = analysisSeries(dataset, 'load.achieved_rps')
  const means = series.map(item => item.points.length ? item.points.reduce((sum, point) => sum + point.value, 0) / item.points.length : undefined).filter((value): value is number => value !== undefined)
  return means.length ? means.reduce((sum, value) => sum + value, 0) : undefined
}

// Mean server-side request rate per workload (application.request_rps).
export function measuredServiceRates(dataset: TimeSeriesDataset): Map<string, number> {
  const rates = new Map<string, number>()
  for (const series of analysisSeries(dataset, 'application.request_rps')) {
    const workload = series.labels.destination_workload ?? series.labels.workload ?? series.labels.service
    if (!workload || !series.points.length) continue
    rates.set(workload, (rates.get(workload) ?? 0) + series.points.reduce((sum, point) => sum + point.value, 0) / series.points.length)
  }
  return rates
}

function topServices(services: Record<string, number> | undefined, limit = 3): string {
  if (!services) return '—'
  return Object.entries(services).sort((left, right) => right[1] - left[1]).slice(0, limit).map(([service, value]) => `${service} ${value.toLocaleString('en-US', {maximumFractionDigits: 2})}`).join(' · ') || '—'
}

function DatasetCallChain({item, index}: {item: LoadedDataset; index: number}) {
  const {callModel: model, taskMix} = item.dataset.spec
  const label = item.label || item.artifact.name
  const rate = measuredHttpRate(item.dataset)
  const prediction = model && taskMix?.length && rate !== undefined ? predictChain(taskMix.map(entry => ({name: entry.task, share: entry.share})), model, rate) : undefined
  const measured = measuredServiceRates(item.dataset)
  const rows: RankingRow[] = prediction ? prediction.services.map(service => ({...service, measured: measured.get(service.service)})) : []
  return <section className="statistics-card call-chain-results" aria-label={`Call chain for ${label}`}>
    <h3>{label}</h3>
    {taskMix?.length ? <div className="statistics-table-wrap"><ResponsiveTable aria-label={`Effective task mix for ${label}`}>
      <thead><tr><th scope="col">Task</th><th scope="col">Weight applied</th><th scope="col">Task share</th></tr></thead>
      <tbody>{taskMix.map(entry => <tr key={entry.task}><td>{entry.task}</td><td>{entry.weight.toLocaleString('en-US')}</td><td>{(entry.share * 100).toFixed(1)}%</td></tr>)}</tbody>
    </ResponsiveTable><p className="table-footnote">Effective Locust task mix applied by the load generator during this run.</p></div> : <p className="empty-inline">This run did not record its task mix.</p>}
    {model ? <>
      <p className="table-lead">Call model measured {new Date(model.measuredAt).toLocaleString('en-US')} ({model.source}{model.method ? `, ${model.method}` : ''}){model.rps ? ` at ${formatRate(model.rps)} req/s` : ''}{model.secondsPerTask ? `, ${model.secondsPerTask} s per task` : ''}{model.windowTrimSeconds ? `, first ${model.windowTrimSeconds} s of each window discarded` : ''}. Scenario {model.scenarioId}.</p>
      <div className="statistics-table-wrap"><ResponsiveTable aria-label={`Measured call model for ${label}`}>
        <thead><tr><th scope="col">Task</th><th scope="col">Executions</th><th scope="col">HTTP requests</th><th scope="col">Requests per execution</th><th scope="col">Top services (requests per execution)</th></tr></thead>
        <tbody>{model.tasks.map(task => {
          const perExecution = modelRequestsPerExecution(task)
          return <tr key={task.task}><td>{task.task}</td><td>{task.executions === undefined ? '—' : task.executions.toLocaleString('en-US', {maximumFractionDigits: 0})}</td><td>{task.entryRequests.toLocaleString('en-US', {maximumFractionDigits: 0})}</td><td>{perExecution === undefined ? '—' : perExecution.toLocaleString('en-US', {maximumFractionDigits: 2})}</td><td>{task.services ? topServices(task.services) : task.servicesPerRequest ? `${topServices(task.servicesPerRequest)} (per HTTP request)` : '—'}</td></tr>
        })}</tbody>
      </ResponsiveTable>{model.description && <p className="table-footnote">{model.description}</p>}</div>
      <CallChainGraph components={modelComponents(model)} rates={prediction ? Object.fromEntries(prediction.services.map(service => [service.service, {rps: measured.get(service.service) ?? service.rps}])) : undefined} edges={prediction?.edges} chartID={`call-chain-graph-${index}`} actions={<ChartExportActions chartID={`call-chain-graph-${index}`} metric="call-chain" />} heading={prediction ? 'Measured call chain (node labels: measured mean req/s; edges: predicted)' : 'Measured call chain'} label={`Call chain measured for ${label}`} />
      {prediction ? <>
        <h4>Predicted vs measured service rates</h4>
        <p className="table-lead">Predicted from the call model, this run's task mix and its mean HTTP rate ({formatRate(rate)} req/s); measured is the mean of application.request_rps (Istio, server side) over the measured window.</p>
        <ServiceRankingTable rows={rows} label={`Predicted and measured requests per service for ${label}`} measured />
        {prediction.unmeasured.length > 0 && <p className="table-footnote">Not in the call model: {prediction.unmeasured.join(', ')}.</p>}
        {prediction.parsedOnly.length > 0 && <p className="table-footnote">Execution counts missing for {prediction.parsedOnly.join(', ')}: one HTTP request per execution is assumed for them.</p>}
      </> : <p className="empty-inline">Predicted vs measured rates need both the call model and the run's task mix.</p>}
    </> : <p className="empty-inline">No call model in this run (call-chain profiling was off).</p>}
  </section>
}

export function CallChainPanel({datasets}: {datasets: LoadedDataset[]}) {
  const shown = datasets.filter(item => hasCallChainData(item.dataset))
  if (!shown.length) return <section className="statistics-card"><h3>Call chain</h3><p className="empty-inline">These runs have no call model or task mix. Turn on “Measure call chain” in the Request mix tab of the load profile to measure one.</p></section>
  return <div className="call-chain-results">{shown.map((item, index) => <DatasetCallChain key={item.artifact.id} item={item} index={index} />)}</div>
}
