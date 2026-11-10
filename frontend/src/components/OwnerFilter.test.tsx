import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {OwnerFilter, canFilterByOwner, ownerLabel} from './OwnerFilter'

describe('owner filter', () => {
  it('lists unique creators and selects one', () => {
    const onChange = vi.fn()
    const items = [
      {ownerUserId: 'user_b', ownerUsername: 'bruno'},
      {ownerUserId: 'user_a', ownerUsername: 'alice'},
      {ownerUserId: 'user_a', ownerUsername: 'alice'}
    ]

    render(<OwnerFilter items={items} value="" onChange={onChange} />)

    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual(['All users', 'alice', 'bruno'])
    fireEvent.change(screen.getByRole('combobox', {name: 'Filter by user'}), {target: {value: 'user_b'}})
    expect(onChange).toHaveBeenCalledWith('user_b')
    expect(ownerLabel({ownerUserId: 'user_a', ownerUsername: 'alice'})).toBe('alice')
  })

  it('is offered only when creators differ or a choice is active', () => {
    const mine = [{ownerUserId: 'user_a'}, {ownerUserId: 'user_a'}]
    expect(canFilterByOwner(mine, '')).toBe(false)
    expect(canFilterByOwner([...mine, {ownerUserId: 'user_b'}], '')).toBe(true)
    expect(canFilterByOwner(mine, 'user_b')).toBe(true)
  })
})
