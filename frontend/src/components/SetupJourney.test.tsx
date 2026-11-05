import {describe, expect, it, vi} from 'vitest'
import {fireEvent, render, screen, within} from '@testing-library/react'
import {SetupJourney, setupSteps, type SetupProgress} from './SetupJourney'

const empty: SetupProgress = {isAdmin: true, connections: 0, readyClusters: 0, configurations: 0, completedExperiments: 0}

describe('setupSteps', () => {
  it('starts administrators from the Proxmox connection', () => {
    expect(setupSteps(empty).map(step => step.id)).toEqual(['connection', 'cluster', 'configuration', 'experiment'])
  })

  it('skips the administrator-only connection step for other users', () => {
    expect(setupSteps({...empty, isAdmin: false}).map(step => step.id)).toEqual(['cluster', 'configuration', 'experiment'])
  })

  it('marks steps done from the workspace counts', () => {
    const steps = setupSteps({...empty, connections: 1, readyClusters: 2})
    expect(steps.map(step => step.done)).toEqual([true, true, false, false])
  })
})

describe('SetupJourney', () => {
  it('offers a single action on the first incomplete step', () => {
    const navigate = vi.fn()
    render(<SetupJourney steps={setupSteps({...empty, connections: 1})} navigate={navigate} />)

    expect(screen.getByRole('heading', {name: '3 steps to your first experiment'})).toBeInTheDocument()
    const current = screen.getAllByRole('listitem').find(item => item.getAttribute('aria-current') === 'step')!
    expect(within(current).getByText('Prepare a cluster')).toBeInTheDocument()
    expect(screen.getAllByRole('button')).toHaveLength(1)

    fireEvent.click(within(current).getByRole('button', {name: 'Open clusters'}))
    expect(navigate).toHaveBeenCalledWith('kubernetes')
  })

  it('disappears once every step is complete', () => {
    const {container} = render(<SetupJourney steps={setupSteps({isAdmin: true, connections: 1, readyClusters: 1, configurations: 1, completedExperiments: 1})} navigate={() => {}} />)
    expect(container).toBeEmptyDOMElement()
  })
})
