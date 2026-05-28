import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { ChevronRight, ExternalLink, EyeOff, MoreHorizontal, Search, ShieldCheck, Wrench, X } from 'lucide-react'
import { ClusterName, EmptyState, FilterPill } from '../ui'
import type { CheckMeta } from '../audit'
import { CHECK_SEVERITIES, CHECK_SEVERITY_RANK, type Check, type CheckSeverity, type EffectiveCheckFinding, type CheckResourceRef } from './types'
import {
  SEVERITY_BADGE_CLASS,
  SEVERITY_FILL_CLASS,
  SEVERITY_LABEL,
  SEVERITY_RAIL_CLASS,
  SEVERITY_TEXT_CLASS,
  categoryBadgeClass,
} from './severity'

const CATEGORIES: readonly string[] = ['Security', 'Reliability', 'Efficiency']

// Affected-resources shown inline before "View all" — a check can fail on
// thousands of resources; the queue card stays scannable and only the rare
// big-list case pays the cost of a full expand.
const RESOURCE_CAP = 10

export interface ChecksViewProps {
  /** Failing checks, typically flattened across the fleet by the host. */
  checks: Check[]
  /** Check catalog (checkID → definition) for how-to-fix / description / the
   *  compliance-framework filter. */
  catalog: Record<string, CheckMeta>
  /** True when at least one source returned audit data — distinguishes "clean"
   *  from "nothing audited / everything errored". */
  anyData: boolean
  /** Resolve a deep-link href for a resource (host-specific routing). Omit to
   *  render non-link text. */
  resourceHref?: (ref: CheckResourceRef) => string
  /** In-app resource navigation. When set, resource lines call this (client-
   *  side, no reload) instead of following resourceHref — OSS opens its own
   *  drawer this way. Takes precedence over resourceHref when both are given. */
  onResourceClick?: (ref: CheckResourceRef) => void
  /** Display label for a check's source cluster. Omit (or return falsy) to hide
   *  the cluster line — e.g. single-cluster OSS. */
  clusterLabel?: (check: Check) => string | undefined
  /** Empty-state CTA shown when there's no data (host-specific: connect a
   *  cluster vs run an audit). */
  emptyAction?: ReactNode
  /** Optional per-row "hide" actions. OSS wires these to local ~/.radar
   *  settings; the Hub omits them (hiding is the governed Policy tab there).
   *  When omitted, no row menu renders. */
  onHideCheck?: (checkID: string, title: string) => void
  onHideCategory?: (category: string) => void
}

