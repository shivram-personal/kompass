import { useMemo, useState, type ReactNode } from 'react';
import { ChevronRight, CircleCheck, ExternalLink } from 'lucide-react';
import { ClusterName, EmptyState } from '../ui';
import {
  ISSUE_SEVERITY_BADGE_CLASS,
  ISSUE_SEVERITY_LABEL,
  ISSUE_SEVERITY_RAIL_CLASS,
  categoryLabel,
  groupBadgeClass,
  groupLabel,
} from './severity';
import {
  ISSUE_SEVERITY_RANK,
  memberRef,
  subjectRef,
  type Issue,
  type IssueAffected,
  type IssueResourceRef,
} from './types';

export interface IssuesViewProps {
  /** Grouped live issues — one row per subject+category. Typically flattened
   *  across the fleet by the host (the hub) or a single cluster (OSS). */
  issues: Issue[];
  /** True when at least one source returned issue data — distinguishes "clean"
   *  from "nothing connected / everything errored". */
  anyData: boolean;
  /** Resolve a deep-link href for a resource (host-specific routing). Omit to
   *  render non-link text. */
  resourceHref?: (ref: IssueResourceRef) => string;
  /** In-app resource navigation. When set, resource lines call this (no reload)
   *  instead of following resourceHref — OSS opens its own drawer this way.
   *  Takes precedence over resourceHref. */
  onResourceClick?: (ref: IssueResourceRef) => void;
  /** Display label for an issue's source cluster. Omit (or return falsy) to
   *  hide the cluster line — e.g. single-cluster OSS. */
  clusterLabel?: (issue: Issue) => string | undefined;
  /** Empty-state CTA shown when there's no data. */
  emptyAction?: ReactNode;
}

// The queue list. Filtering/faceting is the host page's job (FleetPageShell on
// the hub, a thin wrapper in OSS) — this renders the rows + the healthy /
// no-data terminal states only.
export function IssuesView({ issues, anyData, resourceHref, onResourceClick, clusterLabel, emptyAction }: IssuesViewProps) {
  // Single-open accordion: opening a row collapses the previous one, so the
  // queue stays scannable and you never lose your place to a wall of expansions.
  const [openId, setOpenId] = useState<string | null>(null);

  const sorted = useMemo(() => {
    // Worst-first: severity, then most-recent, then name. Mirrors the server's
    // ordering so the queue is stable across refetches.
    return [...issues].sort((a, b) => {
      const r = ISSUE_SEVERITY_RANK[b.severity] - ISSUE_SEVERITY_RANK[a.severity];
      if (r !== 0) return r;
      const la = a.last_seen ?? '';
      const lb = b.last_seen ?? '';
      if (la !== lb) return lb.localeCompare(la);
      return a.name.localeCompare(b.name);
    });
  }, [issues]);

  if (sorted.length === 0) {
    return anyData ? (
      <EmptyState
        tone="healthy"
        variant="card"
        icon={CircleCheck}
        headline="Nothing broken right now"
        body="No active issues across the selected scope."
      />
    ) : (
      <EmptyState headline="No issue data yet" body="Connect a cluster to populate the issue queue." action={emptyAction} />
    );
  }

  return (
    <ol className="flex flex-col gap-1.5">
      {sorted.map((issue) => {
        const rowKey = `${issue.cluster_id ?? ''}:${issue.id}:${issue.category}`;
        return (
          <IssueRow
            key={rowKey}
            issue={issue}
            clusterLabel={clusterLabel}
            open={openId === rowKey}
            onToggle={() => setOpenId((cur) => (cur === rowKey ? null : rowKey))}
            resourceHref={resourceHref}
            onResourceClick={onResourceClick}
          />
        );
      })}
    </ol>
  );
}

