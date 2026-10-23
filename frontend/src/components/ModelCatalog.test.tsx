import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {ModelCatalog} from './ModelCatalog'
import type {CatalogModel} from '../types'

const model: CatalogModel = {id: 'MyModel-1B', url: 'https://models.example.org/model.gguf', sha256: 'a'.repeat(64), size: 1024, backend: 'llama-cpp', quantization: 'Q4_K_M', origin: 'custom', enabled: true}
const response = (value: unknown, status = 200) => new Response(JSON.stringify(value), {status, headers: {'Content-Type': 'application/json'}})

describe('Model catalog', () => {
  beforeEach(() => {window.location.hash = '#catalog'; HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}; HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}})
  it('registers a custom model and refreshes application choices', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(response({items: []})).mockResolvedValueOnce(response(model, 201)).mockResolvedValueOnce(response({items: [model]}))
    vi.stubGlobal('fetch', fetch)
    const changed = vi.fn().mockResolvedValue(undefined)
    render(<ModelCatalog session={{authenticated: true, csrfToken: 'csrf'}} isAdmin changed={changed} />)
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: 'Add model'}))
    fireEvent.change(screen.getByRole('textbox', {name: 'GGUF download URL'}), {target: {value: model.url}})
    expect(screen.queryByRole('textbox', {name: 'SHA-256'})).not.toBeInTheDocument()
    expect(screen.queryByRole('spinbutton', {name: 'File size (bytes)'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Save model'}))
    await waitFor(() => expect(changed).toHaveBeenCalledOnce())
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({url: model.url})
    expect(screen.getByText(model.id)).toBeInTheDocument()
  })
  it('offers archived models for editing and restoration', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({items: [{...model, enabled: false}]})))
    render(<ModelCatalog session={{authenticated: true}} isAdmin changed={async () => {}} />)
    await screen.findByText('Archived')
    fireEvent.click(screen.getByRole('button', {name: `Edit ${model.id}`}))
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveAttribute('readonly')
    expect(screen.getByRole('checkbox', {name: 'Enabled'})).not.toBeChecked()
  })
  it('allows non-administrators to view models without mutation controls', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({items: [model]})))
    render(<ModelCatalog session={{authenticated: true}} isAdmin={false} changed={async () => {}} />)
    await screen.findByText(model.id)
    expect(screen.queryByRole('button', {name: 'Add model'})).not.toBeInTheDocument()
    expect(screen.queryByRole('button', {name: `Edit ${model.id}`})).not.toBeInTheDocument()
  })
})

it('filters model rows by name and quantization without changing the catalog', async () => {
  const fetch = vi.fn().mockResolvedValue(response({items: [model, {...model, id: 'Other-3B', quantization: 'Q8_0'}]}))
  vi.stubGlobal('fetch', fetch)
  render(<ModelCatalog session={{authenticated: true}} isAdmin={false} changed={async () => {}} />)
  await screen.findByText(model.id)
  fireEvent.change(screen.getByRole('searchbox', {name: 'Search ai models'}), {target: {value: 'Q8_0'}})
  expect(screen.queryByText(model.id)).not.toBeInTheDocument()
  expect(screen.getByText('Other-3B')).toBeInTheDocument()
  expect(fetch).toHaveBeenCalledOnce()
})

it('opens labeled model details from the table for a read-only user', async () => {
  window.location.hash = '#catalog'
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response({items: [model]})))
  render(<ModelCatalog session={{authenticated: true}} isAdmin={false} changed={async () => {}} />)
  await screen.findByRole('table', {name: 'AI models'})
  fireEvent.click(screen.getByRole('button', {name: `View ${model.id} details`}))
  expect(screen.getByRole('heading', {name: model.id})).toBeInTheDocument()
  expect(screen.getByText('Download URL')).toBeInTheDocument()
  expect(screen.getByText(model.url)).toBeInTheDocument()
  expect(screen.getByText('SHA-256')).toBeInTheDocument()
  expect(screen.queryByRole('button', {name: `Edit ${model.id}`})).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Back to Catalog'}))
  expect(screen.getByRole('table', {name: 'AI models'})).toBeInTheDocument()
})
