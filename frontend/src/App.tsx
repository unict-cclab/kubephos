import {useCallback, useEffect, useMemo, useState} from 'react'
import {ApiError, request} from './api'
import {ApplicationDialog, AuthDialog, ConnectionDialog, CredentialDialog, OperationDialog, WorkspaceDialog} from './components/Forms'
import {OperationDrawer} from './components/OperationDrawer'
import {ToastRegion, type ToastMessage} from './components/ToastRegion'
import {Views} from './components/Views'
import type {PlatformData, Session, View, Workspace} from './types'

const emptyData: PlatformData = {system: null, workspaces: [], operations: [], artifacts: [], plugins: [], applications: [], credentials: [], connections: [], resources: [], audit: []}
const viewMetadata: Record<View, [string, string]> = {
  overview: ['CONTROL PLANE', 'Overview'],
  workspaces: ['ENVIRONMENTS', 'Workspaces'],
  operations: ['BACKGROUND WORK', 'Operations'],
  catalog: ['APPLICATIONS', 'Catalog'],
  plugins: ['CAPABILITIES', 'Plugins'],
  infrastructure: ['MANAGED ACCESS', 'Infrastructure']
}
const navigation: Array<[View, string, string]> = [
  ['overview', '⌂', 'Overview'],
  ['workspaces', '◇', 'Workspaces'],
  ['operations', '↻', 'Operations'],
  ['catalog', '◫', 'Catalog'],
  ['plugins', '⌘', 'Plugins'],
  ['infrastructure', '▦', 'Infrastructure']
]

type Modal = 'workspace' | 'operation' | 'credential' | 'connection' | 'application' | null

