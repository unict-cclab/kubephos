import type {ClusterAction} from './clusterActions'
import {ActionIcon} from './components/ActionIcon'
import {useDeleteConfirmation} from './components/ConfirmationDialog'
import {useCallback, useEffect, useMemo, useRef, useState} from 'react'
import {request} from './api'
import {usePlatformData} from './hooks/usePlatformData'
import {ApplicationDialog, AdministratorSetupDialog, ConnectionDialog, InfrastructureServiceDialog, KubernetesClusterDialog, MachineTemplateDialog} from './components/Forms'
import {OperationDetailsPage, PipelineRunLogsPage} from './components/OperationDrawer'
import {NotificationProvider, useNotifications} from './components/ToastRegion'
import {ThemeToggle} from './components/ThemeToggle'
import {SidebarNavigation} from './components/SidebarNavigation'
import {Views} from './components/Views'
import type {Application, Session, View} from './types'
import {retriesOnlyClusterMesh} from './clusterRetry'
import {hashView, newExperimentHash} from './detailRoute'
import {currentSection, isNavigableView, navigationTargets, visibleNavigation} from './navigation'
import {AccountMenu} from './components/AccountMenu'
import {WorkflowSteps, workflowFor} from './components/WorkflowSteps'
import {SectionContext} from './components/ResourceListHeading'
import {CommandPalette, isPaletteShortcut, paletteShortcut} from './components/CommandPalette'

type Modal = 'connection' | 'machineTemplate' | 'infrastructureService' | 'kubernetesCluster' | 'application' | null

export default function App() {
  return <NotificationProvider><PlatformApp /></NotificationProvider>
}

