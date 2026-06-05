// Shared model for the Applications surface — host-agnostic. The OSS single-
// cluster view and (eventually) the Cloud fleet view both build on these types
// and helpers. No React, no fetching.
//
// The wire shape mirrors radar OSS's GET /api/applications response
// (internal/server/applications.go). Field names match the Go json tags.

export type AppWorkloadClass = 'service' | 'worker' | 'job' | 'unknown'
export type AppHealth = 'healthy' | 'degraded' | 'unhealthy' | 'unknown'

export interface AppWorkload {
  kind: string
  namespace: string
  name: string
  workload_class?: AppWorkloadClass
  image?: string
  version?: string
  health: string
  ready: number
  desired: number
  restarts: number
  reason?: string
}

export interface AppRelationships {
  services?: string[]
  ingresses?: string[]
  routes?: string[]
  configs?: number
  scalers?: number
  pdbs?: number
}

export interface AppEvent {
  type: string
  reason: string
  message?: string
  count: number
  object: string
  lastSeen?: string
}

export interface AppRow {
  key: string
  name: string
  namespace?: string
  /** pkg/subject overlay tier (0 = raw, no signal); 1-9. */
  tier?: number
  /** high | medium | low */
  confidence?: string
  /** app | addon | mixed — classification hint, never identity. */
  category?: string
  addonReason?: string
  workload_class?: AppWorkloadClass
  /** worst-of across workloads: healthy | degraded | unhealthy | unknown. */
  health: string
  /** distinct image tags. */
  versions?: string[]
  workloads: AppWorkload[]
  events?: AppEvent[]
  relationships?: AppRelationships
}

// -----------------------------------------------------------------------------
// Environment ladder. Higher rank = more-promoted; prod is the top. Unranked
// envs sort trailing.
// -----------------------------------------------------------------------------

export const ENV_RANK: Record<string, number> = { dev: 0, staging: 1, prod: 2 }

/** Rank for an environment label, or null when it isn't on the ladder. */
export function envRank(env: string | undefined): number | null {
  if (!env) return null
  const r = ENV_RANK[env.toLowerCase()]
  return r === undefined ? null : r
}

// Namespace-name token → canonical env. Matched on the whole name first, then
// on `-`/`_`-delimited segments (so `myapp-prod`, `staging-svc` resolve), which
// avoids substring false-hits like `prod` inside `product`.
const ENV_NS_TOKENS: Record<string, string> = {
  dev: 'dev', devel: 'dev', develop: 'dev', development: 'dev',
  stg: 'staging', stage: 'staging', staging: 'staging',
  prd: 'prod', prod: 'prod', production: 'prod', live: 'prod',
  qa: 'qa', uat: 'uat', sandbox: 'sandbox', sbx: 'sandbox', preview: 'preview', demo: 'demo',
}

/** Infer a canonical environment from a namespace name, or null when nothing
 *  recognizable is present (conservative — `kube-system`, `billing` → null). */
export function envFromNamespace(namespace: string | undefined): string | null {
  if (!namespace) return null
  const lower = namespace.toLowerCase()
  if (ENV_NS_TOKENS[lower]) return ENV_NS_TOKENS[lower]
  for (const seg of lower.split(/[-_]/).filter(Boolean)) {
    if (ENV_NS_TOKENS[seg]) return ENV_NS_TOKENS[seg]
  }
  return null
}

export interface ResolvedAppEnv {
  /** Canonical env token (lowercased), or '' when unlabeled. */
  env: string
  /** True when derived from the namespace heuristic (not an explicit label). */
  inferred: boolean
}

/** Resolve an environment via the precedence cascade: an explicit env wins;
 *  otherwise the namespace heuristic (tagged inferred); otherwise unlabeled. */
export function resolveEnv(explicitEnv: string | undefined, namespace: string | undefined): ResolvedAppEnv {
  const explicit = (explicitEnv || '').trim()
  if (explicit) return { env: explicit.toLowerCase(), inferred: false }
  const inferred = envFromNamespace(namespace)
  if (inferred) return { env: inferred, inferred: true }
  return { env: '', inferred: false }
}

