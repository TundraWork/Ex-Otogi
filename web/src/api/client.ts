import createClient, { type Middleware } from 'openapi-fetch'

import type { components, paths } from './generated/management'

export interface ApiProblem {
  title?: string
  detail?: string
  status?: number
  type?: string
  instance?: string
  errors?: Array<{
    location?: string
    message?: string
    value?: unknown
  }> | null
}

export class ApiError extends Error {
  readonly status: number
  readonly problem?: ApiProblem
  readonly response: Response

  constructor(response: Response, problem?: ApiProblem) {
    super(problem?.detail ?? problem?.title ?? `Request failed with status ${response.status}`)
    this.name = 'ApiError'
    this.status = response.status
    this.problem = problem
    this.response = response
  }
}

type ErrorModel = components['schemas']['ErrorModel']

let authTokenGetter: () => string | null = () => null
let unauthorizedHandler: (() => void | Promise<void>) | null = null

function getBaseUrl() {
  return import.meta.env.VITE_MANAGEMENT_API_BASE_URL?.trim() || '/'
}

async function parseApiProblem(response: Response) {
  const contentType = response.headers.get('content-type') ?? ''
  if (!contentType.includes('application/problem+json')) {
    return undefined
  }

  try {
    return (await response.clone().json()) as ErrorModel
  } catch {
    return undefined
  }
}

function createAuthMiddleware(options?: {
  getAuthToken?: () => string | null
  onUnauthorized?: (() => void | Promise<void>) | null
}): Middleware {
  return {
    async onRequest({ request }) {
      const token = options?.getAuthToken?.() ?? null
      if (!token) {
        return request
      }

      const headers = new Headers(request.headers)
      headers.set('Authorization', `Bearer ${token}`)

      return new Request(request, { headers })
    },
    async onResponse({ response }) {
      if (response.status === 401 && options?.onUnauthorized) {
        await options.onUnauthorized()
      }

      return response
    },
  }
}

export function createManagementClient(options?: {
  getAuthToken?: () => string | null
  onUnauthorized?: (() => void | Promise<void>) | null
}) {
  const client = createClient<paths>({
    baseUrl: getBaseUrl(),
  })

  client.use(createAuthMiddleware(options))

  return client
}

export const managementClient = createManagementClient({
  getAuthToken: () => authTokenGetter(),
  onUnauthorized: async () => {
    if (unauthorizedHandler) {
      await unauthorizedHandler()
    }
  },
})

export function configureApiClient(options: {
  getAuthToken?: () => string | null
  onUnauthorized?: (() => void | Promise<void>) | null
}) {
  if (options.getAuthToken) {
    authTokenGetter = options.getAuthToken
  }

  unauthorizedHandler = options.onUnauthorized ?? null
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError
}

export async function unwrapApiResult<T>(result: {
  data?: T
  error?: unknown
  response: Response
}) {
  if (result.data !== undefined) {
    return result.data
  }

  if (result.error !== undefined) {
    throw new ApiError(result.response, result.error as ApiProblem)
  }

  const problem = await parseApiProblem(result.response)
  throw new ApiError(result.response, problem)
}
