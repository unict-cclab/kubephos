import {render, screen} from '@testing-library/react'
import {expect, it} from 'vitest'
import {ResourceAttribution} from './ResourceAttribution'
import {formatDate} from '../lib'

it('uses the same author/date labels and formatting for editable resources', () => {
  const resource = {createdBy: 'angelo.marchese', modifiedBy: 'editor', createdAt: '2026-10-20T10:00:00Z', updatedAt: '2026-10-20T12:00:00Z'}
  render(<ResourceAttribution resource={resource} editable />)
  expect(screen.getByText('Created by')).toBeInTheDocument()
  expect(screen.getByText('Created')).toBeInTheDocument()
  expect(screen.getByText('Last updated by')).toBeInTheDocument()
  expect(screen.getByText('Last updated')).toBeInTheDocument()
  expect(screen.getByText(formatDate(resource.createdAt))).toHaveAttribute('datetime', resource.createdAt)
  expect(screen.getByText(formatDate(resource.updatedAt))).toHaveAttribute('datetime', resource.updatedAt)
})

it('keeps immutable resource metadata pertinent and tolerates legacy missing dates', () => {
  render(<ResourceAttribution resource={{createdBy: 'angelo.marchese'}} />)
  expect(screen.queryByText('Last updated by')).not.toBeInTheDocument()
  expect(screen.getByText('—')).toBeInTheDocument()
  expect(formatDate('invalid')).toBe('—')
})
