export class ApiError extends Error {
  status: number
  body: Record<string, unknown>

  constructor(status: number, body: Record<string, unknown>) {
    super(apiErrorMessage(body))
    this.status = status
    this.body = body
  }
}

export function apiErrorMessage(body: Record<string, unknown>): string {
  if (typeof body.message === 'string' && body.message.trim()) return body.message
  const validation = body.validation as {issues?: Array<{path?: string; message?: string}>} | undefined
  const issues = validation?.issues ?? body.issues as Array<{path?: string; message?: string}> | undefined
  const details = issues?.map(issue => issue.message?.trim()).filter((message): message is string => Boolean(message)) ?? []
  return details.length ? details.join(' ') : 'The request could not be completed.'
}

export async function request<T>(path: string, options: RequestInit = {}, csrfToken?: string, signal?: AbortSignal): Promise<T> {
  const method = options.method ?? 'GET'
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type')) headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(`/api/v1${path}`, {...options, headers, signal})
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => ({})) as Record<string, unknown>
  if (!response.ok) throw new ApiError(response.status, body)
  return body as T
}
