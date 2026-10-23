import {render, screen} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import axe from 'axe-core'
import {afterEach, describe, expect, it, vi} from 'vitest'
import {Dialog} from '../components/Dialog'
import {FormTabs} from '../components/FormTabs'
import {WizardForm, WizardReview, WizardStep} from '../components/WizardForm'
import {ResourceListTable} from '../components/ResourceListTable'
import {ResourceSharing} from '../components/ResourceSharing'
import {LoadProfileEditor} from '../components/LoadProfileEditor'
import {SchemaFields} from '../components/SchemaFields'

afterEach(() => vi.unstubAllGlobals())

// jsdom has no layout or canvas. Contrast and responsive layout are verified in the browser.
async function audit() {
  const results = await axe.run(document.body, {
    runOnly: {type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21aa', 'wcag22aa']},
    rules: {'color-contrast': {enabled: false}}
  })
  expect(results.violations.map(({id, nodes}) => ({id, targets: nodes.map(node => node.target)}))).toEqual([])
}

describe('shared console accessibility', () => {
  it('keeps wizard steps, tabs, validation and review accessible', async () => {
    const user = userEvent.setup()
    render(<main><h1>KubePhos</h1><Dialog open title="Create cluster" presentation="page" onClose={() => {}}><WizardForm onCancel={() => {}} submitLabel="Create cluster">
      <WizardStep title="Cluster"><label>Name<input name="name" required /></label></WizardStep>
      <WizardStep title="Pools"><FormTabs label="Node pools" items={[
        {id: 'master', title: 'Master', content: <label>Master nodes<input name="master" type="number" min="1" defaultValue="1" /></label>},
        {id: 'applications', title: 'Applications', content: <label>Application nodes<input name="applications" type="number" min="1" defaultValue="2" /></label>}
      ]} /></WizardStep><WizardStep title="Review"><WizardReview /></WizardStep>
    </WizardForm></Dialog></main>)
    await audit()
    await user.type(screen.getByRole('textbox', {name: 'Name'}), 'lab')
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await audit()
    await user.click(screen.getByRole('tab', {name: 'Applications'}))
    await audit()
    await user.click(screen.getByRole('button', {name: 'Continue'}))
    await screen.findByText('lab')
    await audit()
  })

  it('keeps search, sorting and optional table fields accessible', async () => {
    const user = userEvent.setup()
    render(<main><h1>Configurations</h1><ResourceListTable items={[{id: 'a', name: 'Baseline', createdAt: '', updatedAt: '', createdBy: 'alice'}]} label="Configurations" columns={[{label: 'Name', render: item => item.name}]} searchText={item => item.name} actions={() => <button aria-label="View Baseline">View</button>} /></main>)
    await audit()
    await user.click(screen.getByLabelText('Table display'))
    await audit()
    await user.type(screen.getByRole('searchbox'), 'missing')
    await audit()
  })

  it('audits the actual sharing dialog with selected recipients', async () => {
    const user = userEvent.setup()
    vi.stubGlobal('fetch', vi.fn(async (path: string) => new Response(JSON.stringify(path.endsWith('/sharing/users') ? {items: [{id: 'bob', username: 'bob'}]} : {visibility: 'private', userIds: []}), {status: 200})))
    render(<ResourceSharing kind="experiments" id="experiment" name="Baseline" ownerUserId="alice" session={{authenticated: true, user: {id: 'alice', username: 'alice', role: 'user'}}} changed={async () => {}} />)
    await user.click(screen.getByRole('button', {name: 'Share Baseline'}))
    const visibility = await screen.findByRole('combobox', {name: 'Who can view'})
    await audit()
    await user.selectOptions(visibility, 'selected')
    await audit()
    expect(screen.getByRole('checkbox', {name: 'bob'})).toBeVisible()
  })

  it('audits workload timelines, distribution controls and the chart preview', async () => {
    const user = userEvent.setup()
    render(<main><h1>Load profile</h1><LoadProfileEditor prefix="load" zones={['zone-a', 'zone-b']} /></main>)
    await audit()
    await user.click(screen.getByRole('button', {name: 'Add workload phase'}))
    await audit()
  })

  it('audits schema inputs, advanced settings and optional resource overrides', async () => {
    const user = userEvent.setup()
    render(<main><h1>Application</h1><SchemaFields schema={{type: 'object', properties: {
      replicas: {type: 'integer', title: 'Replicas', default: 3},
      policy: {type: 'object', title: 'Policy', 'x-kubephos-advanced': true},
      ...Object.fromEntries(['cpuRequest', 'cpuLimit', 'memoryRequest', 'memoryLimit'].map(name => [name, {type: 'string'}]))
    }}} applications={[]} artifacts={[]} connections={[]} credentials={[]} serviceTargets={[{id: 'api', traits: ['schedulable']}]} /></main>)
    await audit()
    await user.click(screen.getByText('Advanced settings', {selector: 'summary'}))
    await audit()
    await user.click(screen.getByText('Resource overrides by service'))
    await user.click(screen.getByRole('checkbox', {name: 'Override resources for api'}))
    await audit()
  })

  it('gives compact dialogs an accessible name and labeled controls', async () => {
    render(<Dialog open title="Share configuration" onClose={() => {}} footer={<><button>Cancel</button><button>Save sharing</button></>}><label>Who can view<select defaultValue="private"><option value="private">Private</option><option value="everyone">All users</option></select></label></Dialog>)
    await audit()
  })
})

it('keeps responsive metric tables and configuration readouts accessible', async () => {
  const {ResponsiveTable} = await import('../components/ResponsiveTable')
  const {ConfigurationReadout} = await import('../components/ConfigurationReadout')
  const first = render(<main><h1>Experiment results</h1><ResponsiveTable aria-label="Run statistics"><thead><tr><th>Metric</th><th>Mean</th></tr></thead><tbody><tr><td>CPU usage</td><td>2 cores</td></tr></tbody></ResponsiveTable></main>)
  await audit()
  expect(first.container.querySelector('td[data-label="Mean"]')).toBeInTheDocument()
  first.unmount()
  const {unmount} = render(<main><h1>Configuration</h1><ConfigurationReadout item={{id: 'cfg', name: 'Baseline', workspaceId: 'ws', clusterResourceId: 'cluster', description: '', applicationRef: 'app', applicationDigest: '', validation: {valid: true, issues: []}, createdAt: '', updatedAt: '', definition: {applicationValues: {}, components: []}}} clusters={[]} applications={[]} plugins={[]} strategies={[]} /></main>)
  await audit()
  unmount()
})
