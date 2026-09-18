export interface ProblemDetails {
  type: string
  title: string
  status: number
  detail?: string
  instance?: string
  code: string
  requestId: string
  [key: string]: unknown
}

export class HttpError extends Error {
  constructor(
    public readonly status: number,
    public readonly problem?: ProblemDetails,
  ) {
    super(problem?.title ?? `HTTP request failed: ${status}`)
    this.name = 'HttpError'
  }
}

const API_BASE_URL = '/api/v1'

function isJsonContentType(contentType: string | null) {
  return Boolean(contentType && (contentType.includes('application/json') || contentType.includes('+json')))
}

async function readProblem(response: Response): Promise<ProblemDetails | undefined> {
  const contentType = response.headers.get('content-type')

  if (!isJsonContentType(contentType)) {
    return undefined
  }

  try {
    return (await response.json()) as ProblemDetails
  } catch {
    return undefined
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json, application/problem+json')

  if (init.body && !(init.body instanceof FormData) && !headers.has('Content-Type')) {
    headers.set('Content-Type', 'application/json')
  }

  const response = await fetch(`${API_BASE_URL}${path}`, {
    ...init,
    credentials: 'include',
    headers,
  })

  if (!response.ok) {
    throw new HttpError(response.status, await readProblem(response))
  }

  if (response.status === 204) {
    return undefined as T
  }

  return (await response.json()) as T
}
