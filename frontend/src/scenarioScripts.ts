import {request} from './api'

// Locust files of a catalog application's load scenarios, read from its manifest
// preview once per application and shared by the load profile editors.

export interface ScenarioPreview {loadScenarios?: Array<{id: string; script?: string; scriptDigest?: string}>}

const previews = new Map<string, Promise<ScenarioPreview>>()

export function loadScenarioPreview(applicationId: string, refresh = false): Promise<ScenarioPreview> {
  if (!refresh && previews.has(applicationId)) return previews.get(applicationId)!
  const pending = request<ScenarioPreview>(`/catalog/applications/${encodeURIComponent(applicationId)}/manifest`)
  previews.set(applicationId, pending)
  pending.catch(() => previews.delete(applicationId))
  return pending
}

export function clearScenarioPreviews() {
  previews.clear()
}
