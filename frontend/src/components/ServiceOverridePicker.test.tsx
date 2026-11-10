import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {ServiceOverridePicker} from './ServiceOverridePicker'

const services = ['api', 'web', 'db'].map(id => ({id}))

describe('service override picker', () => {
  it('lets the user choose the service and blocks services already overridden', () => {
    const onAdd = vi.fn()
    render(<ServiceOverridePicker services={services} taken={id => id === 'api'} onAdd={onAdd} />)
    fireEvent.click(screen.getByRole('button', {name: 'Add service override'}))
    const dialog = within(screen.getByRole('dialog', {name: 'Choose a service'}))
    expect(dialog.getByRole('button', {name: /api/})).toBeDisabled()
    expect(dialog.getByText('Already overridden')).toBeInTheDocument()
    fireEvent.click(dialog.getByRole('button', {name: 'db'}))
    expect(onAdd).toHaveBeenCalledWith('db')
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('is unavailable once every service has an override', () => {
    render(<ServiceOverridePicker services={services} taken={() => true} onAdd={vi.fn()} />)
    expect(screen.getByRole('button', {name: 'Add service override'})).toBeDisabled()
  })

  it('filters long service lists', () => {
    const many = Array.from({length: 12}, (_, index) => ({id: `service-${index}`}))
    render(<ServiceOverridePicker services={many} taken={() => false} onAdd={vi.fn()} />)
    fireEvent.click(screen.getByRole('button', {name: 'Add service override'}))
    fireEvent.change(screen.getByRole('searchbox', {name: 'Filter services'}), {target: {value: 'service-11'}})
    expect(within(screen.getByRole('list', {name: 'Services'})).getAllByRole('button')).toHaveLength(1)
  })
})
