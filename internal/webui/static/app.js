const state = {
  system: null,
  workspaces: [],
  operations: [],
  plugins: [],
  credentials: [],
  resources: [],
  audit: [],
  selectedOperation: null,
  eventSource: null
}

const views = {
  overview: ['CONTROL PLANE', 'Overview'],
  workspaces: ['ENVIRONMENTS', 'Workspaces'],
  operations: ['BACKGROUND WORK', 'Operations'],
  plugins: ['CAPABILITIES', 'Plugins'],
  infrastructure: ['MANAGED ACCESS', 'Infrastructure']
}

const $ = selector => document.querySelector(selector)
const $$ = selector => [...document.querySelectorAll(selector)]

async function api(path, options = {}) {
  const response = await fetch(`/api/v1${path}`, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) }
  })
  if (response.status === 204 || response.status === 202 && response.headers.get('content-length') === '0') return null
  const body = await response.json().catch(() => ({}))
  if (!response.ok) {
    const error = new Error(body.message || 'The request could not be completed.')
    error.body = body
    throw error
  }
  return body
}

async function loadAll(silent = false) {
  try {
    const [system, workspaces, operations, plugins, credentials, resources, audit] = await Promise.all([
      api('/system'), api('/workspaces'), api('/operations'), api('/plugins'), api('/credentials'), api('/infrastructure/resources'), api('/audit?limit=20')
    ])
    state.system = system
    state.workspaces = workspaces.items
    state.operations = operations.items
    state.plugins = plugins.items
    state.credentials = credentials.items
    state.resources = resources.items
    state.audit = audit.items
    render()
    setHealth(true)
    if (!silent) toast('Everything is up to date.')
  } catch (error) {
    setHealth(false)
    if (!silent) toast(error.message, true)
  }
}

function render() {
  const stats = state.system?.stats || {}
  $('#stat-workspaces').textContent = stats.workspaces ?? '—'
  $('#stat-active').textContent = stats.activeOperations ?? '—'
  $('#stat-ready').textContent = stats.readyOperations ?? '—'
  $('#stat-failed').textContent = stats.failedOperations ?? '—'
  $('#sidebar-version').textContent = `Version ${state.system?.version || 'dev'}`
  renderOperations('#overview-operations', state.operations.slice(0, 5))
  renderOperations('#operation-list', state.operations)
  renderWorkspaces()
  renderPlugins()
  renderCredentials()
  renderResources()
  renderAudit()
}

function renderOperations(selector, operations) {
  const root = $(selector)
  if (!operations.length) {
    root.innerHTML = `<div class="empty-state"><div><strong>No operations yet</strong>Create a workspace and validate your first plan.</div></div>`
    return
  }
  root.innerHTML = operations.map(operation => {
    const workspace = state.workspaces.find(item => item.id === operation.workspaceId)
    return `<article class="operation-row" data-operation="${escapeText(operation.id)}">
      <div><h3>${escapeText(operation.title)}</h3><p>${escapeText(shortID(operation.id))}</p></div>
      <div><h3>${escapeText(workspace?.name || 'Workspace')}</h3><p>${escapeText(operation.pluginId)}</p></div>
      <span class="status ${escapeText(operation.status)}">${escapeText(operation.status)}</span>
      <span class="date">${formatDate(operation.createdAt)}</span>
      <span class="operation-arrow">›</span>
    </article>`
  }).join('')
  root.querySelectorAll('[data-operation]').forEach(element => element.addEventListener('click', () => openOperation(element.dataset.operation)))
}

function renderWorkspaces() {
  const root = $('#workspace-list')
  if (!state.workspaces.length) {
    root.innerHTML = `<div class="empty-state"><div><strong>No workspace created</strong>Workspaces keep development and experiments independent.</div></div>`
    return
  }
  root.innerHTML = state.workspaces.map(workspace => `<article class="workspace-card">
    <span class="workspace-icon">${escapeText(workspace.name.slice(0, 1).toUpperCase())}</span>
    <h3>${escapeText(workspace.name)}</h3>
    <p>${escapeText(workspace.description || 'An isolated environment ready for operations.')}</p>
    <div class="card-footer"><time>${formatDate(workspace.createdAt)}</time><button class="button secondary compact" data-run-workspace="${escapeText(workspace.id)}">New operation</button></div>
  </article>`).join('')
  root.querySelectorAll('[data-run-workspace]').forEach(button => button.addEventListener('click', () => openOperationForm(button.dataset.runWorkspace)))
}

