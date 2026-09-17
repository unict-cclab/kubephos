import {render, screen} from '@testing-library/react'
import {beforeEach, describe, expect, it} from 'vitest'
import type {CatalogStrategy} from '../types'
import {StrategyCatalog} from './StrategyCatalog'

const strategy = (kind: CatalogStrategy['kind'], name: string): CatalogStrategy => ({id: name, workspaceId: 'workspace', harborResourceId: 'harbor', name, kind, sourceImage: `example.org/${name}:v1`, defaultConfiguration: {}, operationId: 'operation', status: 'ready', createdAt: '', updatedAt: ''})
const items = [strategy('scheduler', 'sample-scheduler'), strategy('descheduler', 'sample-descheduler'), strategy('autoscaler', 'sample-autoscaler')]

describe('catalog categories', () => {
  beforeEach(() => {window.location.hash = '#catalog'})

  it.each([['scheduler', 'sample-scheduler'], ['descheduler', 'sample-descheduler'], ['autoscaler', 'sample-autoscaler']] as const)('shows only %s entries in its tab', (kind, name) => {
    render(<StrategyCatalog kind={kind} items={items} workspaces={[]} services={[]} session={{authenticated: true}} isAdmin={false} changed={async () => {}} openOperation={() => {}} />)
    expect(screen.getByText(name)).toBeInTheDocument()
    for (const item of items.filter(item => item.kind !== kind)) expect(screen.queryByText(item.name)).not.toBeInTheDocument()
    expect(screen.queryByText(/strategy images/i)).not.toBeInTheDocument()
  })

  it('keeps only view and guarded delete actions on an imported image', () => {
    render(<StrategyCatalog kind="scheduler" items={[{...items[0], status: 'succeeded'}]} workspaces={[]} services={[]} session={{authenticated: true}} isAdmin changed={async () => {}} openOperation={() => {}} />)
    expect(screen.getByRole('button', {name: 'View sample-scheduler details'})).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Delete sample-scheduler'})).toBeEnabled()
    expect(screen.queryByRole('button', {name: 'Activity'})).not.toBeInTheDocument()
  })
})
