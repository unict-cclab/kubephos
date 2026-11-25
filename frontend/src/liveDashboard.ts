import {request} from './api'

/*
 * Opens the live Grafana dashboard in a new tab. The server first installs or
 * updates the dashboard in the cluster's Grafana, which takes a moment, so the
 * tab opens immediately (browsers only allow pop-ups during the click) and
 * navigates once the address is known. The tab closes again if that fails.
 */
export function openLiveDashboard(path: string, csrfToken?: string): Promise<void> {
  return openDashboard(path, 'POST', csrfToken)
}

export function openClusterLens(path: string): Promise<void> {
  return openDashboard(path, 'GET')
}

async function openDashboard(path: string, method: 'GET' | 'POST', csrfToken?: string): Promise<void> {
  const tab = window.open('', '_blank')
  try {
    const {url} = await request<{url: string}>(path, method === 'POST' ? {method, body: '{}'} : {method}, csrfToken)
    if (!tab) {
      window.open(url, '_blank', 'noopener')
      return
    }
    tab.opener = null
    tab.location.href = url
  } catch (cause) {
    tab?.close()
    throw cause
  }
}