export function ChecksView({ checks, catalog, anyData, resourceHref, onResourceClick, clusterLabel, emptyAction, onHideCheck, onHideCategory }: ChecksViewProps) {
  const [severityFilter, setSeverityFilter] = useState<Set<CheckSeverity>>(new Set())
  const [categoryFilter, setCategoryFilter] = useState<Set<string>>(new Set())
  const [frameworkFilter, setFrameworkFilter] = useState<Set<string>>(new Set())
  const [search, setSearch] = useState('')
  // Single-open accordion: opening a check collapses the previous one, so the
  // queue stays scannable and you never lose your place to a wall of expansions.
  const [openId, setOpenId] = useState<string | null>(null)

  const { totals, totalFindings, clusterCount } = useMemo(() => {
    const totals: Record<CheckSeverity, number> = { critical: 0, high: 0, medium: 0, low: 0 }
    const clusters = new Set<string>()
    let totalFindings = 0
    for (const c of checks) {
      totals[c.effectiveSeverity] += 1
      totalFindings += c.affectedFindings
      clusters.add(c.subject.cluster_id)
    }
    return { totals, totalFindings, clusterCount: clusters.size }
  }, [checks])

  // Compliance frameworks present in the catalog (CIS, NSA/CISA, …). Empty when
  // no loaded check carries a framework tag, in which case the filter hides.
  const frameworks = useMemo(() => {
    const set = new Set<string>()
    for (const m of Object.values(catalog)) m.frameworks?.forEach((f) => set.add(f))
    return Array.from(set).sort()
  }, [catalog])

  const searchLower = search.toLowerCase()
  const filtered = useMemo(() => {
    const out = checks.filter((c) => {
      if (severityFilter.size > 0 && !severityFilter.has(c.effectiveSeverity)) return false
      if (categoryFilter.size > 0 && !categoryFilter.has(c.category)) return false
      if (frameworkFilter.size > 0) {
        const fws = catalog[c.checkID]?.frameworks
        if (!fws || !fws.some((f) => frameworkFilter.has(f))) return false
      }
      if (searchLower) {
        const hay = `${c.title} ${c.checkID} ${c.message} ${c.subject.namespace} ${c.subject.name}`.toLowerCase()
        if (!hay.includes(searchLower)) return false
      }
      return true
    })
    // Worst-first across the whole queue (severity, then blast radius, then title).
    return out.sort((a, b) => {
      const r = CHECK_SEVERITY_RANK[b.effectiveSeverity] - CHECK_SEVERITY_RANK[a.effectiveSeverity]
      if (r !== 0) return r
      if (b.affectedResources !== a.affectedResources) return b.affectedResources - a.affectedResources
      return a.title.localeCompare(b.title)
    })
  }, [checks, catalog, severityFilter, categoryFilter, frameworkFilter, searchLower])

  const toggle = <T,>(setter: React.Dispatch<React.SetStateAction<Set<T>>>, v: T) =>
    setter((prev) => {
      const next = new Set(prev)
      if (next.has(v)) next.delete(v)
      else next.add(v)
      return next
    })

  const hasFilters = severityFilter.size > 0 || categoryFilter.size > 0 || frameworkFilter.size > 0 || search !== ''
  const clearAll = () => {
    setSeverityFilter(new Set())
    setCategoryFilter(new Set())
    setFrameworkFilter(new Set())
    setSearch('')
  }

  return (
    <div className="flex flex-col gap-4">
      {/* Triage header: distribution bar + filter chips + search. */}
      <div className="flex flex-col gap-3.5 rounded-2xl border border-theme-border bg-theme-surface p-4 shadow-theme-sm">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div className="flex items-baseline gap-2">
            <span className="text-2xl font-semibold tabular-nums text-theme-text-primary">{checks.length}</span>
            <span className="text-sm text-theme-text-secondary">
              {checks.length === 1 ? 'check' : 'checks'}
              {totalFindings > checks.length && <span className="text-theme-text-tertiary"> · {totalFindings} findings</span>}
              {clusterCount > 1 && <span className="text-theme-text-tertiary"> · {clusterCount} clusters</span>}
            </span>
          </div>
          <div className="relative">
            <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-theme-text-tertiary" />
            <input
              type="text"
              placeholder="Search checks…"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              className="w-64 rounded-lg border border-theme-border-light bg-theme-base py-1.5 pl-9 pr-8 text-sm text-theme-text-primary placeholder-theme-text-disabled focus:outline-none focus:ring-2 focus:ring-[var(--color-radar-accent)]"
            />
            {search && (
              <button
                type="button"
                onClick={() => setSearch('')}
                aria-label="Clear search"
                className="absolute right-2 top-1/2 -translate-y-1/2 rounded p-0.5 text-theme-text-tertiary hover:text-theme-text-primary"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        </div>

        <SeverityBar totals={totals} />

        <div className="flex flex-wrap items-center gap-1.5">
          {CHECK_SEVERITIES.map((s) => (
            <SeverityChip key={s} severity={s} count={totals[s]} active={severityFilter.has(s)} onClick={() => toggle(setSeverityFilter, s)} />
          ))}
          <span className="mx-1.5 h-5 w-px bg-theme-border" />
          {CATEGORIES.map((c) => (
            <FilterPill key={c} label={c} active={categoryFilter.has(c)} onClick={() => toggle(setCategoryFilter, c)} />
          ))}
          {frameworks.length > 0 && (
            <>
              <span className="mx-1.5 h-5 w-px bg-theme-border" />
              {frameworks.map((fw) => (
                <FilterPill key={fw} label={fw} active={frameworkFilter.has(fw)} onClick={() => toggle(setFrameworkFilter, fw)} />
              ))}
            </>
          )}
        </div>
      </div>

      {filtered.length === 0 ? (
        hasFilters ? (
          <EmptyState
            tone="filtered"
            variant="card"
            headline="No checks match the current filters"
            body="Clear a filter to see more of the queue."
            action={
              <button
                type="button"
                onClick={clearAll}
                className="badge badge-sm border border-theme-border bg-theme-elevated text-theme-text-primary transition-colors hover:bg-theme-hover"
              >
                Clear all filters
              </button>
            }
          />
        ) : anyData ? (
          <EmptyState
            tone="healthy"
            variant="card"
            icon={ShieldCheck}
            headline="Nothing to remediate"
            body="Every audited resource passed its checks."
          />
        ) : (
          <EmptyState headline="No check data yet" body="Run an audit to populate the remediation queue." action={emptyAction} />
        )
      ) : (
        <ol className="flex flex-col gap-1.5">
          {filtered.map((check) => (
            <CheckRow
              key={check.id}
              check={check}
              meta={catalog[check.checkID]}
              clusterLabel={clusterLabel}
              open={openId === check.id}
              onToggle={() => setOpenId((cur) => (cur === check.id ? null : check.id))}
              resourceHref={resourceHref}
              onResourceClick={onResourceClick}
              onHideCheck={onHideCheck}
              onHideCategory={onHideCategory}
            />
          ))}
        </ol>
      )}
    </div>
  )
}

function SeverityBar({ totals }: { totals: Record<CheckSeverity, number> }) {
  const sum = CHECK_SEVERITIES.reduce((n, s) => n + totals[s], 0)
  return (
    <div className="flex h-1.5 overflow-hidden rounded-full bg-theme-elevated" role="img" aria-label="Severity distribution">
      {sum === 0
        ? null
        : CHECK_SEVERITIES.map((s) =>
            totals[s] > 0 ? (
              <div key={s} className={`${SEVERITY_FILL_CLASS[s]} transition-[width] duration-500 ease-out`} style={{ width: `${(totals[s] / sum) * 100}%` }} />
            ) : null,
          )}
    </div>
  )
}

function SeverityChip({ severity, count, active, onClick }: { severity: CheckSeverity; count: number; active: boolean; onClick: () => void }) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={[
        'group inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs transition-colors',
        active ? 'border-theme-border bg-theme-elevated text-theme-text-primary' : 'border-transparent text-theme-text-secondary hover:bg-theme-hover/60',
      ].join(' ')}
    >
      <span className={`h-2 w-2 rounded-full ${SEVERITY_FILL_CLASS[severity]} ${count === 0 ? 'opacity-30' : ''}`} />
      <span className={`font-semibold tabular-nums ${count > 0 ? SEVERITY_TEXT_CLASS[severity] : 'text-theme-text-tertiary'}`}>{count}</span>
      <span>{SEVERITY_LABEL[severity]}</span>
    </button>
  )
}

