// Shared Issues identity contract + data shapes for the live-issues queue.
//
// k8s-ui owns these because the Issues queue presentation (IssuesView) is
// host-agnostic: Radar Hub feeds it fleet-resolved grouped issues, and OSS
// Radar feeds a single-cluster ("fleet of one") set. Hosts map their wire
// payloads onto these types; the component renders against them.
//
// Mirrors the grouped Issue model radar emits (internal/issues.GroupIssues →
// /api/issues, and the hub's /api/fleet/issues). The identity primitives
// (IssueResourceRef, resourceKey) intentionally match the Checks queue's contract
// (components/checks/types.ts) and radar/pkg/audit.ResourceKey — so Issues and
// Checks share keys + deep-links rather than forking a second convention.

/** Operational severity for live issues — distinct from the Checks 4-tier
 *  posture ladder on purpose (operational urgency vs compliance risk are
 *  separate axes). Matches radar's issues.Severity. */
export type IssueSeverity = 'critical' | 'warning';

/** Ordered worst→least. */
export const ISSUE_SEVERITIES: IssueSeverity[] = ['critical', 'warning'];

export const ISSUE_SEVERITY_RANK: Record<IssueSeverity, number> = {
  critical: 2,
  warning: 1,
};

export function isIssueSeverity(s: string): s is IssueSeverity {
  return s === 'critical' || s === 'warning';
}

/**
 * Canonical resource identity. `group` is '' for the core API group;
 * `namespace` is '' for cluster-scoped resources. `cluster_id` scopes the ref
 * to its source cluster (the hub injects it; single-cluster OSS leaves it
 * undefined). Same shape as Checks' CheckResourceRef so deep-link plumbing is
 * shared.
 */
export interface IssueResourceRef {
  cluster_id?: string;
  group: string;
  kind: string;
  namespace: string;
  name: string;
}

/**
 * resourceKey mirrors Go `audit.ResourceKey(group, kind, namespace, name)`:
 * `group|Kind|namespace|name`. Group first because group and namespace can each
 * independently be empty; `|` is delimiter-safe (K8s API groups follow
 * DNS-subdomain rules and can't contain it).
 */
export function resourceKey(group: string, kind: string, namespace: string, name: string): string {
  return `${group}|${kind}|${namespace}|${name}`;
}

export function resourceRefKey(ref: IssueResourceRef): string {
  return resourceKey(ref.group, ref.kind, ref.namespace, ref.name);
}

/** Rollup of the underlying resources folded into a grouped issue, by kind
 *  bucket. Empty for single-resource issues (no fan-out). Mirrors the Go
 *  issues.Affected struct. */
export interface IssueAffected {
  pods?: number;
  workloads?: number;
  services?: number;
  pvcs?: number;
  nodes?: number;
}

/**
 * A grouped live issue — one row of the triage queue. Subject (kind/group/
 * namespace/name) is the topmost owner when the rows folded under a workload,
 * else the resource itself; `members` are the folded underlying resources
 * (the fan-out), bounded inline with `members_truncated`. Mirrors the Go
 * issues.Issue after GroupIssues.
 */
export interface Issue {
  id: string;
  severity: IssueSeverity;
  /** Detection channel (problem|missing_ref|scheduling|condition) — an output
   *  label, not the triage axis. */
  source: string;
  /** Symptom taxonomy (image_pull_failed, crashloop, …) — the triage axis. */
  category: string;
  /** Coarse rollup of category (startup|runtime|networking|…). Server-emitted
   *  so the UI never needs its own category→group map. */
  category_group: string;
  /** Subject kind bucket (workload|service|pvc|ingress|node|unknown). */
  grouping_scope: string;

  // Subject identity (the grouped thing).
  cluster_id?: string;
  cluster_name?: string;
  group: string;
  kind: string;
  namespace: string;
  name: string;

  reason: string;
  message?: string;
  first_seen?: string;
  last_seen?: string;
  /** Number of folded member rows (≥1). */
  count?: number;

  affected?: IssueAffected;
  members?: IssueResourceRef[];
  members_truncated?: boolean;

  // Pod crash context carried from the representative member.
  restart_count?: number;
  last_terminated_reason?: string;
}

/** subjectRef builds a deep-linkable ref for an issue's subject — the row's
 *  cluster_id threaded onto its group/kind/namespace/name. */
export function subjectRef(issue: Issue): IssueResourceRef {
  return {
    cluster_id: issue.cluster_id,
    group: issue.group,
    kind: issue.kind,
    namespace: issue.namespace,
    name: issue.name,
  };
}

/** memberRef threads the issue's cluster_id onto a member ref (members carry
 *  no cluster_id of their own — every member shares the issue's cluster). */
export function memberRef(issue: Issue, member: IssueResourceRef): IssueResourceRef {
  return { ...member, cluster_id: issue.cluster_id };
}
