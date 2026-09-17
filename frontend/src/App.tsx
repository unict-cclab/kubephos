import {lazy, Suspense, useCallback, useEffect, useMemo, useState} from 'react'
import {ApiError, request} from './api'
import {ApplicationDialog, AuthDialog, ConnectionDialog, CredentialDialog, InfrastructureServiceDialog, KubernetesClusterDialog, MachineTemplateDialog, OperationDialog, PluginDialog, RuntimeDialog, WorkspaceDialog} from './components/Forms'
import {OperationDetailsPage} from './components/OperationDrawer'
import {PipelineDialog} from './components/PipelineDialog'
import {ToastRegion, type ToastMessage} from './components/ToastRegion'
import {Views} from './components/Views'
import {WorkspaceFlowDialog} from './components/WorkspaceFlowDialog'
import type {Application, PlatformData, PluginPackage, Session, View, Workspace} from './types'
import {hashView} from './detailRoute'

const TerminalDialog = lazy(() => import('./components/TerminalDialog').then(module => ({default: module.TerminalDialog})))

const emptyData: PlatformData = {system: null, pluginRuntime: null, workspaces: [], pipelines: [], pipelineRuns: [], experiments: [], experimentConfigurations: [], operations: [], artifacts: [], plugins: [], pluginPackages: [], pluginImports: [], applications: [], strategies: [], credentials: [], connections: [], machineTemplates: [], infrastructureServices: [], kubernetesClusters: [], resources: [], audit: []}
const viewMetadata: Record<View, string> = {
  overview: 'Overview',
  workspaces: 'Workspaces',
  pipelines: 'Pipelines',
  operations: 'Operations',
  results: 'Experiments',
  catalog: 'Catalog',
  plugins: 'Plugins',
  infrastructure: 'Infrastructure',
  kubernetes: 'Kubernetes',
  experiments: 'Configurations',
  suites: 'Suites',
  advanced: 'Advanced'
}
const navigation: Array<[View, string]> = [
  ['overview', 'Overview'],
  ['infrastructure', 'Infrastructure'],
  ['kubernetes', 'Kubernetes'],
  ['catalog', 'Catalog'],
  ['experiments', 'Configurations'],
  ['results', 'Experiments'],
  ['suites', 'Suites']
]
const navigationGroups = [navigation.slice(0, 4), navigation.slice(4)]

function NavigationIcon({view}: {view: View}) {
  const common = {fill: 'none', stroke: 'currentColor', strokeWidth: 1.8, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const}
  const shape = {
    overview: <><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></>,
    infrastructure: <><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 8h10M7 12h10M7 16h4"/></>,
    kubernetes: <><circle cx="12" cy="12" r="9"/><circle cx="12" cy="12" r="3"/><path d="M12 3v6m0 6v6M3 12h6m6 0h6"/></>,
    catalog: <><path d="M4 5h16v14H4zM4 12h16M9 5v7m6 0v7"/></>,
    experiments: <><path d="M6 4h12v16H6zM9 8h6M9 12h6M9 16h4"/></>,
    results: <><circle cx="12" cy="12" r="9"/><path d="m10 8 6 4-6 4z"/></>,
    suites: <><path d="M4 7h14m-3-3 3 3-3 3M20 17H6m3-3-3 3 3 3"/></>
  } as Partial<Record<View, React.ReactNode>>
  return <svg width="20" height="20" viewBox="0 0 24 24" aria-hidden="true" {...common}>{shape[view]}</svg>
}

type Modal = 'workspace' | 'workspaceFlow' | 'operation' | 'pipeline' | 'credential' | 'connection' | 'machineTemplate' | 'infrastructureService' | 'kubernetesCluster' | 'application' | 'plugin' | 'runtime' | 'terminal' | null

