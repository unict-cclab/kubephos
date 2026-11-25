import {useEffect, useRef, useState} from 'react'

// Undo / redo over JSON snapshots of everything the user edits. Bursts (typing,
// dragging) within 700 ms coalesce into one step.
export function useHistory<T>(snapshot: T, apply: (value: T) => void) {
  const json = JSON.stringify(snapshot)
  const past = useRef<string[]>([])
  const future = useRef<string[]>([])
  const last = useRef(json)
  const changedAt = useRef(0)
  const ready = useRef(false)
  const [, bump] = useState(0)
  useEffect(() => {const timer = setTimeout(() => {ready.current = true}, 0); return () => clearTimeout(timer)}, [])
  useEffect(() => {
    if (json === last.current) return
    if (!ready.current) {last.current = json; return}
    const now = Date.now()
    if (now - changedAt.current > 700 || !past.current.length) past.current = [...past.current, last.current].slice(-100)
    changedAt.current = now
    future.current = []
    last.current = json
    bump(value => value + 1)
  }, [json])
  const move = (from: typeof past, to: typeof past) => {
    const target = from.current.at(-1)
    if (target === undefined) return false
    from.current = from.current.slice(0, -1)
    to.current = [...to.current, last.current]
    last.current = target
    changedAt.current = 0
    apply(JSON.parse(target) as T)
    bump(value => value + 1)
    return true
  }
  return {undo: () => move(past, future), redo: () => move(future, past), canUndo: past.current.length > 0, canRedo: future.current.length > 0}
}
