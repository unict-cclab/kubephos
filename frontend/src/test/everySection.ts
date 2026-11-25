import {vi} from 'vitest'
import type * as ResultsLayout from '../components/results/kubestudio/resultsLayout'

// For tests that look inside the optional sections of a results page: every section is
// shown, as for a user who added them all. Use it as the factory of
// vi.mock('…/results/kubestudio/resultsLayout', async () => (await import('…/test/everySection')).everySection()).
export async function everySection(): Promise<typeof ResultsLayout> {
  const actual = await vi.importActual<typeof ResultsLayout>('../components/results/kubestudio/resultsLayout')
  const sections = actual.pageSections.map(section => section.id)
  return {...actual, useResultsLayout: (...args) => ({...actual.useResultsLayout(...args), sections})}
}
