import { Tooltip } from './Tooltip'
import { parseContextName } from '../../utils/context-name'
import type { ParsedContextName } from '../../utils/context-name'
import awsLogo from './provider-logos/aws.png'
import gcpLogo from './provider-logos/gcp.png'
import azureLogo from './provider-logos/azure.svg'

// ClusterName renders a kubectl context string with the meaningful
// cluster identity surfaced as primary text and provider/region pushed
// into supporting metadata. Wraps parseContextName from utils/context-name
// so all surfaces (cluster cards, table cells, column headers, switcher
// dropdowns, breadcrumb, error views) share identical cluster-identity
// rendering.
//
// Variants:
//   inline   — name + small provider logo, fits in a table cell or
//              column header
//   stacked  — name on top, provider/region on a smaller second line,
//              for card-sized surfaces
//
// User-named clusters that don't match a known shape pass through
// unchanged — no provider badge, no tooltip needed.

const PROVIDER_LOGOS: Record<NonNullable<ParsedContextName['provider']>, string> = {
  GKE: gcpLogo,
  EKS: awsLogo,
  AKS: azureLogo,
}

interface Props {
  /** Raw context / display string, as stored in the cluster record. */
  name: string
  /** Visual shape. Default: inline. */
  variant?: 'inline' | 'stacked'
  /** Suppress the provider badge — use when context already conveys provider. */
  noBadge?: boolean
  /** Optional className on the outer span. */
  className?: string
}

function ProviderBadge({ provider }: { provider: NonNullable<ParsedContextName['provider']> }) {
  return (
    <img
      src={PROVIDER_LOGOS[provider]}
      alt={`${provider} cluster`}
      className="h-4 w-4 flex-shrink-0"
    />
  )
}

export function ClusterName({ name, variant = 'inline', noBadge, className }: Props) {
  const parsed = parseContextName(name)

  const showBadge = !noBadge && parsed.provider !== null
  const showRegion = parsed.region !== null && variant === 'stacked'
  const needsTooltip = parsed.raw !== parsed.clusterName

  const body = (
    <span className={['inline-flex items-center gap-1.5 min-w-0', className ?? ''].join(' ')}>
      {showBadge && <ProviderBadge provider={parsed.provider!} />}
      {variant === 'stacked' ? (
        <span className="flex flex-col min-w-0">
          <span className="truncate">{parsed.clusterName}</span>
          {showRegion && (
            <span className="text-[10px] text-theme-text-tertiary truncate">
              {parsed.provider} · {parsed.region}
            </span>
          )}
        </span>
      ) : (
        <span className="truncate">{parsed.clusterName}</span>
      )}
    </span>
  )

  if (!needsTooltip) return body

  return (
    <Tooltip content={parsed.raw} delay={250}>
      {body}
    </Tooltip>
  )
}
