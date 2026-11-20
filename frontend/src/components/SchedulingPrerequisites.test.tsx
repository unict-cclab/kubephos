import {render, screen} from '@testing-library/react'
import {expect, it} from 'vitest'
import type {ManagedResource, Plugin} from '../types'
import {SchedulingPrerequisites} from './SchedulingPrerequisites'

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

it('stays silent when every plugin can be added', () => {
  const {container} = render(<SchedulingPrerequisites {...props} plugins={[cpa, hpa]} cluster={cluster(['cpa-operator'])} hasImage={() => true} />)
  expect(container).toBeEmptyDOMElement()
})
