import { useIssues } from '../../api/client'
import type { SelectedResource } from '../../types'
import { IssuesView, PaneLoader, type IssueResourceRef } from '@skyhook-io/k8s-ui'
import { AlertTriangle, ArrowLeft } from 'lucide-react'

interface IssuesPaneProps {
  namespaces: string[]
  onBack: () => void
  onNavigateToResource: (resource: SelectedResource) => void
}

// The per-cluster Issues surface. Renders the same shared triage queue
// (IssuesView) the Hub fleet view uses — single cluster here, so no cluster
// label and in-app (client-side) resource navigation. Classification +
// owner-grouping come pre-computed from radar's /api/issues
// (internal/issues.Compose → Classify → Group).
export function IssuesPane({ namespaces, onBack, onNavigateToResource }: IssuesPaneProps) {
  const { data, isLoading, error } = useIssues(namespaces)

  const onResourceClick = (ref: IssueResourceRef) =>
    onNavigateToResource({ kind: ref.kind, namespace: ref.namespace, name: ref.name, group: ref.group })

  if (isLoading) {
    return <PaneLoader label="Loading issues…" className="flex-1" />
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center text-theme-text-secondary">
        <p>Failed to load issues</p>
      </div>
    )
  }

  return (
    <div className="flex-1 flex flex-col min-h-0 p-6 gap-6 overflow-auto">
      <div className="flex items-center gap-4">
        <button
          onClick={onBack}
          className="p-1.5 rounded-lg hover:bg-theme-hover transition-colors"
        >
          <ArrowLeft className="w-5 h-5 text-theme-text-secondary" />
        </button>
        <div className="flex-1">
          <div className="flex items-center gap-2">
            <AlertTriangle className="w-5 h-5 text-theme-text-secondary" />
            <h1 className="text-lg font-semibold text-theme-text-primary">Issues</h1>
          </div>
          <p className="text-sm text-theme-text-tertiary mt-1 ml-7">
            Live cluster problems — crashes, scheduling failures, bad references — grouped by the workload they affect.
          </p>
        </div>
      </div>

      {/* Truncation honesty: when more issues matched than were returned, say
          so — don't present a capped list as the complete picture. */}
      {data?.total_matched != null && data.total_matched > (data.issues?.length ?? 0) && (
        <p className="-mt-3 text-xs text-theme-text-tertiary">
          Showing {data.issues?.length ?? 0} of {data.total_matched} issues (capped) — narrow by namespace to see the rest.
        </p>
      )}

      {/* anyData = the query resolved, i.e. the cluster is reachable; an empty
          list then means "nothing broken" rather than "not connected". */}
      <IssuesView issues={data?.issues ?? []} anyData={!!data} onResourceClick={onResourceClick} />
    </div>
  )
}