function CheckRow({
  check,
  meta,
  clusterLabel,
  open,
  onToggle,
  resourceHref,
  onResourceClick,
  onHideCheck,
  onHideCategory,
}: {
  check: Check
  meta?: CheckMeta
  clusterLabel?: (check: Check) => string | undefined
  open: boolean
  onToggle: () => void
  resourceHref?: (ref: CheckResourceRef) => string
  onResourceClick?: (ref: CheckResourceRef) => void
  onHideCheck?: (checkID: string, title: string) => void
  onHideCategory?: (category: string) => void
}) {
  const cluster = clusterLabel?.(check)

  const menuItems: { label: string; onClick: () => void }[] = []
  if (onHideCheck) menuItems.push({ label: `Hide "${check.title}" check`, onClick: () => onHideCheck(check.checkID, check.title) })
  if (onHideCategory) menuItems.push({ label: `Hide all ${check.category} checks`, onClick: () => onHideCategory(check.category) })

  return (
    <li className="overflow-hidden rounded-xl border border-theme-border bg-theme-surface shadow-theme-sm">
      {/* The whole header is the single toggle target — chevron is just the
          open/closed indicator, not a separate action. */}
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault()
            onToggle()
          }
        }}
        className={`group flex cursor-pointer items-center gap-3 border-l-2 py-3 pl-3 pr-4 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-radar-accent)]/40 ${SEVERITY_RAIL_CLASS[check.effectiveSeverity]}`}
      >
        <ChevronRight className={`h-4 w-4 shrink-0 text-theme-text-tertiary transition-transform duration-200 ${open ? 'rotate-90' : ''}`} />

        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-theme-text-primary">{check.title}</span>
            <span className={`badge-sm shrink-0 text-[10px] ${categoryBadgeClass(check.category)}`}>{check.category}</span>
          </div>
          <div className="flex min-w-0 items-center gap-1.5 text-xs text-theme-text-tertiary">
            {cluster ? (
              <>
                <span className="max-w-[180px] truncate">
                  <ClusterName name={cluster} />
                </span>
                <span aria-hidden>·</span>
              </>
            ) : null}
            <span className="shrink-0 font-medium text-theme-text-secondary tabular-nums">
              {check.affectedResources} {check.affectedResources === 1 ? 'resource' : 'resources'}
            </span>
          </div>
        </div>

        <span className={`badge-sm shrink-0 text-[10px] font-semibold ${SEVERITY_BADGE_CLASS[check.effectiveSeverity]}`}>{SEVERITY_LABEL[check.effectiveSeverity]}</span>
        {menuItems.length > 0 && <RowMenu items={menuItems} />}
      </div>

      <div className="grid transition-[grid-template-rows] duration-200 ease-out" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
        {/* Kept mounted (not `open &&`) so the grid-rows transition animates the
            collapse too, not just the expand; inert when closed so SR + tab skip
            the clipped content. */}
        <div className="overflow-hidden" inert={!open || undefined}>
          <div className="border-t border-theme-border bg-theme-base/40 px-4 py-4 pl-11">
            {/* Fix + context side by side when there's room — both are short, so
                they balance; the resource list below always spans full width (a
                two-column split was unbalanced + truncated the messages). */}
            <div className="flex flex-col gap-4">
              <div className="flex flex-col gap-4 md:flex-row md:gap-8">
                {meta?.remediation && (
                  <section className="md:flex-1">
                    <h4 className="mb-1 flex items-center gap-1.5 text-[11px] font-semibold uppercase tracking-wide text-[var(--color-radar-accent)]">
                      <Wrench className="h-3.5 w-3.5" /> How to fix
                    </h4>
                    <p className="text-sm leading-relaxed text-theme-text-primary">{meta.remediation}</p>
                  </section>
                )}
                {meta?.description && (
                  <section className="md:flex-1">
                    <h4 className="mb-1 text-[11px] font-semibold uppercase tracking-wide text-theme-text-tertiary">What this checks</h4>
                    <p className="text-sm leading-relaxed text-theme-text-secondary">{meta.description}</p>
                  </section>
                )}
              </div>

              {meta?.references && meta.references.length > 0 && (
                <div className="flex flex-wrap items-center gap-x-4 gap-y-1.5">
                  {meta.references.map((r) => (
                    <a
                      key={r.url}
                      href={r.url}
                      target="_blank"
                      rel="noreferrer"
                      className="inline-flex items-center gap-1 text-xs font-medium text-[var(--color-radar-accent)] hover:underline"
                    >
                      {r.label}
                      <ExternalLink className="h-3 w-3" />
                    </a>
                  ))}
                </div>
              )}

              <PriorityLine check={check} />

              <div className="border-t border-theme-border/70 pt-3">
                <AffectedResources check={check} resourceHref={resourceHref} onResourceClick={onResourceClick} />
              </div>
            </div>
          </div>
        </div>
      </div>
    </li>
  )
}