export default function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [data, setData] = useState<PlatformData>(emptyData)
  const [connected, setConnected] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [view, setView] = useState<View>(initialView)
  const [modal, setModal] = useState<Modal>(null)
  const [workspace, setWorkspace] = useState<Workspace | null>(null)
  const [operationID, setOperationID] = useState<string | null>(null)
  const [toasts, setToasts] = useState<ToastMessage[]>([])

  const notify = useCallback((message: string, error = false) => {
    setToasts(items => [...items, {id: Date.now() + Math.random(), message, error}])
  }, [])
  const dismiss = useCallback((id: number) => setToasts(items => items.filter(item => item.id !== id)), [])

  const load = useCallback(async (silent = true) => {
    if (!session?.authenticated) return
    setRefreshing(true)
    try {
      const [system, workspaces, operations, artifacts, plugins, applications, credentials, connections, resources, audit] = await Promise.all([
        request<PlatformData['system']>('/system'),
        request<{items: PlatformData['workspaces']}>('/workspaces'),
        request<{items: PlatformData['operations']}>('/operations'),
        request<{items: PlatformData['artifacts']}>('/artifacts?limit=500'),
        request<{items: PlatformData['plugins']}>('/plugins'),
        request<{items: PlatformData['applications']}>('/catalog/applications'),
        request<{items: PlatformData['credentials']}>('/credentials'),
        request<{items: PlatformData['connections']}>('/connections'),
        request<{items: PlatformData['resources']}>('/infrastructure/resources'),
        request<{items: PlatformData['audit']}>('/audit?limit=20')
      ])
      setData({system, workspaces: workspaces.items, operations: operations.items, artifacts: artifacts.items, plugins: plugins.items, applications: applications.items, credentials: credentials.items, connections: connections.items, resources: resources.items, audit: audit.items})
      setConnected(true)
      if (!silent) notify('Everything is up to date.')
    } catch (cause) {
      setConnected(false)
      if (cause instanceof ApiError && cause.status === 401) setSession({authenticated: false, setupRequired: false})
      if (!silent) notify(cause instanceof Error ? cause.message : 'Could not refresh KubePhos.', true)
    } finally {
      setRefreshing(false)
    }
  }, [notify, session?.authenticated])

  useEffect(() => {
    request<Session>('/auth/status').then(result => setSession(result)).catch(cause => notify(cause instanceof Error ? cause.message : 'Could not check session.', true)).finally(() => setCheckingSession(false))
  }, [notify])

  useEffect(() => {
    if (!session?.authenticated) return
    load(true)
    const timer = window.setInterval(() => load(true), 5000)
    return () => window.clearInterval(timer)
  }, [load, session?.authenticated])

  useEffect(() => {
    const update = () => setView(initialView())
    window.addEventListener('hashchange', update)
    return () => window.removeEventListener('hashchange', update)
  }, [])

  const navigate = (next: View) => {
    window.location.hash = next
    setView(next)
  }
  const afterMutation = useCallback(async (message: string) => {
    await load(true)
    notify(message)
  }, [load, notify])
  const common = useMemo(() => ({session: session ?? {authenticated: false}, applications: data.applications, artifacts: data.artifacts, connections: data.connections, credentials: data.credentials, onDone: afterMutation}), [afterMutation, data.applications, data.artifacts, data.connections, data.credentials, session])

  const logout = async () => {
    try {
      await request('/auth/logout', {method: 'POST', body: '{}'}, session?.csrfToken)
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not close the session.', true)
    }
    setData(emptyData)
    setSession({authenticated: false, setupRequired: false})
  }

  const openOperationForm = (selected: Workspace) => {
    if (!data.plugins.length) {
      notify('No plugin is installed.', true)
      return
    }
    setWorkspace(selected)
    setModal('operation')
  }

  if (checkingSession) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Starting KubePhos…</strong></div>
  if (!session?.authenticated) return <><AuthDialog setupRequired={Boolean(session?.setupRequired)} onAuthenticated={result => {setSession(result); notify(session?.setupRequired ? 'Administrator created.' : 'Signed in.')}} /><ToastRegion items={toasts} dismiss={dismiss} /></>

  const [eyebrow, title] = viewMetadata[view]
  return <>
    <div className="app-shell">
      <aside className="sidebar">
        <button className="brand" onClick={() => navigate('overview')} aria-label="KubePhos home"><span className="brand-mark">K</span><span><strong>KubePhos</strong><small>Kubernetes workspace</small></span></button>
        <nav className="navigation" aria-label="Main navigation">{navigation.map(([target, icon, label]) => <button className={`nav-item ${view === target ? 'active' : ''}`} key={target} onClick={() => navigate(target)}><span>{icon}</span>{label}</button>)}</nav>
        <div className="sidebar-footer"><div className={`health-dot ${connected ? 'healthy' : 'unhealthy'}`} /><span><strong>{connected ? 'All systems healthy' : 'Connection unavailable'}</strong><small>Version {data.system?.version ?? 'dev'}</small></span></div>
      </aside>
      <main className="main">
        <header className="topbar"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1></div><div className="topbar-actions"><span className="signed-user">{session.user?.username} · {session.user?.role}</span><button className="button secondary" onClick={logout}>Sign out</button><button className="button secondary" disabled={refreshing} onClick={() => load(false)}>{refreshing ? 'Refreshing…' : 'Refresh'}</button><button className="button primary" onClick={() => setModal('workspace')}>New workspace</button></div></header>
        <Views
          view={view}
          {...data}
          isAdmin={session.user?.role === 'admin'}
          navigate={navigate}
          createWorkspace={() => setModal('workspace')}
          createOperation={openOperationForm}
          openOperation={setOperationID}
          importApplication={() => setModal('application')}
          addCredential={() => setModal('credential')}
          addConnection={() => setModal('connection')}
        />
      </main>
    </div>
    <WorkspaceDialog {...common} open={modal === 'workspace'} close={() => setModal(null)} />
    <OperationDialog {...common} open={modal === 'operation'} close={() => setModal(null)} workspace={workspace} plugins={data.plugins} onCreated={async id => {await load(true); setOperationID(id)}} />
    <CredentialDialog {...common} open={modal === 'credential'} close={() => setModal(null)} plugins={data.plugins} />
    <ConnectionDialog {...common} open={modal === 'connection'} close={() => setModal(null)} plugins={data.plugins} />
    <ApplicationDialog {...common} open={modal === 'application'} close={() => setModal(null)} />
    <OperationDrawer operationID={operationID} session={session} plugins={data.plugins} close={() => setOperationID(null)} open={setOperationID} changed={() => load(true)} notify={notify} />
    <ToastRegion items={toasts} dismiss={dismiss} />
  </>
}

function initialView(): View {
  const value = window.location.hash.slice(1) as View
  return value in viewMetadata ? value : 'overview'
}