function renderPlugins() {
  const root = $('#plugin-list')
  root.innerHTML = state.plugins.map(plugin => `<article class="feature-card">
    <span class="feature-icon">⌘</span>
    <div><h3>${escapeText(plugin.name)}</h3><p>${escapeText(plugin.description)}</p><p>${escapeText(plugin.id)} · ${escapeText(plugin.version)}</p></div>
    <span class="planned">Installed</span>
  </article>`).join('') || `<div class="empty-state"><div><strong>No plugins installed</strong>Add a conforming plugin to provide a capability.</div></div>`
}

function credentialTypes() {
  const values = new Map()
  state.plugins.forEach(plugin => (plugin.credentialSchemas || []).forEach(definition => values.set(definition.kind, definition)))
  return [...values.values()].sort((left, right) => left.name.localeCompare(right.name))
}

function renderCredentials() {
  const root = $('#credential-list')
  if (!state.credentials.length) {
    root.innerHTML = `<div class="empty-state"><div><strong>No credentials stored</strong>Add a credential declared by an installed plugin.</div></div>`
    return
  }
  root.innerHTML = state.credentials.map(credential => `<article class="credential-row">
    <span class="feature-icon">⌁</span>
    <div><h3>${escapeText(credential.name)}</h3><p>${escapeText(credential.kind)} · fingerprint ${escapeText(credential.fingerprint)}</p></div>
    <span class="status ready">Encrypted</span>
  </article>`).join('')
}

function renderResources() {
  const root = $('#resource-list')
  if (!state.resources.length) {
    root.innerHTML = `<div class="empty-state"><div><strong>No resources discovered</strong>Run an infrastructure discovery operation to populate the ledger.</div></div>`
    return
  }
  root.innerHTML = state.resources.map(resource => `<article class="resource-row">
    <span class="feature-icon">□</span>
    <div><h3>${escapeText(resource.name)}</h3><p>${escapeText(resource.kind)} · ${escapeText(resource.externalId)} · ${escapeText(resource.state)}</p></div>
    <div class="resource-badges"><span class="status ${resource.ownership === 'managed' ? 'succeeded' : ''}">${escapeText(resource.ownership)}</span><span class="protection">${escapeText(resource.protection)}</span></div>
  </article>`).join('')
}

function renderAudit() {
  const root = $('#audit-list')
  if (!state.audit.length) {
    root.innerHTML = `<div class="empty-state"><div><strong>No audit events</strong>Mutating actions will appear here.</div></div>`
    return
  }
  root.innerHTML = state.audit.map(event => `<article class="audit-row">
    <span class="audit-sequence">#${event.sequence}</span>
    <div><h3>${escapeText(event.action)}</h3><p>${escapeText(event.actor)} · ${escapeText(event.targetType)}${event.targetId ? ` · ${escapeText(shortID(event.targetId))}` : ''}</p></div>
    <span class="status ${event.outcome === 'succeeded' ? 'succeeded' : event.outcome === 'rejected' ? 'canceled' : 'failed'}">${escapeText(event.outcome)}</span>
    <time>${formatDate(event.createdAt)}</time>
  </article>`).join('')
}

function switchView(view) {
  if (!views[view]) return
  $$('.view').forEach(element => element.classList.toggle('active', element.id === `view-${view}`))
  $$('.nav-item').forEach(element => element.classList.toggle('active', element.dataset.view === view))
  $('#page-eyebrow').textContent = views[view][0]
  $('#page-title').textContent = views[view][1]
  location.hash = view
}

function openWorkspaceForm() {
  const dialog = $('#workspace-dialog')
  $('#workspace-error').textContent = ''
  dialog.querySelector('form').reset()
  dialog.showModal()
}

