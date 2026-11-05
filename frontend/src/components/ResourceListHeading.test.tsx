import {expect, it} from 'vitest'
import {render, screen} from '@testing-library/react'
import {ResourceListHeading, SectionContext} from './ResourceListHeading'

it('introduces the heading with its section group and purpose', () => {
  render(<SectionContext.Provider value={{group: 'Platform', description: 'Clusters available for experiments'}}><ResourceListHeading title="Kubernetes clusters" /></SectionContext.Provider>)

  expect(screen.getByRole('heading', {name: 'Kubernetes clusters'})).toBeInTheDocument()
  expect(screen.getByText('Platform')).toHaveClass('page-eyebrow')
  expect(screen.getByText('Clusters available for experiments')).toHaveClass('page-description')
})

it('renders the bare heading outside the application shell', () => {
  const {container} = render(<ResourceListHeading title="Catalog" />)
  expect(container.querySelectorAll('p')).toHaveLength(0)
})
