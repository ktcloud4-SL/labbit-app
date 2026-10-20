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

export interface HttpResponse<T> {
  data: T
  status: number
  headers: Headers
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

export class ApiResponseFormatError extends Error {
  constructor(
    public readonly status: number,
    public readonly contentType: string | null,
  ) {
    super('API 응답이 JSON 형식이 아닙니다.')
    this.name = 'ApiResponseFormatError'
  }
}

const API_BASE_URL = '/api/v1'

function isJsonContentType(contentType: string | null) {
  const mediaType = contentType?.split(';', 1)[0].trim().toLowerCase()
  return (
    mediaType === 'application/json' ||
    Boolean(mediaType && /^[^\\s/]+\\/[^\\s/]+\\+json$/.test(mediaType))
  )
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

export async function requestWithMetadata<T>(
  path: string,
  init: RequestInit = {},
): Promise<HttpResponse<T>> {
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

  // A misrouted /api/v1 request can receive 200 text/html from the SPA.
  // Reject even JSON-shaped bodies with an incorrect media type; do not record
  // or parse the response body when the API contract is already violated.
  if (response.status !== 204 && !isJsonContentType(response.headers.get('content-type'))) {
    throw new ApiResponseFormatError(response.status, response.headers.get('content-type'))
  }

  const data =
    response.status === 204 ? (undefined as T) : ((await response.json()) as T)

  return {
    data,
    status: response.status,
    headers: response.headers,
  }
}

export async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  return (await requestWithMetadata<T>(path, init)).data
}
