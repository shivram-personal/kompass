import { useMemo, useState, useCallback, type ReactNode } from 'react'
import { ArrowLeft, Boxes, Network } from 'lucide-react'
import { clsx } from 'clsx'
import type { Topology, TopologyNode } from '../../types'
import { StatusDot, mapHealthToTone } from '../ui/status-tone'
import { Tooltip } from '../ui/Tooltip'
import { TopologyGraph } from '../topology/TopologyGraph'
import { pluralize } from '../../utils/pluralize'
import { kindToPlural, apiVersionToGroup } from '../../utils/navigation'
import { tagWorkloadOwnership, seedNodeIds, ownershipOf, workloadKey, type NeighborhoodSeed } from '../../utils/topology-neighborhood'
import { workloadHue, NEUTRAL_OWNER, type WorkloadFocus } from '../../utils/workload-colors'
import {
  type AppRow,
  type AppWorkload,
  CHIP_TONE,
  HEALTH_META,
  healthOf,
  namespaceOf,
  namespacesOf,
  resolveEnv,
  workloadClassOf,
  worstHealth,
} from '../../utils/applications'
import { VersionTooltip } from './AppTooltips'
import { ProvenanceBadge, ClassBadge, CategoryChip, VersionInfo } from './AppChips'
import { ReadyBar } from './ReadyBar'

// ApplicationDetail — pure single-cluster detail shell. Owns the title row
// (icon, name, provenance/ungrouped chip, add-on/mixed chip, class badge,
// health pill) + a context strip (Environment · Namespace · Ready · Version) +
// a workload selector (only when >1 workload). The embedded WorkloadView is
// injected by the host via the `renderWorkload` render-prop, keyed to the
// selected workload — the shell never touches data hooks.
//
// Count-adaptive landing: a multi-workload app with a topology lands on the
// app-level graph (the whole app's neighborhood) and drills into a workload's
// runtime on node/pill click. A single-workload app (or no topology) goes
// straight to the workload runtime — a graph of one Deployment + a Service
// adds nothing.

export type SelectedAppWorkload = NeighborhoodSeed

/** Workload selection is either fully controlled (key + callback, the host
 *  wires it to the URL so back/forward works) or fully internal — providing
 *  only half silently freezes the rail, so the types forbid it. `null` = the
 *  app graph landing; a key (see `workloadKey`) = that workload's runtime. */
type SelectionProps =
  | { selectedWorkloadKey: string | null; onSelectWorkload: (key: string | null) => void }
  | { selectedWorkloadKey?: undefined; onSelectWorkload?: undefined }

export type ApplicationDetailProps = {
  app: AppRow
  onBack: () => void
  /** Render the host's WorkloadView for the chosen workload. */
  renderWorkload: (workload: SelectedAppWorkload) => ReactNode
  /** Resources-view topology spanning the app's namespaces. When present and the
   *  app has >1 workload, the detail lands on the app graph. */
  topology?: Topology
  /** Open a related (non-workload) resource clicked in the app graph. */
  onNavigateToResource?: (resource: { kind: string; namespace: string; name: string; group?: string }) => void
} & SelectionProps

function ContextFact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-w-0 items-baseline gap-1.5">
      <span className="text-[10px] uppercase tracking-wide text-theme-text-tertiary">{label}</span>
      <span className="min-w-0 truncate text-xs text-theme-text-secondary">{children}</span>
    </div>
  )
}

