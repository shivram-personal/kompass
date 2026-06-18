import { useState, useCallback, useEffect, useMemo } from 'react'
import { clsx } from 'clsx'
import {
  AlertTriangle,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  Copy,
  Check,
  ShieldAlert,
  Network,
} from 'lucide-react'
import type { Trace, Hop, Finding, FindingSeverity, Verdict, HopConfig, ProbeResult, ResourceRef } from './types'
import { collapseSkipRows } from './probe-display'
import { AlertBanner } from '../ui/drawer-components'
import { Badge } from '../ui/Badge'
import { StatusDot, type StatusTone } from '../ui/status-tone'

interface TracePanelProps {
  trace: Trace | undefined
  isLoading?: boolean
  error?: Error | null
  /** Optional: parent-provided refetch action surfaced when the trace verdict
   *  is unknown — gives the operator a way to retry after RBAC/cache changes
   *  without forcing a drawer close+reopen. */
  onRefresh?: () => void
  /** True when the next refetch should request reachability probes. The
   *  parent passes back the resulting Trace via the trace prop. Probes are
   *  one-shot — the parent should reset this to false after results arrive. */
  probeRequested?: boolean
  /** Callback when the operator asks for a reachability test. The parent
   *  is expected to set probeRequested=true and trigger a refetch; results
   *  flow back through `trace.downstream[].probes`. */
  onRunProbes?: () => void
  /** When provided, the resource label on each hop becomes clickable and
   *  invokes this with the hop's ResourceRef so the host can open the
   *  resource's detail view. Hops without a routable name (collections like
   *  the Pods fan-out) stay non-clickable. Without this prop, hop rows are
   *  inert text and the only interaction is the expand chevron. */
  onNavigateToResource?: (ref: ResourceRef) => void
}

/**
 * TracePanel renders the path-shaped diagnosis for one network entry kind.
 *
 * Layout invariant — top to bottom mirrors traffic direction:
 *   1) Verdict banner (the one-sentence answer)
 *   2) Upstreams (parallel hops INTO the subject — judged independently)
 *   3) Subject + Downstream chain (where BrokenAt applies)
 *
 * The hop rail (dots + connector line) is the visual spine; the rail color at
 * each dot mirrors that hop's worst finding. The first critical Downstream
 * hop gets a heavier ring + a left-edge accent so the eye lands on the break
 * before reading the message.
 */
export function TracePanel({ trace, isLoading, error, onRefresh, probeRequested, onRunProbes, onNavigateToResource }: TracePanelProps) {
  if (isLoading && !trace) {
    return <PanelMessage tone="muted" message="Loading trace…" />
  }
  if (error && !trace) {
    return <PanelMessage tone="error" message={`Failed to load trace: ${error.message}`} onAction={onRefresh} actionLabel="Retry" />
  }
  if (!trace) {
    return <PanelMessage tone="muted" message="No trace data available." />
  }
  // Server-side normalization fills these as []; the nullish-coalesce
  // covers any future shape drift without crashing the iteration.
  const upstreams = (trace.upstreams ?? []).map(normalizeHopFindings)
  const downstream = (trace.downstream ?? []).map(normalizeHopFindings)
  const hasPath = downstream.length > 0 || upstreams.length > 0
  const probesPresent = hasAnyProbes(trace)
  const feasibility = useMemo(() => probeFeasibility([...upstreams, ...downstream]), [upstreams, downstream])
  // Layout is padding-free; the consumer (Diagnose tab or drawer Section)
  // owns the outer spacing so we don't double-pad when nested inside a
  // Radar Section component.
  const brokenHop = trace.brokenAt >= 0 && trace.brokenAt < downstream.length
    ? downstream[trace.brokenAt]
    : undefined
  return (
    <div className="flex flex-col gap-3">
      <VerdictBanner verdict={trace.verdict} reason={trace.reason} brokenHop={brokenHop} onRefresh={onRefresh} />
      {onRunProbes && hasPath && (
        <ReachabilitySection
          feasibility={feasibility}
          probesPresent={probesPresent}
          isLoading={Boolean(isLoading)}
          requested={Boolean(probeRequested)}
          onRun={onRunProbes}
        />
      )}
      {upstreams.length > 0 && <UpstreamsBlock upstreams={upstreams} onNavigate={onNavigateToResource} />}
      {downstream.length > 0 && (
        <DownstreamBlock subject={trace.subject} downstream={downstream} brokenAt={trace.brokenAt} onNavigate={onNavigateToResource} />
      )}
      {trace.truncated && (
        <p className="text-xs text-theme-text-tertiary">
          Some attached routes were omitted to bound the trace response.
        </p>
      )}
      <ZeroConfigDisclaimer />
    </div>
  )
}

