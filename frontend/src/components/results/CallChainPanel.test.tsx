import {render, screen, within} from '@testing-library/react'
import axe from 'axe-core'
import {describe, expect, it} from 'vitest'
import {CallChainPanel, measuredHttpRate} from './CallChainPanel'
import type {LoadedDataset} from '../../resultsData'
import type {TimeSeriesDataset} from '../../types'

const points = (value: number) => [{timestamp: '2026-11-18T10:01:00Z', value}, {timestamp: '2026-11-18T10:02:00Z', value}]
const dataset: TimeSeriesDataset = {
  apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', metadata: {name: 'run', version: 'v1alpha1'},
  spec: {
    applicationRef: 'app', clusterServer: 'https://k8s', namespace: 'ns', start: '2026-11-18T10:00:00Z', end: '2026-11-18T10:10:00Z', stepSeconds: 15,
    source: {kind: 'prometheus', version: 'v1', profile: 'default'}, summary: {metrics: 1, series: 2, samples: 4},
    locustSummary: {measuredFrom: '2026-11-18T10:00:00Z', measuredTo: '2026-11-18T10:10:00Z', requests: 60000, failures: 0},
    series: [
      {metric: 'application.request_rps', unit: 'requests_per_second', labels: {destination_workload: 'frontend'}, points: points(98)},
      {metric: 'application.request_rps', unit: 'requests_per_second', labels: {destination_workload: 'currency'}, points: points(310)}
    ],
    taskMix: [{task: 'browse', weight: 3, share: .75}, {task: 'checkout', weight: 1, share: .25}],
    callModel: {source: 'istio', method: 'counter-difference', measuredAt: '2026-11-18T09:50:00Z', applicationRef: 'app', scenarioId: 'journey', rps: 10, secondsPerTask: 60, windowTrimSeconds: 15, proxies: ['node-proxy'], description: 'Measured call chain.', tasks: [
      {task: 'browse', executions: 500, entryRequests: 500, services: {'node-proxy': 1, frontend: 1, currency: 4}, edges: [{source: 'node-proxy', destination: 'frontend', perExecution: 1}, {source: 'frontend', destination: 'currency', perExecution: 4}]},
      {task: 'checkout', executions: 200, entryRequests: 600, services: {'node-proxy': 3, frontend: 3}}
    ]}
  }
}
const item: LoadedDataset = {artifact: {id: 'a1', operationId: 'o', name: 'Run 1', type: 'TimeSeriesDataset', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:x', sizeBytes: 1, sensitive: false, verifiedAt: '2026-11-18T10:11:00Z'}, dataset, label: 'Baseline · run 1'}

describe('Call chain results', () => {
  it('shows the measured model, the effective mix and predicted vs measured rates', async () => {
    render(<main><h1>Results</h1><CallChainPanel datasets={[item]} /></main>)
    expect(measuredHttpRate(dataset)).toBe(100)
    const mix = screen.getByRole('table', {name: 'Effective task mix for Baseline · run 1'})
    expect(within(mix).getByText('75.0%')).toBeInTheDocument()
    const model = screen.getByRole('table', {name: 'Measured call model for Baseline · run 1'})
    expect(within(model).getAllByRole('row')[2]).toHaveTextContent('checkout200600' + '3node-proxy 3 · frontend 3')
    // 100 req/s; Σ s·r = 0.75 + 0.75 → 66.7 executions/s; currency = 66.7 × 0.75 × 4 = 200.
    const ranking = screen.getByRole('table', {name: 'Predicted and measured requests per service for Baseline · run 1'})
    const currency = within(ranking).getAllByRole('row').find(row => row.textContent?.includes('currency'))!
    expect([...currency.querySelectorAll('td')].map(cell => cell.textContent)).toEqual(['1', 'currency', '200', expect.any(String), '—', '—', '3', '310', '155%'])
    expect(screen.getByRole('img', {name: 'Call chain measured for Baseline · run 1'})).toHaveTextContent('310 req/s')
    const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))).toEqual([])
  })

  it('explains runs without a call model', () => {
    render(<CallChainPanel datasets={[{...item, dataset: {...dataset, spec: {...dataset.spec, callModel: undefined, taskMix: undefined}}}]} />)
    expect(screen.getByText(/no call model or task mix/)).toBeInTheDocument()
  })
})