function openOperationForm(workspaceID) {
  const dialog = $('#operation-dialog')
  dialog.querySelector('form').reset()
  dialog.querySelector('[name=workspaceId]').value = workspaceID
  const pluginSelect = $('#operation-plugin')
  pluginSelect.innerHTML = state.plugins.map(plugin => `<option value="${escapeText(plugin.id)}">${escapeText(plugin.name)} · ${escapeText(plugin.version)}</option>`).join('')
  if (!state.plugins.length) {
    toast('No plugin is installed.', true)
    return
  }
  renderPluginFields(pluginSelect.value)
  $('#operation-error').textContent = ''
  dialog.showModal()
}

function renderPluginFields(pluginID) {
  const plugin = state.plugins.find(item => item.id === pluginID)
  const form = $('#operation-form')
  form.title.value = plugin?.name || 'Operation'
  const schema = plugin?.schema || {}
  const required = new Set(schema.required || [])
  $('#plugin-fields').innerHTML = Object.entries(schema.properties || {}).map(([name, property]) => schemaField(name, property, required.has(name))).join('')
}

function schemaField(name, property, required, scope = 'schema') {
  const title = property.title || humanize(name)
  const hint = property.description ? `<small>${escapeText(property.description)}</small>` : ''
  const requiredAttribute = required ? ' required' : ''
  const value = property.default ?? (property.type === 'boolean' ? false : '')
  const width = property.type === 'string' || property.type === 'object' || property.type === 'array' ? ' wide' : ''
  const fieldAttribute = `data-${scope}-property="${escapeText(name)}" data-${scope}-type="${escapeText(property.type || 'string')}"`
  if (property.format === 'kubephos-secret-ref') {
    const kind = property['x-kubephos-secret-kind'] || ''
    const credentials = state.credentials.filter(item => item.kind === kind)
    const options = credentials.map(item => `<option value="${escapeText(item.id)}">${escapeText(item.name)} · ${escapeText(item.fingerprint)}</option>`).join('')
    return `<label class="${width}">${escapeText(title)}<select ${fieldAttribute}${requiredAttribute}${credentials.length ? '' : ' disabled'}>${options || '<option value="">Add a compatible credential first</option>'}</select>${hint}</label>`
  }
  if (property.enum) {
    return `<label class="${width}">${escapeText(title)}<select ${fieldAttribute}${requiredAttribute}>${property.enum.map(option => `<option value="${escapeText(option)}"${option === value ? ' selected' : ''}>${escapeText(option)}</option>`).join('')}</select>${hint}</label>`
  }
  if (property.type === 'boolean') {
    return `<label class="checkbox-field wide"><input type="checkbox" ${fieldAttribute}${value ? ' checked' : ''}>${escapeText(title)}</label>`
  }
  if (property.type === 'object' || property.type === 'array') {
    const initial = value === '' ? '' : JSON.stringify(value, null, 2)
    return `<label class="wide">${escapeText(title)}<textarea rows="4" ${fieldAttribute}${requiredAttribute}>${escapeText(initial)}</textarea>${hint}</label>`
  }
  const numeric = property.type === 'integer' || property.type === 'number'
  const bounds = `${property.minimum !== undefined ? ` min="${Number(property.minimum)}"` : ''}${property.maximum !== undefined ? ` max="${Number(property.maximum)}"` : ''}`
  const length = `${property.minLength !== undefined ? ` minlength="${Number(property.minLength)}"` : ''}${property.maxLength !== undefined ? ` maxlength="${Number(property.maxLength)}"` : ''}`
  return `<label class="${width}">${escapeText(title)}<input type="${numeric ? 'number' : property.writeOnly ? 'password' : 'text'}" value="${escapeText(value)}" ${fieldAttribute}${bounds}${length}${requiredAttribute}>${hint}</label>`
}

