import {useEffect, useMemo, useRef, useState} from 'react'
import type {LogEntry} from '../types'

export function LogViewer<T extends Pick<LogEntry, 'sequence' | 'level' | 'message' | 'createdAt'>>({entries, label, context, filename, emptyMessage = 'No log entries yet.'}: {
  entries: T[]
  label: string
  context?: (entry: T) => string
  filename: string
  emptyMessage?: string
}) {
  const [query, setQuery] = useState('')
  const [level, setLevel] = useState('all')
  const [follow, setFollow] = useState(true)
  const [visibleCount, setVisibleCount] = useState(500)
  const consoleRef = useRef<HTMLDivElement>(null)
  const filtered = useMemo(() => entries.filter(entry => (level === 'all' || entry.level === level) && (!query || `${entry.message} ${context?.(entry) ?? ''}`.toLowerCase().includes(query.toLowerCase()))), [entries, query, level, context])
  const visible = filtered.slice(-visibleCount)
  const lastSequence = entries.at(-1)?.sequence
  useEffect(() => {
    if (follow && consoleRef.current) consoleRef.current.scrollTop = consoleRef.current.scrollHeight
  }, [follow, lastSequence, query, level])
  const download = () => {
    const text = filtered.map(entry => `${entry.createdAt} [${entry.level}] ${context?.(entry) ?? ''}${entry.message}`).join('\n')
    const url = URL.createObjectURL(new Blob([text + '\n'], {type: 'text/plain;charset=utf-8'}))
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = filename
    anchor.click()
    window.setTimeout(() => URL.revokeObjectURL(url), 1000)
  }
  return <>
    <div className="log-toolbar">
      <input type="search" aria-label={`Search ${label}`} placeholder="Search logs…" value={query} onChange={event => {setQuery(event.target.value); setVisibleCount(500)}} />
      <select aria-label={`Filter ${label} by level`} value={level} onChange={event => {setLevel(event.target.value); setVisibleCount(500)}}><option value="all">All levels</option><option value="info">Info</option><option value="warning">Warnings</option><option value="error">Errors</option><option value="debug">Debug</option></select>
      <button type="button" className="button secondary compact" aria-pressed={follow} onClick={() => setFollow(value => !value)}>{follow ? 'Following live' : 'Follow live'}</button>
      <button type="button" className="button secondary compact" disabled={!filtered.length} onClick={download}>Download logs</button>
    </div>
    {filtered.length > visible.length && <div className="log-history-notice"><span>Showing the latest {visible.length.toLocaleString()} of {filtered.length.toLocaleString()} matching entries.</span><button type="button" className="button secondary compact" onClick={() => {setFollow(false); setVisibleCount(count => count + 500)}}>Show earlier logs</button></div>}
    <div className="log-console" ref={consoleRef} role="region" aria-label={label} tabIndex={0}>
      {visible.length ? visible.map(entry => <div className={`log-line ${entry.level}`} key={entry.sequence}><time className="time" dateTime={entry.createdAt}>{new Date(entry.createdAt).toLocaleTimeString()}</time><span className="message">{context && <span className="run-log-context">{context(entry)}</span>}{entry.message}</span></div>) : <div className="log-empty">{query || level !== 'all' ? 'No entries match your filters.' : emptyMessage}</div>}
    </div>
  </>
}
