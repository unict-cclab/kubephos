import {useEffect, useState} from 'react'
import type {View} from './types'

export interface DetailRoute {
  kind: string
  id: string
}

export function hashView(hash: string): View {
  const view = hash.replace(/^#/, '').split('/')[0] as View
  return ['overview', 'workspaces', 'pipelines', 'operations', 'results', 'catalog', 'plugins', 'infrastructure', 'kubernetes', 'experiments', 'suites', 'advanced'].includes(view) ? view : 'overview'
}

export function parseDetailRoute(hash: string, view: View): DetailRoute | null {
  const [section, kind, rawID, ...extra] = hash.replace(/^#/, '').split('/')
  if (section !== view || !kind || !rawID || extra.length) return null
  try {
    return {kind, id: decodeURIComponent(rawID)}
  } catch {
    return null
  }
}

export function useDetailRoute(view: View) {
  const [route, setRoute] = useState<DetailRoute | null>(() => parseDetailRoute(window.location.hash, view))
  useEffect(() => {
    const update = () => setRoute(parseDetailRoute(window.location.hash, view))
    window.addEventListener('hashchange', update)
    return () => window.removeEventListener('hashchange', update)
  }, [view])
  const open = (kind: string, id: string) => {
    window.location.hash = `${view}/${kind}/${encodeURIComponent(id)}`
    setRoute({kind, id})
  }
  const close = () => {
    window.location.hash = view
    setRoute(null)
  }
  return {route, open, close}
}
