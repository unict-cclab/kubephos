import {useState} from 'react'
import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {beforeEach, describe, expect, it, vi} from 'vitest'
import {Dialog} from './Dialog'
import {FormSection} from './FormSection'

beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {this.setAttribute('open', '')}
  HTMLDialogElement.prototype.close = function () {this.removeAttribute('open')}
})

describe('Editor presentation', () => {
  it('opens long forms as workspace pages and restores the workspace on Back', async () => {
    const workspace = document.createElement('div')
    workspace.className = 'page-content'
    const previous = document.createElement('button')
    previous.textContent = 'Configuration list'
    workspace.append(previous)
    document.body.append(workspace)
    previous.focus()
    function Editor() {
      const [open, setOpen] = useState(true)
      return <Dialog open={open} title="Create configuration" className="template-modal experiment-modal" presentation="page" onClose={() => setOpen(false)}><form><FormSection step={1} title="Cluster" /><label>Name<input /></label><FormSection step={2} title="Application" /></form></Dialog>
    }
    const {unmount} = render(<Editor />)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(screen.getByRole('region', {name: 'Create configuration'}).parentElement).toBe(workspace)
    expect(workspace).toHaveClass('has-editor-page')
    await waitFor(() => expect(screen.getByRole('navigation', {name: 'Form sections'})).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: 'Back'}))
    expect(screen.queryByRole('region', {name: 'Create configuration'})).not.toBeInTheDocument()
    expect(workspace).not.toHaveClass('has-editor-page')
    expect(previous).toHaveFocus()
    unmount()
    workspace.remove()
  })
  it('navigates between sections without changing routes or resetting entered values', async () => {
    const scroll = vi.fn()
    const originalHash = window.location.hash
    render(<Dialog open title="Configuration" presentation="page"><form><FormSection step={1} title="Cluster" /><label>Name<input /></label><FormSection step={2} title="Application" /></form></Dialog>)
    fireEvent.change(screen.getByRole('textbox', {name: 'Name'}), {target: {value: 'CPU baseline'}})
    const application = screen.getByRole('heading', {name: 'Application'})
    application.scrollIntoView = scroll
    await waitFor(() => expect(screen.getByRole('button', {name: 'Application'})).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: 'Application'}))
    expect(application).toHaveFocus()
    expect(scroll).toHaveBeenCalledOnce()
    expect(window.location.hash).toBe(originalHash)
    expect(screen.getByRole('textbox', {name: 'Name'})).toHaveValue('CPU baseline')
  })
  it('keeps dynamic section targets unique when sections are inserted', async () => {
    const scroll = vi.fn()
    function Editor() {
      const [network, setNetwork] = useState(false)
      return <Dialog open title="Configuration" presentation="page"><form><FormSection step={1} title="Cluster" /><label><input type="checkbox" checked={network} onChange={event => setNetwork(event.target.checked)} />Network enabled</label>{network && <FormSection step={2} title="Network" />}<FormSection step={network ? 3 : 2} title="Monitoring" /></form></Dialog>
    }
    render(<Editor />)
    fireEvent.click(screen.getByRole('checkbox', {name: 'Network enabled'}))
    const network = screen.getByRole('heading', {name: 'Network'})
    const monitoring = screen.getByRole('heading', {name: 'Monitoring'})
    expect(network.id).not.toBe(monitoring.id)
    monitoring.scrollIntoView = scroll
    await waitFor(() => expect(screen.getByRole('button', {name: 'Network'})).toBeInTheDocument())
    fireEvent.click(screen.getByRole('button', {name: 'Monitoring'}))
    expect(monitoring).toHaveFocus()
    expect(scroll).toHaveBeenCalledOnce()
  })
  it('keeps presentation independent of styling classes', () => {
    render(<Dialog open title="Compact form" className="template-modal"><label>Name<input /></label></Dialog>)
    expect(screen.getByRole('dialog', {name: 'Compact form'})).toBeInTheDocument()
    expect(screen.queryByRole('region', {name: 'Compact form'})).not.toBeInTheDocument()
  })
  it('keeps short execution dialogs compact with a header outside the scroll body', () => {
    render(<Dialog open title="Create experiment" className="template-modal execution-modal"><form><label>Name<input /></label><div className="modal-actions"><button>Run</button></div></form></Dialog>)
    const dialog = screen.getByRole('dialog', {name: 'Create experiment'})
    expect(dialog.querySelector('.modal-header')?.closest('.modal-body')).toBeNull()
    expect(screen.getByRole('textbox', {name: 'Name'}).closest('.modal-body')).not.toBeNull()
    expect(screen.getByRole('button', {name: 'Run'})).toBeInTheDocument()
  })
})


describe('dialog keyboard focus', () => {
  it('focuses the first editable field and restores the trigger on unmount', () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const {unmount} = render(<Dialog open title="Add model" onClose={() => {}}><label>URL<input /></label></Dialog>)
    expect(screen.getByRole('textbox', {name: 'URL'})).toHaveFocus()
    unmount()
    expect(trigger).toHaveFocus()
    trigger.remove()
  })
  it('restores focus when a mounted dialog closes', () => {
    const trigger = document.createElement('button')
    document.body.append(trigger)
    trigger.focus()
    const {rerender} = render(<Dialog open title="Add model"><label>URL<input /></label></Dialog>)
    rerender(<Dialog open={false} title="Add model"><label>URL<input /></label></Dialog>)
    expect(trigger).toHaveFocus()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    trigger.remove()
  })
})
