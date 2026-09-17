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
  it('orders components by topology index and connects declared dependencies', () => {
    const topology = buildApplicationTopology(components)
    expect(topology?.layers).toEqual([-1, 0, 1, 2])
    expect(topology?.nodes.map(node => node.component.id)).toEqual(['node-proxy', 'frontend', 'worker', 'database'])
    expect(topology?.edges.map(edge => `${edge.source.component.id}->${edge.target.component.id}`)).toEqual(['node-proxy->frontend', 'frontend->worker', 'worker->database'])
  })

  it('renders a scientific export action for PNG and PDF', () => {
    render(<ApplicationTopologyGraph components={components} />)
    const graph = screen.getByRole('img', {name: 'Application component topology'}) as unknown as SVGSVGElement
    expect(graph).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PDF'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PNG 600 DPI'})).toBeInTheDocument()
    expect(graph.querySelectorAll('.application-topology-node circle')).toHaveLength(4)
    expect(graph.querySelectorAll('.application-topology-node rect')).toHaveLength(0)
    const pdf = new TextDecoder().decode(svgToPDF(graph))
    expect(pdf).toContain('/MediaBox [0 0 504')
    expect(pdf).toContain('(node-proxy) Tj')
    expect(pdf).toMatch(/ c\n/)
  })

  it('hides the graph for legacy descriptors without topology metadata', () => {
    const {container} = render(<ApplicationTopologyGraph components={[{...components[0], index: undefined}]} />)
    expect(container).toBeEmptyDOMElement()
  })
})
