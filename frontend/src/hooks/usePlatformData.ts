import {useCallback, useEffect, useRef, useState, type Dispatch, type SetStateAction} from 'react'
import {ApiError, request} from '../api'
import type {PlatformData, Session} from '../types'

export const emptyData: PlatformData = {system: null, pluginRuntime: null, workspaces: [], pipelines: [], pipelineRuns: [], experiments: [], experimentConfigurations: [], operations: [], artifacts: [], plugins: [], pluginPackages: [], pluginImports: [], applications: [], strategies: [], credentials: [], connections: [], machineTemplates: [], infrastructureServices: [], kubernetesClusters: [], resources: [], audit: [], users: [], clusterAssignments: []}

const endpoints: Array<{key: keyof PlatformData; path: string; admin: boolean; live: boolean; list: boolean}> = [
  {key: 'system', path: '/system', admin: false, live: true, list: false},
  {key: 'workspaces', path: '/workspaces', admin: false, live: false, list: true},
  {key: 'pipelines', path: '/pipelines', admin: true, live: false, list: true},
  {key: 'pipelineRuns', path: '/pipeline-runs', admin: false, live: true, list: true},
  {key: 'experiments', path: '/experiments', admin: false, live: true, list: true},
  {key: 'experimentConfigurations', path: '/experiment-configurations', admin: false, live: false, list: true},
  {key: 'operations', path: '/operations', admin: false, live: true, list: true},
  {key: 'artifacts', path: '/artifacts?limit=500', admin: false, live: true, list: true},
  {key: 'plugins', path: '/plugins', admin: false, live: false, list: true},
  {key: 'applications', path: '/catalog/applications', admin: false, live: false, list: true},
  {key: 'strategies', path: '/catalog/strategies', admin: false, live: false, list: true},
  {key: 'credentials', path: '/credentials', admin: true, live: false, list: true},
  {key: 'connections', path: '/connections', admin: true, live: true, list: true},
  {key: 'machineTemplates', path: '/machine-templates', admin: true, live: true, list: true},
  {key: 'infrastructureServices', path: '/infrastructure-services', admin: true, live: true, list: true},
  {key: 'kubernetesClusters', path: '/kubernetes-clusters', admin: false, live: true, list: true},
  {key: 'users', path: '/users', admin: true, live: false, list: true},
]

export function usePlatformData(session: Session | null, setSession: Dispatch<SetStateAction<Session | null>>, notify: (message: string, error?: boolean) => void) {
  const [data, setData] = useState<PlatformData>(emptyData)
  const [connected, setConnected] = useState(false)
  const [ready, setReady] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const controller = useRef<AbortController | null>(null)
  const flight = useRef<Promise<void> | null>(null)
  const lastFullRefresh = useRef(0)
  const failedEndpoints = useRef(new Set<keyof PlatformData>())

  const load = useCallback(async (silent = true, live = false, polling = false): Promise<void> => {
    const active = controller.current
    if (!session?.authenticated || !active || active.signal.aborted) return
    if (flight.current) {
      if (polling) return
      await flight.current
      // Mutations and manual refreshes need a fresh snapshot after the current request.
      if (!live && controller.current === active && !active.signal.aborted) await load(silent)
      return
    }
    if (!live) setRefreshing(true)
    const selected = endpoints.filter(endpoint => (!endpoint.admin || session.user?.role === 'admin') && (!live || endpoint.live))
    const pending = (async () => {
      const results = await Promise.allSettled(selected.map(async endpoint => {
        const value = await request<Record<string, unknown>>(endpoint.path, {}, undefined, active.signal)
        if (!value || typeof value !== 'object' || endpoint.list && !Array.isArray(value.items)) throw new ApiError(200, {code: 'invalid_response', message: 'KubePhos returned incomplete data. Refresh the page and try again.'})
        return {...endpoint, value}
      }))
      if (active.signal.aborted) return
      const patch: Partial<PlatformData> = {}
      const failures: unknown[] = []
      for (const [index, result] of results.entries()) {
        if (result.status === 'rejected') {failures.push(result.reason); failedEndpoints.current.add(selected[index].key); continue}
        failedEndpoints.current.delete(selected[index].key)
        const {key, list, value} = result.value
        Object.assign(patch, {[key]: list ? value.items : value})
        if (key === 'users') patch.clusterAssignments = (value.assignments ?? []) as PlatformData['clusterAssignments']
      }
      if (failures.some(cause => cause instanceof ApiError && cause.status === 401)) {
        setSession({authenticated: false, setupRequired: false})
        return
      }
      setData(current => ({...current, ...patch}))
      setReady(true)
      setConnected(failedEndpoints.current.size === 0)
      if (!live && !failures.length) lastFullRefresh.current = Date.now()
      if (!silent) notify(failures.length ? failures[0] instanceof Error ? failures[0].message : 'Could not refresh KubePhos.' : 'Everything is up to date.', failures.length > 0)
    })()
    flight.current = pending
    try {await pending} finally {
      if (flight.current === pending) flight.current = null
      if (!active.signal.aborted) setRefreshing(false)
    }
  }, [notify, session?.authenticated, session?.user?.id, session?.user?.role, setSession])

  useEffect(() => {
    const active = new AbortController()
    controller.current = active
    flight.current = null
    lastFullRefresh.current = 0
    failedEndpoints.current.clear()
    setData(emptyData)
    setReady(false)
    setConnected(false)
    setRefreshing(false)
    if (session?.authenticated) void load()
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'hidden') return
      void load(true, Date.now() - lastFullRefresh.current < 60000, true)
    }, 5000)
    const resume = () => {if (document.visibilityState === 'visible') void load(true, Date.now() - lastFullRefresh.current < 60000, true)}
    document.addEventListener('visibilitychange', resume)
    return () => {
      active.abort()
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', resume)
    }
  }, [load, session?.authenticated])

  return {data, connected, refreshing, ready, load}
}