function hasAnyProbes(trace: Trace): boolean {
  return (
    trace.downstream.some((h) => (h.probes?.length ?? 0) > 0) ||
    trace.upstreams.some((h) => (h.probes?.length ?? 0) > 0)
  )
}

function normalizeHopFindings(hop: Hop): Hop {
  return hop.findings ? hop : { ...hop, findings: [] }
}

// Verdict banner — delegates to the shared AlertBanner so the trace surface
// matches the rest of Radar (Helm, GitOps, renderers all use AlertBanner).
// The verdict→variant mapping keeps the operator's color vocabulary stable
// across surfaces.

const VERDICT_TITLE: Record<Verdict, string> = {
  healthy: 'Traffic path looks healthy',
  degraded: 'Traffic path is degraded',
  broken: 'Traffic path is broken',
  unknown: "Traffic path can't be verified",
}

const VERDICT_VARIANT: Record<Verdict, 'success' | 'warning' | 'error' | 'info'> = {
  healthy: 'success',
  degraded: 'warning',
  broken: 'error',
  // Unknown is an investigate state, not an informational one — operators
  // opening the panel mid-incident need a visual cue that something needs
  // attention. The warning variant matches that intent.
  unknown: 'warning',
}

const VERDICT_ICON: Record<Verdict, React.ComponentType<{ className?: string }>> = {
  healthy: CheckCircle2,
  degraded: AlertTriangle,
  broken: ShieldAlert,
  unknown: AlertTriangle,
}