function IssueRow({
  issue,
  clusterLabel,
  open,
  onToggle,
  resourceHref,
  onResourceClick,
}: {
  issue: Issue;
  clusterLabel?: (issue: Issue) => string | undefined;
  open: boolean;
  onToggle: () => void;
  resourceHref?: (ref: IssueResourceRef) => string;
  onResourceClick?: (ref: IssueResourceRef) => void;
}) {
  const cluster = clusterLabel?.(issue);
  const affected = affectedSummary(issue.affected);

  return (
    <li className="overflow-hidden rounded-xl border border-theme-border bg-theme-surface shadow-theme-sm">
      {/* The whole header is the single toggle target — chevron is just the
          open/closed indicator, not a separate action. Deep-links live in the
          expanded body (a link nested in a button would be invalid). */}
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget) return;
          if (e.key === 'Enter' || e.key === ' ') {
            e.preventDefault();
            onToggle();
          }
        }}
        className={`group flex cursor-pointer items-center gap-3 border-l-2 py-3 pl-3 pr-4 transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-radar-accent)]/40 ${ISSUE_SEVERITY_RAIL_CLASS[issue.severity]}`}
      >
        <ChevronRight className={`h-4 w-4 shrink-0 text-theme-text-tertiary transition-transform duration-200 ${open ? 'rotate-90' : ''}`} />

        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-sm font-medium text-theme-text-primary">{categoryLabel(issue.category)}</span>
            <span className={`badge-sm shrink-0 text-[10px] ${groupBadgeClass(issue.category_group)}`}>{groupLabel(issue.category_group)}</span>
          </div>
          <div className="flex min-w-0 items-center gap-1.5 text-xs text-theme-text-tertiary">
            <span className="shrink-0 font-mono uppercase tracking-wide">{issue.kind}</span>
            <span className="min-w-0 truncate font-medium text-theme-text-secondary">
              {issue.namespace ? `${issue.namespace} / ` : ''}
              {issue.name}
            </span>
            {cluster ? (
              <>
                <span aria-hidden>·</span>
                <span className="max-w-[160px] shrink-0 truncate">
                  <ClusterName name={cluster} />
                </span>
              </>
            ) : null}
            {affected ? (
              <>
                <span aria-hidden>·</span>
                <span className="shrink-0 tabular-nums">{affected}</span>
              </>
            ) : null}
          </div>
        </div>

        <span className={`badge-sm shrink-0 text-[10px] font-semibold ${ISSUE_SEVERITY_BADGE_CLASS[issue.severity]}`}>
          {ISSUE_SEVERITY_LABEL[issue.severity]}
        </span>
      </div>

      <div className="grid transition-[grid-template-rows] duration-200 ease-out" style={{ gridTemplateRows: open ? '1fr' : '0fr' }}>
        {/* Kept mounted (not `open &&`) so the grid-rows transition animates the
            collapse too; inert when closed so SR + tab skip the clipped content. */}
        <div className="overflow-hidden" inert={!open || undefined}>
          <div className="border-t border-theme-border bg-theme-base/40 px-4 py-4 pl-11">
            <div className="flex flex-col gap-4">
              <Diagnosis issue={issue} />
              <div className="border-t border-theme-border/70 pt-3">
                <AffectedResources issue={issue} resourceHref={resourceHref} onResourceClick={onResourceClick} />
              </div>
            </div>
          </div>
        </div>
      </div>
    </li>
  );
}

// What's-wrong block: the specific detector reason + message, plus pod crash
// context when present (the "chronic vs acute" signal).
function Diagnosis({ issue }: { issue: Issue }) {
  const crash =
    issue.restart_count || issue.last_terminated_reason
      ? [issue.restart_count ? `${issue.restart_count} restarts` : null, issue.last_terminated_reason ? `last exit: ${issue.last_terminated_reason}` : null]
          .filter(Boolean)
          .join(' · ')
      : null;
  return (
    <section className="flex flex-col gap-1">
      <h4 className="text-[11px] font-semibold uppercase tracking-wide text-theme-text-tertiary">What's wrong</h4>
      <p className="text-sm leading-relaxed text-theme-text-primary">
        <span className="font-medium">{issue.reason}</span>
        {issue.message ? <span className="text-theme-text-secondary"> — {issue.message}</span> : null}
      </p>
      {crash ? <p className="text-xs text-theme-text-tertiary tabular-nums">{crash}</p> : null}
    </section>
  );
}