export function ApplicationDetail({ app, onBack, renderWorkload, topology, onNavigateToResource, selectedWorkloadKey, onSelectWorkload }: ApplicationDetailProps) {
  // Stable order regardless of API ordering: rail rows and the per-workload
  // color assignment both follow this array, so an order flap between
  // refetches must not reshuffle rows or reassign a workload's hue.
  const workloads = useMemo(
    () =>
      [...(app.workloads ?? [])].sort(
        (a, b) => a.name.localeCompare(b.name) || a.kind.localeCompare(b.kind) || a.namespace.localeCompare(b.namespace),
      ),
    [app.workloads],
  )
  const overall = worstHealth([app.health, ...workloads.map((w) => w.health)])
  const verdictTone = HEALTH_META[overall].pill
  const verdictLabel = HEALTH_META[overall].label
  const workloadClass = workloadClassOf(app.workload_class)
  const versions = useMemo(() => Array.from(new Set((app.versions || []).filter(Boolean))), [app.versions])
  const ready = workloads.reduce((n, w) => n + (w.ready ?? 0), 0)
  const desired = workloads.reduce((n, w) => n + (w.desired ?? 0), 0)
  const restartSignal = restartWarning(workloads)
  // Resolve namespace the same way the list does (the workloads' shared
  // namespace) so env/namespace match across list and detail. Multi-namespace
  // apps get the count, never an arbitrary pick.
  const namespace = namespaceOf(app)
  const namespaces = namespacesOf(app)
  const { env, inferred } = resolveEnv(undefined, namespace)

  // The app graph is the landing for multi-workload apps when a topology was
  // injected. A single workload (or no topology) skips straight to runtime.
  const appGraphAvailable = !!topology && workloads.length > 1

  // `null` = the app graph (landing); a key = that workload's runtime. Initially
  // null so multi-workload apps land on the graph; single-workload apps ignore
  // this and render the lone workload directly. Controlled by the host (URL) when
  // `selectedWorkloadKey` is provided, otherwise internal.
  const [internalSelected, setInternalSelected] = useState<string | null>(null)
  const rawSelected = selectedWorkloadKey !== undefined ? selectedWorkloadKey : internalSelected
  const setSelected = useCallback(
    (key: string | null) => (onSelectWorkload ? onSelectWorkload(key) : setInternalSelected(key)),
    [onSelectWorkload],
  )
  const selectedWorkload = rawSelected ? workloads.find((w) => workloadKey(w) === rawSelected) : undefined
  // A stale controlled key (a deleted workload still in the URL) falls back to
  // the app graph rather than silently rendering a different workload under a
  // URL that names the missing one.
  const selected = rawSelected !== null && !selectedWorkload ? null : rawSelected

  // Hover-focus: the workload (or NEUTRAL_OWNER) whose nodes should stay lit
  // while the rest of the graph dims. Driven by the rail and, reciprocally, by
  // hovering a node.
  const [focusedOwnerId, setFocusedOwnerId] = useState<WorkloadFocus>(null)

  const appSeeds = useMemo(
    () => workloads.map((w) => ({ kind: w.kind, namespace: w.namespace, name: w.name })),
    [workloads],
  )
  // Neighborhood subgraph + per-workload color/ownership tagging in one pass.
  const ownership = useMemo(
    () => (topology ? tagWorkloadOwnership(topology, appSeeds) : null),
    [topology, appSeeds],
  )
  const appGraph = ownership?.topology ?? null
  const appGraphFocusId = useMemo(
    () => (topology ? seedNodeIds(topology, appSeeds)[0] : undefined),
    [topology, appSeeds],
  )

  // Hovering a node lights up its owning workload (and the rail row); a shared/
  // unscoped node clears the focus rather than dimming everything.
  const handleNodeHover = useCallback((node: TopologyNode | null) => {
    setFocusedOwnerId(node ? ownershipOf(node.data).ownerWorkloadId : null)
  }, [])

  // A node click in the app graph either drills into one of the app's workloads
  // (its runtime) or opens a related resource (Service/config/…) via the host.
  const handleAppNodeClick = useCallback(
    (node: TopologyNode) => {
      const ns = (node.data?.namespace as string) || ''
      const match = workloads.find((w) => w.kind === node.kind && w.name === node.name && w.namespace === ns)
      if (match) {
        setSelected(workloadKey(match))
        return
      }
      onNavigateToResource?.({
        kind: kindToPlural(node.kind),
        namespace: ns,
        name: node.name,
        group: apiVersionToGroup(node.data?.apiVersion as string | undefined),
      })
    },
    [workloads, onNavigateToResource, setSelected],
  )

  return (
    <div className="flex w-full flex-col">
      {/* Title row */}
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2 border-b border-theme-border px-4 py-3 sm:px-6">
        <button type="button" onClick={onBack} className="flex items-center gap-1.5 text-xs text-theme-text-tertiary hover:text-theme-text-primary">
          <ArrowLeft className="h-3.5 w-3.5" aria-hidden /> Applications
        </button>
        <span className="h-6 w-px bg-theme-border" aria-hidden />
        <span className={`flex h-8 w-8 items-center justify-center rounded-md ring-1 ring-inset ${verdictTone}`}>
          <Boxes className="h-4 w-4" aria-hidden />
        </span>
        <h1 className="text-2xl font-semibold text-theme-text-primary">{app.name}</h1>
        <ProvenanceBadge tier={app.tier} appKey={app.key} confidence={app.confidence} />
        <CategoryChip category={app.category} addonReason={app.addonReason} />
        <ClassBadge workloadClass={workloadClass} />
        <div className="ml-auto flex flex-wrap items-center justify-end gap-2">
          <span className={`inline-flex items-center gap-2 rounded-md px-2.5 py-1 ring-1 ring-inset ${verdictTone}`}>
            <StatusDot tone={mapHealthToTone(overall)} />
            <span className="text-sm font-semibold">{verdictLabel}</span>
          </span>
          {restartSignal && (
            <Tooltip content={`${restartSignal.workload} · ${pluralize(restartSignal.restarts, 'restart')}`} delay={150}>
              <span className={`inline-flex items-center rounded-md px-2 py-1 text-xs font-semibold ring-1 ring-inset ${CHIP_TONE.amber}`}>
                Pod warning: {restartSignal.reason || 'Restarts'} · {pluralize(restartSignal.restarts, 'restart')}
              </span>
            </Tooltip>
          )}
          {/* Amber only on real skew (same image, different tags) — the context
              strip already covers the multi-image "N versions" case neutrally. */}
          {app.versionSkew && versions.length > 1 && (
            <Tooltip content={<VersionTooltip workloads={workloads} />} delay={150}>
              <span className={`inline-flex items-center rounded-md px-2 py-1 font-mono text-xs ring-1 ring-inset ${CHIP_TONE.amber}`}>{versions.length} versions</span>
            </Tooltip>
          )}
        </div>
      </div>

      {/* Context strip */}
      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-b border-theme-border px-4 py-2 sm:px-6">
        {env && (
          <ContextFact label="Environment">
            {inferred ? (
              <Tooltip content={`Inferred from namespace "${namespace || env}".`} delay={150}>
                <span className="italic">~{env}</span>
              </Tooltip>
            ) : (
              env
            )}
          </ContextFact>
        )}
        {namespace ? (
          <ContextFact label="Namespace">
            <span className="font-mono">{namespace}</span>
          </ContextFact>
        ) : namespaces.length > 1 ? (
          <ContextFact label="Namespaces">
            <Tooltip content={namespaces.join(', ')} delay={150}>
              <span>{namespaces.length} namespaces</span>
            </Tooltip>
          </ContextFact>
        ) : null}
        <ContextFact label="Ready">
          <ReadyBar ready={ready} desired={desired} width="w-16" />
        </ContextFact>
        {(app.appVersion || versions.length > 0) && (
          <ContextFact label="Version">
            <VersionInfo app={app} variant="fact" />
          </ContextFact>
        )}
      </div>

      {/* Body: for a multi-workload app, a workload rail (navigation + color
          legend + hover-focus) beside the app graph or the selected workload's
          runtime. A single-workload app skips straight to its runtime. */}
      {workloads.length > 1 ? (
        <div className="flex h-[calc(100vh-13rem)] min-h-[480px]">
          <WorkloadRail
            workloads={workloads}
            colorByWorkload={ownership?.colorByWorkload ?? null}
            showAllEntry={appGraphAvailable}
            // Without an app graph the body always renders a workload (selected ??
            // workloads[0]); mirror that so the rail marks it active.
            selectedKey={appGraphAvailable ? selected : selected ?? (workloads[0] ? workloadKey(workloads[0]) : null)}
            focusedOwnerId={focusedOwnerId}
            onSelect={setSelected}
            onFocus={setFocusedOwnerId}
          />
          <div className="relative min-h-0 flex-1 overflow-hidden bg-theme-surface">
            {appGraphAvailable && selected === null ? (
              <TopologyGraph
                topology={appGraph}
                viewMode="resources"
                groupingMode="namespace"
                hideGroupHeader
                onNodeClick={handleAppNodeClick}
                showExportButton={false}
                focusNodeId={appGraphFocusId}
                focusedOwnerId={focusedOwnerId}
                onNodeHover={handleNodeHover}
              />
            ) : (
              <div key={workloadKey(selectedWorkload ?? workloads[0])} className="h-full min-h-0">
                {renderWorkload(selectedWorkload ?? workloads[0])}
              </div>
            )}
          </div>
        </div>
      ) : (
        renderRuntime(workloads[0], renderWorkload)
      )}
    </div>
  )
}

