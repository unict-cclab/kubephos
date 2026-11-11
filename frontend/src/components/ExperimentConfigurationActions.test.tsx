import {fireEvent, render, screen, within} from '@testing-library/react'
import {beforeAll, beforeEach, describe, expect, it} from 'vitest'
import type {ExperimentConfiguration} from '../types'
import {ExperimentConfigurationsView} from './ExperimentConfigurationsView'

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
  HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
})

const item: ExperimentConfiguration = {id: 'cfg_1', workspaceId: 'ws_1', clusterResourceId: 'cluster_1', name: 'Simple load', description: '', applicationRef: 'app@1', applicationDigest: 'sha256:test', definition: {applicationValues: {}, components: []}, validation: {valid: true, issues: []}, createdAt: '2026-09-15T00:00:00Z', updatedAt: '2026-09-15T00:00:00Z'}
const props = {items: [item], experiments: [], clusters: [], workspaces: [], applications: [], plugins: [], strategies: [], session: {authenticated: true, user: {id: 'admin', username: 'admin', role: 'admin'} as const}, isAdmin: true, changed: async () => {}, openOperation: () => {}}

describe('experiment configuration actions', () => {
  beforeEach(() => {window.location.hash = '#experiments'})

  it('keeps only eye and trash in the list and launches runs from detail', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    expect(screen.getByRole('button', {name: 'View Simple load details'}).textContent).toBe('')
    expect(screen.getByRole('button', {name: 'Delete Simple load'}).textContent).toBe('')
    expect(screen.queryByRole('button', {name: 'Run'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))
    expect(screen.getByRole('button', {name: 'Run experiment'})).toBeInTheDocument()
    expect(screen.queryByText('Input load')).not.toBeInTheDocument()
    expect(screen.queryByText('Setup')).not.toBeInTheDocument()
    expect(screen.queryByText('Geographic phases')).not.toBeInTheDocument()
    expect(screen.queryByText('All entered data')).not.toBeInTheDocument()
    expect(screen.queryByText('Exact saved configuration')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Run experiment'}))
    expect(view.container.querySelector('dialog[open]')).toBeInTheDocument()
    expect(screen.getByRole('spinbutton', {name: 'Number of executions'})).toBeInTheDocument()
  })

  it('opens configuration editing as a page with the saved values', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))
    fireEvent.click(screen.getByRole('button', {name: 'Edit configuration'}))

    expect(screen.getByRole('heading', {name: 'Edit configuration'})).toBeInTheDocument()
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('Simple load')
    expect(screen.getByRole('navigation', {name: 'Setup steps'})).toBeInTheDocument()
    expect(view.container.querySelector('dialog[open]')).not.toBeInTheDocument()
    expect(screen.queryByText('Changes apply only to experiments created after this update.')).not.toBeInTheDocument()
  })

  it('opens the complete saved configuration in read-only mode', () => {
    const view = render(<ExperimentConfigurationsView {...props} />)
    fireEvent.click(screen.getByRole('button', {name: 'View Simple load details'}))

    expect(screen.getByRole('navigation', {name: 'Configuration sections'})).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'View configuration'})).not.toBeInTheDocument()
    expect(screen.getByRole('heading', {name: 'Simple load'})).toBeInTheDocument()
    expect(screen.queryByRole('textbox', {name: 'Name'})).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: 'Validate and update'})).not.toBeInTheDocument()
    expect(view.container.querySelector('dialog[open]')).not.toBeInTheDocument()
    expect(screen.queryByText('Complete saved configuration in read-only mode.')).not.toBeInTheDocument()
  })
})

it.each([0,1])('blocks creation of a comparison with only %i single configurations',count=>{
 window.location.hash='#experiments'
 render(<ExperimentConfigurationsView {...props} clusters={[{id:"ready",status:"ready"} as never]} items={Array.from({length:count},(_,i)=>({...item,id:`cfg_${i}`}))}/>)
 fireEvent.click(screen.getByRole('button',{name:'New configuration'}))
 expect(screen.getByRole('button',{name:'Comparison'})).toBeDisabled()
})
it('enables comparisons with two valid singles and excludes suites or invalid sources',()=>{
 window.location.hash='#experiments'
 const items=[item,{...item,id:'cfg_2'}, {...item,id:'suite',kind:'suite' as const}, {...item,id:'invalid',validation:{valid:false,issues:[]}}]
 render(<ExperimentConfigurationsView {...props} clusters={[{id:"ready",status:"ready"} as never]} items={items}/>)
 fireEvent.click(screen.getByRole('button',{name:'New configuration'}))
 expect(screen.getByRole('button',{name:'Comparison'})).toBeEnabled()
 fireEvent.click(screen.getByRole('button',{name:'Comparison'}))
 fireEvent.click(screen.getByRole('button',{name:'Add configurations'}))
 fireEvent.change(screen.getByLabelText('Filter by user'),{target:{value:''}})
 expect(within(screen.getByRole('dialog',{name:'Add configurations'})).getAllByRole('checkbox')).toHaveLength(2)
})
