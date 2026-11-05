import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import type {Application} from '../types'
import {ApplicationGrid, applicationMonogram, applicationServices} from './ApplicationGrid'

function application(name: string, extra: Partial<Application> = {}): Application {
  return {id: name, reference: `dev.example.${name}`, name, description: `${name} demo`, origin: 'built-in', digest: '', descriptor: {spec: {interface: {components: [{id: 'web'}, {id: 'api'}] as never}}}, ...extra}
}

describe('application helpers', () => {
  it('summarizes services and builds a two-letter monogram', () => {
    expect(applicationServices(application('Shop'))).toBe('2 services')
    expect(applicationServices(application('Model', {descriptor: {spec: {valuesSchema: {'x-kubephos-dynamic-topology': true} as never}}}))).toBe('Configurable services')
    expect(applicationMonogram('Bank of Anthos')).toBe('BA')
    expect(applicationMonogram('Bookinfo')).toBe('B')
  })
})

describe('ApplicationGrid', () => {
  it('opens an application from its card and filters by name or description', () => {
    const open = vi.fn()
    render(<ApplicationGrid items={[application('Bookinfo'), application('Online Boutique', {origin: 'imported'})]} isAdmin={false} open={open} remove={async () => true} />)

    const grid = screen.getByRole('list')
    expect(within(grid).getAllByRole('listitem')).toHaveLength(2)
    expect(within(grid).getByText('Imported')).toBeInTheDocument()
    expect(screen.queryByRole('button', {name: /Delete/})).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', {name: 'Bookinfo'}))
    expect(open).toHaveBeenCalledWith(expect.objectContaining({name: 'Bookinfo'}))

    fireEvent.change(screen.getByRole('searchbox', {name: 'Search applications'}), {target: {value: 'boutique'}})
    expect(within(screen.getByRole('list')).getAllByRole('listitem')).toHaveLength(1)
    expect(screen.getByText('1 of 2')).toBeInTheDocument()

    fireEvent.change(screen.getByRole('searchbox', {name: 'Search applications'}), {target: {value: 'nothing'}})
    expect(screen.getByText('No matching applications')).toBeInTheDocument()
  })

  it('lets administrators delete an application without opening it', () => {
    const open = vi.fn()
    const remove = vi.fn(async () => true)
    render(<ApplicationGrid items={[application('Bookinfo')]} isAdmin open={open} remove={remove} />)

    fireEvent.click(screen.getByRole('button', {name: 'Delete Bookinfo'}))
    expect(remove).toHaveBeenCalledWith(expect.objectContaining({name: 'Bookinfo'}))
    expect(open).not.toHaveBeenCalled()
  })
})