function openCredentialForm() {
  const types = credentialTypes()
  if (!types.length) {
    toast('No installed plugin declares a credential type.', true)
    return
  }
  const dialog = $('#credential-dialog')
  dialog.querySelector('form').reset()
  $('#credential-kind').innerHTML = types.map(value => `<option value="${escapeText(value.kind)}">${escapeText(value.name)}</option>`).join('')
  renderCredentialFields(types[0].kind)
  $('#credential-error').textContent = ''
  dialog.showModal()
}

function renderCredentialFields(kind) {
  const definition = credentialTypes().find(value => value.kind === kind)
  const schema = definition?.schema || {}
  const required = new Set(schema.required || [])
  $('#credential-description').textContent = definition?.description || ''
  $('#credential-fields').innerHTML = Object.entries(schema.properties || {}).map(([name, property]) => schemaField(name, property, required.has(name), 'credential')).join('')
}

async function createCredential(event) {
  event.preventDefault()
  const form = event.currentTarget
  const submit = $('#credential-submit')
  submit.disabled = true
  $('#credential-error').textContent = ''
  try {
    await api('/credentials', {
      method: 'POST',
      body: JSON.stringify({ name: form.name.value, kind: form.kind.value, value: readValues('#credential-fields', 'credential') })
    })
    $('#credential-dialog').close()
    await loadAll(true)
    switchView('infrastructure')
    toast('Credential encrypted and stored.')
  } catch (error) {
    $('#credential-error').textContent = (error.body?.issues || []).map(issue => `${issue.path}: ${issue.message}`).join(' ') || error.message
  } finally {
    submit.disabled = false
  }
}

async function createWorkspace(event) {
  event.preventDefault()
  const form = event.currentTarget
  const submit = $('#workspace-submit')
  submit.disabled = true
  $('#workspace-error').textContent = ''
  try {
    await api('/workspaces', {
      method: 'POST',
      body: JSON.stringify({ name: form.name.value, description: form.description.value })
    })
    $('#workspace-dialog').close()
    await loadAll(true)
    switchView('workspaces')
    toast('Workspace created.')
  } catch (error) {
    $('#workspace-error').textContent = error.message
  } finally {
    submit.disabled = false
  }
}

async function createOperation(event) {
  event.preventDefault()
  const form = event.currentTarget
  const submit = $('#operation-submit')
  submit.disabled = true
  $('#operation-error').textContent = ''
  try {
    const operation = await api('/operations', {
      method: 'POST',
      body: JSON.stringify({
        workspaceId: form.workspaceId.value,
        pluginId: form.pluginId.value,
        title: form.title.value,
        spec: readValues('#plugin-fields', 'schema')
      })
    })
    $('#operation-dialog').close()
    await loadAll(true)
    openOperation(operation.id)
    toast('Validation passed. Review the plan before starting.')
  } catch (error) {
    const issues = error.body?.validation?.issues || []
    $('#operation-error').textContent = issues.map(issue => issue.message).join(' ') || error.message
  } finally {
    submit.disabled = false
  }
}

function readValues(selector, scope) {
  const result = {}
  $$(`${selector} [data-${scope}-property]`).forEach(field => {
    const property = field.dataset[`${scope}Property`]
    const type = field.dataset[`${scope}Type`]
    let value = field.value
    if (type === 'boolean') value = field.checked
    if (type === 'integer') value = Number.parseInt(value, 10)
    if (type === 'number') value = Number(value)
    if ((type === 'object' || type === 'array') && value !== '') value = JSON.parse(value)
    if (value !== '') result[property] = value
  })
  return result
}

async function openOperation(operationID) {
  try {
    const operation = await api(`/operations/${operationID}`)
    state.selectedOperation = operation
    renderDrawer(operation)
    $('#operation-drawer').classList.add('open')
    $('#operation-drawer').setAttribute('aria-hidden', 'false')
    connectEvents(operation)
  } catch (error) {
    toast(error.message, true)
  }
}

