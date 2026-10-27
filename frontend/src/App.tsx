import {ActionIcon} from './components/ActionIcon'
import {useDeleteConfirmation} from './components/ConfirmationDialog'
import {useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {request} from './api'
import {usePlatformData} from './hooks/usePlatformData'
import {ApplicationDialog, AdministratorSetupDialog, ConnectionDialog, InfrastructureServiceDialog, KubernetesClusterDialog, MachineTemplateDialog} from './components/Forms'
import {OperationDetailsPage, PipelineRunLogsPage} from './components/OperationDrawer'
import {ToastRegion, type ToastMessage} from './components/ToastRegion'
import {ThemeToggle} from './components/ThemeToggle'
import {Views} from './components/Views'
import type {Application, Session, View} from './types'
import {retriesOnlyClusterMesh} from './clusterRetry'
import {hashView} from './detailRoute'

const viewMetadata: Record<View, string> = {
  overview: 'Overview',
  results: 'Experiments',
  catalog: 'Catalog',
  infrastructure: 'Infrastructure',
  kubernetes: 'Kubernetes',
  experiments: 'Configurations',
  users: 'Users',
  administration: 'Administration'
}
const navigation: Array<[View, string]> = [
  ['overview', 'Overview'],
  ['infrastructure', 'Infrastructure'],
  ['kubernetes', 'Kubernetes'],
  ['catalog', 'Catalog'],
  ['experiments', 'Configurations'],
  ['results', 'Experiments']
]

function NavigationIcon({view}: {view: View}) {
  const common = {fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const}
  const shape = {
    overview: <><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></>,
    infrastructure: <><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 8h10M7 12h10M7 16h4"/></>,
    kubernetes: <><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><path d="M12 3v6m0 6v6M3 12h6m6 0h6"/></>,
    catalog: <><path d="M4 5h16v14H4zM4 12h16M9 5v7m6 0v7"/></>,
    experiments: <><path d="M6 4h12v16H6zM9 8h6M9 12h6M9 16h4"/></>,
    results: <><circle cx="12" cy="12" r="9"/><path d="m10 8 6 4-6 4z"/></>,
    administration: <><rect x="4" y="4" width="16" height="16" rx="2"/><path d="M8 8h8M8 12h8M8 16h5"/></>,
    users: <><circle cx="9" cy="8" r="3"/><path d="M3.5 19c.5-4 2.3-6 5.5-6s5 2 5.5 6M16 7h5M18.5 4.5v5"/></>
  } as Partial<Record<View, React.ReactNode>>
  return <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true" {...common}>{shape[view]}</svg>
}

type Modal = 'connection' | 'machineTemplate' | 'infrastructureService' | 'kubernetesCluster' | 'application' | null

export default function App() {
  const {confirmDelete, confirmAction, confirmationDialog} = useDeleteConfirmation()
  const [session, setSession] = useState<Session | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [view, setView] = useState<View>(initialView)
  const [modal, setModal] = useState<Modal>(null)
  const [operationID, setOperationID] = useState<string | null>(null)
  const [pipelineRunID, setPipelineRunID] = useState<string | null>(null)
  const [toasts, setToasts] = useState<ToastMessage[]>([])
  const [clusterActions, setClusterActions] = useState<Record<string, {pending: boolean; message: string; error: boolean}>>({})
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false)
  const mobileNavigationButton = useRef<HTMLButtonElement>(null)
  const [serviceKind, setServiceKind] = useState<'harbor' | 'nfs'>('harbor')

  const notify = useCallback((message: string, error = false) => {
    setToasts(items => [...items, {id: Date.now() + Math.random(), message, error}])
  }, [])
  const dismiss = useCallback((id: number) => setToasts(items => items.filter(item => item.id !== id)), [])

  const {data, connected, refreshing, ready, load} = usePlatformData(session, setSession, notify)

  useEffect(() => {
    const controller = new AbortController()
    request<Session>('/auth/status', {}, undefined, controller.signal)
      .then(result => setSession(result))
      .catch(cause => {if (!controller.signal.aborted) notify(cause instanceof Error ? cause.message : 'Could not check session.', true)})
      .finally(() => {if (!controller.signal.aborted) setCheckingSession(false)})
    return () => controller.abort()
  }, [notify])

  useEffect(() => {
    if (session && !session.authenticated && !session.setupRequired) window.location.replace('/api/v1/auth/login')
  }, [session?.authenticated, session?.setupRequired])

  useEffect(() => {
    const update = () => {
      setModal(null)
      setView(initialView())
      setOperationID(null)
      setPipelineRunID(null)
    }
    window.addEventListener('hashchange', update)
    return () => window.removeEventListener('hashchange', update)
  }, [])

  const navigate = (next: View) => {
    if (mobileNavigationOpen) mobileNavigationButton.current?.focus()
    setMobileNavigationOpen(false)
    setModal(null)
    setOperationID(null)
    setPipelineRunID(null)
    window.location.hash = next
    setView(next)
  }
  const afterMutation = useCallback(async (message: string) => {
    await load(true)
    notify(message)
  }, [load, notify])
  const refreshAfterChange = useCallback(async () => {await load(true)}, [load])
  const changesApplied = useCallback(() => afterMutation('Changes saved.'), [afterMutation])
  const common = useMemo(() => ({session: session ?? {authenticated: false}, applications: data.applications, artifacts: data.artifacts, connections: data.connections, credentials: data.credentials, onDone: afterMutation}), [afterMutation, data.applications, data.artifacts, data.connections, data.credentials, session])

  const logout = async () => {
    try {
	  const result = await request<{logoutURL: string}>('/auth/logout', {method: 'POST', body: '{}'}, session?.csrfToken)
	  window.location.assign(result.logoutURL)
	  return
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not close the session.', true)
    }
    setSession({authenticated: false, setupRequired: false})
  }

  const deleteApplication = async (application: Application) => {
    if (!await confirmDelete(`Delete ${application.name} from the catalog? Existing experiment configurations must be removed first.`)) return false
    try {
      await request(`/catalog/applications/${encodeURIComponent(application.id)}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Application removed from the catalog. Its audit history was preserved.')
      return true
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not remove the application from the catalog.', true)
      return false
    }
  }

  const deleteConnection = async (connectionID: string, name: string) => {
    if (!await confirmDelete(`Delete connection ${name}?`)) return
    try {
      await request(`/connections/${connectionID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Connection deleted.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the connection.', true)
    }
  }

  const deleteMachineTemplate = async (templateID: string, name: string) => {
    if (!await confirmDelete(`Delete Proxmox template ${name}? The task will run in the background.`)) return
    try {
      await request(`/machine-templates/${templateID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Template deletion queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the template.', true)
    }
  }

  const deleteInfrastructureService = async (serviceID: string, name: string) => {
    if (!await confirmDelete(`Delete service ${name} and its dedicated VM?`)) return
    try {
      await request(`/infrastructure-services/${serviceID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Service deletion validated and queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the service.', true)
    }
  }

  const deleteKubernetesCluster = async (clusterID: string, name: string) => {
    if (!await confirmDelete(`Delete cluster ${name}, its Kubernetes components and all dedicated VMs?`)) return
    setClusterActions(items => ({...items, [clusterID]: {pending: true, message: 'Validating cluster health and cleanup targets…', error: false}}))
    notify('Validating cluster deletion. This can take up to a minute.')
    try {
      await request(`/kubernetes-clusters/${clusterID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Cluster deletion validated and queued.')
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message: 'Deletion queued. Progress is available in View activity.', error: false}}))
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Could not delete the cluster.'
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message, error: true}}))
      notify(message, true)
    }
  }

  const recreateKubernetesCluster = async (clusterID: string, name: string) => {
    const resource = data.kubernetesClusters.find(item => item.id === clusterID)
    const retry = ['failed', 'recreation-failed', 'maintenance-failed'].includes(resource?.status ?? '')
    const meshOnly = retriesOnlyClusterMesh(resource, data.pipelineRuns)
    if (!await confirmAction(meshOnly ? `Retry the ClusterMesh connection for ${name} using the existing clusters?` : retry ? `Retry ${name}? Existing cluster VMs will be replaced using the saved settings.` : `Recreate ${name}? Its VMs will be replaced using the saved topology.`, {title: retry ? 'Retry cluster creation' : 'Recreate cluster', label: retry ? 'Retry' : 'Recreate'})) return
    setClusterActions(items => ({...items, [clusterID]: {pending: true, message: meshOnly ? 'Validating existing clusters and mesh inputs…' : 'Validating cluster health, ownership and cleanup targets…', error: false}}))
    notify(retry ? 'Validating cluster retry.' : 'Validating cluster recreation.')
    try {
      await request(`/kubernetes-clusters/${clusterID}/recreate`, {method: 'POST', body: '{}'}, session?.csrfToken)
      await afterMutation(retry ? 'Cluster retry queued.' : 'Cluster recreation validated and queued.')
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message: retry ? 'Retry queued. Progress is available in Activity.' : 'Recreation queued. Progress is available in Activity.', error: false}}))
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : retry ? 'Could not retry cluster creation.' : 'Could not recreate the cluster.'
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message, error: true}}))
      notify(message, true)
    }
  }

  if (checkingSession) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Loading…</strong></div>
  if (!session) return <div className="startup-screen"><strong>Could not check session.</strong><button className="button" onClick={() => window.location.reload()}>Retry</button><ToastRegion items={toasts} dismiss={dismiss} /></div>
  if (!session.authenticated && !session.setupRequired) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Redirecting to sign in…</strong></div>
  if (!session.authenticated) return <><div className="auth-theme-toggle"><ThemeToggle /></div><AdministratorSetupDialog onAuthenticated={result => {setSession(result); notify(session?.setupRequired ? 'Administrator created.' : 'Signed in.')}} /><ToastRegion items={toasts} dismiss={dismiss} /></>

  const title = pipelineRunID ? 'Run logs' : operationID ? 'Activity' : viewMetadata[view]
  return <>
    {confirmationDialog}
    <div className={`app-shell ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`}>
      <aside className="sidebar">
        <button className="brand" onClick={() => navigate('overview')} aria-label="KubePhos home"><span className="brand-mark">K</span><strong>KubePhos</strong></button>
        <button className="sidebar-toggle" type="button" aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'} onClick={() => setSidebarCollapsed(value => !value)}><ActionIcon name={sidebarCollapsed ? 'right' : 'left'} /></button>
		<button ref={mobileNavigationButton} className="mobile-navigation-toggle icon-button" type="button" aria-label={mobileNavigationOpen ? 'Close navigation' : 'Open navigation'} aria-controls="main-navigation" aria-expanded={mobileNavigationOpen} onClick={() => setMobileNavigationOpen(value => !value)}><ActionIcon name={mobileNavigationOpen ? 'close' : 'menu'} /></button>
		<nav id="main-navigation" className={`navigation${mobileNavigationOpen ? ' is-open' : ''}`} aria-label="Main navigation" onKeyDown={event => {if (event.key === 'Escape' && mobileNavigationOpen) {setMobileNavigationOpen(false); mobileNavigationButton.current?.focus()}}}>{visibleNavigation(session.user?.role === 'admin').map((group, index) => <div className="navigation-group" key={index}>{group.map(([target, label]) => <button className={`nav-item ${view === target ? 'active' : ''}`} title={sidebarCollapsed ? label : undefined} aria-label={label} key={target} onClick={() => navigate(target)} aria-current={view === target ? 'page' : undefined}><NavigationIcon view={target} /><span className="nav-label">{label}</span></button>)}</div>)}</nav>
      </aside>
      <main className="main">
		<header className="topbar"><nav className="app-breadcrumbs" aria-label="Breadcrumb"><button onClick={() => navigate('overview')}>KubePhos</button><ActionIcon name="right" /><strong aria-current="page">{title}</strong></nav><div className="topbar-actions"><span className="signed-user">{session.user?.username}</span><ThemeToggle /><button className="button secondary compact" disabled={refreshing} onClick={() => load(false)}>{refreshing ? 'Refreshing…' : 'Refresh'}</button><button className="button secondary compact" onClick={logout}>Sign out</button></div></header>
        <div className="page-content">
          {ready && !connected && <div className="connection-notice" role="status"><div><strong>Some information could not be refreshed</strong><span>Showing the latest available data. We’ll retry automatically.</span></div><button className="button secondary compact" disabled={refreshing} onClick={() => void load(false)}>Try again</button></div>}
          {ready && (pipelineRunID ? <PipelineRunLogsPage runID={pipelineRunID} close={() => setPipelineRunID(null)} notify={notify} /> : operationID ? <OperationDetailsPage operationID={operationID} session={session} plugins={data.plugins} close={() => setOperationID(null)} open={setOperationID} changed={refreshAfterChange} notify={notify} /> : null)}
          {!ready ? <div className="page-loading" role="status"><span className="loading-indicator" aria-hidden="true" /><strong>Loading KubePhos…</strong></div> : <div hidden={Boolean(pipelineRunID || operationID)}><Views
          view={view}
          {...data}
          session={session}
          changed={changesApplied}
          isAdmin={session.user?.role === 'admin'}
          navigate={navigate}
          openOperation={setOperationID}
          openPipelineRun={setPipelineRunID}
          importApplication={() => setModal('application')}
          deleteApplication={deleteApplication}
		  addConnection={() => setModal('connection')}
		  addMachineTemplate={() => setModal('machineTemplate')}
		  addInfrastructureService={kind => {setServiceKind(kind); setModal('infrastructureService')}}
		  deleteConnection={deleteConnection}
		  deleteMachineTemplate={deleteMachineTemplate}
		  deleteInfrastructureService={deleteInfrastructureService}
		  addKubernetesCluster={() => setModal('kubernetesCluster')}
		  deleteKubernetesCluster={deleteKubernetesCluster}
		  recreateKubernetesCluster={recreateKubernetesCluster}
		  clusterActions={clusterActions}
        /></div>}</div>
      </main>
    </div>
	{modal === 'connection' && <ConnectionDialog {...common} open close={() => setModal(null)} plugins={data.plugins} />}
	{modal === 'machineTemplate' && <MachineTemplateDialog {...common} open close={() => setModal(null)} plugins={data.plugins} workspaces={data.workspaces} />}
	{modal === 'infrastructureService' && <InfrastructureServiceDialog {...common} open close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} initialKind={serviceKind} />}
	{modal === 'kubernetesCluster' && <KubernetesClusterDialog {...common} open close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} services={data.infrastructureServices} />}
    {modal === 'application' && <ApplicationDialog {...common} plugins={data.plugins} open close={() => setModal(null)} />}
    <ToastRegion items={toasts} dismiss={dismiss} />
  </>
}

function initialView(): View {
  const next = hashView(window.location.hash)
  return next === 'users' || next === 'administration' || navigation.some(([view]) => view === next) ? next : 'overview'
}

function visibleNavigation(admin: boolean): Array<Array<[View, string]>> {
	const items = admin ? [...navigation, ['users', 'Users'] as [View, string], ['administration', 'Administration'] as [View, string]] : navigation.filter(([view]) => !['infrastructure'].includes(view))
	return [items.slice(0, admin ? 4 : 3), items.slice(admin ? 4 : 3)]
}