// -----------------------------------------------------------------------------
// System namespaces — cluster plumbing hidden by default on the app surface.
// -----------------------------------------------------------------------------

const SYSTEM_NAMESPACES = new Set(['kube-system', 'kube-public', 'kube-node-lease', 'kube-flannel', 'local-path-storage'])

/** True for cluster-plumbing namespaces (kube-*, *-system operators) the app
 *  list hides by default. The `-system` suffix catches operator namespaces like
 *  `cert-manager`'s `gatekeeper-system`, `kourier-system`, etc. */
export function isSystemNamespace(ns: string | undefined): boolean {
  if (!ns) return false
  const lower = ns.toLowerCase()
  return SYSTEM_NAMESPACES.has(lower) || lower.endsWith('-system')
}

// -----------------------------------------------------------------------------
// Category — the app/add-on/mixed classification hint (never identity).
// -----------------------------------------------------------------------------

export type AppCategory = 'app' | 'addon' | 'mixed'

export const CATEGORY_ORDER: AppCategory[] = ['app', 'addon', 'mixed']

export const CATEGORY_META: Record<AppCategory, { label: string }> = {
  app: { label: 'App' },
  addon: { label: 'Add-on' },
  mixed: { label: 'Mixed' },
}

/** The category bucket for a row — apps with no category default to 'app'. */
export function categoryOf(category: string | undefined): AppCategory {
  return category === 'addon' || category === 'mixed' ? category : 'app'
}

// -----------------------------------------------------------------------------
// Version comparison. Conservative semver-ish: compares only clean numeric
// versions (optional leading `v`). Anything non-numeric — a range, a branch, a
// git SHA — returns null so callers render "no lag" rather than guessing.
// -----------------------------------------------------------------------------

function parseVersion(v: string | undefined): number[] | null {
  if (!v) return null
  const t = v.trim().replace(/^v/i, '')
  if (!/^\d+(\.\d+)*$/.test(t)) return null
  return t.split('.').map((n) => parseInt(n, 10))
}

/** -1 if a<b, 1 if a>b, 0 if equal, null if either isn't a comparable version. */
export function compareVersions(a: string | undefined, b: string | undefined): number | null {
  const pa = parseVersion(a)
  const pb = parseVersion(b)
  if (!pa || !pb) return null
  const len = Math.max(pa.length, pb.length)
  for (let i = 0; i < len; i++) {
    const x = pa[i] ?? 0
    const y = pb[i] ?? 0
    if (x !== y) return x < y ? -1 : 1
  }
  return 0
}

// -----------------------------------------------------------------------------
// Provenance — overlay tier → human label, mirroring pkg/subject's Tier
// constants (1-9).
// -----------------------------------------------------------------------------

// Short badge label — the tool/source that grouped the app, at a glance.
const TIER_PROVENANCE: Record<number, string> = {
  1: 'Flux',
  2: 'Flux',
  3: 'Argo CD',
  4: 'Argo CD',
  5: 'Helm',
  6: 'Label',
  7: 'Label',
  8: 'Label',
  9: 'Label',
}

/** Short badge label for an app's provenance tier (which tool/source grouped it). */
export function overlayProvenance(tier: number | undefined): string {
  if (!tier) return 'raw'
  return TIER_PROVENANCE[tier] ?? 'Label'
}

function appNameFromKey(key: string): string {
  const slash = key.lastIndexOf('/')
  return slash >= 0 && slash < key.length - 1 ? key.slice(slash + 1) : key
}

/** A user-facing explanation of how an app was grouped, for the provenance
 *  tooltip — plain language naming the source resource, not the raw resolver key. */
