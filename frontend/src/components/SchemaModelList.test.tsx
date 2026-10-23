import {fireEvent, render, screen} from '@testing-library/react'
import {describe, expect, it, vi} from 'vitest'
import {SchemaFields} from './SchemaFields'
import {readSchemaValues} from '../lib'
import type {JsonSchema} from '../types'

const schema: JsonSchema = {type: 'object', properties: {models: {type: 'array', title: 'Models per service', minItems: 1, maxItems: 3, items: {type: 'string', enum: ['Qwen-small', 'Qwen-coder']}, 'x-kubephos-item-prefix': 'm'}, cpuRequest: {type: 'string', title: 'CPU request (optional)'}, cpuLimit: {type: 'string', title: 'CPU limit (optional)'}}}

describe('Model service list', () => {
  it('submits ordered model selections and updates topology on add and remove', () => {
    const changed = vi.fn()
    const {container} = render(<form><SchemaFields schema={schema} values={{models: ['Qwen-small']}} applications={[]} artifacts={[]} connections={[]} credentials={[]} onValuesChange={changed} /></form>)
    const form = container.querySelector('form')!
    expect(screen.getByRole('button', {name: 'Remove m1'})).toBeDisabled()
    fireEvent.click(screen.getByRole('button', {name: 'Add service'}))
    fireEvent.change(screen.getByRole('combobox', {name: 'Model for m2'}), {target: {value: 'Qwen-coder'}})
    expect(readSchemaValues(form, schema)).toEqual({models: ['Qwen-small', 'Qwen-coder']})
    fireEvent.click(screen.getByRole('button', {name: 'Add service'}))
    expect(screen.getByRole('button', {name: 'Add service'})).toBeDisabled()
    fireEvent.click(screen.getByRole('button', {name: 'Remove m1'}))
    expect(readSchemaValues(form, schema)).toEqual({models: ['Qwen-coder', 'Qwen-small']})
    expect(changed).toHaveBeenCalledTimes(4)
  })
  it('keeps resource requests and limits independent and optional', () => {
    const {container} = render(<form><SchemaFields schema={schema} values={{models: ['Qwen-small']}} applications={[]} artifacts={[]} connections={[]} credentials={[]} /></form>)
    const form = container.querySelector('form')!
    expect(readSchemaValues(form, schema)).toEqual({models: ['Qwen-small']})
    fireEvent.change(screen.getByRole('textbox', {name: 'CPU limit (optional)'}), {target: {value: '1'}})
    expect(readSchemaValues(form, schema)).toEqual({models: ['Qwen-small'], cpuLimit: '1'})
  })
})
