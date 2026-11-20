import {render, screen} from '@testing-library/react'
import {expect, it} from 'vitest'
import type {ManagedResource, Plugin} from '../types'
import {SchedulingPrerequisites} from './ExperimentConfigurationsView'

const autoscaler = {id: 'autoscaler', name: 'Autoscaler', version: '1', description: '', schema: {type: 'object', properties: {}}, capabilities: ['autoscaler.kubernetes.install'], requiredComponents: ['cpa-operator'], artifactInputs: [], runtime: {kind: 'process'}} as unknown as Plugin
const cluster = (components: string[]) => ({id: 'c', workspaceId: 'w', name: 'fg-cluster', kind: 'kubernetes-cluster', provider: 'proxmox', status: 'ready', validation: {valid: true, issues: []}, spec: {components}, createdAt: '', updatedAt: ''}) as unknown as ManagedResource

it('explains a missing cluster component and a missing strategy image instead of hiding the autoscaler', () => {
  render(<SchedulingPrerequisites kind="autoscaler" plugins={[autoscaler]} cluster={cluster(['prometheus'])} componentTitle={id => id === 'cpa-operator' ? 'CPA operator' : id} readyStrategies={0} />)
  const note = screen.getByRole('note')
  expect(note).toHaveTextContent('needs the CPA operator component on cluster fg-cluster')
  expect(note).toHaveTextContent('No autoscaler image is ready')
  expect(screen.getByRole('link', {name: 'Kubernetes clusters'})).toHaveAttribute('href', '#kubernetes')
})

it('stays silent when the autoscaler can be added', () => {
  const {container} = render(<SchedulingPrerequisites kind="autoscaler" plugins={[autoscaler]} cluster={cluster(['cpa-operator'])} componentTitle={id => id} readyStrategies={1} />)
  expect(container).toBeEmptyDOMElement()
})
