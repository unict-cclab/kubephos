import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import type {ApplicationComponent} from '../types'
import {ApplicationTopologyGraph, buildApplicationTopology} from './ApplicationTopologyGraph'
import {svgToPDF} from './ResultsView'
import {figureDimensions} from '../charts'

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

  it('places dependencies below callers and centers a chain', () => {
    const graph = buildApplicationTopology(components)!
    expect(new Set(graph.nodes.map(node => node.x)).size).toBe(1)
    for (const edge of graph.edges) expect(edge.target.y).toBeGreaterThan(edge.source.y)
    expect(buildApplicationTopology([...components].reverse())).toEqual(graph)
  })

  it('preserves cycles, self-links and disconnected services without overlapping labels', () => {
    const graph = buildApplicationTopology([
      {id: 'entry', traits: ['traffic-entrypoint'], dependencies: ['a', 'unknown']},
      {id: 'a', dependencies: ['b']}, {id: 'b', dependencies: ['a', 'b', 'end']},
      {id: 'end'}, {id: 'isolated'},
    ])!
    expect(graph.nodes).toHaveLength(5)
    expect(graph.edges).toHaveLength(5)
    const byID = new Map(graph.nodes.map(node => [node.component.id, node]))
    expect(byID.get('a')!.y).toBe(byID.get('b')!.y)
    expect(byID.get('end')!.y).toBeGreaterThan(byID.get('b')!.y)
    for (let i = 0; i < graph.nodes.length; i++) for (const other of graph.nodes.slice(i + 1)) {
      const node = graph.nodes[i]
      expect(Math.abs(node.y - other.y) >= 80 || Math.abs(node.x - other.x) > (node.labelWidth + other.labelWidth) / 2).toBe(true)
    }
    render(<ApplicationTopologyGraph components={graph.nodes.map(node => node.component)} />)
    for (const edge of document.querySelectorAll('.application-topology-edges path')) expect(edge.getAttribute('d')).not.toMatch(/NaN|Infinity/)
  })

  it('renders a scientific export action for PNG and PDF', () => {
    render(<ApplicationTopologyGraph components={components} />)
    const graph = screen.getByRole('img', {name: 'Application component topology'}) as unknown as SVGSVGElement
    expect(graph).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PDF'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'PNG'})).toBeInTheDocument()
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

  it('keeps a 15-service binary tree symmetric with eight leaves on one level', () => {
    const tree = Array.from({length: 15}, (_, index) => ({id: `service-${index}`, dependencies: [index * 2 + 1, index * 2 + 2].filter(child => child < 15).map(child => `service-${child}`)}))
    const graph = buildApplicationTopology(tree)!
    expect([0, 1, 2, 3].map(row => graph.nodes.filter(node => node.row === row).length)).toEqual([1, 2, 4, 8])
    for (const node of graph.nodes) {
      const children = graph.edges.filter(edge => edge.source === node).map(edge => edge.target)
      if (children.length) expect(node.x).toBeCloseTo((children[0].x + children[1].x) / 2)
      expect(node.x - node.labelWidth / 2).toBeGreaterThan(0)
      expect(node.x + node.labelWidth / 2).toBeLessThan(graph.width)
      expect(node.y + 47).toBeLessThan(graph.height)
    }
    const leaves = graph.nodes.filter(node => node.row === 3)
    for (let index = 1; index < leaves.length; index++) expect(leaves[index].x - leaves[index - 1].x).toBeCloseTo(leaves[1].x - leaves[0].x)
    expect(graph.nodes[0].x).toBeCloseTo(graph.width / 2)
    expect(buildApplicationTopology([...tree].reverse())).toEqual(graph)
    const larger = Array.from({length: 31}, (_, index) => ({id: `service-${index}`, dependencies: [index * 2 + 1, index * 2 + 2].filter(child => child < 31).map(child => `service-${child}`)}))
    expect(buildApplicationTopology(larger)!.width).toBeGreaterThan(graph.width)
    render(<ApplicationTopologyGraph components={tree} />)
    const svg = screen.getByRole('img', {name: 'Application component topology'}) as unknown as SVGSVGElement
    const pdf = new TextDecoder().decode(svgToPDF(svg))
    expect(pdf).toContain(`/MediaBox [0 0 ${graph.width * .75} ${graph.height * .75}]`)
  })

  it('bounds raster memory while preserving content-sized vector exports', () => {
    const size = figureDimensions(12000, 500, true)
    expect(size.pageWidth).toBe(9000)
    expect(size.pageHeight).toBe(375)
    expect(Math.max(size.pixelWidth, size.pixelHeight)).toBeLessThanOrEqual(16384)
    expect(size.pixelWidth * size.pixelHeight).toBeLessThanOrEqual(32000000)
  })

  it('keeps large topology layers on one row for proportional scaling', () => {
    const large = Array.from({length: 40}, (_, index) => ({id: `service-${index}`, index: 1, traits: ['scalable'], dependencies: []}))
    const topology = buildApplicationTopology([{id: 'node-proxy', index: 0, traits: ['traffic-entrypoint'], dependencies: large.map(item => item.id)}, ...large])
    expect(topology).not.toBeNull()
    expect(topology?.height).toBeLessThan(1000)
    expect(new Set(topology?.nodes.map(node => node.x)).size).toBeGreaterThan(4)
    expect(topology?.edges).toHaveLength(40)
    expect(new Set(topology?.nodes.filter(node => node.component.id !== 'node-proxy').map(node => node.y)).size).toBe(1)
  })
})
