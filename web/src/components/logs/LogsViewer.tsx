import { useCallback } from 'react'
import { fetchJSON } from '../../api/client'
import { createLogStream } from '../../api/client'
import { LogsViewer as SharedLogsViewer } from '@skyhook/k8s-ui'
import type { LogsFetchParams } from '@skyhook/k8s-ui'

interface LogsViewerProps {
  namespace: string
  podName: string
  containers: string[]
  initialContainer?: string
}

export function LogsViewer({ namespace, podName, containers, initialContainer }: LogsViewerProps) {
  const fetchLogs = useCallback(async (params: LogsFetchParams) => {
    const query = new URLSearchParams()
    query.set('container', params.container)
    if (params.tailLines) query.set('tailLines', String(params.tailLines))
    if (params.sinceSeconds) query.set('sinceSeconds', String(params.sinceSeconds))
    if (params.previous) query.set('previous', 'true')
    const data = await fetchJSON<{ logs: { [container: string]: string } }>(
      `/pods/${namespace}/${podName}/logs?${query}`
    )
    return data.logs
  }, [namespace, podName])

  const makeStream = useCallback((params: Omit<LogsFetchParams, 'previous'>) => {
    return createLogStream(namespace, podName, params)
  }, [namespace, podName])

  return (
    <SharedLogsViewer
      namespace={namespace}
      podName={podName}
      containers={containers}
      initialContainer={initialContainer}
      fetchLogs={fetchLogs}
      createStream={makeStream}
    />
  )
}
