import {render, screen} from '@testing-library/react'
import axe from 'axe-core'
import {describe, expect, it} from 'vitest'
import type {ManagedResource, Plugin} from '../types'
import {AgentPrerequisites, NetworkPrerequisites, SchedulingPrerequisites} from './SchedulingPrerequisites'

const plugin = (id: string, name: string, requiredComponents: string[] = []) => ({id, name, version: '1', description: '', schema: {type: 'object', properties: {}}, capabilities: ['autoscaler.kubernetes.install'], requiredComponents, artifactInputs: [], runtime: {kind: 'process'}}) as unknown as Plugin
const cpa = plugin('cpa', 'Custom Pod Autoscaler', ['cpa-operator'])
const hpa = plugin('hpa', 'Horizontal Pod Autoscaler')
const cluster = (components: string[]) => ({id: 'c', workspaceId: 'w', name: 'fg-cluster', kind: 'kubernetes-cluster', provider: 'proxmox', status: 'ready', validation: {valid: true, issues: []}, spec: {components}, createdAt: '', updatedAt: ''}) as unknown as ManagedResource
const props = {kind: 'autoscaler', componentTitle: (id: string) => id === 'cpa-operator' ? 'CPA operator' : id, pluginTitle: (item: Plugin) => item.name}

it('explains why the Custom Pod Autoscaler is not offered instead of hiding it', () => {
  render(<SchedulingPrerequisites {...props} plugins={[cpa, hpa]} cluster={cluster(['prometheus'])} hasImage={item => item.id === 'hpa'} />)
  const note = screen.getByRole('note')
  expect(note).toHaveTextContent('Custom Pod Autoscaler needs the CPA operator component on cluster fg-cluster')
  expect(note).not.toHaveTextContent('Horizontal Pod Autoscaler')
  expect(screen.getByRole('link', {name: 'Kubernetes clusters'})).toHaveAttribute('href', '#kubernetes')
})

it('asks for a catalog image once the cluster component is there', () => {
  render(<SchedulingPrerequisites {...props} plugins={[cpa]} cluster={cluster(['cpa-operator'])} hasImage={() => false} />)
  expect(screen.getByRole('note')).toHaveTextContent('runs a strategy image, and none is ready')
})

it('only states the default behaviour when every plugin can be added', () => {
  render(<SchedulingPrerequisites {...props} plugins={[cpa, hpa]} cluster={cluster(['cpa-operator'])} hasImage={() => true} />)
  expect(screen.getByRole('note')).toHaveTextContent(/^Without an autoscaler, every service keeps its initial replica count for the whole run\.$/)
})

it('says what happens without a component of the kind and names a cluster that has the missing component', () => {
  const other = {...cluster(['cpa-operator']), id: 'other', name: 'lab-autoscaling'} as ManagedResource
  render(<SchedulingPrerequisites {...props} plugins={[cpa]} cluster={cluster([])} clusters={[cluster([]), other]} hasImage={() => true} />)
  const note = screen.getByRole('note')
  expect(note).toHaveTextContent('Without an autoscaler, every service keeps its initial replica count')
  expect(note).toHaveTextContent('Or select cluster lab-autoscaling in General, which already has it.')
})

it('explains a kind without any installed plugin instead of leaving the tab empty', () => {
  render(<SchedulingPrerequisites {...props} kind="descheduler" plugins={[]} cluster={cluster([])} hasImage={() => true} />)
  expect(screen.getByRole('note')).toHaveTextContent('No descheduler plugin is installed, so a descheduler cannot be added.')
})

it('drops the default-behaviour note once a component of the kind is configured', () => {
  const {container} = render(<SchedulingPrerequisites {...props} plugins={[]} configured={1} cluster={cluster([])} hasImage={() => true} />)
  expect(container).toBeEmptyDOMElement()
})

describe('network injection and mon-agent requirements', () => {
  const chaos = {...plugin('chaos', 'Network injection', ['chaos-mesh']), capabilities: ['chaos.network.inject']} as Plugin
  it('lists every unmet condition with the place where it is fixed', async () => {
    render(<NetworkPrerequisites plugin={chaos} cluster={cluster([])} zones={1} managed={false} componentTitle={id => id === 'chaos-mesh' ? 'Chaos Mesh' : id} />)
    const note = screen.getByRole('note', {name: 'Network injection requirements'})
    expect(note).toHaveTextContent('needs a cluster provisioned by KubePhos')
    expect(note).toHaveTextContent('has only one')
    expect(note).toHaveTextContent('Network injection needs the Chaos Mesh component on cluster fg-cluster')
    const results = await axe.run(document.body, {runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']}, rules: {'color-contrast': {enabled: false}}})
    expect(results.violations).toEqual([])
  })
  it('is silent when injection can be enabled', () => {
    const {container} = render(<NetworkPrerequisites plugin={chaos} cluster={cluster(['chaos-mesh'])} zones={3} managed componentTitle={id => id} />)
    expect(container).toBeEmptyDOMElement()
  })
  it('explains why the mon-agent cannot be customized', () => {
    render(<AgentPrerequisites plugin={chaos} cluster={cluster([])} managed={false} />)
    expect(screen.getByRole('note', {name: 'Mon-agent customization'})).toHaveTextContent('fg-cluster has none')
  })
})
