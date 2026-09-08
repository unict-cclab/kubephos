export class ApiError extends Error {
  status: number
  body: Record<string, unknown>

  constructor(status: number, body: Record<string, unknown>) {
    super(typeof body.message === 'string' ? body.message : 'The request could not be completed.')
    this.status = status
    this.body = body
  }
}

export async function request<T>(path: string, options: RequestInit = {}, csrfToken?: string, signal?: AbortSignal): Promise<T> {
  const method = options.method ?? 'GET'
  const headers = new Headers(options.headers)
  headers.set('Content-Type', 'application/json')
  if (!['GET', 'HEAD', 'OPTIONS'].includes(method) && csrfToken) headers.set('X-CSRF-Token', csrfToken)
  const response = await fetch(`/api/v1${path}`, {...options, headers, signal})
  if (response.status === 204) return undefined as T
  const body = await response.json().catch(() => ({})) as Record<string, unknown>
  if (!response.ok) throw new ApiError(response.status, body)
  return body as T
}
