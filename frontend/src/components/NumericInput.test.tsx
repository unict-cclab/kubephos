import {fireEvent, render, screen} from '@testing-library/react'
import {useState} from 'react'
import {describe, expect, it} from 'vitest'
import {NumericInput} from './NumericInput'

function Example() {
  const [value, setValue] = useState(0)
  return <label>Value<NumericInput value={value} min={0} onValueChange={setValue} /></label>
}

describe('NumericInput', () => {
  it('allows replacing zero through an empty intermediate value', () => {
    render(<Example />)
    const field = screen.getByRole('spinbutton', {name: 'Value'})

    fireEvent.change(field, {target: {value: ''}})
    expect(field).toHaveValue(null)
    fireEvent.change(field, {target: {value: '25'}})
    expect(field).toHaveValue(25)
  })

  it('restores the last valid number when an empty field loses focus', () => {
    render(<Example />)
    const field = screen.getByRole('spinbutton', {name: 'Value'})

    fireEvent.change(field, {target: {value: '12'}})
    fireEvent.change(field, {target: {value: ''}})
    fireEvent.blur(field)
    expect(field).toHaveValue(12)
  })
})
