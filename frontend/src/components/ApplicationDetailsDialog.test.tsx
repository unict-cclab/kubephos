import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {request} from '../api'
import type {Application} from '../types'
import {ApplicationDetailsPage} from './ApplicationDetailsDialog'

vi.mock('../api', () => ({request: vi.fn()}))

const application: Application = {
  id: 'dev.example.app', reference: 'app:dev.example.app@1.0.0', name: 'Example app', version: '1.0.0', description: '', origin: 'built-in', digest: 'sha256:test',
  descriptor: {spec: {package: {type: 'git', format: 'plain-yaml'}, interface: {group: 'example', components: [{id: 'api', workload: {apiVersion: 'apps/v1', kind: 'Deployment', name: 'api'}, traits: ['scalable']}]}, defaults: {}}}
}

describe('application details', () => {
  it('loads and presents the validated deployment manifest on demand', async () => {
    vi.mocked(request).mockResolvedValue({content: 'apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n'} as never)
    render(<ApplicationDetailsPage application={application} close={() => {}} canDelete={false} remove={async () => false} />)
    fireEvent.click(screen.getByRole('button', {name: 'View YAML'}))
    expect(await screen.findByLabelText('Deployment manifest YAML')).toHaveTextContent('kind: Deployment')
    expect(request).toHaveBeenCalledWith('/catalog/applications/dev.example.app/1.0.0/manifest')
    expect(screen.getByRole('button', {name: 'Download .yml'})).toBeInTheDocument()
  })
})
