import {fireEvent, render, screen, waitFor, within} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import {afterEach, beforeEach, describe, expect, it, vi} from 'vitest'
import {RequestMixEditor, type RequestMixContext} from './RequestMixEditor'
import {clearScenarioPreviews} from '../scenarioScripts'
import {LoadProfileEditor} from './LoadProfileEditor'
import {clearCallModelCache} from '../callChain'
import {onlineBoutiqueLocustfile} from '../test/locustFixtures'
import type {ApplicationComponent, CallModel} from '../types'

const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}})

const components: ApplicationComponent[] = [
  {id: 'node-proxy', traits: ['traffic-entrypoint'], workload: {kind: 'DaemonSet', name: 'node-proxy'}, dependencies: ['frontend']},
  {id: 'frontend', workload: {kind: 'Deployment', name: 'frontend'}, dependencies: ['currencyservice', 'productcatalogservice']},
  {id: 'currencyservice', workload: {kind: 'Deployment', name: 'currencyservice'}},
  {id: 'productcatalogservice', workload: {kind: 'Deployment', name: 'productcatalogservice'}}
]
const context: RequestMixContext = {applicationId: 'online-boutique', applicationRef: 'app:online-boutique', scenarioId: 'storefront-journey', components}
const samples = [{seconds: 0, rps: 20}, {seconds: 60, rps: 100}, {seconds: 120, rps: 50}]
const calls: Record<string, number> = {index: 1, setCurrency: 1, browseProduct: 1, addToCart: 2, viewCart: 1, checkout: 3}
const model: CallModel = {
  source: 'istio', method: 'counter-difference', measuredAt: '2026-11-18T10:00:00Z', applicationRef: 'app:online-boutique', scenarioId: 'storefront-journey', rps: 10, secondsPerTask: 60, windowTrimSeconds: 15, proxies: ['node-proxy'],
  tasks: Object.entries(calls).map(([task, requests]) => ({task, executions: 100, entryRequests: requests * 100, services: {'node-proxy': requests, frontend: requests, ...(task === 'browseProduct' ? {currencyservice: 2} : {})}, edges: [{source: 'node-proxy', destination: 'frontend', perExecution: requests}]}))
}

function stubApi({script = onlineBoutiqueLocustfile, withModel = true} = {}) {
  const fetch = vi.fn(async (url: string) => {
    if (url.includes('/manifest')) return response({content: '', loadScenarios: [{id: 'storefront-journey', script, scriptDigest: 'sha256:0123456789abcdef0123'}]})
    if (url.endsWith('/experiments')) return response({items: withModel ? [{id: 'e1', name: 'Profiling run', updatedAt: '2026-11-18T11:00:00Z', variants: [{id: 'v1', configuration: {components: [{capability: 'load.session.control', configuration: {scenarioId: 'storefront-journey', profileCallChain: true}}]}, trials: [{id: 't1', status: 'succeeded', resultArtifactId: 'a1', completedAt: '2026-11-18T11:00:00Z'}]}]}] : []})
    if (url.includes('/artifacts/a1/download')) return response({apiVersion: 'artifacts.kubephos.dev/v1alpha1', kind: 'TimeSeriesDataset', spec: {applicationRef: 'app:online-boutique', series: [], callModel: model}})
    return response({message: 'not found'}, 404)
  })
  vi.stubGlobal('fetch', fetch)
  return fetch
}

async function audit() {
  const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
  expect(results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))).toEqual([])
}

const hidden = (container: HTMLElement) => container.querySelector<HTMLInputElement>('input[name="load.taskWeights"]')!