export default function App() {
  const [session, setSession] = useState<Session | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [data, setData] = useState<PlatformData>(emptyData)
  const [connected, setConnected] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [view, setView] = useState<View>(initialView)
  const [modal, setModal] = useState<Modal>(null)
  const [workspace, setWorkspace] = useState<Workspace | null>(null)
  const [operationPluginID, setOperationPluginID] = useState<string | undefined>()
  const [operationPreset, setOperationPreset] = useState<Record<string, unknown>>({})
  const [operationID, setOperationID] = useState<string | null>(null)
  const [toasts, setToasts] = useState<ToastMessage[]>([])
  const [clusterActions, setClusterActions] = useState<Record<string, {pending: boolean; message: string; error: boolean}>>({})
  const [sidebarCollapsed, setSidebarCollapsed] = useState(false)
  const [serviceKind, setServiceKind] = useState<'harbor' | 'nfs'>('harbor')

  const notify = useCallback((message: string, error = false) => {
    setToasts(items => [...items, {id: Date.now() + Math.random(), message, error}])
  }, [])
  const dismiss = useCallback((id: number) => setToasts(items => items.filter(item => item.id !== id)), [])

  const load = useCallback(async (silent = true) => {
    if (!session?.authenticated) return
    setRefreshing(true)
    try {
		const [system, pluginRuntime, workspaces, pipelines, pipelineRuns, experiments, experimentConfigurations, operations, artifacts, plugins, pluginPackages, pluginImports, applications, strategies, credentials, connections, machineTemplates, infrastructureServices, kubernetesClusters, resources, audit] = await Promise.all([
        request<PlatformData['system']>('/system'),
        request<PlatformData['pluginRuntime']>('/plugin-runtime'),
        request<{items: PlatformData['workspaces']}>('/workspaces'),
        request<{items: PlatformData['pipelines']}>('/pipelines'),
        request<{items: PlatformData['pipelineRuns']}>('/pipeline-runs'),
        request<{items: PlatformData['experiments']}>('/experiments'),
        request<{items: PlatformData['experimentConfigurations']}>('/experiment-configurations'),
        request<{items: PlatformData['operations']}>('/operations'),
        request<{items: PlatformData['artifacts']}>('/artifacts?limit=500'),
        request<{items: PlatformData['plugins']}>('/plugins'),
        request<{items: PlatformData['pluginPackages']}>('/plugin-packages'),
        request<{items: PlatformData['pluginImports']}>('/plugin-imports'),
        request<{items: PlatformData['applications']}>('/catalog/applications'),
        request<{items: PlatformData['strategies']}>('/catalog/strategies'),
        request<{items: PlatformData['credentials']}>('/credentials'),
		request<{items: PlatformData['connections']}>('/connections'),
		request<{items: PlatformData['machineTemplates']}>('/machine-templates'),
		request<{items: PlatformData['infrastructureServices']}>('/infrastructure-services'),
		request<{items: PlatformData['kubernetesClusters']}>('/kubernetes-clusters'),
		request<{items: PlatformData['resources']}>('/infrastructure/resources'),
        request<{items: PlatformData['audit']}>('/audit?limit=20')
      ])
		setData({system, pluginRuntime, workspaces: workspaces.items, pipelines: pipelines.items, pipelineRuns: pipelineRuns.items, experiments: experiments.items, experimentConfigurations: experimentConfigurations.items, operations: operations.items, artifacts: artifacts.items, plugins: plugins.items, pluginPackages: pluginPackages.items, pluginImports: pluginImports.items, applications: applications.items, strategies: strategies.items, credentials: credentials.items, connections: connections.items, machineTemplates: machineTemplates.items, infrastructureServices: infrastructureServices.items, kubernetesClusters: kubernetesClusters.items, resources: resources.items, audit: audit.items})
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

  const activatePlugin = async (pluginPackage: PluginPackage) => {
    if (!window.confirm(`Activate ${pluginPackage.pluginId} ${pluginPackage.version}? New runs will use this image digest.`)) return
    try {
      await request(`/plugin-packages/${pluginPackage.sequence}/activate`, {method: 'POST', body: '{}'}, session?.csrfToken)
      await afterMutation('Plugin version activated.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not activate the plugin version.', true)
    }
  }

  const deactivatePlugin = async (pluginPackage: PluginPackage) => {
    if (!window.confirm(`Deactivate ${pluginPackage.pluginId} ${pluginPackage.version}? Validated runs that require it will not start.`)) return
    try {
      await request(`/plugin-packages/${pluginPackage.sequence}/deactivate`, {method: 'POST', body: '{}'}, session?.csrfToken)
      await afterMutation('Plugin deactivated. Its history was preserved.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not deactivate the plugin.', true)
    }
  }

  const deleteApplication = async (application: Application) => {
    if (!window.confirm(`Delete ${application.name} ${application.version} from the catalog? Existing experiment configurations must be removed first.`)) return false
    try {
      await request(`/catalog/applications/${encodeURIComponent(application.id)}/${encodeURIComponent(application.version)}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Application removed from the catalog. Its audit history was preserved.')
      return true
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not remove the application from the catalog.', true)
      return false
    }
  }

  const deleteConnection = async (connectionID: string, name: string) => {
    if (!window.confirm(`Delete connection ${name}?`)) return
    try {
      await request(`/connections/${connectionID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Connection deleted.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the connection.', true)
    }
  }

  const deleteMachineTemplate = async (templateID: string, name: string) => {
    if (!window.confirm(`Delete Proxmox template ${name}? The task will run in the background.`)) return
    try {
      await request(`/machine-templates/${templateID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Template deletion queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the template.', true)
    }
  }

  const deleteInfrastructureService = async (serviceID: string, name: string) => {
    if (!window.confirm(`Delete service ${name} and its dedicated VM?`)) return
    try {
      await request(`/infrastructure-services/${serviceID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Service deletion validated and queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the service.', true)
    }
  }

  const deleteKubernetesCluster = async (clusterID: string, name: string) => {
    if (!window.confirm(`Delete cluster ${name}, its Kubernetes components and all dedicated VMs?`)) return
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
    if (!window.confirm(`Recreate cluster ${name} with its saved topology and managed components? Existing cluster VMs will be replaced.`)) return
    setClusterActions(items => ({...items, [clusterID]: {pending: true, message: 'Validating cluster health, ownership and cleanup targets…', error: false}}))
    notify('Validating cluster recreation. This can take up to a minute.')
    try {
      await request(`/kubernetes-clusters/${clusterID}/recreate`, {method: 'POST', body: '{}'}, session?.csrfToken)
      await afterMutation('Cluster recreation validated and queued.')
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message: 'Recreation queued. Progress is available in View activity.', error: false}}))
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Could not recreate the cluster.'
      setClusterActions(items => ({...items, [clusterID]: {pending: false, message, error: true}}))
      notify(message, true)
    }
  }

  const openOperationForm = (selected: Workspace) => {
    if (!data.plugins.length) {
      notify('No plugin is installed.', true)
      return
    }
    setWorkspace(selected)
    setOperationPluginID(undefined)
    setOperationPreset({})
    setModal('operation')
  }

  const openWorkspace = (selected: Workspace) => {
    setWorkspace(selected)
    setModal('workspaceFlow')
  }

  const configureWorkspaceCapability = (pluginID: string, preset: Record<string, unknown> = {}) => {
    setOperationPluginID(pluginID)
    setOperationPreset(preset)
    setModal('operation')
  }

  const openInfrastructureCapability = (selected: Workspace, pluginID: string) => {
    setWorkspace(selected)
    setOperationPluginID(pluginID)
    setOperationPreset({})
    setModal('operation')
  }

  if (checkingSession) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Loading…</strong></div>
  if (!session?.authenticated) return <><AuthDialog setupRequired={Boolean(session?.setupRequired)} onAuthenticated={result => {setSession(result); notify(session?.setupRequired ? 'Administrator created.' : 'Signed in.')}} /><ToastRegion items={toasts} dismiss={dismiss} /></>

  const title = viewMetadata[view]
  const healthy = connected && data.system?.status === 'healthy'
  return <>
    <div className={`app-shell ${sidebarCollapsed ? 'sidebar-collapsed' : ''}`}>
      <aside className="sidebar">
        <button className="brand" onClick={() => navigate('overview')} aria-label="KubePhos home"><span className="brand-mark">K</span><strong>KubePhos</strong></button>
        <button className="sidebar-toggle" type="button" aria-label={sidebarCollapsed ? 'Expand sidebar' : 'Collapse sidebar'} onClick={() => setSidebarCollapsed(value => !value)}>{sidebarCollapsed ? '›' : '‹'}</button>
        <nav className="navigation" aria-label="Main navigation">{navigationGroups.map((group, index) => <div className="navigation-group" key={index}>{group.map(([target, label]) => <button className={`nav-item ${view === target ? 'active' : ''}`} title={sidebarCollapsed ? label : undefined} aria-label={label} key={target} onClick={() => navigate(target)} aria-current={view === target ? 'page' : undefined}><NavigationIcon view={target} /><span className="nav-label">{label}</span></button>)}</div>)}</nav>
        <div className="sidebar-footer"><div className={`health-dot ${healthy ? 'healthy' : 'unhealthy'}`} /><span><strong>{healthy ? 'Healthy' : connected ? 'Needs attention' : 'Offline'}</strong><small>{data.system?.version ?? 'dev'}</small></span></div>
      </aside>
      <main className="main">
		<header className="topbar"><nav className="app-breadcrumbs" aria-label="Breadcrumb"><button onClick={() => navigate('overview')}>KubePhos</button><span aria-hidden="true">/</span><strong aria-current="page">{title}</strong></nav><div className="topbar-actions"><span className="signed-user">{session.user?.username}</span><button className="button secondary compact" disabled={refreshing} onClick={() => load(false)}>{refreshing ? 'Refreshing…' : 'Refresh'}</button><button className="button secondary compact" onClick={logout}>Sign out</button></div></header>
        <div className="page-content">{operationID ? <OperationDetailsPage operationID={operationID} session={session} plugins={data.plugins} close={() => setOperationID(null)} open={setOperationID} changed={() => load(true)} notify={notify} /> : <Views
          view={view}
          {...data}
          session={session}
          changed={() => afterMutation('Changes applied.')}
          isAdmin={session.user?.role === 'admin'}
          navigate={navigate}
          createWorkspace={() => setModal('workspace')}
          createPipeline={() => setModal('pipeline')}
          createOperation={openOperationForm}
          openWorkspace={openWorkspace}
          openOperation={setOperationID}
          importApplication={() => setModal('application')}
          deleteApplication={deleteApplication}
          importPlugin={() => setModal('plugin')}
          configureRuntime={() => setModal('runtime')}
          activatePlugin={activatePlugin}
          deactivatePlugin={deactivatePlugin}
          addCredential={() => setModal('credential')}
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
          openInfrastructureCapability={openInfrastructureCapability}
          openTerminal={selected => {setWorkspace(selected); setModal('terminal')}}
        />}</div>
      </main>
    </div>
    <WorkspaceDialog {...common} open={modal === 'workspace'} close={() => setModal(null)} />
    <PipelineDialog open={modal === 'pipeline'} close={() => setModal(null)} session={session} workspaces={data.workspaces} plugins={data.plugins} applications={data.applications} artifacts={data.artifacts} connections={data.connections} credentials={data.credentials} changed={() => afterMutation('Pipeline validated and saved.')} />
    <WorkspaceFlowDialog open={modal === 'workspaceFlow'} close={() => setModal(null)} workspace={workspace} plugins={data.plugins} artifacts={data.artifacts} operations={data.operations} configure={configureWorkspaceCapability} />
    <OperationDialog key={`${workspace?.id ?? ''}-${operationPluginID ?? 'advanced'}-${JSON.stringify(operationPreset)}`} {...common} initialPluginID={operationPluginID} initialSpec={operationPreset} open={modal === 'operation'} close={() => setModal(null)} workspace={workspace} plugins={data.plugins} onCreated={async id => {await load(true); setOperationID(id)}} />
    <CredentialDialog {...common} open={modal === 'credential'} close={() => setModal(null)} plugins={data.plugins} />
	<ConnectionDialog {...common} open={modal === 'connection'} close={() => setModal(null)} plugins={data.plugins} />
	<MachineTemplateDialog {...common} open={modal === 'machineTemplate'} close={() => setModal(null)} plugins={data.plugins} workspaces={data.workspaces} />
	<InfrastructureServiceDialog {...common} open={modal === 'infrastructureService'} close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} initialKind={serviceKind} />
	<KubernetesClusterDialog {...common} open={modal === 'kubernetesCluster'} close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} services={data.infrastructureServices} />
    <ApplicationDialog {...common} plugins={data.plugins} open={modal === 'application'} close={() => setModal(null)} />
    <PluginDialog {...common} open={modal === 'plugin'} close={() => setModal(null)} />
    <RuntimeDialog {...common} open={modal === 'runtime'} close={() => setModal(null)} runtime={data.pluginRuntime} />
    {modal === 'terminal' && <Suspense fallback={null}><TerminalDialog open close={() => setModal(null)} session={session} workspaces={data.workspaces} initialWorkspace={workspace} /></Suspense>}
    <ToastRegion items={toasts} dismiss={dismiss} />
  </>
}

function initialView(): View {
  const next = hashView(window.location.hash)
  return navigation.some(([view]) => view === next) ? next : 'overview'
}
