import {fireEvent, render, screen, waitFor} from '@testing-library/react'
import {afterEach, expect, it, vi} from 'vitest'
import {CodeBlock} from './CodeBlock'
import {copyText} from '../clipboard'
afterEach(() => {vi.unstubAllGlobals(); vi.restoreAllMocks()})
it('copies the exact snippet, including newlines and plus signs, with accessible feedback', async () => {
 const writeText = vi.fn().mockResolvedValue(undefined)
 vi.stubGlobal('navigator', {clipboard: {writeText}})
 const text = 'mean ± std\nvalue = 1 + 2\n'
 render(<CodeBlock label="Manifest">{text}</CodeBlock>)
 fireEvent.click(screen.getByRole('button', {name: 'Copy Manifest'}))
 await waitFor(() => expect(screen.getByRole('button', {name: 'Copy Manifest'})).toHaveTextContent('Copied'))
 expect(writeText).toHaveBeenCalledWith(text)
 expect(screen.getByRole('status')).toHaveTextContent('Code copied')
})
it('supports HTTP instances and keeps the fallback inside an open dialog', async () => {
 vi.stubGlobal('navigator', {})
 const execute = vi.fn(() => {
   expect(document.querySelector('dialog textarea')).toHaveValue('copied text')
   return true
 })
 Object.defineProperty(document, 'execCommand', {configurable: true, value: execute})
 render(<dialog open><button>Original focus</button></dialog>)
 const focus = screen.getByRole('button', {name: 'Original focus'}); focus.focus()
 await copyText('copied text')
 expect(execute).toHaveBeenCalledWith('copy')
 expect(document.querySelector('textarea')).not.toBeInTheDocument()
 expect(focus).toHaveFocus()
})
it('shows a useful error if both clipboard mechanisms are blocked', async () => {
 vi.stubGlobal('navigator', {clipboard: {writeText: vi.fn().mockRejectedValue(new Error('denied'))}})
 Object.defineProperty(document, 'execCommand', {configurable: true, value: () => false})
 render(<CodeBlock>{'blocked text'}</CodeBlock>)
 fireEvent.click(screen.getByRole('button', {name: 'Copy Code snippet'}))
 expect(await screen.findByText('Copy was blocked by the browser. Select the code and copy it manually.')).toBeInTheDocument()
 expect(document.querySelector('textarea')).not.toBeInTheDocument()
})
