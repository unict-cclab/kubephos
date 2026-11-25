import {describe, expect, it} from 'vitest'
import type {AggregateSeries} from '../../../resultsData'
import {provenanceFor} from '../runs'
import type {StudioData} from './image'
import {buildSuggestions} from './suggestions'

const curve = (label: string, unit: string, values: number[]): AggregateSeries => ({artifactID: label, label, unit, stepMilliseconds: 15000, runs: 1, points: values.map((value, index) => ({timestamp: new Date(index * 15000).toISOString(), value}))})
const base: StudioData = {metrics: ['load.p95_response_time'], plot: metric => metric === 'load.p95_response_time' ? [curve('Run', 'milliseconds', [200, 220, 240])] : [], runLevel: [], ladder: [], fidelity: [], services: () => undefined, serviceMetrics: [], provenance: name => provenanceFor([], name), omitSLO: true}
const groups = [{id: 'a', label: 'Run', color: '#1f77b4'}]
const why = (data: StudioData, id: string) => buildSuggestions(data, groups).find(item => item.id === id)?.why

describe('KubeStudio suggestions', () => {
  it('always offers the latency over time, with its mean, for one run without findings', () => {
    expect(buildSuggestions(base, groups).map(item => [item.id, item.why])).toEqual([['latency-time', 'P95 latency — client (Locust) averaged 220 ms → its course over the measured window']])
  })

  it('tells a load shortfall, an over-delivery and a delivered load apart', () => {
    const fidelity = (offered: number, achieved: number): StudioData => ({...base, fidelity: [{...curve('Run', 'requests_per_second', [offered]), role: 'offered'}, {...curve('Run', 'requests_per_second', [achieved]), role: 'achieved'}]})
    expect(why(fidelity(200, 130), 'fidelity')).toBe('Achieved 65 % of the target rate → offered vs achieved load')
    expect(why(fidelity(100, 178), 'fidelity')).toBe('Sent 178 % of the target rate, more than asked → offered vs achieved load')
    expect(why(fidelity(100, 99), 'fidelity')).toBe('Achieved 99 % of the target rate → show the load was delivered')
    const ranks = (data: StudioData) => buildSuggestions(data, groups).map(item => item.id)
    expect(ranks(fidelity(200, 130))[0]).toBe('fidelity')
    expect(ranks(fidelity(100, 99))[0]).toBe('latency-time')
  })

  it('suggests replicas over time against the load whenever the replicas changed', () => {
    const plots: Record<string, AggregateSeries[]> = {'deployment.ready_replicas': [curve('Run', 'replicas', [2, 2, 4])], 'load.achieved_rps': [curve('Run', 'requests_per_second', [50, 80, 90])]}
    const scaled: StudioData = {...base, metrics: [...base.metrics, ...Object.keys(plots)], plot: metric => plots[metric] ?? base.plot(metric)}
    const suggestion = buildSuggestions(scaled, groups).find(item => item.id === 'scaling')!
    expect(suggestion.why).toBe('Ready replicas went from 2 to 4 → replicas over time against the request rate')
    expect(suggestion.chart.spec?.panels[0].metrics).toEqual([{metric: 'deployment.ready_replicas', axis: 'left'}, {metric: 'load.achieved_rps', axis: 'right'}])
  })
})