function VerdictBanner({ verdict, reason, brokenHop, onRefresh }: { verdict: Verdict; reason?: string; brokenHop?: Hop; onRefresh?: () => void }) {
  // When a single hop is the locus of the break/degrade, name it in the
  // banner title — a generic "Traffic path is degraded" leaves the
  // operator without a starting point. Synthetic collection hops (Pods,
  // Routes) carry an empty Resource.Name; skip the rewrite for those
  // since "Pods is broken" is grammatically off and the row below
  // already carries the same identity.
  let title = VERDICT_TITLE[verdict]
  if (brokenHop && brokenHop.resource.name && (verdict === 'broken' || verdict === 'degraded')) {
    const ref = brokenHop.resource
    const label = `${ref.kind} ${ref.name}`
    title = verdict === 'broken'
      ? `${label} is broken - traffic can't pass`
      : `${label} is degraded`
  }
  return (
    <AlertBanner
      variant={VERDICT_VARIANT[verdict]}
      icon={VERDICT_ICON[verdict]}
      title={title}
      message={reason}
    >
      {onRefresh && verdict === 'unknown' && (
        <button
          type="button"
          onClick={onRefresh}
          className="mt-2 text-xs px-2 py-1 rounded border border-current/30 hover:bg-current/10 transition-colors"
        >
          Refresh
        </button>
      )}
    </AlertBanner>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Upstreams — parallel hops INTO the subject
// ────────────────────────────────────────────────────────────────────────────

function UpstreamsBlock({ upstreams, onNavigate }: { upstreams: Hop[]; onNavigate?: (ref: ResourceRef) => void }) {
  const label = upstreams.length === 1 ? '1 parallel entry, judged independently' : `${upstreams.length} parallel entries, each judged independently`
  return (
    <section>
      <SectionHeader title="Upstreams" subtitle={label} />
      <div className="flex flex-col gap-2">
        {upstreams.map((hop, i) => (
          <HopRow key={hopKey(hop, i)} hop={hop} broken={false} compact onNavigate={onNavigate} />
        ))}
      </div>
    </section>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Downstream — chain from subject toward pods
// ────────────────────────────────────────────────────────────────────────────

function DownstreamBlock({ subject, downstream, brokenAt, onNavigate }: { subject: { kind: string; name: string; namespace?: string }; downstream: Hop[]; brokenAt: number; onNavigate?: (ref: ResourceRef) => void }) {
  return (
    <section>
      <SectionHeader
        title="Path"
        subtitle="Top-to-bottom is the direction traffic flows."
      />
      <div className="relative">
        {/* Spine — the visual continuity that says "these hops are one chain" */}
        <div className="absolute left-[14px] top-2 bottom-2 w-px bg-theme-border" aria-hidden />
        <div className="flex flex-col gap-2">
          {downstream.map((hop, i) => (
            <HopRow
              key={hopKey(hop, i)}
              hop={hop}
              broken={brokenAt === i}
              isSubject={i === 0 && hop.resource.kind === subject.kind && hop.resource.name === subject.name}
              onNavigate={onNavigate}
            />
          ))}
        </div>
      </div>
    </section>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Hop row — one resource along the path
// ────────────────────────────────────────────────────────────────────────────

function HopRow({ hop, broken, compact, isSubject, onNavigate }: { hop: Hop; broken: boolean; compact?: boolean; isSubject?: boolean; onNavigate?: (ref: ResourceRef) => void }) {
  // The server sorts findings worst-first, so the first finding's severity
  // is the row's overall severity. No need for a TS-side dupe of the
  // server's worst-severity walk.
  const worstSev: FindingSeverity | '' = hop.findings.length > 0 ? hop.findings[0].severity : ''
  const shouldAutoExpand = broken || worstSev === 'critical'
  const [expanded, setExpanded] = useState<boolean>(shouldAutoExpand)
  // When a probe run upgrades a hop to broken/critical, auto-expand it so
  // the new finding isn't hidden behind a collapsed row. We never auto-
  // collapse — once the user opens a hop, it stays open until they close it.
  useEffect(() => {
    if (shouldAutoExpand) setExpanded(true)
  }, [shouldAutoExpand])
  const toggle = useCallback(() => setExpanded((e) => !e), [])
  const hopMeta = hopMetaSummary(hop)
  const hasFindings = hop.findings.length > 0
  // A hop is navigable when the host provided a callback AND the row points
  // at one identifiable resource that is NOT the subject (clicking the
  // subject would navigate to the page the user is already on). Collection
  // hops (Pods fan-out, attached routes) carry an empty name and stay
  // non-clickable — there's no single resource for the host to open.
  const navigable = Boolean(onNavigate && hop.resource.name && !isSubject)
  // Row body navigates when there's a routable target; otherwise it falls
  // back to toggling the findings panel so collection hops stay useful.
  // Chevron is always toggle so the two gestures don't overload.
  const primary = navigable
    ? () => onNavigate!(hop.resource)
    : hasFindings ? toggle : undefined
  return (
    <div
      className={clsx(
        'relative transition-colors',
        broken && 'bg-red-500/5 -mx-2 px-2 rounded-md',
      )}
    >
      <div className="flex items-stretch">
        <button
          type="button"
          onClick={primary}
          className={clsx(
            'flex-1 min-w-0 flex items-start gap-2.5 py-1.5 text-left',
            primary && 'hover:bg-theme-hover rounded-md -mx-1 px-1',
            !primary && 'cursor-default',
          )}
          aria-label={navigable ? `Open ${hop.resource.kind} ${hop.resource.name}` : undefined}
          aria-expanded={primary === toggle ? expanded : undefined}
        >
          <span className="mt-1 shrink-0" aria-hidden>
            <StatusDot tone={severityToTone(worstSev)} size="sm" />
          </span>
          <div className="flex-1 min-w-0">
            <div className="flex items-center gap-1.5 flex-wrap">
              <Badge kind={hop.resource.kind} size="sm">{hop.resource.kind}</Badge>
              <span className={clsx(
                'text-sm font-medium break-all text-theme-text-primary',
                navigable && 'hover:underline',
              )}>
                {hop.resource.name || hopFallbackLabel(hop)}
              </span>
              {hop.resource.namespace && (
                <span className="text-[11px] text-theme-text-tertiary break-all">in {hop.resource.namespace}</span>
              )}
              <SeverityChip severity={worstSev} count={hop.findings.length} probes={hop.probes} />
            </div>
            {!compact && (
              <>
                {(!isSubject || hopMeta) && (
                  <div className="text-[11px] text-theme-text-tertiary mt-0.5 flex items-center gap-1.5 flex-wrap">
                    <Network className="w-3 h-3" />
                    {!isSubject && <span>{prettyEdge(hop.edge, isSubject)}</span>}
                    {!isSubject && hopMeta && <span aria-hidden>·</span>}
                    {hopMeta && <span>{hopMeta}</span>}
                  </div>
                )}
                {hop.config && <ConfigPills config={hop.config} hopKind={hop.resource.kind} />}
              </>
            )}
          </div>
        </button>
        {hasFindings && navigable && (
          <button
            type="button"
            onClick={toggle}
            className="shrink-0 px-2.5 flex items-center text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary rounded-r-md border-l border-theme-border"
            aria-expanded={expanded}
            aria-label={expanded ? 'Hide findings' : 'Show findings'}
            title={expanded ? 'Hide findings' : 'Show findings'}
          >
            {expanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </button>
        )}
        {hasFindings && !navigable && (
          <span className="shrink-0 px-2.5 flex items-center text-theme-text-tertiary" aria-hidden>
            {expanded ? <ChevronDown className="w-4 h-4" /> : <ChevronRight className="w-4 h-4" />}
          </span>
        )}
      </div>
      {expanded && hop.findings.length > 0 && (
        <div className="border-t border-theme-border px-3 py-2 flex flex-col gap-2">
          {hop.findings.map((f, i) => (
            <FindingRow key={f.code + ':' + i} finding={f} />
          ))}
        </div>
      )}
      {hop.probes && hop.probes.length > 0 && (
        <ProbeRows probes={hop.probes} />
      )}
    </div>
  )
}

// ProbeRows renders each probe result as an inline list row under the hop.
// The row carries everything an operator needs: severity dot, target,
// optional path label (when the probe traversed the API server, knowing
// that path is part of the answer), latency, status, and any error text.
// No diagram — the hop above is already the visual anchor.
function ProbeRows({ probes }: { probes: ProbeResult[] }) {
  // Failures first, then OK rows, then skipped — matches the operator's
  // scan priority. Same comparator the per-hop findings use. Skip rows
  // with identical (layer, path, reason) are collapsed into one row with
  // a count suffix so a 3-pod × 1-port Pods hop doesn't repeat the same
  // "non-HTTP port" line three times.
  const display = useMemo(() => {
    const sorted = [...probes].sort((a, b) => probeRank(a) - probeRank(b))
    return collapseSkipRows(sorted)
  }, [probes])
  return (
    <ul className="mt-2 pl-4 border-l border-theme-border ml-1 flex flex-col gap-1">
      {display.map((p, i) => <ProbeRow key={i} probe={p} />)}
    </ul>
  )
}

// probeRank orders probes for display: unhealthy first (most actionable),
// then degraded (responded but not at expected route / redirected), then
// healthy, then skipped last. Falls back to the binary ok+skipped split
// when tone is unset.
function probeRank(p: ProbeResult): number {
  if (p.skipped) return 3
  if (p.tone === 'unhealthy' || !p.ok) return 0
  if (p.tone === 'degraded') return 1
  return 2
}

function ProbeRow({ probe }: { probe: ProbeResult }) {
  // tone may carry a non-binary signal (3xx/4xx render degraded). When the
  // server doesn't set it, fall back to the binary derivation from ok+skipped.
  const tone: StatusTone = probe.tone ?? (probe.skipped ? 'unknown' : probe.ok ? 'healthy' : 'unhealthy')
  return (
    <li className="flex items-start gap-2 text-[11px]">
      <span className="mt-1 shrink-0" aria-hidden><StatusDot tone={tone} size="sm" /></span>
      <div className="flex-1 min-w-0">
        <div className="flex items-baseline gap-1.5 flex-wrap text-theme-text-primary">
          <span className="font-mono break-all">{probe.target || probe.layer.toUpperCase()}</span>
          {probePathLabel(probe) && (
            <span className="text-theme-text-tertiary">· {probePathLabel(probe)}</span>
          )}
          {typeof probe.latencyNs === 'number' && probe.latencyNs > 0 && !probe.skipped && (
            <span className="text-theme-text-tertiary">· {formatProbeLatency(probe.latencyNs)}</span>
          )}
          {probe.detail && !probe.skipped && (
            <span className="text-theme-text-tertiary break-words">· {probe.detail}</span>
          )}
        </div>
        {probe.skipped && probe.reason && (
          <div className="text-theme-text-tertiary italic break-words">skipped: {probe.reason}</div>
        )}
        {probe.error && !probe.skipped && (
          <div className="text-red-500 break-words">{probe.error}</div>
        )}
      </div>
    </li>
  )
}

// probePathLabel surfaces the path discriminator in operator language. The
// data path is the real workload route, so it gets a callout that names
// what a success there actually proves. The API path callout names what a
// success there does NOT prove, so the operator doesn't over-read the result.
function probePathLabel(p: ProbeResult): string {
  if (p.path === 'data') return 'pod-to-pod path'
  if (p.path === 'apiserver') return 'via Kubernetes API'
  return ''
}

function formatProbeLatency(ns: number): string {
  const ms = ns / 1_000_000
  if (ms >= 100) return `${ms.toFixed(0)}ms`
  return `${ms.toFixed(1)}ms`
}

// ReachabilitySection is the operator's affordance for "send live traffic
// against the declared path and tell me what happened". It only renders
// the button when at least one hop has a probeable surface; for traces
// whose only hops are routes-without-addresses or headless services we
// explain why instead of pretending the button works. Feasibility is
// computed from each hop's HopConfig — see probeFeasibility().
function ReachabilitySection({
  feasibility,
  probesPresent,
  isLoading,
  requested,
  onRun,
}: {
  feasibility: ProbeFeasibility
  probesPresent: boolean
  isLoading: boolean
  requested: boolean
  onRun: () => void
}) {
  const running = requested && !probesPresent && isLoading

  if (!feasibility.probeable && !probesPresent) {
    return (
      <div className="flex items-baseline gap-2 text-xs">
        <span className="font-medium text-theme-text-secondary">Reachability test</span>
        <span className="text-theme-text-tertiary">not applicable. {feasibility.reason}</span>
      </div>
    )
  }
  return (
    <div className="flex items-center gap-2">
      <span className="text-xs font-medium text-theme-text-secondary">Reachability test</span>
      <span className="text-xs text-theme-text-tertiary flex-1 truncate">
        {probesPresent
          ? 'Results shown beneath each hop.'
          : running
            ? 'Running…'
            : ''}
      </span>
      <button
        type="button"
        onClick={onRun}
        disabled={isLoading}
        className={clsx(
          'shrink-0 btn-brand text-xs px-3 py-1 flex items-center gap-1.5',
          isLoading && 'opacity-50 cursor-not-allowed',
        )}
      >
        <span
          className={clsx(
            'w-1.5 h-1.5 rounded-full',
            running ? 'bg-amber-300 animate-pulse' : 'bg-white/80',
          )}
          aria-hidden
        />
        Run reachability test
      </button>
    </div>
  )
}

interface ProbeFeasibility {
  probeable: boolean
  reason: string
}

// probeFeasibility decides whether any hop in the trace has a surface a
// reachability probe could target. The logic mirrors the kinds runProbes
// supports, but stays in the UI so the server doesn't have to ship a
// prediction the UI is the only consumer of. Matches the hop kinds in
// internal/trace/probes.go probeHop().
function probeFeasibility(hops: Hop[]): ProbeFeasibility {
  let reason = 'no reachable surface declared for this resource.'
  for (const hop of hops) {
    const cfg = hop.config
    if (!cfg) continue
    switch (hop.resource.kind) {
      case 'Service':
        // Headless Services (clusterIP === 'None') still resolve probes via
        // the apiserver proxy when a client is available. The backend runs
        // the same ladder; the "headless" tag is informational, not a gate.
        if ((cfg.ports?.length ?? 0) > 0) return { probeable: true, reason: '' }
        break
      case 'Pods':
        if ((cfg.containerPorts?.length ?? 0) > 0 && ((cfg.podIPs?.length ?? 0) > 0 || (cfg.podNames?.length ?? 0) > 0)) {
          return { probeable: true, reason: '' }
        }
        if ((cfg.containerPorts?.length ?? 0) > 0) {
          reason = 'no ready pods to probe.'
        }
        break
      case 'Ingress':
        if ((cfg.hostnames?.length ?? 0) > 0) return { probeable: true, reason: '' }
        reason = 'this Ingress declares no hostnames; there\'s no target to probe.'
        break
      case 'Gateway':
        if ((cfg.addresses?.length ?? 0) > 0 && (cfg.listeners?.length ?? 0) > 0) {
          return { probeable: true, reason: '' }
        }
        if ((cfg.addresses?.length ?? 0) === 0) {
          reason = 'this Gateway has no programmed addresses yet; probes would have no target.'
        }
        break
      case 'HTTPRoute':
      case 'GRPCRoute':
        reason = 'routes have no own routable address; reachability lives on the parent Gateway and the backend Service.'
        break
    }
  }
  return { probeable: false, reason }
}

// ────────────────────────────────────────────────────────────────────────────
// Config pills — declared shape per hop (ports, hostnames, listeners)
// ────────────────────────────────────────────────────────────────────────────

/**
 * ConfigPills renders the hop's declared-config shape as a row of compact
 * pills. The rule of thumb: surface what the operator needs to *reason*
 * about traffic at this hop (ports, hostnames, listeners) without
 * duplicating data already in the resource's own page.
 *
 * Noise control:
 *   - Pills are filtered to ≤6 visible; the rest collapse into "+N more".
 *   - Long hostname lists collapse to first + count.
 *   - Selector is omitted from the pill strip (always available in the
 *     kubectl reproducer command instead).
 */
function ConfigPills({ config, hopKind }: { config: HopConfig; hopKind: string }) {
  const pills: { key: string; text: string; tone?: 'muted' | 'accent'; title?: string }[] = []

  if (config.serviceType && hopKind === 'Service') {
    pills.push({ key: 'svctype', text: config.serviceType, tone: 'accent' })
  }
  if (config.clusterIP && config.clusterIP !== '' && hopKind === 'Service') {
    if (config.clusterIP === 'None') {
      pills.push({ key: 'headless', text: 'headless', tone: 'muted' })
    }
  }

  for (const p of config.ports ?? []) {
    const label = p.name ? `${p.name} ` : ''
    const proto = p.protocol && p.protocol !== 'TCP' ? `/${p.protocol}` : ''
    pills.push({
      key: `port:${p.name || p.port}`,
      text: `${label}${p.port} → :${p.targetPort ?? p.port}${proto}`,
      title: `Service port ${p.port}${p.name ? ` (${p.name})` : ''} routes to targetPort ${p.targetPort ?? p.port}${proto}`,
    })
  }

  for (const cp of config.containerPorts ?? []) {
    const namePart = cp.name ? `${cp.name}:` : ''
    pills.push({
      key: `cp:${cp.container}:${cp.port}`,
      text: `${cp.container} ${namePart}${cp.port}`,
      tone: 'muted',
      title: `Container ${cp.container} exposes ${cp.name ? `named port "${cp.name}" → ` : ''}${cp.port}${cp.protocol ? '/' + cp.protocol : ''}`,
    })
  }

  for (const pr of config.probes ?? []) {
    if (!pr.port && !pr.path) continue
    const portPart = pr.port ? `:${pr.port}` : ''
    const pathPart = pr.path && pr.path !== '/' ? pr.path : ''
    pills.push({
      key: `probe:${pr.container}:${pr.type}`,
      text: `${pr.type === 'readiness' ? 'readiness' : 'liveness'} ${pr.scheme ?? 'HTTP'}${portPart}${pathPart}`,
      tone: 'muted',
      title: `${pr.container} ${pr.type} probe via ${pr.scheme ?? 'HTTP'}${portPart}${pathPart || ''}`,
    })
  }

  for (const host of config.hostnames ?? []) {
    pills.push({ key: `host:${host}`, text: host, tone: 'accent', title: `Hostname: ${host}` })
  }

  for (const l of config.listeners ?? []) {
    const proto = l.protocol ?? 'TCP'
    const host = l.hostname ? ` ${l.hostname}` : ''
    pills.push({
      key: `listener:${l.name || l.port}`,
      text: `${proto}:${l.port}${host}`,
      tone: 'accent',
      title: `Gateway listener${l.name ? ` "${l.name}"` : ''}: ${proto} on port ${l.port}${host}`,
    })
  }

  for (const addr of config.addresses ?? []) {
    pills.push({ key: `addr:${addr}`, text: `@${addr}`, tone: 'accent' })
  }

  // Route rules become a single compact "N paths → X backends" pill — the
  // detail expands into a per-rule list below (see ConfigRules).
  if (config.rules && config.rules.length > 0) {
    const total = config.rules.reduce((n, r) => n + (r.backends?.length ?? 0), 0)
    pills.push({
      key: 'rules',
      text: `${config.rules.length} rule${config.rules.length > 1 ? 's' : ''} → ${total} backend${total === 1 ? '' : 's'}`,
      tone: 'muted',
    })
  }

  if (pills.length === 0) return null

  const limit = 6
  const visible = pills.slice(0, limit)
  const overflow = pills.length - limit

  return (
    <div className="mt-1 flex items-center gap-1 flex-wrap">
      {visible.map((p) => (
        <Badge
          key={p.key}
          size="sm"
          severity={p.tone === 'accent' ? 'info' : 'neutral'}
          className="font-mono"
          title={p.title}
        >
          {p.text}
        </Badge>
      ))}
      {overflow > 0 && (
        <Badge size="sm" severity="neutral" title={`${overflow} additional details elided to keep the trace readable`}>
          +{overflow} more
        </Badge>
      )}
    </div>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Finding row — single observation + copyable kubectl
// ────────────────────────────────────────────────────────────────────────────

function FindingRow({ finding }: { finding: Finding }) {
  const [copied, setCopied] = useState(false)
  const onCopy = useCallback(() => {
    if (!finding.command) return
    void navigator.clipboard.writeText(finding.command)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }, [finding.command])

  // When the detector parsed a domain-specific cause, lead with it — that's
  // the "why" the operator needs first. The raw detector message stays
  // visible below as secondary evidence. Without a parsed cause, message
  // is the primary line as before.
  const primary = finding.cause || finding.message
  const secondary = finding.cause && finding.message && finding.message !== finding.cause ? finding.message : ''
  return (
    <div className="flex gap-2.5 items-start">
      <span className="mt-1 shrink-0" aria-hidden><StatusDot tone={severityToTone(finding.severity)} size="sm" /></span>
      <div className="flex-1 min-w-0">
        <div className="text-xs text-theme-text-primary">{primary}</div>
        {secondary && (
          <div className="text-[11px] text-theme-text-tertiary mt-0.5">{secondary}</div>
        )}
        {finding.action && (
          <div className="text-[11px] text-theme-text-secondary mt-0.5">
            <span className="font-medium">Next step:</span> {finding.action}
          </div>
        )}
        {finding.remediation && !finding.action && (
          <div className="text-[11px] text-theme-text-tertiary mt-0.5">{finding.remediation}</div>
        )}
        {finding.command && (
          <div className="mt-1.5 flex items-center gap-2 bg-theme-base rounded px-2 py-1 font-mono text-[11px] text-theme-text-secondary group">
            <code className="flex-1 truncate" title={finding.command}>{finding.command}</code>
            <button
              type="button"
              onClick={onCopy}
              className="shrink-0 text-theme-text-tertiary hover:text-theme-text-primary"
              aria-label="Copy command"
              title={copied ? 'Copied!' : 'Copy'}
            >
              {copied ? <Check className="w-3 h-3 text-emerald-500" /> : <Copy className="w-3 h-3" />}
            </button>
          </div>
        )}
      </div>
      <span className="shrink-0 text-[10px] uppercase tracking-wide text-theme-text-tertiary mt-1" title={`code: ${finding.code}`}>
        {finding.code.length > 22 ? finding.code.slice(0, 21) + '…' : finding.code}
      </span>
    </div>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Small UI primitives — local, scoped to this panel
// ────────────────────────────────────────────────────────────────────────────

// SectionHeader is local for trace's flat top-level sections (Upstreams,
// Path). Matches the visual weight of `Section` titles elsewhere in the
// drawer (text-sm font-medium text-theme-text-secondary) without the
// collapsible affordance — the trace's sections always stay open.
function SectionHeader({ title, subtitle, action }: { title: string; subtitle?: string; action?: React.ReactNode }) {
  return (
    <div className="mb-2 flex items-center gap-2">
      <h3 className="text-sm font-medium text-theme-text-secondary">{title}</h3>
      {subtitle && <span className="text-xs text-theme-text-tertiary flex-1 truncate">{subtitle}</span>}
      {action}
    </div>
  )
}

// sampledFraction reads the "sampled N of M ready pods" skip row that the
// probe layer emits when the pod sample is truncated below the captured
// pool, returning (N, M) so the chip can label coverage honestly. The
// regex anchors on the full "ready pods" suffix so an unrelated skip
// such as "sampled 1 of 3 listeners" can't feed pod counts into a chip
// whose tooltip names pods.
function sampledFraction(probes?: ProbeResult[]): { sampled: number; total: number } | null {
  for (const p of probes ?? []) {
    if (!p.skipped || !p.reason) continue
    const m = /^sampled (\d+) of (\d+) ready pods/.exec(p.reason)
    if (m) return { sampled: Number(m[1]), total: Number(m[2]) }
  }
  return null
}

function SeverityChip({ severity, count, probes }: { severity: FindingSeverity | ''; count: number; probes?: ProbeResult[] }) {
  // Probe state is computed first so it can outrank an info-only
  // static finding: a hop with one info finding AND a failed probe
  // would otherwise render "1 info" while the probe row below carries
  // the live failure. Critical and warning static findings still
  // outrank probe state.
  const real = probes?.filter(p => !p.skipped) ?? []
  const probeFailed = real.filter(p => !p.ok || p.tone === 'unhealthy').length
  const probeDegraded = real.filter(p => p.tone === 'degraded').length
  const sample = sampledFraction(probes)
  if (count === 0) {
    if (real.length > 0) {
      if (probeFailed > 0) {
        return <Badge severity="warning" size="sm" title={`${probeFailed} of ${real.length} probes failed`}>{probeFailed === real.length ? 'probe failed' : `${probeFailed}/${real.length} probes failed`}</Badge>
      }
      if (probeDegraded > 0) {
        return <Badge severity="warning" size="sm" title={`${probeDegraded} of ${real.length} probes responded with a degraded HTTP status`}>probe degraded</Badge>
      }
      if (sample && sample.sampled < sample.total) {
        // "verified" on a 3-of-10 sample overclaims since the 7
        // unprobed pods could be in any state. The label mirrors the
        // underlying skip-row text and signals coverage, not failure.
        return <Badge severity="info" size="sm" title={`Probes passed on ${sample.sampled} of ${sample.total} pods. The other ${sample.total - sample.sampled} were not sampled.`}>sampled ({sample.sampled}/{sample.total})</Badge>
      }
      return <Badge severity="success" size="sm" title="Every probe that ran for this hop succeeded">verified</Badge>
    }
    return <Badge severity="info" size="sm" title="Static configuration is consistent; no live probe reached this hop">not probed</Badge>
  }
  if (severity === 'critical') return <Badge severity="error" size="sm">{count} critical</Badge>
  if (severity === 'warning') return <Badge severity="warning" size="sm">{count} warning{count > 1 ? 's' : ''}</Badge>
  // info-only findings: a live probe failure outranks them.
  if (probeFailed > 0) {
    return <Badge severity="warning" size="sm" title={`${probeFailed} of ${real.length} probes failed`}>{probeFailed === real.length ? 'probe failed' : `${probeFailed}/${real.length} probes failed`}</Badge>
  }
  if (probeDegraded > 0) {
    return <Badge severity="warning" size="sm" title={`${probeDegraded} of ${real.length} probes responded with a degraded HTTP status`}>probe degraded</Badge>
  }
  return <Badge severity="info" size="sm">{count} info</Badge>
}

function PanelMessage({ tone, message, onAction, actionLabel }: { tone: 'muted' | 'error'; message: string; onAction?: () => void; actionLabel?: string }) {
  return (
    <div className="p-4">
      <AlertBanner
        variant={tone === 'error' ? 'error' : 'info'}
        title={message}
      >
        {onAction && actionLabel && (
          <button
            type="button"
            onClick={onAction}
            className="mt-2 text-xs px-2 py-1 rounded border border-current/30 hover:bg-current/10"
          >
            {actionLabel}
          </button>
        )}
      </AlertBanner>
    </div>
  )
}

function ZeroConfigDisclaimer() {
  return (
    <p className="text-[10px] text-theme-text-tertiary border-t border-theme-border pt-2 mt-1">
      Built from declared config and live probes. NetworkPolicy enforcement isn't tested - a policy could still drop traffic that probes can't see.
    </p>
  )
}

// ────────────────────────────────────────────────────────────────────────────
// Pure helpers
// ────────────────────────────────────────────────────────────────────────────

// severityToTone bridges the trace's Finding vocabulary (critical / warning /
// info, plus empty when a hop is clean) to Radar's shared StatusTone, so the
// dot rendering uses the same primitive every other surface does.
function severityToTone(s: FindingSeverity | ''): StatusTone {
  switch (s) {
    case 'critical': return 'unhealthy'
    case 'warning': return 'degraded'
    case 'info': return 'unknown'
    default: return 'healthy'
  }
}

function prettyEdge(edge: string, _isSubject?: boolean): string {
  if (!edge) return ''
  if (edge.startsWith('entry:')) {
    return `Entry · ${edge.slice('entry:'.length)}`
  }
  return edge.replace('->', ' → ')
}

function hopKey(hop: Hop, i: number): string {
  return `${hop.resource.kind}:${hop.resource.namespace ?? ''}:${hop.resource.name ?? ''}:${i}`
}

// hopFallbackLabel covers the unnamed-collection case — the Pods hop is a
// fan-out over the selector, not a single named resource. Same for any
// future Routes collection hop on a Gateway entry.
function hopFallbackLabel(hop: Hop): string {
  if (hop.resource.kind === 'Pods') {
    const selected = hop.meta?.['selected']
    if (typeof selected === 'number') return selected === 1 ? '1 pod selected' : `${selected} pods selected`
    return 'selected pods'
  }
  if (hop.resource.kind === 'Routes') return 'attached routes'
  return '-'
}

function hopMetaSummary(hop: Hop): string | null {
  if (!hop.meta) return null
  const parts: string[] = []
  const selected = hop.meta['selected']
  const ready = hop.meta['ready']
  if (typeof selected === 'number' && typeof ready === 'number') {
    parts.push(`${ready}/${selected} ready`)
  } else if (typeof selected === 'number') {
    parts.push(`${selected} selected`)
  }
  if (hop.meta['endpointSource'] === 'unknown') {
    parts.push("couldn't read backing pods")
  }
  if (hop.meta['headless'] === true) {
    parts.push('headless (no virtual IP)')
  }
  if (hop.meta['selectorless'] === true) {
    parts.push('manually-managed endpoints')
  }
  return parts.length ? parts.join(' · ') : null
}
