import type { paths } from './generated/management'
import { createManagementClient, managementClient, unwrapApiResult } from './client'

export type GetEventsParams = paths['/panel/events']['get']['parameters']['query']
export type GetSnapshotsParams = paths['/panel/snapshots']['get']['parameters']['query']
export type GetTraceParams = paths['/panel/traces/{trace_id}']['get']['parameters']['query']

export async function getOverview() {
  return unwrapApiResult(await managementClient.GET('/panel/overview'))
}

export async function validateToken(token?: string) {
  if (!token) {
    return getOverview()
  }

  const client = createManagementClient({
    getAuthToken: () => token,
  })

  return unwrapApiResult(await client.GET('/panel/overview'))
}

export async function getEvents(params?: GetEventsParams) {
  return unwrapApiResult(
    await managementClient.GET('/panel/events', {
      params: {
        query: params,
      },
    }),
  )
}

export async function getEventById(id: number) {
  return unwrapApiResult(
    await managementClient.GET('/panel/events/{id}', {
      params: {
        path: { id },
      },
    }),
  )
}

export async function getTrace(traceId: string, params?: GetTraceParams) {
  return unwrapApiResult(
    await managementClient.GET('/panel/traces/{trace_id}', {
      params: {
        path: { trace_id: traceId },
        query: params,
      },
    }),
  )
}

export async function getArtifact(id: string) {
  return unwrapApiResult(
    await managementClient.GET('/panel/artifacts/{id}', {
      params: {
        path: { id },
      },
    }),
  )
}

export async function getSnapshots(params?: GetSnapshotsParams) {
  return unwrapApiResult(
    await managementClient.GET('/panel/snapshots', {
      params: {
        query: params,
      },
    }),
  )
}
