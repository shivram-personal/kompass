import { provenanceSource, type AppWorkload } from '../../utils/applications'

// Structured tooltip content for the application chips. Both render a short
// title/phrase plus the technical signal (label key, resource name, selector)
// in the shared inline-code chip — readable typography instead of a wall of text.

export function ProvenanceTooltip({
  tier,
  appKey,
  confidence,
}: {
  tier: number | undefined
  appKey: string
  confidence: string | undefined
}) {
  const src = provenanceSource(tier, appKey)
  const conf = confidence ?? 'low'
  return (
    <div className="space-y-1">
      <div className="text-xs text-theme-text-primary">
        Grouped by {src.lead}
        {src.code && (
          <>
            {' '}
            <code className="inline-code">{src.code}</code>
          </>
        )}
        {src.trail ? ` ${src.trail}` : ''}
      </div>
      <div className="text-[10px] uppercase tracking-wide text-theme-text-tertiary">{conf} confidence</div>
    </div>
  )
}

// "3 versions" on its own says little; the breakdown says which component runs
// what. (A single chart/release would ideally show its appVersion as the one
// "main version" — that needs the backend to surface chart metadata; until then
// this is the honest per-workload view, and the right shape for umbrella apps
// that genuinely have no single version.)
export function VersionTooltip({ workloads }: { workloads: Pick<AppWorkload, 'name' | 'version'>[] }) {
  const rows = workloads.filter((w) => w.version)
  if (rows.length === 0) return null
  return (
    <div className="max-w-xs space-y-1">
      <div className="text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">Version by workload</div>
      <ul className="space-y-0.5">
        {rows.map((w, i) => (
          <li key={i} className="flex items-baseline justify-between gap-3">
            <span className="truncate text-[11px] text-theme-text-secondary">{w.name}</span>
            <code className="inline-code text-[10px]">{w.version}</code>
          </li>
        ))}
      </ul>
    </div>
  )
}

export function CategoryTooltip({
  category,
  addonReason,
}: {
  category: string
  addonReason?: string
}) {
  const mixed = category === 'mixed'
  const title = mixed ? 'Mixed app / add-on' : 'Platform add-on'
  const lead = mixed
    ? 'Has both application and add-on evidence. Kept visible — classification is informational, not identity.'
    : 'Classified as a platform add-on, shown here for completeness.'
  // addonReason is a "; "-separated list of signals, e.g.
  //   "app.kubernetes.io/name=argocd-redis (argocd); chart=argo-cd".
  // Render each as its own line; the selector goes in code, the trailing
  // "(namespace)" hint is split out as muted text.
  const items = (addonReason ?? '')
    .split(/;\s*/)
    .map((s) => s.trim())
    .filter(Boolean)
  return (
    <div className="max-w-xs space-y-1.5">
      <div className="text-xs font-semibold text-theme-text-primary">{title}</div>
      <div className="text-[11px] leading-snug text-theme-text-secondary">{lead}</div>
      {items.length > 0 && (
        <div className="space-y-1 border-t border-theme-border pt-1.5">
          <div className="text-[10px] font-medium uppercase tracking-wide text-theme-text-tertiary">Evidence</div>
          <ul className="max-h-52 space-y-1 overflow-y-auto">
            {items.map((it, i) => {
              const m = it.match(/^(.*?)\s*\(([^)]+)\)\s*$/)
              const selector = m ? m[1] : it
              const where = m?.[2]
              return (
                <li key={i} className="flex items-baseline gap-1.5">
                  <code className="inline-code text-[10px]">{selector}</code>
                  {where && <span className="text-[10px] text-theme-text-tertiary">{where}</span>}
                </li>
              )
            })}
          </ul>
        </div>
      )}
    </div>
  )
}