// WorkloadRail — the app's workloads as a left rail: color legend (each row's
// swatch matches that workload's node tint in the graph), navigation (click →
// that workload's runtime in place), and hover-focus (hover → that workload's
// nodes stay lit, the rest dim). Reciprocal: hovering a node lights its row.
function WorkloadRail({
  workloads,
  colorByWorkload,
  showAllEntry,
  selectedKey,
  focusedOwnerId,
  onSelect,
  onFocus,
}: {
  workloads: AppWorkload[]
  colorByWorkload: Map<string, number> | null
  showAllEntry: boolean
  selectedKey: string | null
  focusedOwnerId: WorkloadFocus
  onSelect: (key: string | null) => void
  onFocus: (owner: WorkloadFocus) => void
}) {
  return (
    <div
      className="flex w-56 shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-theme-border bg-theme-base px-2 py-2"
      onMouseLeave={() => onFocus(null)}
    >
      <div className="flex items-center justify-between px-1.5 pb-1 pt-0.5">
        <span className="text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">Workloads</span>
        <span className="rounded-full bg-theme-hover px-1.5 text-[10px] font-medium text-theme-text-tertiary">{workloads.length}</span>
      </div>

      {showAllEntry && (
        <RailRow
          active={selectedKey === null}
          onClick={() => onSelect(null)}
          onMouseEnter={() => onFocus(null)}
          swatch={<Network className="h-3.5 w-3.5 text-theme-text-secondary" aria-hidden />}
          title="All workloads"
        />
      )}

      {workloads.map((w) => {
        const key = workloadKey(w)
        const idx = colorByWorkload?.get(key)
        const hue = idx != null ? workloadHue(idx) : null
        return (
          <RailRow
            key={key}
            active={selectedKey === key}
            focused={focusedOwnerId === key}
            onClick={() => onSelect(key)}
            onMouseEnter={() => onFocus(key)}
            swatch={
              <span
                className="h-3 w-3 shrink-0 rounded-[3px] ring-1 ring-inset ring-theme-border"
                style={hue ? { background: hue.swatch } : { background: 'var(--color-theme-border, #94a3b8)' }}
              />
            }
            title={w.name}
            tooltip={`${w.name} · ${w.kind}`}
            subtitle={w.kind}
            trailing={<StatusDot tone={mapHealthToTone(healthOf(w.health))} />}
          />
        )
      })}

      {colorByWorkload && (
        <RailRow
          muted
          focused={focusedOwnerId === NEUTRAL_OWNER}
          onMouseEnter={() => onFocus(NEUTRAL_OWNER)}
          swatch={<span className="h-3 w-3 shrink-0 rounded-[3px] bg-theme-border" />}
          title="Shared / unscoped"
        />
      )}
    </div>
  )
}

