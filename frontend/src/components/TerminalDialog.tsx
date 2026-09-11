import {useEffect, useRef, useState, type FormEvent} from 'react'
import {FitAddon} from '@xterm/addon-fit'
import {Terminal} from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import {request} from '../api'
import type {Session, Workspace} from '../types'
import {Dialog} from './Dialog'

interface TerminalMachine {
  index: number
  name: string
  state: string
}

interface TerminalTargetGroup {
  machineSetRef: string
  machineAccessRef: string
  name: string
  machines: TerminalMachine[]
}

interface Ticket {
  id: string
  connectPath: string
  target: string
  expiresAt: string
}

export function TerminalDialog({open, close, session, workspaces, initialWorkspace}: {open: boolean; close: () => void; session: Session; workspaces: Workspace[]; initialWorkspace: Workspace | null}) {
  const [workspaceID, setWorkspaceID] = useState('')
  const [groups, setGroups] = useState<TerminalTargetGroup[]>([])
  const [selection, setSelection] = useState('')
  const [loading, setLoading] = useState(false)
  const [connecting, setConnecting] = useState(false)
  const [connected, setConnected] = useState(false)
  const [message, setMessage] = useState('Select a managed machine and connect.')
  const viewport = useRef<HTMLDivElement>(null)
  const terminal = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const socket = useRef<WebSocket | null>(null)

  useEffect(() => {
    if (!open) return
    const instance = new Terminal({cursorBlink: true, convertEol: true, scrollback: 5000, fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace', fontSize: 13, theme: {background: '#0d1410', foreground: '#dfe8e1', cursor: '#d8f56d', selectionBackground: '#315c46'}})
    const fitAddon = new FitAddon()
    instance.loadAddon(fitAddon)
    if (viewport.current) {
      instance.open(viewport.current)
      fitAddon.fit()
    }
    terminal.current = instance
    fit.current = fitAddon
    const resize = new ResizeObserver(() => fitAddon.fit())
    if (viewport.current) resize.observe(viewport.current)
    const data = instance.onData(value => {
      if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify({type: 'input', data: value}))
    })
    const dimensions = instance.onResize(value => {
      if (socket.current?.readyState === WebSocket.OPEN) socket.current.send(JSON.stringify({type: 'resize', columns: value.cols, rows: value.rows}))
    })
    return () => {
      if (socket.current) {
        socket.current.onclose = null
        socket.current.close(1000, 'Dialog closed')
      }
      socket.current = null
      resize.disconnect()
      data.dispose()
      dimensions.dispose()
      instance.dispose()
      terminal.current = null
      fit.current = null
      setConnected(false)
      setConnecting(false)
    }
  }, [open])

  useEffect(() => {
    if (!open) return
    setWorkspaceID(current => {
      if (initialWorkspace && workspaces.some(item => item.id === initialWorkspace.id)) return initialWorkspace.id
      return workspaces.some(item => item.id === current) ? current : workspaces[0]?.id ?? ''
    })
  }, [initialWorkspace, open, workspaces])

  useEffect(() => {
    if (!open || !workspaceID) {
      setGroups([])
      setSelection('')
      return
    }
    let active = true
    setLoading(true)
    request<{items: TerminalTargetGroup[]}>(`/terminal-targets?workspaceId=${encodeURIComponent(workspaceID)}`)
      .then(result => {
        if (!active) return
        setGroups(result.items)
        const first = result.items[0]
        setSelection(first?.machines[0] ? `${first.machineSetRef}:${first.machines[0].index}` : '')
        setMessage(result.items.length ? 'Ready to open an audited session.' : 'No running managed machine is available in this workspace.')
      })
      .catch(cause => active && setMessage(cause instanceof Error ? cause.message : 'Could not load managed machines.'))
      .finally(() => active && setLoading(false))
    return () => { active = false }
  }, [open, workspaceID])

  const connect = async (event: FormEvent) => {
    event.preventDefault()
    const separator = selection.lastIndexOf(':')
    const machineSetRef = selection.slice(0, separator)
    const machineIndex = Number(selection.slice(separator + 1))
    const group = groups.find(item => item.machineSetRef === machineSetRef)
    if (!group || !Number.isInteger(machineIndex) || !terminal.current || !fit.current) return
    setConnecting(true)
    setMessage('Validating artifacts and opening SSH…')
    terminal.current.reset()
    terminal.current.writeln('\x1b[38;2;216;245;109mKubePhos managed terminal\x1b[0m')
    fit.current.fit()
    try {
      const ticket = await request<Ticket>('/terminals', {method: 'POST', body: JSON.stringify({workspaceId: workspaceID, machineSetRef, machineAccessRef: group.machineAccessRef, machineIndex, columns: terminal.current.cols, rows: terminal.current.rows})}, session.csrfToken)
      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
      const connection = new WebSocket(`${protocol}//${window.location.host}${ticket.connectPath}`)
      connection.binaryType = 'arraybuffer'
      socket.current = connection
      connection.onopen = () => {
        setConnected(true)
        setConnecting(false)
        setMessage(`Connected to ${ticket.target}. Input is not recorded.`)
        terminal.current?.focus()
      }
      connection.onmessage = event => {
        if (typeof event.data === 'string') terminal.current?.write(event.data)
        else terminal.current?.write(new Uint8Array(event.data))
      }
      connection.onerror = () => setMessage('The terminal connection failed.')
      connection.onclose = event => {
        setConnected(false)
        setConnecting(false)
        socket.current = null
        terminal.current?.writeln(`\r\n\x1b[38;2;178;59;56mSession closed${event.reason ? `: ${event.reason}` : '.'}\x1b[0m`)
        setMessage('Session closed. You can reconnect safely.')
      }
    } catch (cause) {
      setConnecting(false)
      setMessage(cause instanceof Error ? cause.message : 'Could not open the terminal.')
    }
  }

  const disconnect = () => socket.current?.close(1000, 'Disconnected by user')
  const dismiss = () => {
    socket.current?.close(1000, 'Dialog closed')
    close()
  }
  return <Dialog open={open} onClose={dismiss} title="Managed terminal" eyebrow="AUDITED SSH" className="terminal-modal">
    <form className="terminal-toolbar" onSubmit={connect}>
      <label>Workspace<select value={workspaceID} onChange={event => setWorkspaceID(event.target.value)} disabled={connected || !workspaces.length}>{workspaces.length ? workspaces.map(item => <option key={item.id} value={item.id}>{item.name}</option>) : <option value="">No workspace</option>}</select></label>
      <label>Machine<select value={selection} onChange={event => setSelection(event.target.value)} disabled={connected || loading || !selection}>{groups.flatMap(group => group.machines.map(machine => <option key={`${group.machineSetRef}:${machine.index}`} value={`${group.machineSetRef}:${machine.index}`}>{machine.name}</option>))}</select></label>
      {connected ? <button type="button" className="button danger" onClick={disconnect}>Disconnect</button> : <button type="submit" className="button primary" disabled={!selection || loading || connecting}>{connecting ? 'Connecting…' : 'Connect'}</button>}
    </form>
    <div className="terminal-status"><span className={connected ? 'connected' : ''} />{message}</div>
    <div className="terminal-viewport" ref={viewport} />
    <p className="terminal-safety">The destination comes from verified MachineSet and encrypted MachineAccess artifacts. Sessions expire after 20 minutes of inactivity and after two hours.</p>
  </Dialog>
}
