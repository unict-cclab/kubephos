import {request} from './api'

/*
 * Opens the live Grafana dashboard in a new tab. The server first installs or
 * updates the dashboard in the cluster's Grafana, which takes a moment, so the
 * tab opens immediately (browsers only allow pop-ups during the click) and
 * navigates once the address is known. The tab closes again if that fails.
 */
export async function openLiveDashboard(path: string, csrfToken?: string): Promise<void> {
  const tab = window.open('', '_blank')
  try {
    const {url} = await request<{url: string}>(path, {method: 'POST', body: '{}'}, csrfToken)
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