function RailRow({
  active,
  focused,
  muted,
  onClick,
  onMouseEnter,
  swatch,
  title,
  tooltip,
  subtitle,
  trailing,
}: {
  active?: boolean
  focused?: boolean
  muted?: boolean
  onClick?: () => void
  onMouseEnter?: () => void
  swatch: ReactNode
  title: string
  /** Full label shown on hover — the row title truncates. */
  tooltip?: string
  subtitle?: string
  trailing?: ReactNode
}) {
  const className = clsx(
    'flex w-full items-center gap-2 rounded-md px-1.5 py-1.5 text-left transition-colors',
    active
      ? 'selection selection-ring'
      : focused
        ? 'bg-theme-hover'
        : onClick && 'hover:bg-theme-hover',
    !onClick && 'cursor-default',
  )
  const titleEl = <span className={clsx('block truncate text-xs', muted ? 'text-theme-text-tertiary' : 'font-medium text-theme-text-primary')}>{title}</span>
  const inner = (
    <>
      {swatch}
      <span className="min-w-0 flex-1">
        {tooltip ? <Tooltip content={tooltip} delay={300} position="right">{titleEl}</Tooltip> : titleEl}
        {subtitle && <span className="block truncate text-[10px] uppercase tracking-wide text-theme-text-tertiary">{subtitle}</span>}
      </span>
      {trailing}
    </>
  )
  return onClick ? (
    <button type="button" onClick={onClick} onMouseEnter={onMouseEnter} className={className}>{inner}</button>
  ) : (
    <div onMouseEnter={onMouseEnter} className={className}>{inner}</div>
  )
}

// renderRuntime shows the single-workload app's runtime (host-injected
// WorkloadView). Multi-workload apps render runtimes beside the rail instead,
// which also owns navigation back to the app graph.
function renderRuntime(
  workload: SelectedAppWorkload | undefined,
  renderWorkload: (workload: SelectedAppWorkload) => ReactNode,
): ReactNode {
  if (!workload) {
    return (
      <div className="rounded-md border border-dashed border-theme-border p-8 text-center text-sm text-theme-text-tertiary">
        This application has no inspectable workloads.
      </div>
    )
  }
  return (
    // Concrete height (not flex-1 of an unsized column): the workload's
    // Topology tab positions ReactFlow absolutely and collapses to zero inside
    // a content-sized ancestor. Mirrors the multi-workload rail row's height.
    <div className="flex h-[calc(100vh-13rem)] min-h-[480px] flex-col">
      <div key={workloadKey(workload)} className="min-h-0 flex-1">{renderWorkload(workload)}</div>
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