// Compact, explainable priority — the score plus its weighted contributors as
// chips (the animated bars were a drawer luxury; inline this stays terse).
// The detector→effective severity line shows only when org policy overrode it
// — for the detector default it's noise.
function PriorityLine({ check }: { check: Check }) {
  const rep = check.representativeFinding
  const overridden = rep.state.source === 'org_config'
  const weighted = check.priorityFactors.filter((f) => f.weight > 0)
  const score = weighted.reduce((n, f) => n + f.weight, 0)
  return (
    <section className="flex flex-col gap-1.5">
      <h4 className="text-[11px] font-semibold uppercase tracking-wide text-theme-text-tertiary">
        Why this priority <span className="ml-1 font-normal normal-case text-theme-text-secondary">score {score}</span>
      </h4>
      <div className="flex flex-wrap items-center gap-1.5">
        {weighted.map((f) => (
          <span
            key={f.key}
            className="inline-flex items-center gap-1 rounded-md bg-theme-elevated px-2 py-0.5 text-[11px] text-theme-text-secondary ring-1 ring-theme-border"
            title={f.detail || undefined}
          >
            <span className="capitalize">{f.label}</span>
            <span className="font-mono text-theme-text-tertiary">+{f.weight}</span>
          </span>
        ))}
      </div>
      {overridden && (
        <p className="text-[11px] text-theme-text-tertiary">
          Severity <span className="capitalize">{rep.originalSeverity}</span> → {SEVERITY_LABEL[check.effectiveSeverity]} · org policy
          {rep.state.reason ? ` (${rep.state.reason})` : ''}
        </p>
      )}
    </section>
  )
}

