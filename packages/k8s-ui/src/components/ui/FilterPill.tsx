import type { ReactNode } from 'react'
import type { LucideIcon } from 'lucide-react'
import { clsx } from 'clsx'
import { Tooltip } from './Tooltip'

// FilterPill is a single-button toggle filter — clickable pill that
// communicates an active/inactive state. Used in horizontal filter rows
// where each pill toggles one filter on/off (no dropdown — this is the
// toggle pattern, not a combobox).
//
// Tone-encoded active state: when tone='danger' and active=true, the
// pill bg+text use rose; tone='warn' uses amber; etc. Matches the
// canonical SEVERITY_BADGE / StatusPill vocabulary so Critical filters
// and Warning filters have visually distinct active states. Useful for
// filter rows that mix severity-bearing categories with neutral ones.
//
// Accessibility: every pill renders aria-pressed automatically, so
// screen readers announce pressed/unpressed correctly. Optional tooltip
// describes the toggle action ("Click to stop filtering by danger").

export type FilterPillTone = 'neutral' | 'danger' | 'warn' | 'ok' | 'brand'

interface Props {
  label: ReactNode
  active: boolean
  onClick: () => void
  /** Active-state color encoding. Default: neutral (Radar's existing style). */
  tone?: FilterPillTone
  /** Optional leading icon. */
  icon?: LucideIcon
  /** Optional count badge — renders " (N)" after label. */
  count?: number
  /** Tooltip explaining the toggle. Wraps button in Tooltip if set. */
  tooltip?: string
  /** Override the accessible name. Defaults to label + active state. */
  'aria-label'?: string
  className?: string
}

const TONE_ACTIVE: Record<FilterPillTone, string> = {
  // Neutral matches Radar's pre-existing style (preserves back-compat with
  // AuditFindingsTable's previous local toggle).
  neutral: 'bg-theme-text-primary/10 text-theme-text-primary font-medium',
  danger:  'bg-red-500/15 text-red-700 dark:text-red-300 font-medium',
  warn:    'bg-amber-500/15 text-amber-800 dark:text-amber-300 font-medium',
  ok:      'bg-emerald-500/15 text-emerald-700 dark:text-emerald-300 font-medium',
  brand:   'bg-[var(--color-brand-50)] text-theme-text-primary ring-1 ring-inset ring-[var(--color-radar-accent)] dark:bg-[var(--color-brand-950)] font-medium',
}

const INACTIVE = 'text-theme-text-tertiary hover:text-theme-text-secondary hover:bg-theme-hover'

export function FilterPill({
  label,
  active,
  onClick,
  tone = 'neutral',
  icon: Icon,
  count,
  tooltip,
  className,
  ...rest
}: Props) {
  const ariaLabel = rest['aria-label']

  const button = (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      aria-label={ariaLabel}
      className={clsx(
        'inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs transition-colors',
        'focus-visible:ring-2 focus-visible:ring-theme-text-primary/20 focus-visible:outline-none',
        active ? TONE_ACTIVE[tone] : INACTIVE,
        className,
      )}
    >
      {Icon && <Icon className="h-3.5 w-3.5" aria-hidden />}
      <span>{label}</span>
      {count !== undefined && (
        <span className="text-theme-text-tertiary">({count})</span>
      )}
    </button>
  )

  if (!tooltip) return button
  return (
    <Tooltip content={tooltip} delay={200}>
      {button}
    </Tooltip>
  )
}
