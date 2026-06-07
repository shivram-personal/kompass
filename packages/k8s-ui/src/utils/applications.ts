// Shared model for the Applications surface — host-agnostic. The OSS single-
// cluster view and (eventually) the Cloud fleet view both build on these types
// and helpers. No React, no fetching.
//
// The wire shape mirrors radar OSS's GET /api/applications response
// (internal/server/applications.go). Field names match the Go json tags.

export type AppWorkloadClass = 'service' | 'worker' | 'job' | 'mixed' | 'unknown'
export type AppHealth = 'healthy' | 'degraded' | 'unhealthy' | 'unknown'

export interface AppWorkload {
  kind: string
  namespace: string
  name: string
  workload_class?: AppWorkloadClass
  image?: string
  version?: string
  appVersion?: string
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
  /** The single namespace the app's WORKLOADS run in; absent/empty when they
   *  span several (use `namespaces`). Residence, not the GitOps manager's home. */
  namespace?: string
  /** All distinct workload namespaces, sorted. */
  namespaces?: string[]
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
  /** True when the SAME image runs different tags across workloads — real
   *  drift. Multiple components on different images is normal, not skew. */
  versionSkew?: boolean
  /** Single upstream version (app.kubernetes.io/version) when all workloads
   *  agree — the app's "main version". Empty for multi-chart umbrellas. */
  appVersion?: string
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
 *  `cert-manager`'s `gatekeeper-system`, `kourier-system`, etc.; `gke-managed-`
 *  is Google's documented prefix for GKE-managed component namespaces. */
export function isSystemNamespace(ns: string | undefined): boolean {
  if (!ns) return false
  const lower = ns.toLowerCase()
  return SYSTEM_NAMESPACES.has(lower) || lower.endsWith('-system') || lower.startsWith('gke-managed-')
}

// -----------------------------------------------------------------------------
// Category — the app/add-on/mixed classification hint (never identity).
// -----------------------------------------------------------------------------

export type AppCategory = 'app' | 'addon' | 'mixed'

export const CATEGORY_ORDER: AppCategory[] = ['app', 'addon', 'mixed']

export const CATEGORY_META: Record<AppCategory, { label: string; tooltip: string }> = {
  app: { label: 'App', tooltip: 'Software you deploy and run — services, workers, jobs.' },
  addon: { label: 'Add-on', tooltip: 'Platform machinery (controllers, operators, system charts), classified by chart/label evidence. Shown for completeness.' },
  mixed: { label: 'Mixed', tooltip: 'Has both app and add-on evidence. Kept visible — classification is informational, not identity.' },
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
// Provenance — overlay tier → everything tier-derived, mirroring pkg/subject's
// Tier constants (1-9). ONE table: the badge label, the Source facet bucket,
// and the tooltip phrase all read from TIER_META, so a new tier added in
// pkg/subject has exactly one place to land here.
// -----------------------------------------------------------------------------

/** Coarse provenance bucket for the Source facet. Stable ids — display labels
 *  live in SOURCE_META (the house meta-map pattern), so they can be re-worded
 *  without breaking facet state or future URL serialization. */
export type AppSource = 'argocd' | 'flux' | 'helm' | 'label' | 'ungrouped'

export const SOURCE_ORDER: AppSource[] = ['argocd', 'flux', 'helm', 'label', 'ungrouped']

export const SOURCE_META: Record<AppSource, { label: string }> = {
  argocd: { label: 'Argo CD' },
  flux: { label: 'Flux' },
  helm: { label: 'Helm' },
  label: { label: 'Label' },
  ungrouped: { label: 'Ungrouped' },
}

interface TierMeta {
  source: AppSource
  /** Tooltip phrase pieces: "Grouped by {lead} `{code(name)}` {trail}". */
  lead: string
  code: (name: string) => string
  trail?: string
}

const TIER_META: Record<number, TierMeta> = {
  1: { source: 'flux', lead: 'its Flux HelmRelease', code: (n) => n },
  2: { source: 'flux', lead: 'its Flux Kustomization', code: (n) => n },
  3: { source: 'argocd', lead: 'its Argo CD Application', code: (n) => n },
  4: { source: 'argocd', lead: 'its Argo CD Application', code: (n) => n },
  5: { source: 'helm', lead: 'its Helm release', code: (n) => n },
  6: { source: 'label', lead: 'the', code: () => 'app.kubernetes.io/instance', trail: 'label' },
  7: { source: 'label', lead: 'the', code: () => 'app.kubernetes.io/part-of', trail: 'label' },
  8: { source: 'label', lead: 'the', code: () => 'app.kubernetes.io/name', trail: 'label' },
  9: { source: 'label', lead: 'the', code: () => 'app', trail: 'label' },
}

export function sourceOf(tier: number | undefined): AppSource {
  if (!tier) return 'ungrouped'
  return TIER_META[tier]?.source ?? 'label'
}

/** Short badge label for an app's provenance tier (which tool/source grouped it). */
export function overlayProvenance(tier: number | undefined): string {
  return SOURCE_META[sourceOf(tier)].label
}

function appNameFromKey(key: string): string {
  const slash = key.lastIndexOf('/')
  return slash >= 0 && slash < key.length - 1 ? key.slice(slash + 1) : key
}

// How an app was grouped, decomposed so the tooltip can render the source
// resource / label key in an inline-code chip rather than a run-on sentence.
// `lead` + `code` + `trail` reads as a phrase: "its Flux HelmRelease `argocd`"
// or "the `app.kubernetes.io/part-of` label". `code` empty → no chip.
export interface ProvenanceSource {
  lead: string
  code: string
  trail?: string
}

export function provenanceSource(tier: number | undefined, key: string): ProvenanceSource {
  const meta = tier ? TIER_META[tier] : undefined
  if (!meta) return { lead: 'cluster-native evidence', code: '' }
  return { lead: meta.lead, code: meta.code(appNameFromKey(key)), trail: meta.trail }
}


/** The distinct namespaces an app's workloads run in, sorted. Prefers the
 *  server's `namespaces` field, deriving from workloads for older payloads. */
export function namespacesOf(app: AppRow): string[] {
  if (app.namespaces && app.namespaces.length > 0) return app.namespaces
  const nss = Array.from(new Set((app.workloads || []).map((w) => w.namespace).filter(Boolean))).sort()
  if (nss.length > 0) return nss
  return app.namespace ? [app.namespace] : []
}

/** An app's single namespace, or '' when it spans several — callers must not
 *  pick an arbitrary one (env inference and the system-namespace filter both
 *  key off this; a wrong pick misleads). Use namespacesOf for the full list. */
export function namespaceOf(app: AppRow): string {
  const nss = namespacesOf(app)
  return nss.length === 1 ? nss[0] : ''
}

/** Normalize a wire health string to the AppHealth union (the health twin of
 *  workloadClassOf — keeps `as AppHealth` casts out of components). */
export function healthOf(value: string | undefined): AppHealth {
  return value === 'unhealthy' || value === 'degraded' || value === 'healthy' ? value : 'unknown'
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

// ─── Chip dialect ────────────────────────────────────────────────────────────
// The Applications surface renders dense metadata as pale pastel chips —
// deliberately lighter than <Badge>'s severity palette, which is sized for
// standalone status pills. A local dialect, but defined ONCE here: call sites
// compose `CHIP` (chrome) + a `CHIP_TONE` (color), never inline the strings.
// Literal class strings are required for Tailwind's content scanner.
export const CHIP = 'inline-flex items-center rounded-sm px-1.5 py-px text-[10px] font-medium ring-1 ring-inset'
export const CHIP_TONE = {
  rose: 'bg-rose-50 text-rose-700 ring-rose-200 dark:bg-rose-950/40 dark:text-rose-300 dark:ring-rose-900',
  amber: 'bg-amber-50 text-amber-700 ring-amber-200 dark:bg-amber-950/40 dark:text-amber-300 dark:ring-amber-900',
  emerald: 'bg-emerald-50 text-emerald-700 ring-emerald-200 dark:bg-emerald-950/40 dark:text-emerald-300 dark:ring-emerald-900',
  blue: 'bg-blue-50 text-blue-700 ring-blue-200 dark:bg-blue-950/40 dark:text-blue-300 dark:ring-blue-900',
  violet: 'bg-violet-50 text-violet-700 ring-violet-200 dark:bg-violet-950/40 dark:text-violet-300 dark:ring-violet-900',
  neutral: 'bg-theme-hover text-theme-text-secondary ring-theme-border',
  muted: 'bg-theme-hover text-theme-text-tertiary ring-theme-border',
} as const

export const HEALTH_META: Record<AppHealth, HealthMeta> = {
  unhealthy: { label: 'Down', bar: 'bg-rose-500', text: 'text-rose-600 dark:text-rose-400', pill: CHIP_TONE.rose },
  degraded: { label: 'Degraded', bar: 'bg-amber-500', text: 'text-amber-600 dark:text-amber-400', pill: CHIP_TONE.amber },
  healthy: { label: 'Healthy', bar: 'bg-emerald-500', text: 'text-emerald-600 dark:text-emerald-400', pill: CHIP_TONE.emerald },
  unknown: { label: 'Unknown', bar: 'bg-slate-400', text: 'text-theme-text-tertiary', pill: CHIP_TONE.muted },
}

export const CLASS_ORDER: AppWorkloadClass[] = ['service', 'worker', 'job', 'unknown']

export const CLASS_META: Record<AppWorkloadClass, { label: string; pill: string; tooltip: string }> = {
  service: { label: 'Service', pill: CHIP_TONE.blue, tooltip: 'Long-running, request-serving (a Deployment/StatefulSet behind a Service/Ingress/route). Inferred from the workload shape + routing.' },
  worker: { label: 'Worker', pill: CHIP_TONE.violet, tooltip: 'Long-running background processor (no serving edge). Inferred from the workload shape.' },
  job: { label: 'Job', pill: CHIP_TONE.amber, tooltip: 'Finite or scheduled work (Job/CronJob).' },
  mixed: { label: 'Mixed', pill: CHIP_TONE.neutral, tooltip: 'Contains workloads of more than one class (e.g. a service plus its scheduled jobs).' },
  unknown: { label: 'Unknown', pill: CHIP_TONE.muted, tooltip: "Couldn't infer a runtime class from the workload." },
}

/** Per-class workload counts for an app, in CLASS_ORDER — the composition
 *  behind a "Mixed" badge and the inclusive Class facet (filtering "Service"
 *  matches mixed apps that contain a service). */
export function classCompositionOf(app: AppRow): { cls: AppWorkloadClass; count: number }[] {
  const counts = new Map<AppWorkloadClass, number>()
  for (const w of app.workloads || []) {
    const c = workloadClassOf(w.workload_class)
    counts.set(c, (counts.get(c) ?? 0) + 1)
  }
  return CLASS_ORDER.filter((c) => counts.has(c)).map((c) => ({ cls: c, count: counts.get(c)! }))
}

/** The distinct KNOWN classes an app contains — the facet-matching set. Falls
 *  back to the app-level class when there are no classifiable workloads. */
export function classSetOf(app: AppRow): AppWorkloadClass[] {
  const known = classCompositionOf(app)
    .map((c) => c.cls)
    .filter((c) => c === 'service' || c === 'worker' || c === 'job')
  if (known.length > 0) return known
  return [workloadClassOf(app.workload_class)]
}

export function workloadClassOf(value?: AppWorkloadClass): AppWorkloadClass {
  switch (value) {
    case 'service':
    case 'worker':
    case 'job':
    case 'mixed':
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
