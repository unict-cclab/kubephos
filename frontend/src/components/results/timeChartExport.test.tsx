import {cleanup, render} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {exportChart} from '../../charts'
import {TimeChart, type ChartPanel} from './TimeChart'
import {downloadTimeChart, flattenTimeChart} from './timeChartExport'

vi.mock('../../charts', async original => ({...await original<typeof import('../../charts')>(), exportChart: vi.fn(async () => {})}))
afterEach(() => {cleanup(); vi.mocked(exportChart).mockReset()})

const points = (values: number[]) => values.map((v, index) => ({t: index * 15000, v}))
const panels: ChartPanel[] = [
  {id: 'rate', title: 'Request rate, Locust side (req/s)', unit: 'requests_per_second', series: [{id: 'a', label: 'Achieved', color: 'rgb(31, 119, 180)', points: points([150, 160])}, {id: 'o', label: 'Offered (target)', color: 'rgb(17, 17, 17)', dashed: true, points: points([170, 175])}]},
  {id: 'pods', title: 'Ready pods', unit: 'replicas', series: [{id: 'p', label: 'Ready pods', color: 'rgb(213, 94, 0)', points: points([12, 15])}]}
]

describe('Load and latency download', () => {
  it('flattens the on-screen panels into one standalone SVG with explicit colours', () => {
    const {container} = render(<TimeChart panels={panels} ariaLabel="Load" />)
    const svg = flattenTimeChart(container.querySelector('.time-chart') as HTMLElement, 'export', 'Load and latency')
    expect(svg.id).toBe('export')
    expect([...svg.querySelectorAll('text')].map(item => item.textContent)).toEqual(expect.arrayContaining(['Load and latency', 'Request rate, Locust side (req/s)', 'Ready pods']))
    // One group per panel, stacked below the titles.
    const groups = [...svg.querySelectorAll(':scope > g')]
    expect(groups.map(group => group.getAttribute('transform'))).toEqual([expect.stringMatching(/^translate\(0 \d+/), expect.stringMatching(/^translate\(0 \d+/)])
    expect(svg.querySelector('[class], [style]')).toBeNull()
    expect([...svg.querySelectorAll('path')].map(path => path.getAttribute('stroke'))).toContain('#1f77b4')
  })

  it('exports through the shared exporter and removes the temporary SVG', async () => {
    const {container} = render(<TimeChart panels={panels} ariaLabel="Load" />)
    await downloadTimeChart(container.querySelector('.time-chart') as HTMLElement, 'svg', {fileName: 'kubephos-load-and-latency', title: 'Load and latency over time'})
    const [id, metric, format] = vi.mocked(exportChart).mock.calls[0]
    expect([metric, format]).toEqual(['kubephos-load-and-latency', 'svg'])
    expect(document.getElementById(id)).toBeNull()
  })
})