function renderDrawer(operation) {
  $('#drawer-title').textContent = operation.title
  const running = ['queued', 'prechecking', 'running', 'verifying'].includes(operation.status)
  const canCancel = running || operation.status === 'ready'
  const canQueue = operation.status === 'ready'
  $('#drawer-content').innerHTML = `
    <div class="detail-summary"><div><span class="status ${escapeText(operation.status)}">${escapeText(operation.status)}</span><p>${escapeText(shortID(operation.id))} · ${formatDate(operation.createdAt)}</p></div></div>
    <div class="detail-actions">
      ${canQueue ? '<button class="button primary" id="queue-operation">Confirm plan and start</button>' : ''}
      ${canCancel ? '<button class="button danger" id="cancel-operation">Cancel</button>' : ''}
    </div>
    ${operation.error ? `<div class="validation-item error">${escapeText(operation.error)}</div>` : ''}
    <p class="eyebrow">VALIDATED PLAN</p>
    <div class="plan-hash" title="${escapeText(operation.planHash)}">SHA-256 ${escapeText(operation.planHash)}</div>
    <div class="validation-list">${operation.validation.issues.map(issue => `<div class="validation-item ${escapeText(issue.level)}"><strong>${escapeText(issue.level.toUpperCase())}</strong> ${escapeText(issue.message)}</div>`).join('')}</div>
    <p class="eyebrow">HEALTH-GATED STEPS</p>
    <div class="step-list">${operation.steps.map(step => `<div class="step-row"><span class="step-index">${step.position}</span><div><h4>${escapeText(step.name)}</h4><p>${step.error ? escapeText(step.error) : step.health ? healthSummary(step.health) : 'Waiting for precheck'}</p></div><span class="status ${escapeText(step.status)}">${escapeText(step.status)}</span></div>`).join('')}</div>
    ${operation.artifacts?.length ? `<p class="eyebrow">ARTIFACTS</p><div class="artifact-list">${operation.artifacts.map(artifact => `<a class="artifact-row" href="/api/v1/artifacts/${escapeText(artifact.id)}/download"><span>↓</span><div><strong>${escapeText(artifact.name)}</strong><small>${formatBytes(artifact.sizeBytes)} · ${escapeText(artifact.digest.slice(0, 24))}…</small></div></a>`).join('')}</div>` : ''}
    <p class="eyebrow">LIVE LOGS</p>
    <div class="log-console" id="log-console"><div class="log-empty">Waiting for logs…</div></div>`
  $('#queue-operation')?.addEventListener('click', () => queueOperation(operation))
  $('#cancel-operation')?.addEventListener('click', () => cancelOperation(operation))
  loadLogs(operation.id)
}

async function queueOperation(operation) {
  const button = $('#queue-operation')
  button.disabled = true
  try {
    const queued = await api(`/operations/${operation.id}/queue`, {
      method: 'POST', body: JSON.stringify({ planHash: operation.planHash, acceptWarnings: true })
    })
    state.selectedOperation = queued
    const full = await api(`/operations/${operation.id}`)
    renderDrawer(full)
    connectEvents(full)
    await loadAll(true)
    toast('Operation queued.')
  } catch (error) {
    button.disabled = false
    toast(error.message, true)
  }
}

async function cancelOperation(operation) {
  const button = $('#cancel-operation')
  button.disabled = true
  try {
    await api(`/operations/${operation.id}/cancel`, { method: 'POST', body: '{}' })
    toast('Cancellation requested.')
  } catch (error) {
    button.disabled = false
    toast(error.message, true)
  }
}

async function loadLogs(operationID) {
  try {
    const response = await api(`/operations/${operationID}/logs`)
    renderLogs(response.items)
  } catch (error) {
    toast(error.message, true)
  }
}

function renderLogs(logs) {
  const consoleElement = $('#log-console')
  if (!consoleElement) return
  consoleElement.innerHTML = logs.length ? logs.map(logLine).join('') : '<div class="log-empty">Waiting for logs…</div>'
  consoleElement.scrollTop = consoleElement.scrollHeight
}

function appendLog(log) {
  const consoleElement = $('#log-console')
  if (!consoleElement) return
  consoleElement.querySelector('.log-empty')?.remove()
  if (consoleElement.querySelector(`[data-sequence="${log.sequence}"]`)) return
  consoleElement.insertAdjacentHTML('beforeend', logLine(log))
  consoleElement.scrollTop = consoleElement.scrollHeight
}

