import {fireEvent, render, screen} from '@testing-library/react'
import {afterEach, describe, expect, it, vi} from 'vitest'
import type {Operation} from '../types'
import {OperationDrawer} from './OperationDrawer'

afterEach(() => vi.unstubAllGlobals())

describe('OperationDrawer', () => {
  it('previews verified JSON artifacts without exposing protected artifacts', async () => {
    const operation: Operation = {
      id: 'op_test', workspaceId: 'ws_test', pluginId: 'dev.example.console', pluginVersion: '1.0.0', title: 'Inspect', status: 'succeeded', planHash: 'hash',
      plan: {steps: []}, validation: {valid: true, issues: []}, steps: [], createdAt: '2026-01-01T00:00:00Z',
      artifacts: [
        {id: 'art_public', operationId: 'op_test', name: 'inventory', type: 'Inventory', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:public', sizeBytes: 20, sensitive: false, verifiedAt: '2026-01-01T00:00:00Z'},
        {id: 'art_secret', operationId: 'op_test', name: 'access', type: 'Access', version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:secret', sizeBytes: 20, sensitive: true, verifiedAt: '2026-01-01T00:00:00Z'}
      ]
    }
    const fetch = vi.fn(async (input: RequestInfo | URL) => {
      const path = String(input)
      if (path.endsWith('/logs')) return new Response(JSON.stringify({items: []}), {status: 200})
      if (path.endsWith('/download')) return new Response(JSON.stringify({items: ['one', 'two']}), {status: 200})
      return new Response(JSON.stringify(operation), {status: 200})
    })
    vi.stubGlobal('fetch', fetch)
    render(<OperationDrawer operationID="op_test" session={{authenticated: true}} plugins={[]} close={() => undefined} open={() => undefined} changed={async () => undefined} notify={() => undefined} />)

    fireEvent.click(await screen.findByRole('button', {name: 'Preview'}))

    expect(await screen.findByText(/"one"/)).toBeInTheDocument()
    expect(screen.getByText(/protected/)).toBeInTheDocument()
    expect(screen.getAllByRole('button', {name: 'Preview'})).toHaveLength(1)
  })
})
