import { useMemo, useState, type ReactNode } from 'react'
import { ArrowLeft, Boxes } from 'lucide-react'
import { StatusDot, mapHealthToTone } from '../ui/status-tone'
import { Tooltip } from '../ui/Tooltip'
import { pluralize } from '../../utils/pluralize'
import {
  type AppRow,
  type AppWorkload,
  type AppHealth,
  type AppWorkloadClass,
  CLASS_META,
  overlayProvenance,
  provenanceTooltip,
  resolveEnv,
  workloadClassOf,
  worstHealth,
} from '../../utils/applications'

// ApplicationDetail — pure single-cluster detail shell. Owns the title row
// (icon, name, provenance/ungrouped chip, add-on/mixed chip, class badge,
// health pill) + a context strip (Environment · Namespace · Ready · Version) +
// a workload selector (only when >1 workload). The embedded WorkloadView is
// injected by the host via the `renderWorkload` render-prop, keyed to the
// selected workload — the shell never touches data hooks.

export interface SelectedAppWorkload {
  kind: string
  namespace: string
  name: string
}

export interface ApplicationDetailProps {
  app: AppRow
  onBack: () => void
  /** Render the host's WorkloadView for the chosen workload. */
  renderWorkload: (workload: SelectedAppWorkload) => ReactNode
}

const VERDICT: Record<AppHealth, { label: string; dot: string; text: string; ring: string }> = {
  unhealthy: { label: 'Down', dot: 'bg-rose-500', text: 'text-rose-600 dark:text-rose-400', ring: 'ring-rose-200 dark:ring-rose-900 bg-rose-50 dark:bg-rose-950/40' },
  degraded: { label: 'Degraded', dot: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400', ring: 'ring-amber-200 dark:ring-amber-900 bg-amber-50 dark:bg-amber-950/40' },
  healthy: { label: 'Healthy', dot: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400', ring: 'ring-emerald-200 dark:ring-emerald-900 bg-emerald-50 dark:bg-emerald-950/40' },
  unknown: { label: 'Unknown', dot: 'bg-slate-400', text: 'text-theme-text-secondary', ring: 'ring-theme-border bg-theme-hover' },
}

function ClassBadge({ workloadClass }: { workloadClass: AppWorkloadClass }) {
  const meta = CLASS_META[workloadClass]
  return <span className={`inline-flex items-center rounded-sm px-1.5 py-px text-[10px] font-medium ring-1 ring-inset ${meta.pill}`}>{meta.label}</span>
}

function ContextFact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline gap-1.5">
      <span className="text-[10px] uppercase tracking-wide text-theme-text-tertiary">{label}</span>
      <span className="min-w-0 truncate text-xs text-theme-text-secondary">{children}</span>
    </div>
  )
}

function ReadyBar({ ready, desired }: { ready: number; desired: number }) {
  if (desired <= 0) return <span className="font-mono text-xs tabular-nums text-theme-text-tertiary">—</span>
  const pct = Math.min(100, Math.round((ready / desired) * 100))
  const ok = ready >= desired
  return (
    <span className="inline-flex items-center gap-1.5">
      <span className="inline-block h-1.5 w-16 rounded-full bg-theme-hover">
        <span className={`block h-1.5 rounded-full ${ok ? 'bg-emerald-500' : ready === 0 ? 'bg-rose-500' : 'bg-amber-500'}`} style={{ width: `${pct}%` }} />
      </span>
      <span className={`font-mono text-xs tabular-nums ${ok ? 'text-theme-text-secondary' : 'text-rose-600 dark:text-rose-400'}`}>{ready}/{desired || '—'}</span>
    </span>
  )
}

function workloadKey(w: SelectedAppWorkload): string {
  return `${w.kind}/${w.namespace}/${w.name}`
}

