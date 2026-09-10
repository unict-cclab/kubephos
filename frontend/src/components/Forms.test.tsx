import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, beforeAll, describe, expect, it, vi} from 'vitest'
import type {Artifact} from '../types'
import {RuntimeDialog} from './Forms'

const artifacts: Artifact[] = [
  artifact('art_executor_endpoint', 'op_executor', 'executor-endpoint', 'OCIExecutorEndpoint', false),
  artifact('art_executor_credential', 'op_executor', 'executor-credential', 'OCIExecutorCredential', true),
  artifact('art_registry_one', 'op_registry_one', 'registry-one', 'RegistryEndpoint', false),
  artifact('art_registry_one_pull', 'op_registry_one', 'registry-one-pull', 'RegistryCredential', true),
  artifact('art_registry_two', 'op_registry_two', 'registry-two', 'RegistryEndpoint', false),
  artifact('art_registry_two_pull', 'op_registry_two', 'registry-two-pull', 'RegistryCredential', true),
]

afterEach(() => vi.unstubAllGlobals())

beforeAll(() => {
  HTMLDialogElement.prototype.showModal = function () { this.setAttribute('open', '') }
  HTMLDialogElement.prototype.close = function () { this.removeAttribute('open') }
})

describe('RuntimeDialog', () => {
  it('validates the resolved profile before activation', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({valid: true, message: 'All runtime gates passed.'}), {status: 200, headers: {'Content-Type': 'application/json'}}))
      .mockResolvedValueOnce(new Response(JSON.stringify({configured: true}), {status: 200, headers: {'Content-Type': 'application/json'}}))
    vi.stubGlobal('fetch', fetch)
    const close = vi.fn()
    const onDone = vi.fn().mockResolvedValue(undefined)
    render(<RuntimeDialog open close={close} runtime={null} session={{authenticated: true, csrfToken: 'csrf'}} applications={[]} artifacts={artifacts} connections={[]} credentials={[]} onDone={onDone} />)

    fireEvent.change(screen.getByRole('combobox', {name: 'Executor endpoint'}), {target: {value: 'art_executor_endpoint'}})
    fireEvent.change(screen.getByRole('combobox', {name: 'Registry endpoint'}), {target: {value: 'art_registry_one'}})
    fireEvent.click(screen.getByRole('button', {name: 'Validate configuration'}))

    await screen.findByText('All runtime gates passed.')
    expect(fetch).toHaveBeenCalledTimes(1)
    expect(fetch.mock.calls[0][0]).toBe('/api/v1/plugin-runtime/validate')
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({
      endpointArtifactId: 'art_executor_endpoint',
      credentialArtifactId: 'art_executor_credential',
      registries: [{endpointArtifactId: 'art_registry_one', credentialArtifactId: 'art_registry_one_pull'}],
    })

    fireEvent.click(screen.getByRole('button', {name: 'Activate runtime'}))
    await waitFor(() => expect(fetch).toHaveBeenCalledTimes(2))
    expect(fetch.mock.calls[1][0]).toBe('/api/v1/plugin-runtime/activate')
    expect(close).toHaveBeenCalledOnce()
    expect(onDone).toHaveBeenCalledWith('Managed OCI runtime activated.')
  })

  it('does not offer the same registry endpoint twice', () => {
    render(<RuntimeDialog open close={() => {}} runtime={null} session={{authenticated: true}} applications={[]} artifacts={artifacts} connections={[]} credentials={[]} onDone={async () => {}} />)
    fireEvent.change(screen.getByRole('combobox', {name: 'Registry endpoint'}), {target: {value: 'art_registry_one'}})
    fireEvent.click(screen.getByRole('button', {name: 'Add registry +'}))
    const selectors = screen.getAllByRole('combobox', {name: 'Registry endpoint'})
    expect(selectors).toHaveLength(2)
    expect(selectors[1].querySelector('option[value="art_registry_one"]')).not.toBeInTheDocument()
    expect(selectors[1].querySelector('option[value="art_registry_two"]')).toBeInTheDocument()
  })
})

function artifact(id: string, operationId: string, name: string, type: string, sensitive: boolean): Artifact {
  return {id, operationId, name, type, version: 'v1alpha1', mediaType: 'application/json', digest: 'sha256:test', sizeBytes: 1, sensitive, verifiedAt: '2026-09-10T00:00:00Z'}
}