function logLine(log) {
  return `<div class="log-line ${escapeText(log.level)}" data-sequence="${log.sequence}"><span class="time">${new Date(log.createdAt).toLocaleTimeString()}</span><span class="source">${escapeText(log.source)}</span><span class="message">${escapeText(log.message)}</span></div>`
}

function connectEvents(operation) {
  state.eventSource?.close()
  if (['succeeded', 'failed', 'canceled'].includes(operation.status)) return
  const source = new EventSource(`/api/v1/operations/${operation.id}/events`)
  state.eventSource = source
  source.addEventListener('log', event => appendLog(JSON.parse(event.data)))
  source.addEventListener('status', event => {
    const updated = JSON.parse(event.data)
    state.selectedOperation = updated
    renderDrawer(updated)
    if (['succeeded', 'failed', 'canceled'].includes(updated.status)) {
      source.close()
      loadAll(true)
      toast(updated.status === 'succeeded' ? 'Operation completed successfully.' : `Operation ${updated.status}.`, updated.status === 'failed')
    }
  })
  source.onerror = () => {
    if (!['succeeded', 'failed', 'canceled'].includes(state.selectedOperation?.status)) setTimeout(() => connectEvents(state.selectedOperation), 1200)
  }
}

function closeDrawer() {
  state.eventSource?.close()
  state.eventSource = null
  $('#operation-drawer').classList.remove('open')
  $('#operation-drawer').setAttribute('aria-hidden', 'true')
}

function setHealth(healthy) {
  $('#sidebar-health').className = `health-dot ${healthy ? 'healthy' : 'unhealthy'}`
  $('#sidebar-status').textContent = healthy ? 'All systems healthy' : 'Connection unavailable'
}

function healthSummary(raw) {
  try {
    const value = typeof raw === 'string' ? JSON.parse(raw) : raw
    return value.summary || 'Health gate completed'
  } catch {
    return 'Health gate completed'
  }
}

function formatDate(value) {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}

function formatBytes(value) {
  if (value < 1024) return `${value} B`
  return `${(value / 1024).toFixed(1)} KB`
}

function shortID(value) {
  return value.length > 18 ? `${value.slice(0, 18)}…` : value
}

function humanize(value) {
  return value.replace(/([a-z])([A-Z])/g, '$1 $2').replace(/^./, letter => letter.toUpperCase())
}

function escapeText(value = '') {
  return String(value).replace(/[&<>'"]/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[character])
}

function toast(message, error = false) {
  const element = document.createElement('div')
  element.className = `toast${error ? ' error' : ''}`
  element.textContent = message
  $('#toast-region').append(element)
  setTimeout(() => element.remove(), 3600)
}

$$('.nav-item').forEach(button => button.addEventListener('click', () => switchView(button.dataset.view)))
$$('[data-go]').forEach(button => button.addEventListener('click', () => switchView(button.dataset.go)))
$$('[data-close-drawer]').forEach(button => button.addEventListener('click', closeDrawer))
$$('[data-close-dialog]').forEach(button => button.addEventListener('click', () => $(`#${button.dataset.closeDialog}`).close()))
$('#new-workspace-button').addEventListener('click', openWorkspaceForm)
$('#hero-workspace-button').addEventListener('click', openWorkspaceForm)
$('#refresh-button').addEventListener('click', () => loadAll())
$('#workspace-form').addEventListener('submit', createWorkspace)
$('#operation-form').addEventListener('submit', createOperation)
$('#operation-plugin').addEventListener('change', event => renderPluginFields(event.target.value))
$('#add-credential-button').addEventListener('click', openCredentialForm)
$('#credential-form').addEventListener('submit', createCredential)
$('#credential-kind').addEventListener('change', event => renderCredentialFields(event.target.value))
window.addEventListener('hashchange', () => switchView(location.hash.slice(1) || 'overview'))
switchView(location.hash.slice(1) || 'overview')
loadAll(true)
setInterval(() => loadAll(true), 5000)