export function provenanceTooltip(tier: number | undefined, key: string, confidence: string | undefined): string {
  const conf = confidence ?? 'low'
  const name = appNameFromKey(key)
  let how: string
  switch (tier) {
    case 1: how = `its Flux HelmRelease "${name}"`; break
    case 2: how = `its Flux Kustomization "${name}"`; break
    case 3:
    case 4: how = `its Argo CD Application "${name}"`; break
    case 5: how = `its Helm release "${name}"`; break
    case 6: how = 'the app.kubernetes.io/instance label'; break
    case 7: how = 'the app.kubernetes.io/part-of label'; break
    case 8: how = 'the app.kubernetes.io/name label'; break
    case 9: how = 'the app label'; break
    default: how = 'cluster-native evidence'
  }
  return `Grouped by ${how} · ${conf} confidence`
}

// -----------------------------------------------------------------------------
// Source — coarse provenance bucket derived from the tier, for facets.
// -----------------------------------------------------------------------------

export type AppSource = 'Argo' | 'Flux' | 'Helm' | 'Label' | 'raw'

export function sourceOf(tier: number | undefined): AppSource {
  const t = tier ?? 0
  if (t === 1 || t === 2) return 'Flux'
  if (t === 3 || t === 4) return 'Argo'
  if (t === 5) return 'Helm'
  if (t === 6 || t === 7 || t === 8) return 'Label'
  return 'raw'
}

// -----------------------------------------------------------------------------
// Health + class meta. Health uses theme tokens for the unknown/neutral end and
// pale-pastel pills (which have no theme token) for the colored tiers.
// -----------------------------------------------------------------------------

export const HEALTH_ORDER: AppHealth[] = ['unhealthy', 'degraded', 'healthy', 'unknown']
export const HEALTH_RANK: Record<string, number> = { unhealthy: 3, degraded: 2, healthy: 1, unknown: 0 }

export interface HealthMeta {
  label: string
  bar: string
  text: string
  pill: string
}

export const HEALTH_META: Record<AppHealth, HealthMeta> = {
  unhealthy: { label: 'Down', bar: 'bg-rose-500', text: 'text-rose-600 dark:text-rose-400', pill: 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:ring-rose-900' },
  degraded: { label: 'Degraded', bar: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400', pill: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900' },
  healthy: { label: 'Healthy', bar: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400', pill: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900' },
  unknown: { label: 'Unknown', bar: 'bg-slate-400', text: 'text-theme-text-tertiary', pill: 'bg-theme-hover text-theme-text-tertiary ring-theme-border' },
}

export const CLASS_ORDER: AppWorkloadClass[] = ['service', 'worker', 'job', 'unknown']

export const CLASS_META: Record<AppWorkloadClass, { label: string; pill: string; tooltip: string }> = {
  service: { label: 'Service', pill: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900', tooltip: 'Long-running, request-serving (a Deployment/StatefulSet behind a Service/Ingress/route). Inferred from the workload shape + routing.' },
  worker: { label: 'Worker', pill: 'bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-950/40 dark:text-violet-300 dark:ring-violet-900', tooltip: 'Long-running background processor (no serving edge). Inferred from the workload shape.' },
  job: { label: 'Job', pill: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900', tooltip: 'Finite or scheduled work (Job/CronJob).' },
  unknown: { label: 'Unknown', pill: 'bg-theme-hover text-theme-text-tertiary ring-theme-border', tooltip: "Couldn't infer a runtime class from the workload." },
}

export function workloadClassOf(value?: AppWorkloadClass): AppWorkloadClass {
  switch (value) {
    case 'service':
    case 'worker':
    case 'job':
      return value
    default:
      return 'unknown'
  }
}

/** Worst health across a set of raw health strings. */
export function worstHealth(hs: string[]): AppHealth {
  let w: AppHealth = 'unknown'
  for (const h of hs) if ((HEALTH_RANK[h] ?? 0) > (HEALTH_RANK[w] ?? 0)) w = h as AppHealth
  return w
}

export function newestTag(versions: string[]): string | undefined {
  let best: string | undefined
  for (const v of versions) {
    const t = v?.trim()
    if (!t) continue
    if (best === undefined) best = t
    else if (compareVersions(t, best) === 1) best = t
  }
  return best
}