describe('Request mix editor', () => {
  beforeEach(() => {clearScenarioPreviews(); clearCallModelCache()})
  afterEach(() => vi.unstubAllGlobals())

  it('shows the Locust tasks, edits weights and writes them only when they differ from the defaults', async () => {
    stubApi({withModel: false})
    const user = userEvent.setup()
    const {container} = render(<main><h1>Load</h1><form><RequestMixEditor prefix="load" samples={samples} context={context} /></form></main>)
    const table = await screen.findByRole('table', {name: 'Request mix by Locust task'})
    expect(within(table).getAllByRole('rowheader').map(cell => cell.textContent)).toEqual(['index in UserBehavior', 'setCurrency in UserBehavior', 'browseProduct in UserBehavior', 'addToCart in UserBehavior', 'viewCart in UserBehavior', 'checkout in UserBehavior', 'Total'])
    expect(within(table).getAllByText('POST /cart/checkout')).toHaveLength(1)
    expect(screen.getByText(/HTTP methods: GET .* · POST/)).toBeInTheDocument()
    expect(hidden(container).value).toBe('')
    await audit()

    const browse = screen.getByRole('spinbutton', {name: 'Weight of browseProduct (%)'})
    expect(browse).toHaveValue(52.63)
    await user.clear(browse)
    await user.type(browse, '0')
    expect(JSON.parse(hidden(container).value)).toMatchObject({browseProduct: 0, checkout: 5.2632})
    expect(screen.getByText('Custom mix: applied by the load generator.')).toBeInTheDocument()

    // Keyboard on the slider.
    const slider = screen.getByRole('slider', {name: 'Weight of checkout slider'})
    slider.focus()
    fireEvent.change(slider, {target: {value: '20'}})
    expect(JSON.parse(hidden(container).value).checkout).toBe(20)

    await user.click(screen.getByRole('button', {name: 'Reset to Locust file defaults'}))
    expect(hidden(container).value).toBe('')
  })

  it('rejects a mix without positive weights', async () => {
    stubApi({withModel: false})
    const user = userEvent.setup()
    const {container} = render(<form><RequestMixEditor prefix="load" samples={samples} context={context} values={{taskWeights: {index: 0, setCurrency: 0, browseProduct: 0, addToCart: 0, viewCart: 0}}} /></form>)
    const checkout = await screen.findByRole('spinbutton', {name: 'Weight of checkout (%)'})
    await user.clear(checkout)
    await user.type(checkout, '0')
    expect(screen.getAllByText('At least one task needs a positive weight.').length).toBeGreaterThan(0)
    expect(container.querySelector('form')!.checkValidity()).toBe(false)
  })

  it('restores saved weights and keeps them unchanged until edited', async () => {
    stubApi({withModel: false})
    const {container} = render(<form><RequestMixEditor prefix="load" samples={samples} context={context} values={{scenarioId: 'storefront-journey', taskWeights: {browseProduct: 0}}} /></form>)
    expect(await screen.findByRole('spinbutton', {name: 'Weight of browseProduct (%)'})).toHaveValue(0)
    expect(screen.getByRole('spinbutton', {name: 'Weight of checkout (%)'})).toHaveValue(11.11)
    expect(JSON.parse(hidden(container).value)).toEqual({browseProduct: 0})
  })

  it('keeps the saved mix when the Locust file cannot be read', async () => {
    stubApi({script: 'print("no locust here")', withModel: false})
    const {container} = render(<form><RequestMixEditor prefix="load" samples={samples} context={context} values={{taskWeights: {browseProduct: 3}}} /></form>)
    expect(await screen.findByRole('alert')).toHaveTextContent('could not be read')
    expect(JSON.parse(hidden(container).value)).toEqual({browseProduct: 3})
  })

  it('explains how to measure the call chain and the time it adds', async () => {
    stubApi({withModel: false})
    const user = userEvent.setup()
    const {container} = render(<main><h1>Load</h1><form><RequestMixEditor prefix="load" samples={samples} context={context} /></form></main>)
    expect(await screen.findByText(/Not measured yet/)).toHaveTextContent('it adds 6 min')
    expect(screen.getByRole('img', {name: 'Application service graph'})).toBeInTheDocument()
    await user.click(screen.getByRole('checkbox', {name: 'Measure call chain before warmup'}))
    expect(screen.getByText(/Adds about 6 min before warmup \(6 tasks × 60 s\)/)).toBeInTheDocument()
    expect(container.querySelector<HTMLInputElement>('input[name="load.profileRps"]')!.value).toBe('10')
    expect(container.querySelector<HTMLInputElement>('input[name="load.profileSecondsPerTask"]')!.value).toBe('60')
    await audit()
  })

  it('predicts per-service rates at the profile peak from the latest call model', async () => {
    const fetch = stubApi()
    render(<main><h1>Load</h1><form><input type="hidden" name="applicationValues.initialReplicas" value="2" readOnly /><input type="hidden" name="applicationValues.replicaOverrides" value='{"currencyservice": 4}' readOnly /><RequestMixEditor prefix="load" samples={samples} context={context} /></form></main>)
    const ranking = await screen.findByRole('table', {name: 'Predicted requests per service at the profile peak'})
    const rows = within(ranking).getAllByRole('row').slice(1).map(row => [...row.querySelectorAll('td')].map(cell => cell.textContent))
    // 100 HTTP req/s; 23/19 requests per execution → frontend 100 req/s, currency 2 × 10/19 executions → 86.96.
    expect(rows[0]).toEqual(['1', 'frontend', '100', expect.any(String), '2', '50', '1.21'])
    expect(rows.find(row => row[1] === 'currencyservice')).toEqual(['3', 'currencyservice', '87', expect.any(String), '4', '21.7', '1.05'])
    expect(rows.find(row => row[1]?.startsWith('node-proxy'))).toEqual(['2', 'node-proxyproxy', '100', '—', '—', '—', '1.21'])
    expect(screen.getByText(/Measured on .* in “Profiling run” from 600 task executions \(100–100 per task\)/)).toBeInTheDocument()
    expect(screen.getByRole('img', {name: /Predicted requests per second by service at a peak of 100/})).toHaveTextContent('87 req/s · 21.7/pod')
    expect(fetch.mock.calls.filter(([url]) => String(url).includes('/download'))).toHaveLength(1)
    await audit()
  })

  it('adds a lazily loaded Request mix tab to the load profile and keeps saved values while it loads', async () => {
    stubApi({withModel: false})
    const user = userEvent.setup()
    const {container} = render(<form><LoadProfileEditor prefix="load" zones={[]} values={{taskWeights: {checkout: 2}, profileCallChain: true}} requestMix={context} /></form>)
    expect(container.querySelector<HTMLInputElement>('input[name="load.taskWeights"]')?.value).toBe('{"checkout":2}')
    await user.click(screen.getByRole('tab', {name: 'Request mix'}))
    expect(await screen.findByRole('table', {name: 'Request mix by Locust task'})).toBeInTheDocument()
    await waitFor(() => expect(screen.getByRole('checkbox', {name: 'Measure call chain before warmup'})).toBeChecked())
    expect(JSON.parse(hidden(container).value)).toEqual({checkout: 2})
  })

  it('shows the virtual users and server-side rate of each rate without renaming the field', async () => {
    stubApi({withModel: false})
    const redirects = [{method: 'POST', path: '/setCurrency'}, {method: 'POST', path: '/cart'}]
    render(<form><LoadProfileEditor prefix="load" zones={[]} values={{steps: [{type: 'constant', durationSeconds: 600, rps: 300}]}} requestMix={{...context, redirects}} /></form>)
    // between(1, 10) waits 5.5 s per task of 23/19 requests: 300 x 5.5 x 19 / 23 users;
    // five of those 23 requests are redirected: 300 x 28 / 23 at the server.
    expect(await screen.findByText('≈ 1,363 virtual users · ≈ 365 req/s server side', {exact: false})).toHaveTextContent('Rate 300 req/s: ≈ 1,363 virtual users · ≈ 365 req/s server side')
    expect(screen.getByLabelText('Requests / second')).toBeInTheDocument()
    await audit()
  })
})
