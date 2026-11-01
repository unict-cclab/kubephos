import {useEffect, useRef, useState, type FormEvent} from 'react'
import {Dialog} from './Dialog'
import {ErrorFeedback, useErrorState} from './ToastRegion'

export function CloneDialog({sourceName, clone, close}: {sourceName: string; clone: (name: string) => Promise<void>; close: () => void}) {
  const [name, setName] = useState('')
  const [pending, setPending] = useState(false)
  const [error, setError] = useErrorState()
  const nameInput = useRef<HTMLInputElement>(null)
  useEffect(() => {setName(`${sourceName.slice(0, 115)} copy`); setError('')}, [sourceName])
  useEffect(() => {if (error && !pending) nameInput.current?.focus({preventScroll: true})}, [error, pending])
  async function submit(event: FormEvent) {
    event.preventDefault()
    if (pending || !name.trim()) return
    setPending(true); setError('')
    try {
      await clone(name.trim())
      close()
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Could not clone the configuration.')
    } finally {setPending(false)}
  }
  return <Dialog open title="Clone configuration" onClose={pending ? undefined : close}>
    <form className="form-stack" onSubmit={submit} aria-busy={pending}>
      <label>Name<input ref={nameInput} autoFocus value={name} onChange={event => setName(event.target.value)} required maxLength={120} disabled={pending} /></label>
      <ErrorFeedback message={error} />
      <div className="dialog-actions"><button type="button" className="button secondary" onClick={close} disabled={pending}>Cancel</button><button className="button primary" disabled={pending || !name.trim()}>{pending ? 'Cloning…' : 'Clone'}</button></div>
    </form>
  </Dialog>
}