function PlatformApp() {
  const {confirmDelete, confirmAction, confirmationDialog} = useDeleteConfirmation()
  const [session, setSession] = useState<Session | null>(null)
  const [checkingSession, setCheckingSession] = useState(true)
  const [view, setView] = useState<View>(initialView)
  const [modal, setModal] = useState<Modal>(null)
  const [operationID, setOperationID] = useState<string | null>(null)
  const [pipelineRunID, setPipelineRunID] = useState<string | null>(null)
  const {notify} = useNotifications()!
  const [clusterActions, setClusterActions] = useState<Record<string, ClusterAction>>({})
  const [mobileNavigationOpen, setMobileNavigationOpen] = useState(false)
  const [paletteOpen, setPaletteOpen] = useState(false)
  const mobileNavigationButton = useRef<HTMLButtonElement>(null)
  const [serviceKind, setServiceKind] = useState<'harbor' | 'nfs'>('harbor')

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
    const openPalette = (event: KeyboardEvent) => {
      if (!isPaletteShortcut(event)) return
      event.preventDefault()
      setPaletteOpen(true)
    }
    window.addEventListener('keydown', openPalette)
    return () => window.removeEventListener('keydown', openPalette)
  }, [])

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
  const changesApplied = useCallback((message = 'Changes saved.') => afterMutation(message), [afterMutation])
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
    setClusterActions(items => ({...items, [clusterID]: 'validating-deletion'}))
    try {
      await request(`/kubernetes-clusters/${clusterID}`, {method: 'DELETE'}, session?.csrfToken)
      await afterMutation('Cluster deletion queued.')
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : 'Could not delete the cluster.'
      notify(message, true)
    } finally {
      setClusterActions(items => {const next = {...items}; delete next[clusterID]; return next})
    }
  }

  const recreateKubernetesCluster = async (clusterID: string, name: string) => {
    const resource = data.kubernetesClusters.find(item => item.id === clusterID)
    const retry = ['failed', 'recreation-failed', 'maintenance-failed'].includes(resource?.status ?? '')
    const meshOnly = retriesOnlyClusterMesh(resource, data.pipelineRuns)
    if (!await confirmAction(meshOnly ? `Retry the ClusterMesh connection for ${name} using the existing clusters?` : retry ? `Retry ${name}? Existing cluster VMs will be replaced using the saved settings.` : `Recreate ${name}? Its VMs will be replaced using the saved topology.`, {title: retry ? 'Retry cluster creation' : 'Recreate cluster', label: retry ? 'Retry' : 'Recreate'})) return
    setClusterActions(items => ({...items, [clusterID]: retry ? 'validating-retry' : 'validating-recreation'}))
    try {
      await request(`/kubernetes-clusters/${clusterID}/recreate`, {method: 'POST', body: '{}'}, session?.csrfToken)
      await afterMutation(retry ? 'Cluster retry queued.' : 'Cluster recreation queued.')
    } catch (cause) {
      const message = cause instanceof Error ? cause.message : retry ? 'Could not retry cluster creation.' : 'Could not recreate the cluster.'
      notify(message, true)
    } finally {
      setClusterActions(items => {const next = {...items}; delete next[clusterID]; return next})
    }
  }

  if (checkingSession) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Loading…</strong></div>
  if (!session) return <div className="startup-screen"><strong>Could not check session.</strong><button className="button" onClick={() => window.location.reload()}>Retry</button></div>
  if (!session.authenticated && !session.setupRequired) return <div className="startup-screen"><span className="brand-mark">K</span><strong>Redirecting to sign in…</strong></div>
  if (!session.authenticated) return <><div className="auth-theme-toggle"><ThemeToggle /></div><AdministratorSetupDialog onAuthenticated={result => {setSession(result); notify(session?.setupRequired ? 'Administrator created.' : 'Signed in.')}} /></>

  const closeMobileNavigation = () => {setMobileNavigationOpen(false); mobileNavigationButton.current?.focus()}
  const isAdmin = session.user?.role === 'admin'
  const section = currentSection(view)
  const workflow = workflowFor(view, {isAdmin, infrastructure: data.connections.length + data.machineTemplates.length + data.infrastructureServices.length, clusters: data.kubernetesClusters.length, deployments: data.deployments.length, configurations: data.experimentConfigurations.length, runs: data.experiments.length})
  const sectionHeading = section && workflow ? {...section, subnav: <WorkflowSteps workflow={workflow} view={view} navigate={navigate} />} : section
  const navigationSections = visibleNavigation(isAdmin)
  // The product's primary action; the experiments view opens the launcher or explains
  // which configuration is missing.
  const startExperiment = () => {
    setMobileNavigationOpen(false)
    window.location.hash = newExperimentHash
  }
  return <>
    {confirmationDialog}
    {paletteOpen && <CommandPalette items={navigationTargets(isAdmin)} close={() => setPaletteOpen(false)} select={navigate} />}
    <div className="app-shell">
      <aside className="app-sidebar">
        <div className="app-sidebar-head">
          <button className="app-brand" type="button" onClick={() => navigate('overview')} aria-label="KubePhos home"><span className="brand-mark">K</span><strong>kubephos</strong></button>
          <button ref={mobileNavigationButton} className="app-nav-toggle icon-button" type="button" aria-label={mobileNavigationOpen ? 'Close navigation' : 'Open navigation'} aria-controls="main-navigation" aria-expanded={mobileNavigationOpen} onClick={() => setMobileNavigationOpen(value => !value)}><ActionIcon name={mobileNavigationOpen ? 'close' : 'menu'} /></button>
        </div>
        <div id="main-navigation" className={`app-sidebar-body${mobileNavigationOpen ? ' is-open' : ''}`} onKeyDown={event => {if (event.key === 'Escape' && mobileNavigationOpen) closeMobileNavigation()}}>
          <button type="button" className="button primary app-new-experiment" onClick={startExperiment}><ActionIcon name="create" />New experiment</button>
          <button type="button" className="app-jump" onClick={() => setPaletteOpen(true)}><ActionIcon name="search" /><span>Jump to…</span><kbd>{paletteShortcut()}</kbd></button>
          <SidebarNavigation sections={navigationSections} view={view} navigate={navigate} />
          <AccountMenu username={session.user?.username ?? 'Account'} isAdmin={isAdmin} version={data.system?.version} refreshing={refreshing} refresh={() => void load(false)} openBackup={() => navigate('administration')} signOut={() => void logout()} />
        </div>
      </aside>
      <main className="app-main">
        <div className="page-content">
          {ready && !connected && <div className="connection-notice" role="status"><div><strong>Some information could not be refreshed</strong><span>Showing the latest available data. We’ll retry automatically.</span></div><button className="button secondary compact" disabled={refreshing} onClick={() => void load(false)}>Try again</button></div>}
          {ready && (pipelineRunID ? <PipelineRunLogsPage runID={pipelineRunID} close={() => setPipelineRunID(null)} notify={notify} /> : operationID ? <OperationDetailsPage operationID={operationID} session={session} plugins={data.plugins} close={() => setOperationID(null)} open={setOperationID} changed={refreshAfterChange} notify={notify} /> : null)}
          {!ready ? <div className="page-loading" role="status"><span className="loading-indicator" aria-hidden="true" /><strong>Loading KubePhos…</strong></div> : <div hidden={Boolean(pipelineRunID || operationID)}><SectionContext.Provider value={sectionHeading}><Views
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
        /></SectionContext.Provider></div>}</div>
      </main>
    </div>
	{modal === 'connection' && <ConnectionDialog {...common} open close={() => setModal(null)} plugins={data.plugins} />}
	{modal === 'machineTemplate' && <MachineTemplateDialog {...common} open close={() => setModal(null)} plugins={data.plugins} workspaces={data.workspaces} />}
	{modal === 'infrastructureService' && <InfrastructureServiceDialog {...common} open close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} initialKind={serviceKind} />}
	{modal === 'kubernetesCluster' && <KubernetesClusterDialog {...common} open close={() => setModal(null)} workspaces={data.workspaces} templates={data.machineTemplates} services={data.infrastructureServices} />}
    {modal === 'application' && <ApplicationDialog {...common} plugins={data.plugins} open close={() => setModal(null)} />}

  </>
}

function initialView(): View {
  const next = hashView(window.location.hash)
  return isNavigableView(next) ? next : 'overview'
}
