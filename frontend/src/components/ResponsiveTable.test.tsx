import {render, screen, within} from '@testing-library/react'
import {expect, it} from 'vitest'
import {ResponsiveTable} from './ResponsiveTable'

it('keeps semantic column and group headers and maps responsive value labels after updates', () => {
  const table = (mean: number) => <ResponsiveTable aria-label="Metrics"><thead><tr><th>Metric</th><th>Mean</th><th>Samples</th></tr></thead><tbody><tr><th scope="rowgroup" colSpan={3}>s0</th></tr><tr><td>Latency</td><td>{mean} ms</td><td>8</td></tr></tbody></ResponsiveTable>
  const view = render(table(10))
  expect(within(screen.getByRole('table', {name: 'Metrics'})).getAllByRole('columnheader')).toHaveLength(3)
  expect(screen.getByText('s0')).toHaveAttribute('scope', 'rowgroup')
  expect(screen.getByText('10 ms')).toHaveAttribute('data-label', 'Mean')
  view.rerender(table(20))
  expect(screen.getByText('20 ms')).toHaveAttribute('data-label', 'Mean')
  expect(screen.getByText('8')).toHaveAttribute('data-label', 'Samples')
})