export function ApplicationDetail({ app, onBack, renderWorkload }: ApplicationDetailProps) {
  const workloads = app.workloads ?? []
  const overall = worstHealth([app.health, ...workloads.map((w) => w.health)])
  const v = VERDICT[overall]
  const workloadClass = workloadClassOf(app.workload_class)
  const provenance = app.tier ? overlayProvenance(app.tier) : null
  const versions = useMemo(() => Array.from(new Set((app.versions || []).filter(Boolean))), [app.versions])
  const ready = workloads.reduce((n, w) => n + (w.ready ?? 0), 0)
  const desired = workloads.reduce((n, w) => n + (w.desired ?? 0), 0)
  const restartSignal = restartWarning(workloads)
  const { env, inferred } = resolveEnv(undefined, app.namespace)

  const [selected, setSelected] = useState('')
  const selectedValue = workloads.some((w) => workloadKey(w) === selected) ? selected : (workloads[0] ? workloadKey(workloads[0]) : '')
  const selectedWorkload = workloads.find((w) => workloadKey(w) === selectedValue)

  return (
    <div className="flex w-full flex-col">
      {/* Title row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-theme-border px-4 py-3 sm:px-6">
        <button type="button" onClick={onBack} className="flex items-center gap-1.5 text-xs text-theme-text-tertiary hover:text-theme-text-primary">
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden /> Applications
        </button>
        <span className="h-6 w-px bg-theme-border" aria-hidden />
        <span className={`flex h-8 w-8 items-center justify-center rounded-md ${v.ring} ring-1 ring-inset`}>
          <Boxes className={`h-4 w-4 ${v.text}`} aria-hidden />
        </span>
        <h1 className="text-2xl font-semibold text-theme-text-primary">{app.name}</h1>
        {provenance ? (
          <Tooltip content={provenanceTooltip(app.tier, app.key, app.confidence)} delay={150}>
            <span className={`inline-flex items-center rounded-sm px-1.5 py-px text-[10px] font-medium ring-1 ring-inset ${app.confidence === 'high' ? 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900' : app.confidence === 'medium' ? 'bg-theme-hover text-theme-text-secondary ring-theme-border' : 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900'}`}>{provenance}</span>
          </Tooltip>
        ) : (
          <Tooltip content="No GitOps, Helm, or app-label grouping signal — shown as the raw workload." delay={150}>
            <span className="inline-flex items-center rounded-sm bg-theme-hover px-1.5 py-px text-[10px] font-medium text-theme-text-tertiary ring-1 ring-inset ring-theme-border">ungrouped</span>
          </Tooltip>
        )}
        {app.category === 'addon' && (
          <Tooltip content={`Classified as platform add-on${app.addonReason ? ` · ${app.addonReason}` : ''}. Shown here for completeness.`} delay={150}>
            <span className="inline-flex items-center rounded-sm bg-theme-hover px-1.5 py-px text-[10px] font-medium text-theme-text-tertiary ring-1 ring-inset ring-theme-border">add-on</span>
          </Tooltip>
        )}
        {app.category === 'mixed' && (
          <Tooltip content={`Mixed app/add-on evidence${app.addonReason ? ` · ${app.addonReason}` : ''}. The row remains visible because classification is not identity.`} delay={150}>
            <span className="inline-flex items-center rounded-sm bg-amber-50 px-1.5 py-px text-[10px] font-medium text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900">mixed</span>
          </Tooltip>
        )}
        <ClassBadge workloadClass={workloadClass} />
        <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
          <span className={`inline-flex items-center gap-2 rounded-md px-2.5 py-1 ring-1 ring-inset ${v.ring}`}>
            <span className={`h-2 w-2 rounded-full ${v.dot}`} />
            <span className={`text-sm font-semibold ${v.text}`}>{v.label}</span>
          </span>
          {restartSignal && (
            <Tooltip content={`${restartSignal.workload} · ${pluralize(restartSignal.restarts, 'restart')}`} delay={150}>
              <span className="inline-flex items-center rounded-md bg-amber-50 px-2 py-1 text-xs font-semibold text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900">
                Pod warning: {restartSignal.reason || 'Restarts'} · {pluralize(restartSignal.restarts, 'restart')}
              </span>
            </Tooltip>
          )}
          {versions.length > 1 && (
            <Tooltip content={`Version skew across workloads: ${versions.join(', ')}`} delay={150}>
              <span className="inline-flex items-center rounded-md bg-amber-50 px-2 py-1 font-mono text-xs text-amber-700 ring-1 ring-inset ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900">{versions.length} versions</span>
            </Tooltip>
          )}
        </div>
      </div>

      {/* Context strip */}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-theme-border px-4 py-2 sm:px-6">
        {env && (
          <ContextFact label="Environment">
            {inferred ? (
              <Tooltip content={`Inferred from namespace "${app.namespace ?? env}".`} delay={150}>
                <span className="italic">~{env}</span>
              </Tooltip>
            ) : (
              env
            )}
          </ContextFact>
        )}
        {app.namespace && (
          <ContextFact label="Namespace">
            <span className="font-mono">{app.namespace}</span>
          </ContextFact>
        )}
        <ContextFact label="Ready">
          <ReadyBar ready={ready} desired={desired} />
        </ContextFact>
        {versions.length > 0 && (
          <ContextFact label="Version">
            <span className="font-mono">{versions.length === 1 ? versions[0] : `${versions.length} versions`}</span>
          </ContextFact>
        )}
      </div>

      {/* Workload selector — only when more than one. */}
      {workloads.length > 1 && (
        <div className="flex flex-wrap items-center gap-1.5 border-b border-theme-border px-4 py-2 sm:px-6">
          <span className="text-[10px] uppercase tracking-wide text-theme-text-tertiary">Workload</span>
          {workloads.map((w) => {
            const key = workloadKey(w)
            const on = key === selectedValue
            return (
              <button
                key={key}
                type="button"
                onClick={() => setSelected(key)}
                className={`inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-xs ${on ? 'bg-skyhook-500/10 text-theme-text-primary ring-1 ring-inset ring-skyhook-500/30' : 'text-theme-text-secondary hover:bg-theme-hover'}`}
              >
                <StatusDot tone={mapHealthToTone((w.health as AppHealth) || 'unknown')} />
                <span className="font-mono">{w.kind}/{w.name}</span>
              </button>
            )
          })}
        </div>
      )}

      {/* Embedded WorkloadView (host-injected). */}
      {selectedWorkload ? (
        <div key={workloadKey(selectedWorkload)}>{renderWorkload(selectedWorkload)}</div>
      ) : (
        <div className="rounded-md border border-dashed border-theme-border p-8 text-center text-sm text-theme-text-tertiary">
          This application has no inspectable workloads.
        </div>
      )}
    </div>
  )
}

function restartWarning(workloads: AppWorkload[]): { restarts: number; reason?: string; workload: string } | null {
  let worst: { restarts: number; reason?: string; workload: string } | null = null
  for (const w of workloads) {
    const r = w.restarts ?? 0
    if (r > 0 && (!worst || r > worst.restarts)) {
      worst = { restarts: r, reason: w.reason, workload: `${w.kind}/${w.name}` }
    }
  }
  return worst
}
