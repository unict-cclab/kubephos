import {lazy, Suspense, useCallback, useEffect, useMemo, useState} from 'react'
import {ApiError, request} from './api'
import {ApplicationDialog, AuthDialog, ConnectionDialog, CredentialDialog, InfrastructureServiceDialog, KubernetesClusterDialog, MachineTemplateDialog, OperationDialog, PluginDialog, RuntimeDialog, WorkspaceDialog} from './components/Forms'
import {OperationDrawer} from './components/OperationDrawer'
import {PipelineDialog} from './components/PipelineDialog'
import {ToastRegion, type ToastMessage} from './components/ToastRegion'
import {Views} from './components/Views'
import {WorkspaceFlowDialog} from './components/WorkspaceFlowDialog'
import type {PlatformData, PluginPackage, Session, View, Workspace} from './types'

const TerminalDialog = lazy(() => import('./components/TerminalDialog').then(module => ({default: module.TerminalDialog})))

const emptyData: PlatformData = {system: null, pluginRuntime: null, workspaces: [], pipelines: [], pipelineRuns: [], experiments: [], operations: [], artifacts: [], plugins: [], pluginPackages: [], pluginImports: [], applications: [], credentials: [], connections: [], machineTemplates: [], infrastructureServices: [], kubernetesClusters: [], resources: [], audit: []}
const viewMetadata: Record<View, [string, string]> = {
  overview: ['CONTROL PLANE', 'Overview'],
  workspaces: ['ENVIRONMENTS', 'Workspaces'],
  pipelines: ['REUSABLE FLOWS', 'Pipelines'],
  operations: ['BACKGROUND WORK', 'Operations'],
  results: ['OBSERVATIONS', 'Results'],
  catalog: ['APPLICATIONS', 'Catalog'],
  plugins: ['CAPABILITIES', 'Plugins'],
  infrastructure: ['MANAGED INFRASTRUCTURE', 'Infrastructure'],
  kubernetes: ['CLUSTERS', 'Kubernetes'],
  experiments: ['REPEATABLE TESTS', 'Experiments'],
  suites: ['COMPARISONS', 'Suites'],
  advanced: ['PLATFORM TOOLS', 'Advanced']
}
const navigation: Array<[View, string, string]> = [
  ['overview', '⌂', 'Overview'],
  ['infrastructure', '▦', 'Infrastructure'],
  ['kubernetes', '⬡', 'Kubernetes'],
  ['experiments', '▶', 'Experiments'],
  ['suites', '⇄', 'Suites'],
  ['results', '⌁', 'Results'],
  ['advanced', '•••', 'Advanced']
]

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

  const notify = useCallback((message: string, error = false) => {
    setToasts(items => [...items, {id: Date.now() + Math.random(), message, error}])
  }, [])
  const dismiss = useCallback((id: number) => setToasts(items => items.filter(item => item.id !== id)), [])

  const load = useCallback(async (silent = true) => {
    if (!session?.authenticated) return
    setRefreshing(true)
    try {
		const [system, pluginRuntime, workspaces, pipelines, pipelineRuns, experiments, operations, artifacts, plugins, pluginPackages, pluginImports, applications, credentials, connections, machineTemplates, infrastructureServices, kubernetesClusters, resources, audit] = await Promise.all([
        request<PlatformData['system']>('/system'),
        request<PlatformData['pluginRuntime']>('/plugin-runtime'),
        request<{items: PlatformData['workspaces']}>('/workspaces'),
        request<{items: PlatformData['pipelines']}>('/pipelines'),
        request<{items: PlatformData['pipelineRuns']}>('/pipeline-runs'),
        request<{items: PlatformData['experiments']}>('/experiments'),
        request<{items: PlatformData['operations']}>('/operations'),
        request<{items: PlatformData['artifacts']}>('/artifacts?limit=500'),
        request<{items: PlatformData['plugins']}>('/plugins'),
        request<{items: PlatformData['pluginPackages']}>('/plugin-packages'),
        request<{items: PlatformData['pluginImports']}>('/plugin-imports'),
        request<{items: PlatformData['applications']}>('/catalog/applications'),
        request<{items: PlatformData['credentials']}>('/credentials'),
		request<{items: PlatformData['connections']}>('/connections'),
		request<{items: PlatformData['machineTemplates']}>('/machine-templates'),
		request<{items: PlatformData['infrastructureServices']}>('/infrastructure-services'),
		request<{items: PlatformData['kubernetesClusters']}>('/kubernetes-clusters'),
		request<{items: PlatformData['resources']}>('/infrastructure/resources'),
        request<{items: PlatformData['audit']}>('/audit?limit=20')
      ])
		setData({system, pluginRuntime, workspaces: workspaces.items, pipelines: pipelines.items, pipelineRuns: pipelineRuns.items, experiments: experiments.items, operations: operations.items, artifacts: artifacts.items, plugins: plugins.items, pluginPackages: pluginPackages.items, pluginImports: pluginImports.items, applications: applications.items, credentials: credentials.items, connections: connections.items, machineTemplates: machineTemplates.items, infrastructureServices: infrastructureServices.items, kubernetesClusters: kubernetesClusters.items, resources: resources.items, audit: audit.items})
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
      const result = await request<{deletionOperationId?: string}>(`/machine-templates/${templateID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Template deletion queued.')
      if (result.deletionOperationId) setOperationID(result.deletionOperationId)
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
    try {
      await request(`/kubernetes-clusters/${clusterID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Cluster deletion validated and queued.')
    } catch (cause) {
      notify(cause instanceof Error ? cause.message : 'Could not delete the cluster.', true)
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

  if (checkingSession) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Starting KubePhos…</strong></div>
  if (!session?.authenticated) return <><AuthDialog setupRequired={Boolean(session?.setupRequired)} onAuthenticated={result => {setSession(result); notify(session?.setupRequired ? 'Administrator created.' : 'Signed in.')}} /><ToastRegion items={toasts} dismiss={dismiss} /></>

  const [eyebrow, title] = viewMetadata[view]
  const healthy = connected && data.system?.status === 'healthy'
  return <>
    <div className="app-shell">
      <aside className="sidebar">
        <button className="brand" onClick={() => navigate('overview')} aria-label="KubePhos home"><span className="brand-mark">K</span><span><strong>KubePhos</strong><small>Kubernetes workspace</small></span></button>
        <nav className="navigation" aria-label="Main navigation">{navigation.map(([target, icon, label]) => <button className={`nav-item ${view === target ? 'active' : ''}`} key={target} onClick={() => navigate(target)}><span>{icon}</span>{label}</button>)}</nav>
        <div className="sidebar-footer"><div className={`health-dot ${healthy ? 'healthy' : 'unhealthy'}`} /><span><strong>{healthy ? 'All systems healthy' : connected ? 'Platform degraded' : 'Connection unavailable'}</strong><small>Version {data.system?.version ?? 'dev'}</small></span></div>
      </aside>
      <main className="main">
		<header className="topbar"><div><p className="eyebrow">{eyebrow}</p><h1>{title}</h1></div><div className="topbar-actions"><span className="signed-user">{session.user?.username} · {session.user?.role}</span><button className="button secondary" onClick={logout}>Sign out</button><button className="button secondary" disabled={refreshing} onClick={() => load(false)}>{refreshing ? 'Refreshing…' : 'Refresh'}</button></div></header>
        <Views
          view={view}
          {...data}
          session={session}
          changed={() => afterMutation('Experiment saved.')}
          isAdmin={session.user?.role === 'admin'}
          navigate={navigate}
          createWorkspace={() => setModal('workspace')}
          createPipeline={() => setModal('pipeline')}
          createOperation={openOperationForm}
          openWorkspace={openWorkspace}
          openOperation={setOperationID}
          importApplication={() => setModal('application')}
          importPlugin={() => setModal('plugin')}
          configureRuntime={() => setModal('runtime')}
          activatePlugin={activatePlugin}
          deactivatePlugin={deactivatePlugin}
          addCredential={() => setModal('credential')}
		  addConnection={() => setModal('connection')}
		  addMachineTemplate={() => setModal('machineTemplate')}
		  addInfrastructureService={() => setModal('infrastructureService')}
		  deleteConnection={deleteConnection}
		  deleteMachineTemplate={deleteMachineTemplate}
		  deleteInfrastructureService={deleteInfrastructureService}
		  addKubernetesCluster={() => setModal('kubernetesCluster')}
		  deleteKubernetesCluster={deleteKubernetesCluster}
          openInfrastructureCapability={openInfrastructureCapability}
          openTerminal={selected => {setWorkspace(selected); setModal('terminal')}}
        />
      </main>
    </div>
    <WorkspaceDialog {...common} open={modal === 'workspace'} close={() => setModal(null)} />
    <PipelineDialog open={modal === 'pipeline'} close={() => setModal(null)} session={session} workspaces={data.workspaces} plugins={data.plugins} applications={data.applications} artifacts={data.artifacts} connections={data.connections} credentials={data.credentials} changed={() => afterMutation('Pipeline validated and saved.')} />
    <WorkspaceFlowDialog open={modal === 'workspaceFlow'} close={() => setModal(null)} workspace={workspace} plugins={data.plugins} artifacts={data.artifacts} operations={data.operations} configure={configureWorkspaceCapability} />
    <OperationDialog key={`${workspace?.id ?? ''}-${operationPluginID ?? 'advanced'}-${JSON.stringify(operationPreset)}`} {...common} initialPluginID={operationPluginID} initialSpec={operationPreset} open={modal === 'operation'} close={() => setModal(null)} workspace={workspace} plugins={data.plugins} onCreated={async id => {await load(true); setOperationID(id)}} />
    <CredentialDialog {...common} open={modal === 'credential'} close={() => setModal(null)} plugins={data.plugins} />
	<ConnectionDialog {...common} open={modal === 'connection'} close={() => setModal(null)} plugins={data.plugins} />
	<MachineTemplateDialog {...common} open={modal === 'machineTemplate'} close={() => setModal(null)} plugins={data.plugins} workspaces={data.workspaces} />
	<InfrastructureServiceDialog {...common} open={modal === 'infrastructureService'} close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} />
	<KubernetesClusterDialog {...common} open={modal === 'kubernetesCluster'} close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} services={data.infrastructureServices} />
    <ApplicationDialog {...common} open={modal === 'application'} close={() => setModal(null)} />
    <PluginDialog {...common} open={modal === 'plugin'} close={() => setModal(null)} />
    <RuntimeDialog {...common} open={modal === 'runtime'} close={() => setModal(null)} runtime={data.pluginRuntime} />
    {modal === 'terminal' && <Suspense fallback={null}><TerminalDialog open close={() => setModal(null)} session={session} workspaces={data.workspaces} initialWorkspace={workspace} /></Suspense>}
    <OperationDrawer operationID={operationID} session={session} plugins={data.plugins} close={() => setOperationID(null)} open={setOperationID} changed={() => load(true)} notify={notify} />
    <ToastRegion items={toasts} dismiss={dismiss} />
  </>
}

function initialView(): View {
  const value = window.location.hash.slice(1) as View
  return value in viewMetadata ? value : 'overview'
}
