import {beforeEach, describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen} from '@testing-library/react'
import {navigationTargets} from '../navigation'
import {CommandPalette, isPaletteShortcut} from './CommandPalette'

const items = navigationTargets(true)

describe('CommandPalette', () => {
  beforeEach(() => {
    HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
    HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
  })

  it('filters sections by label or description and opens the highlighted one with Enter', () => {
    const close = vi.fn()
    const select = vi.fn()
    render(<CommandPalette items={items} close={close} select={select} />)

    const input = screen.getByRole('combobox', {name: 'Jump to'})
    fireEvent.change(input, {target: {value: 'run it'}})
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['RunsConfigure an experiment, run it and compare the results'])

    fireEvent.keyDown(input, {key: 'Enter'})
    expect(close).toHaveBeenCalled()
    expect(select).toHaveBeenCalledWith('results')
  })

  it('moves the highlight with the arrow keys and wraps around', () => {
    const select = vi.fn()
    render(<CommandPalette items={items} close={() => {}} select={select} />)

    const input = screen.getByRole('combobox', {name: 'Jump to'})
    fireEvent.keyDown(input, {key: 'ArrowUp'})
    expect(input).toHaveAttribute('aria-activedescendant', 'command-administration')
    fireEvent.keyDown(input, {key: 'ArrowDown'})
    fireEvent.keyDown(input, {key: 'ArrowDown'})
    expect(screen.getByRole('option', {selected: true})).toHaveTextContent('Infrastructure')
  })

  it('explains when nothing matches', () => {
    render(<CommandPalette items={items} close={() => {}} select={() => {}} />)
    fireEvent.change(screen.getByRole('combobox', {name: 'Jump to'}), {target: {value: 'zzz'}})
    expect(screen.getByText('No sections match “zzz”.')).toBeInTheDocument()
  })
})

it('recognizes the shortcut on every platform', () => {
  expect(isPaletteShortcut({key: 'k', metaKey: true, ctrlKey: false})).toBe(true)
  expect(isPaletteShortcut({key: 'K', metaKey: false, ctrlKey: true})).toBe(true)
  expect(isPaletteShortcut({key: 'k', metaKey: false, ctrlKey: false})).toBe(false)
})
