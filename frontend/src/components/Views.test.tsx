import {describe, expect, it} from 'vitest'
import type {Plugin} from '../types'
import {infrastructureControls} from './Views'

function plugin(id: string, capabilities: string[]): Plugin {
  return {id, name: id, version: '1.0.0', description: '', schema: {}, capabilities, runtime: {kind: 'bundled'}}
}

describe('infrastructureControls', () => {
  it('discovers interactive infrastructure tools only from plugin capabilities', () => {
    const values = [
      plugin('ssh', ['infrastructure.ssh.control']),
      plugin('future-provider', ['infrastructure.future.control']),
      plugin('provisioner', ['infrastructure.provision']),
      plugin('load', ['load.session.control'])
    ]
    expect(infrastructureControls(values).map(item => item.id)).toEqual(['ssh', 'future-provider'])
  })
})
