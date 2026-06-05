import { useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import {
  ApplicationsList,
  ApplicationDetail,
  CenteredEmpty,
  type AppRow,
  type SelectedAppWorkload,
  type SelectedResource,
} from '@skyhook-io/k8s-ui'
import { Boxes } from 'lucide-react'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../../api/config'
import { kindToPlural } from '../../utils/navigation'
import { WorkloadView } from '../workload/WorkloadView'

interface ApplicationsResponse {
  applications: AppRow[]
}

interface ApplicationsViewProps {
  namespaces: string[]
  onOpenResource: (resource: SelectedResource) => void
}

export function ApplicationsView({ namespaces, onOpenResource }: ApplicationsViewProps) {
  const namespacesParam = namespaces.join(',')
  const query = useQuery({
    queryKey: ['applications', namespacesParam],
    queryFn: async () => {
      const params = new URLSearchParams()
      if (namespaces.length > 0) params.set('namespaces', namespacesParam)
      const qs = params.toString()
      const res = await fetch(apiUrl(`/applications${qs ? `?${qs}` : ''}`), {
        credentials: getCredentialsMode(),
        headers: getAuthHeaders(),
      })
      if (!res.ok) throw new Error(`Failed to load applications: HTTP ${res.status}`)
      return (await res.json()) as ApplicationsResponse
    },
    staleTime: 30_000,
    refetchInterval: 60_000,
  })

  const apps = query.data?.applications ?? []
  const [selectedKey, setSelectedKey] = useState<string | null>(null)
  const selected = useMemo(() => apps.find((a) => a.key === selectedKey) ?? null, [apps, selectedKey])

  if (selectedKey && selected) {
    return (
      <div className="flex-1 overflow-auto">
        <ApplicationDetail
          app={selected}
          onBack={() => setSelectedKey(null)}
          renderWorkload={(workload: SelectedAppWorkload) => (
            <div className="h-[calc(100vh-14rem)] min-h-[560px] overflow-hidden">
              <WorkloadView
                kind={kindToPlural(workload.kind)}
                namespace={workload.namespace}
                name={workload.name}
                onBack={() => setSelectedKey(null)}
                onNavigateToResource={onOpenResource}
              />
            </div>
          )}
        />
      </div>
    )
  }

  return (
    <div className="flex-1 overflow-auto px-4 py-4 sm:px-6">
      <header className="mb-4 flex flex-col gap-1">
        <h1 className="text-xl font-semibold text-theme-text-primary">Applications</h1>
        <p className="max-w-3xl text-sm text-theme-text-secondary">Deployable software in this cluster — your services, workers, and jobs, grouped by app/release evidence.</p>
      </header>

      {query.isLoading ? (
        <CenteredEmpty icon={Boxes} headline="Loading applications…" />
      ) : query.error ? (
        <CenteredEmpty tone="filtered" icon={Boxes} headline="Failed to load applications" body={(query.error as Error).message} />
      ) : apps.length === 0 ? (
        <CenteredEmpty
          icon={Boxes}
          headline="No applications detected yet"
          body="Deploy services, workers, or jobs to this cluster to see them grouped by app."
        />
      ) : (
        <ApplicationsList apps={apps} onSelect={setSelectedKey} />
      )}
    </div>
  )
}
