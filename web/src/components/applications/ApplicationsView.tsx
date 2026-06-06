import { useCallback, useMemo } from 'react'
import { useSearchParams } from 'react-router-dom'
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
import { useTopology } from '../../api/client'
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

  // Which app is open lives in the URL (?app=<key>) so the detail view is
  // deep-linkable and the browser back button returns to the list. Opening or
  // closing an app also clears the per-app params (workload, tab).
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedKey = searchParams.get('app')
  const selected = useMemo(() => apps.find((a) => a.key === selectedKey) ?? null, [apps, selectedKey])

  const selectApp = useCallback(
    (key: string | null) => {
      const params = new URLSearchParams(searchParams)
      if (key) params.set('app', key)
      else params.delete('app')
      params.delete('workload')
      params.delete('tab')
      setSearchParams(params)
    },
    [searchParams, setSearchParams],
  )

  if (selectedKey && selected) {
    return <AppDetailRoute app={selected} onBack={() => selectApp(null)} onOpenResource={onOpenResource} />
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
        <ApplicationsList apps={apps} onSelect={selectApp} />
      )}
    </div>
  )
}

// AppDetailRoute wires the OSS data hooks the shared ApplicationDetail can't:
// the resources-view topology over the app's namespaces (for the app graph)
// and the per-workload WorkloadView (which fetches its own topology for the
// Topology tab). Split out so useTopology runs unconditionally (Rules of Hooks).
function AppDetailRoute({ app, onBack, onOpenResource }: { app: AppRow; onBack: () => void; onOpenResource: (resource: SelectedResource) => void }) {
  const appNamespaces = useMemo(
    () => Array.from(new Set((app.workloads ?? []).map((w) => w.namespace).filter(Boolean))).sort(),
    [app.workloads],
  )
  const { data: topology } = useTopology(appNamespaces, 'resources', { enabled: appNamespaces.length > 0 })

  // The selected workload (?workload=<key>) lives in the URL too: deep-linkable,
  // and back returns from a workload's runtime to the app graph. Clearing it
  // also drops the workload's tab param.
  const [searchParams, setSearchParams] = useSearchParams()
  const selectedWorkloadKey = searchParams.get('workload')
  const selectWorkload = useCallback(
    (key: string | null) => {
      const params = new URLSearchParams(searchParams)
      if (key) params.set('workload', key)
      else {
        params.delete('workload')
        params.delete('tab')
      }
      setSearchParams(params)
    },
    [searchParams, setSearchParams],
  )

  return (
    <div className="flex-1 overflow-auto">
      <ApplicationDetail
        app={app}
        onBack={onBack}
        topology={topology}
        onNavigateToResource={onOpenResource}
        selectedWorkloadKey={selectedWorkloadKey}
        onSelectWorkload={selectWorkload}
        renderWorkload={(workload: SelectedAppWorkload) => (
          <div className="h-full overflow-hidden">
            <WorkloadView
              kind={kindToPlural(workload.kind)}
              namespace={workload.namespace}
              name={workload.name}
              onBack={() => selectWorkload(null)}
              onNavigateToResource={onOpenResource}
            />
          </div>
        )}
      />
    </div>
  )
}