function AffectedResources({
  issue,
  resourceHref,
  onResourceClick,
}: {
  issue: Issue;
  resourceHref?: (ref: IssueResourceRef) => string;
  onResourceClick?: (ref: IssueResourceRef) => void;
}) {
  const members = issue.members ?? [];
  const total = issue.count ?? members.length + 1;
  return (
    <section className="flex flex-col gap-1.5">
      {/* The subject (the grouped thing — e.g. the Deployment) is always the
          first deep-link; members (the folded pods) follow. */}
      <ResourceLine label="Subject" refForLink={subjectRef(issue)} resourceHref={resourceHref} onResourceClick={onResourceClick} />
      {members.length > 0 && (
        <>
          <h4 className="mt-1.5 text-[11px] font-semibold uppercase tracking-wide text-theme-text-tertiary">
            Affected resources <span className="tabular-nums">({total})</span>
          </h4>
          <ul className="flex flex-col gap-px">
            {members.map((m, i) => (
              <ResourceLine
                key={`${m.group}/${m.kind}/${m.namespace}/${m.name}#${i}`}
                refForLink={memberRef(issue, m)}
                resourceHref={resourceHref}
                onResourceClick={onResourceClick}
              />
            ))}
          </ul>
          {issue.members_truncated && (
            <p className="mt-0.5 text-xs text-theme-text-tertiary">
              Showing {members.length} of {total} — open the subject to see the rest.
            </p>
          )}
        </>
      )}
    </section>
  );
}

function ResourceLine({
  label,
  refForLink,
  resourceHref,
  onResourceClick,
}: {
  label?: string;
  refForLink: IssueResourceRef;
  resourceHref?: (ref: IssueResourceRef) => string;
  onResourceClick?: (ref: IssueResourceRef) => void;
}) {
  const r = refForLink;
  const linkable = !!(onResourceClick || resourceHref);
  const body = (
    <>
      {label ? <span className="shrink-0 text-[10px] font-semibold uppercase tracking-wide text-theme-text-tertiary">{label}</span> : null}
      <span className="shrink-0 font-mono text-[11px] uppercase tracking-wide text-theme-text-tertiary">{r.kind}</span>
      <span className={`min-w-0 truncate font-medium ${linkable ? 'text-[var(--color-radar-accent)]' : 'text-theme-text-primary'}`}>
        {r.namespace ? `${r.namespace} / ` : ''}
        {r.name}
      </span>
      {linkable && <ExternalLink className="h-3 w-3 shrink-0 text-theme-text-tertiary opacity-0 transition-opacity group-hover/r:opacity-100" />}
    </>
  );
  const cls = 'group/r flex w-full items-center gap-2 rounded-md px-2 py-1 text-left text-sm transition-colors hover:bg-theme-hover/60';
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
  );
}

// "3 pods · 1 service" from the affected rollup; null when there's no fan-out
// (single-resource issue — the subject line already says everything).
function affectedSummary(a?: IssueAffected): string | null {
  if (!a) return null;
  const parts: string[] = [];
  const add = (n: number | undefined, singular: string, plural: string) => {
    if (n && n > 0) parts.push(`${n} ${n === 1 ? singular : plural}`);
  };
  add(a.pods, 'pod', 'pods');
  add(a.workloads, 'workload', 'workloads');
  add(a.services, 'service', 'services');
  add(a.pvcs, 'PVC', 'PVCs');
  add(a.nodes, 'node', 'nodes');
  return parts.length > 0 ? parts.join(' · ') : null;
}
