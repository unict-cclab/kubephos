import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {StrategyTargetEditor} from './StrategyTargetEditor'

const components = [
  {id: 'frontend', traits: ['schedulable', 'scalable']},
  {id: 'checkout', traits: ['schedulable']},
  {id: 'database', traits: ['scalable']},
]

describe('strategy targets', () => {
  it('selects every compatible service by default and serializes deselections', () => {
    const view = render(<form><StrategyTargetEditor componentKey={4} components={components} trait="schedulable" resetKey="new" /></form>)
    expect(screen.getByText('Compatible services').closest('details')).not.toHaveAttribute('open')
    expect(screen.getByText('2 / 2')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', {name: 'frontend'})).toBeChecked()
    expect(screen.getByRole('checkbox', {name: 'checkout'})).toBeChecked()
    expect(screen.queryByRole('checkbox', {name: 'database'})).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('checkbox', {name: 'checkout'}))
    expect(view.container.querySelector<HTMLInputElement>('input[name="exclude-4"]')?.value).toBe('checkout')
  })

  it('restores an existing restricted selection', () => {
    render(<form><StrategyTargetEditor componentKey={4} components={components} trait="schedulable" resetKey="saved" included={['checkout']} /></form>)
    expect(screen.getByRole('checkbox', {name: 'frontend'})).not.toBeChecked()
    expect(screen.getByRole('checkbox', {name: 'checkout'})).toBeChecked()
  })
  it('supports descheduling for legacy stateless services only', () => {
    const legacy = [
      {id: 'frontend', traits: ['schedulable', 'scalable']},
      {id: 'database', traits: ['schedulable', 'scalable', 'stateful']},
      {id: 'node-proxy', traits: ['traffic-entrypoint']},
    ]
    render(<form><StrategyTargetEditor componentKey={7} components={legacy} trait="deschedulable" resetKey="legacy" /></form>)
    expect(screen.getByText('1 / 1')).toBeInTheDocument()
    expect(screen.getByRole('checkbox', {name: 'frontend'})).toBeChecked()
    expect(screen.queryByRole('checkbox', {name: 'database'})).not.toBeInTheDocument()
    expect(screen.queryByRole('checkbox', {name: 'node-proxy'})).not.toBeInTheDocument()
  })

})
