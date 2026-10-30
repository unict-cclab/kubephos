export class ApiError extends Error {
  status: number
  body: Record<string, unknown>

  constructor(status: number, body: Record<string, unknown>) {
    super(apiErrorMessage(body))
    this.status = status
    this.body = body
  }
}

function isAbort(cause: unknown): boolean {
  return Boolean(cause && typeof cause === 'object' && 'name' in cause && cause.name === 'AbortError')
}

export function apiErrorMessage(body: Record<string, unknown>, fallback = 'The request could not be completed.'): string {
  if (typeof body.message === 'string' && body.message.trim()) return body.message
  const validation = body.validation as {issues?: Array<{path?: string; message?: string}>} | undefined
  const issues = validation?.issues ?? body.issues as Array<{path?: string; message?: string}> | undefined
  const details = Array.isArray(issues) ? issues.flatMap(issue => typeof issue?.message === 'string' && issue.message.trim() ? [issue.message.trim()] : []) : []
  return details.length ? details.join(' ') : fallback
}

// Shared transport also handles archive uploads and binary downloads.
export async function requestResponse(path: string, options: RequestInit = {}, csrfToken?: string, signal?: AbortSignal): Promise<Response> {
  const method = options.method ?? 'GET'
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  let response: Response
  try {
    response = await fetch(`/api/v1${path}`, {...options, headers, signal: signal ?? options.signal})
  } catch (cause) {
    if (isAbort(cause)) throw cause
    throw new ApiError(0, {code: 'connection_failed', message: 'Cannot reach KubePhos. Check your connection and try again.'})
  }
  if (response.ok) return response
  let body: unknown
  try {body = await response.json()} catch (cause) {
    if (isAbort(cause)) throw cause
  }
  const details = body && typeof body === 'object' && !Array.isArray(body) ? body as Record<string, unknown> : {}
  const fallback = response.status === 401 ? 'Your session has expired. Sign in again.' : response.status === 403 ? 'You do not have permission to perform this action.' : response.status >= 500 ? 'KubePhos is temporarily unavailable. Try again shortly.' : 'The request could not be completed.'
  throw new ApiError(response.status, {...details, message: apiErrorMessage(details, fallback)})
}

export async function request<T>(path: string, options: RequestInit = {}, csrfToken?: string, signal?: AbortSignal): Promise<T> {
  const response = await requestResponse(path, options, csrfToken, signal)
  if (response.status === 204) return undefined as T
  try {return await response.json() as T} catch (cause) {
    if (isAbort(cause)) throw cause
    throw new ApiError(response.status, {code: 'invalid_response', message: 'KubePhos returned an incomplete response. Refresh the page and try again.'})
  }
}
