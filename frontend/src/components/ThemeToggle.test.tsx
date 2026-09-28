import {fireEvent, render, screen} from '@testing-library/react'
import {afterEach, describe, expect, it} from 'vitest'
import {ThemeToggle} from './ThemeToggle'

describe('theme toggle', () => {
  afterEach(() => {
    document.documentElement.dataset.theme = 'light'
    document.documentElement.style.colorScheme = ''
    window.localStorage.clear()
  })

  it('selects and persists dark mode', () => {
    document.documentElement.dataset.theme = 'light'
    render(<ThemeToggle />)
    fireEvent.click(screen.getByRole('button', {name: 'Use dark mode'}))
    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('kubephos-theme')).toBe('dark')
    expect(screen.getByRole('button', {name: 'Use light mode'})).toHaveAttribute('aria-pressed', 'true')
  })

  it('restores the persisted theme after a refresh', () => {
    delete document.documentElement.dataset.theme
    window.localStorage.setItem('kubephos-theme', 'dark')

    render(<ThemeToggle />)

    expect(document.documentElement.dataset.theme).toBe('dark')
    expect(document.documentElement.style.colorScheme).toBe('dark')
    expect(window.localStorage.getItem('kubephos-theme')).toBe('dark')
    expect(screen.getByRole('button', {name: 'Use light mode'})).toBeInTheDocument()
  })
})