function AffectedResources({
  check,
  resourceHref,
  onResourceClick,
}: {
  check: Check
  resourceHref?: (ref: CheckResourceRef) => string
  onResourceClick?: (ref: CheckResourceRef) => void
}) {
  const [showAll, setShowAll] = useState(false)
  // The per-finding message only earns a place when it adds something the line
  // doesn't already show. Normalize each message by removing its own resource
  // name, then compare: if they're all the same, the message either repeats the
  // check verbatim (e.g. "token is auto-mounted") or varies only by the object
  // name (already on the line) — drop it. If they still differ, the variation is
  // real new info (e.g. a container name) — keep it.
  const showMessage = useMemo(() => {
    if (check.findings.length === 0) return false
    const norm = (f: EffectiveCheckFinding) => {
      const n = f.resource.name
      return n ? (f.message ?? '').split(n).join('') : f.message ?? ''
    }
    const first = norm(check.findings[0])
    return check.findings.some((f) => norm(f) !== first)
  }, [check.findings])
  const list = showAll ? check.findings : check.findings.slice(0, RESOURCE_CAP)
  const hidden = check.findings.length - list.length
  return (
    <section className="flex flex-col gap-1.5">
      <h4 className="text-[11px] font-semibold uppercase tracking-wide text-theme-text-tertiary">
        Affected resources <span className="tabular-nums">({check.affectedResources})</span>
      </h4>
      <ul className="flex flex-col gap-px">
        {list.map((f, i) => (
          <FindingLine
            key={`${f.resource.group}/${f.resource.kind}/${f.resource.namespace}/${f.resource.name}#${i}`}
            finding={f}
            showMessage={showMessage}
            resourceHref={resourceHref}
            onResourceClick={onResourceClick}
          />
        ))}
      </ul>
      {hidden > 0 && (
        <button
          type="button"
          onClick={() => setShowAll(true)}
          className="mt-0.5 inline-flex w-fit items-center gap-1 rounded px-2 py-1 text-xs font-medium text-[var(--color-radar-accent)] hover:underline"
        >
          View all {check.findings.length} →
        </button>
      )}
    </section>
  )
}

