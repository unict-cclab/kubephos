import {render, screen} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {JsonPreview} from './JsonPreview'

describe('JSON table alignment', () => {
  it('aligns each header with values according to the column data type', () => {
    render(<JsonPreview value={[
      {service: 'frontend', replicas: 2, ready: true},
      {service: 'checkout', replicas: 12, ready: false}
    ]} />)

    expect(screen.getByRole('columnheader', {name: 'Service'})).toHaveClass('text-column')
    expect(screen.getByRole('columnheader', {name: 'Replicas'})).toHaveClass('numeric-column')
    expect(screen.getByRole('columnheader', {name: 'Ready'})).toHaveClass('boolean-column')
    expect(screen.getByRole('cell', {name: '12'})).toHaveClass('numeric-column')
    expect(screen.getByRole('cell', {name: 'No'})).toHaveClass('boolean-column')
  })
})
