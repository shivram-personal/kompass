import { useMemo } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { useQueries } from '@tanstack/react-query'
import { ApiError, isForbiddenError, useSecretCertExpiry, useTopPodMetrics, useTopNodeMetrics } from '../../api/client'
import { useAPIResources } from '../../api/apiResources'
import { usePinnedKinds } from '../../hooks/useFavorites'
import { useOpenLogs, useOpenWorkloadLogs } from '../dock'
import {
  ResourcesView as BaseResourcesView,
  categorizeResources,
  CORE_RESOURCES,
} from '@skyhook/k8s-ui'
import type { ResourceQueryResult } from '@skyhook/k8s-ui'
import type { SelectedResource } from '../../types'
import type { NavigateToResource } from '../../utils/navigation'

interface ResourcesViewProps {
  namespaces: string[]
  selectedResource?: SelectedResource | null
  onResourceClick?: (resource: SelectedResource | null) => void
  onResourceClickYaml?: NavigateToResource
  onKindChange?: () => void
}

export function ResourcesView({ namespaces, selectedResource, onResourceClick, onResourceClickYaml, onKindChange }: ResourcesViewProps) {
  const location = useLocation()
  const navigate = useNavigate()

  // API resources discovery
  const { data: apiResources } = useAPIResources()

  // Compute resourcesToCount from categories (same logic as package)
  const categories = useMemo(() => {
    if (!apiResources) return null
    return categorizeResources(apiResources)
  }, [apiResources])

  const resourcesToCount = useMemo(() => {
    if (categories) {
      return categories.flatMap(c => c.resources).map(r => ({
        kind: r.kind,
        name: r.name,
        group: r.group,
        isCrd: r.isCrd,
      }))
    }
    return CORE_RESOURCES.map(r => ({
      kind: r.kind,
      name: r.name,
      group: r.group,
      isCrd: r.isCrd,
    }))
  }, [categories])

  // Fetch ALL resources using useQueries
  const resourceQueries = useQueries({
    queries: resourcesToCount.map((resource) => ({
      // Only include group in query key for CRDs — core K8s resources (apps, batch, etc.)
      // must NOT send ?group= because the backend skips the fast typed cache when group is set.
      queryKey: ['resources', resource.name, resource.isCrd ? resource.group : '', namespaces],
      queryFn: async () => {
        const params = new URLSearchParams()
        if (namespaces.length > 0) params.set('namespaces', namespaces.join(','))
        if (resource.isCrd && resource.group) params.set('group', resource.group)
        const res = await fetch(`/api/resources/${resource.name}?${params}`)
        if (!res.ok) {
          if (res.status === 403) {
            throw new ApiError('Insufficient permissions', 403)
          }
          return []
        }
        return res.json()
      },
      staleTime: 30000,
      refetchInterval: 30000,
      retry: (failureCount: number, error: Error) => {
        if (isForbiddenError(error)) return false
        return failureCount < 3
      },
    })),
  })

  // Map react-query results to ResourceQueryResult shape
  const resourceQueryResults: ResourceQueryResult[] = useMemo(() =>
    resourceQueries.map(q => ({
      data: q.data as any[] | undefined,
      isLoading: q.isLoading,
      error: q.error,
      refetch: q.refetch,
      dataUpdatedAt: q.dataUpdatedAt,
    })),
    [resourceQueries]
  )

  // Metrics
  const { data: topPodMetrics } = useTopPodMetrics()
  const { data: topNodeMetrics } = useTopNodeMetrics()

  // Certificate expiry
  const { data: certExpiry, isError: certExpiryError } = useSecretCertExpiry()

  // Pinned kinds
  const { pinned, togglePin, isPinned } = usePinnedKinds()

  // Dock actions
  const openLogs = useOpenLogs()
  const openWorkloadLogs = useOpenWorkloadLogs()

  // Navigation adapter
  const handleNavigate = useMemo(() => {
    return (path: string, options?: { replace?: boolean }) => {
      navigate(path, { replace: options?.replace })
    }
  }, [navigate])

  return (
    <BaseResourcesView
      namespaces={namespaces}
      selectedResource={selectedResource}
      onResourceClick={onResourceClick}
      onResourceClickYaml={onResourceClickYaml}
      onKindChange={onKindChange}
      // Injected data
      apiResources={apiResources}
      resourceQueries={resourceQueryResults}
      topPodMetrics={topPodMetrics}
      topNodeMetrics={topNodeMetrics}
      certExpiry={certExpiry}
      certExpiryError={certExpiryError}
      // Pinned kinds
      pinned={pinned}
      togglePin={togglePin}
      isPinned={(kind: string, group?: string) => isPinned(kind, group ?? '')}
      // Navigation
      locationSearch={location.search}
      locationPathname={location.pathname}
      onNavigate={handleNavigate}
      // Dock actions
      onOpenLogs={openLogs}
      onOpenWorkloadLogs={openWorkloadLogs}
    />
  )
}
