import {fireEvent, render, screen, within} from '@testing-library/react'
import {describe, expect, it} from 'vitest'
import {LogViewer} from './LogViewer'

const entries = Array.from({length: 1200}, (_, index) => ({sequence: index + 1, level: index === 2 ? 'error' : 'info', message: `Entry ${index + 1}`, createdAt: '2026-01-01T00:00:00Z'}))

describe('log viewer', () => {
  it('limits initial rendering while allowing earlier logs to be inspected', () => {
    render(<LogViewer entries={entries} label="Process logs" filename="logs.txt" />)
    const region = screen.getByRole('region', {name: 'Process logs'})
    expect(region.querySelectorAll('.log-line')).toHaveLength(500)
    expect(within(region).queryByText('Entry 1')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', {name: 'Show earlier logs'}))
    expect(region.querySelectorAll('.log-line')).toHaveLength(1000)
    expect(screen.getByRole('button', {name: 'Follow live'})).toHaveAttribute('aria-pressed', 'false')
  })

  it('searches the full history, combines level filters and explains empty results', () => {
    render(<LogViewer entries={entries} label="Process logs" filename="logs.txt" />)
    fireEvent.change(screen.getByRole('combobox', {name: 'Filter Process logs by level'}), {target: {value: 'error'}})
    expect(screen.getByText('Entry 3')).toBeInTheDocument()
    expect(screen.getByRole('region').querySelectorAll('.log-line')).toHaveLength(1)
    fireEvent.change(screen.getByRole('searchbox', {name: 'Search Process logs'}), {target: {value: 'missing'}})
    expect(screen.getByText('No entries match your filters.')).toBeInTheDocument()
    expect(screen.getByRole('button', {name: 'Download logs'})).toBeDisabled()
  })
})

it('follows the last entries when a hidden log tab becomes active without losing filters',()=>{
 const props={entries,label:'Process logs',filename:'logs.txt'}
 const view=render(<LogViewer {...props} active={false}/>)
 const region=screen.getByRole('region',{name:'Process logs'})
 Object.defineProperty(region,'scrollHeight',{configurable:true,value:1200})
 fireEvent.change(screen.getByRole('searchbox'),{target:{value:'Entry 12'}})
 expect(region.scrollTop).toBe(0)
 view.rerender(<LogViewer {...props} active/>)
 expect(region.scrollTop).toBe(1200)
 expect(screen.getByRole('searchbox')).toHaveValue('Entry 12')
 fireEvent.click(screen.getByRole('button',{name:'Following live'}))
 region.scrollTop=10
 view.rerender(<LogViewer {...props} active={false}/>)
 view.rerender(<LogViewer {...props} active/>)
 expect(region.scrollTop).toBe(10)
})
