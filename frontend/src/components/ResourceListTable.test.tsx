import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {ResourceListTable} from './ResourceListTable'

describe('resource list ordering', () => {
  it('defaults to latest modification and toggles both date columns without changing the input', () => {
    const items = [
      {id: 'a', name: 'Edited earlier item', createdAt: '2026-09-01T00:00:00Z', updatedAt: '2026-10-01T00:00:00Z'},
      {id: 'b', name: 'Newer item', createdAt: '2026-09-20T00:00:00Z', updatedAt: '2026-09-20T00:00:00Z'},
      {id: 'c', name: 'Legacy item', createdAt: '2026-09-10T00:00:00Z', updatedAt: ''}
    ]
    render(<ResourceListTable items={items} label="Resources" columns={[{label: 'Name', render: item => item.name}]} actions={() => null} />)
    const names = () => within(screen.getByRole('table')).getAllByRole('row').slice(1).map(row => within(row).getAllByRole('cell')[0].textContent)
    expect(names()).toEqual(['Edited earlier item', 'Newer item', 'Legacy item'])
    expect(screen.getByRole('columnheader', {name: 'Last updated'})).toHaveAttribute('aria-sort', 'descending')
    fireEvent.click(screen.getByRole('button', {name: 'Last updated'}))
    expect(names()).toEqual(['Legacy item', 'Newer item', 'Edited earlier item'])
    fireEvent.click(screen.getByLabelText('Table display'))
    expect(screen.getByRole('checkbox', {name: 'Created'})).toBeChecked()
    fireEvent.click(screen.getByRole('button', {name: 'Created'}))
    expect(names()).toEqual(['Newer item', 'Legacy item', 'Edited earlier item'])
    fireEvent.click(screen.getByRole('button', {name: 'Created'}))
    expect(names()).toEqual(['Edited earlier item', 'Legacy item', 'Newer item'])
    expect(items.map(item => item.id)).toEqual(['a', 'b', 'c'])
  })
})

it('searches resources and provides a way to clear an empty search', () => {
  const items = [{id: 'a', name: 'Baseline', createdAt: '', updatedAt: ''}, {id: 'b', name: 'Autoscaler', createdAt: '', updatedAt: ''}]
  render(<ResourceListTable items={items} label="Configurations" columns={[{label: 'Name', render: item => item.name}]} actions={() => null} searchText={item => item.name} />)
  const search = screen.getByRole('searchbox', {name: 'Search configurations'})
  fireEvent.change(search, {target: {value: 'base'}})
  expect(screen.getByText('Baseline')).toBeInTheDocument()
  expect(screen.queryByText('Autoscaler')).not.toBeInTheDocument()
  fireEvent.change(search, {target: {value: 'missing'}})
  expect(screen.getByText('No results match your search.')).toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', {name: 'Clear search'}))
  expect(screen.getByText('Autoscaler')).toBeInTheDocument()
})

it('keeps creator and last editor separate and searchable', () => {
  render(<ResourceListTable items={[{id: 'cfg', createdAt: '2026-09-12T00:00:00Z', updatedAt: '2026-09-13T00:00:00Z', createdBy: 'alice', modifiedBy: 'bruno'}]} label="Configurations" editable columns={[{label: 'Name', render: () => 'Baseline'}]} searchText={() => 'Baseline'} actions={() => null} />)
  expect(screen.getAllByRole('columnheader').map(header => header.textContent)).toEqual(['Name', 'Created', 'Created by', 'Last updated', 'Last updated by', 'Actions'])
  expect(screen.getByRole('columnheader', {name: 'Created by'})).toBeInTheDocument()
  expect(screen.getByRole('columnheader', {name: 'Last updated by'})).toBeInTheDocument()
  expect(screen.getByRole('cell', {name: 'alice'})).toBeInTheDocument()
  expect(screen.getByRole('cell', {name: 'bruno'})).toBeInTheDocument()
  fireEvent.change(screen.getByRole('searchbox'), {target: {value: 'bruno'}})
  expect(screen.getByText('Baseline')).toBeInTheDocument()
})

it('opens display options without a text button and closes them with Escape or an outside click', () => {
  render(<ResourceListTable items={[]} label="Resources" columns={[{label: 'Name', render: () => null}]} actions={() => null} />)
  const toggle = screen.getByLabelText('Table display')
  const menu = toggle.closest('details')!
  expect(screen.queryByText('Columns')).not.toBeInTheDocument()
  fireEvent.click(toggle)
  expect(menu).toHaveAttribute('open')
  fireEvent.keyDown(screen.getByRole('checkbox', {name: 'Created'}), {key: 'Escape'})
  expect(menu).not.toHaveAttribute('open')
  expect(toggle).toHaveFocus()
  fireEvent.click(toggle)
  fireEvent.pointerDown(screen.getByRole('table'))
  expect(menu).not.toHaveAttribute('open')
})

it('sorts through display settings when table headers are not shown on a small screen', () => {
  render(<ResourceListTable items={[{id: 'a', name: 'Older', createdAt: '2026-01-01', updatedAt: '2026-10-01'}, {id: 'b', name: 'Newer', createdAt: '2026-02-01', updatedAt: '2026-09-01'}]} label="Resources" columns={[{label: 'Name', render: item => item.name}]} actions={() => null} />)
  fireEvent.click(screen.getByLabelText('Table display'))
  fireEvent.change(screen.getByRole('combobox', {name: 'Sort by'}), {target: {value: 'createdAt:true'}})
  const rows = within(screen.getByRole('table')).getAllByRole('row').slice(1)
  expect(within(rows[0]).getAllByRole('cell')[0]).toHaveTextContent('Newer')
})

it('selects every available column by default', () => {
  render(<ResourceListTable items={[]} label="Resources" editable columns={[{label: 'Name', render: () => null}, {label: 'Status', render: () => null}]} actions={() => null} />)
  fireEvent.click(screen.getByLabelText('Table display'))
  screen.getAllByRole('checkbox').forEach(checkbox => expect(checkbox).toBeChecked())
  expect(screen.getByRole('columnheader', {name: 'Created'})).toBeInTheDocument()
  expect(screen.getByRole('columnheader', {name: 'Last updated by'})).toBeInTheDocument()
})


it('keeps filters available on empty lists and distinguishes an empty list from an empty search', () => {
  const props = {label: 'Resources', columns: [{label: 'Name', render: (item: {name: string}) => item.name}], actions: () => null, searchText: (item: {name: string}) => item.name, filters: <label>Created by<select><option>All users</option></select></label>, empty: <strong>No resources yet</strong>}
  const {rerender} = render(<ResourceListTable {...props} items={[]} />)
  expect(screen.getByRole('combobox', {name: 'Created by'})).toBeInTheDocument()
  expect(screen.getByText('No resources yet')).toBeInTheDocument()
  rerender(<ResourceListTable {...props} items={[{id: 'one', name: 'Baseline', createdAt: '', updatedAt: ''}]} />)
  fireEvent.change(screen.getByRole('searchbox'), {target: {value: 'missing'}})
  expect(screen.getByText('No results match your search.')).toBeInTheDocument()
  expect(screen.queryByText('No resources yet')).not.toBeInTheDocument()
})