function FindingLine({
  finding,
  showMessage,
  resourceHref,
  onResourceClick,
}: {
  finding: EffectiveCheckFinding
  showMessage?: boolean
  resourceHref?: (ref: CheckResourceRef) => string
  onResourceClick?: (ref: CheckResourceRef) => void
}) {
  const r = finding.resource
  const linkable = !!(onResourceClick || resourceHref)
  const body = (
    <>
      <span className="shrink-0 font-mono text-[11px] uppercase tracking-wide text-theme-text-tertiary">{r.kind}</span>
      <span className={`shrink-0 font-medium ${linkable ? 'text-[var(--color-radar-accent)]' : 'text-theme-text-primary'}`}>
        {r.namespace ? `${r.namespace} / ` : ''}
        {r.name}
      </span>
      {linkable && <ExternalLink className="h-3 w-3 shrink-0 text-theme-text-tertiary opacity-0 transition-opacity group-hover/f:opacity-100" />}
      {showMessage && <span className="ml-1 truncate text-xs text-theme-text-tertiary">{finding.message}</span>}
    </>
  )
  const cls = 'group/f flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-sm transition-colors hover:bg-theme-hover/60'
  return (
    <li>
      {onResourceClick ? (
        <button type="button" onClick={() => onResourceClick(r)} className={cls}>
          {body}
        </button>
      ) : resourceHref ? (
        <a href={resourceHref(r)} className={cls}>
          {body}
        </a>
      ) : (
        <span className="flex items-center gap-2 rounded-md px-2 py-1 text-sm">{body}</span>
      )}
    </li>
  )
}

// Quiet per-row overflow menu for the OSS local-tuning actions (hide check /
// category). The dropdown is portaled to document.body: the row is
// overflow-hidden (rounded corners + the expand animation), which would
// otherwise clip the menu. Position is captured at open time and anchored to
// the trigger; any scroll/resize closes it rather than letting it drift.
function RowMenu({ items }: { items: { label: string; onClick: () => void }[] }) {
  const [menu, setMenu] = useState<{ top: number; right: number } | null>(null)
  const btnRef = useRef<HTMLButtonElement>(null)

  useEffect(() => {
    if (!menu) return
    const close = () => setMenu(null)
    const onDown = (e: MouseEvent) => {
      if (btnRef.current?.contains(e.target as Node)) return
      close()
    }
    document.addEventListener('mousedown', onDown)
    window.addEventListener('scroll', close, true)
    window.addEventListener('resize', close)
    return () => {
      document.removeEventListener('mousedown', onDown)
      window.removeEventListener('scroll', close, true)
      window.removeEventListener('resize', close)
    }
  }, [menu])

  const toggle = (e: React.MouseEvent) => {
    e.stopPropagation()
    if (menu) {
      setMenu(null)
      return
    }
    const r = btnRef.current?.getBoundingClientRect()
    if (r) setMenu({ top: r.bottom + 4, right: window.innerWidth - r.right })
  }

  return (
    <>
      <button
        ref={btnRef}
        type="button"
        aria-label="More actions"
        aria-haspopup="menu"
        aria-expanded={menu != null}
        onClick={toggle}
        onKeyDown={(e) => e.stopPropagation()}
        className="shrink-0 rounded p-1 text-theme-text-tertiary opacity-0 transition-opacity hover:bg-theme-hover hover:text-theme-text-secondary group-hover:opacity-100"
      >
        <MoreHorizontal className="h-4 w-4" />
      </button>
      {menu &&
        createPortal(
          <div
            role="menu"
            className="fixed z-[60] min-w-48 rounded-lg border border-theme-border bg-theme-surface py-1 shadow-xl"
            style={{ top: menu.top, right: menu.right }}
            onClick={(e) => e.stopPropagation()}
          >
            {items.map((it, i) => (
              <button
                key={i}
                type="button"
                role="menuitem"
                onClick={(e) => {
                  e.stopPropagation()
                  it.onClick()
                  setMenu(null)
                }}
                className="flex w-full items-center gap-2 px-3 py-1.5 text-left text-xs text-theme-text-secondary transition-colors hover:bg-theme-hover hover:text-theme-text-primary"
              >
                <EyeOff className="h-3.5 w-3.5 shrink-0" />
                {it.label}
              </button>
            ))}
          </div>,
          document.body,
        )}
    </>
  )
}
