import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {ApplicationComponent} from '../types'
import {ApplicationTopologyGraph, buildApplicationTopology} from './ApplicationTopologyGraph'
import {svgToPDF} from './ResultsView'

const components: ApplicationComponent[] = [
  {id: 'database', index: 2, dependencies: [], traits: ['scalable'], workload: {apiVersion: 'apps/v1', kind: 'Deployment', name: 'database'}},
  {id: 'frontend', index: 0, dependencies: ['worker'], traits: ['scalable'], workload: {apiVersion: 'apps/v1', kind: 'Deployment', name: 'frontend'}},
  {id: 'node-proxy', index: 0, dependencies: ['frontend'], traits: ['traffic-entrypoint'], workload: {apiVersion: 'apps/v1', kind: 'DaemonSet', name: 'node-proxy'}},
  {id: 'worker', index: 1, dependencies: ['database'], traits: ['scalable'], workload: {apiVersion: 'apps/v1', kind: 'Deployment', name: 'worker'}}
]

describe('application topology graph', () => {
  it('orders connected components without exposing index layers', () => {
    const topology = buildApplicationTopology(components)
    expect(topology?.nodes.map(node => node.component.id)).toEqual(['node-proxy', 'frontend', 'worker', 'database'])
    expect(topology?.edges.map(edge => `${edge.source.component.id}->${edge.target.component.id}`)).toEqual(['node-proxy->frontend', 'frontend->worker', 'worker->database'])
  })

  it('renders a scientific export action for PNG and PDF', () => {
    render(<ApplicationTopologyGraph components={components} />)
    const graph = screen.getByRole('img', {name: 'Application component topology'}) as unknown as SVGSVGElement
    expect(graph).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PDF'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PNG 600 DPI'})).toBeInTheDocument()
    expect(screen.queryByText('How services connect')).not.toBeInTheDocument()
    expect(screen.queryByText('Arrows show declared downstream dependencies')).not.toBeInTheDocument()
    expect(graph.querySelectorAll('.application-topology-node circle')).toHaveLength(4)
    expect(graph.querySelectorAll('.application-topology-node rect')).toHaveLength(4)
    expect(graph.querySelector('.application-topology-label-bg')).toHaveAttribute('fill', '#ffffff')
    expect(graph.querySelector('.application-topology-label-bg')).toHaveAttribute('stroke', '#dfe4eb')
    expect(graph.querySelectorAll('.application-topology-layer')).toHaveLength(0)
    expect(graph.querySelectorAll('.application-topology-edges path')).toHaveLength(3)
    expect(screen.getByText('3 connections')).toBeInTheDocument()
    const pdf = new TextDecoder().decode(svgToPDF(graph))
    expect(pdf).toMatch(/\/MediaBox \[0 0 [\d.]+ [\d.]+\]/)
    expect(pdf).toContain('(node-proxy) Tj')
    expect(pdf.match(/0\.1882 0\.2196 0\.2588 rg/g)).toHaveLength(4)
    expect(pdf.match(/\nS\n/g)?.length).toBeGreaterThanOrEqual(3)
  })

  it('renders applications without index metadata', () => {
    const {container} = render(<ApplicationTopologyGraph components={[{...components[0], index: undefined}]} />)
    expect(container.querySelectorAll('.application-topology-node')).toHaveLength(1)
  })

  it('keeps complete service names in publication exports', () => {
    render(<ApplicationTopologyGraph components={[{id: 'a-very-long-product-catalog-service-name', traits: ['scalable'], dependencies: []}]} />)
    expect(screen.getAllByText('a-very-long-product-catalog-service-name')).toHaveLength(2)
    expect(screen.queryByText(/…/)).not.toBeInTheDocument()
  })

  it('packs large topology layers into bounded rows', () => {
    const large = Array.from({length: 40}, (_, index) => ({id: `service-${index}`, index: 1, traits: ['scalable'], dependencies: []}))
    const topology = buildApplicationTopology([{id: 'node-proxy', index: 0, traits: ['traffic-entrypoint'], dependencies: large.map(item => item.id)}, ...large])
    expect(topology).not.toBeNull()
    expect(topology?.height).toBeLessThan(1000)
    expect(new Set(topology?.nodes.map(node => node.x)).size).toBeGreaterThan(4)
    expect(topology?.edges).toHaveLength(40)
    expect(topology?.height).toBeLessThan(1800)
  })
})
