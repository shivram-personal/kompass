import { ReactNode, useState, useRef, useEffect, useCallback } from 'react'
import { createPortal } from 'react-dom'
import { clsx } from 'clsx'

// Module-level singleton coordinator: only one Tooltip can be visible
// at a time across the whole app. Without this, two Tooltip instances
// could both render their portal simultaneously — happens when a
// trigger element unmounts/remounts during an in-progress hover (React
// re-render, HMR, rapid cursor movement between adjacent triggers),
// because the old trigger's mouseleave never fires so its visible
// state stays stuck. Observed in multi-cluster visual tests on
// densely-populated source-chip hovers.
//
// Each visible Tooltip registers a `hide` callback. When the next
// Tooltip becomes visible, it calls the previous active tooltip's
// hide(), guaranteeing a single visible portal. Registry clears on
// hide or unmount.
let activeHide: (() => void) | null = null

interface TooltipProps {
  content: ReactNode
  children: ReactNode
  /** Delay before showing tooltip in ms (default: 300) */
  delay?: number
  /** Position of tooltip (default: 'top') */
  position?: 'top' | 'bottom' | 'left' | 'right'
  /** Additional class for the tooltip content */
  className?: string
  /** Whether tooltip is disabled */
  disabled?: boolean
  /** Additional class for the wrapper span (useful for positioning) */
  wrapperClassName?: string
  /** Inline styles for the wrapper span (useful for absolute positioning) */
  wrapperStyle?: React.CSSProperties
}

export function Tooltip({
  content,
  children,
  delay = 300,
  position = 'top',
  className,
  disabled = false,
  wrapperClassName,
  wrapperStyle,
}: TooltipProps) {
  const [isVisible, setIsVisible] = useState(false)
  const [coords, setCoords] = useState({ top: 0, left: 0 })
  const triggerRef = useRef<HTMLSpanElement>(null)
  const tooltipRef = useRef<HTMLSpanElement>(null)
  const timeoutRef = useRef<number | null>(null)

  const updatePosition = useCallback(() => {
    if (!triggerRef.current) return

    const rect = triggerRef.current.getBoundingClientRect()
    const tooltipRect = tooltipRef.current?.getBoundingClientRect()
    const tooltipWidth = tooltipRect?.width || 0
    const tooltipHeight = tooltipRect?.height || 0

    let top = 0
    let left = 0

    switch (position) {
      case 'top':
        top = rect.top - tooltipHeight - 6
        left = rect.left + rect.width / 2 - tooltipWidth / 2
        break
      case 'bottom':
        top = rect.bottom + 6
        left = rect.left + rect.width / 2 - tooltipWidth / 2
        break
      case 'left':
        top = rect.top + rect.height / 2 - tooltipHeight / 2
        left = rect.left - tooltipWidth - 6
        break
      case 'right':
        top = rect.top + rect.height / 2 - tooltipHeight / 2
        left = rect.right + 6
        break
    }

    // Keep tooltip within viewport
    const padding = 8
    if (left < padding) left = padding
    if (left + tooltipWidth > window.innerWidth - padding) {
      left = window.innerWidth - tooltipWidth - padding
    }
    if (top < padding) top = rect.bottom + 6 // flip to bottom
    if (top + tooltipHeight > window.innerHeight - padding) {
      top = rect.top - tooltipHeight - 6 // flip to top
    }

    setCoords({ top, left })
  }, [position])

  // Stable hide function for the singleton registry — useRef so the
  // identity stays the same across renders, otherwise the registry
  // could hold a stale closure that doesn't see the latest setState.
  const hideRef = useRef<() => void>(() => {})
  hideRef.current = () => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current)
      timeoutRef.current = null
    }
    setIsVisible(false)
  }

  const showTooltip = () => {
    if (disabled || !content) return
    timeoutRef.current = window.setTimeout(() => {
      // Singleton: hide whoever was visible before us, register self
      // as the new active tooltip. Guards against stuck duplicates.
      if (activeHide && activeHide !== hideRef.current) {
        activeHide()
      }
      activeHide = hideRef.current
      setIsVisible(true)
    }, delay)
  }

  const hideTooltip = () => {
    if (timeoutRef.current) {
      clearTimeout(timeoutRef.current)
      timeoutRef.current = null
    }
    if (activeHide === hideRef.current) {
      activeHide = null
    }
    setIsVisible(false)
  }

  useEffect(() => {
    if (isVisible) {
      // Small delay to let the tooltip render before measuring
      requestAnimationFrame(updatePosition)
    }
  }, [isVisible, updatePosition])

  useEffect(() => {
    return () => {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current)
      }
      // Clear from singleton registry on unmount — otherwise a Tooltip
      // that unmounts while visible (e.g. row removed during hover)
      // would leave a stale entry in activeHide pointing at a torn-down
      // setState, blocking the next Tooltip from registering.
      if (activeHide === hideRef.current) {
        activeHide = null
      }
    }
  }, [])

  // When disabled flips true, proactively cancel any pending show timer and
  // clear visible state. Without this, a tooltip that was visible (or armed)
  // when disabled became true would pop back on as soon as disabled flips
  // false — even though the cursor is elsewhere and no fresh mouseenter has
  // fired. Also covers the case where the trigger becomes unreachable via
  // pointer-events-none and never fires mouseleave.
  useEffect(() => {
    if (disabled) {
      if (timeoutRef.current) {
        clearTimeout(timeoutRef.current)
        timeoutRef.current = null
      }
      setIsVisible(false)
    }
  }, [disabled])

  if (disabled || !content) {
    return <>{children}</>
  }

  return (
    <>
      <span
        ref={triggerRef}
        className={clsx('inline-flex max-w-full', wrapperClassName)}
        style={wrapperStyle}
        onMouseEnter={showTooltip}
        onMouseLeave={hideTooltip}
        onFocus={showTooltip}
        onBlur={hideTooltip}
      >
        {children}
      </span>
      {isVisible &&
        createPortal(
          <span
            ref={tooltipRef}
            className={clsx(
              'fixed z-[9999] px-2 py-1 text-xs text-theme-text-primary bg-theme-base rounded shadow-lg border border-theme-border',
              'whitespace-nowrap pointer-events-none',
              className
            )}
            style={{ top: coords.top, left: coords.left }}
            role="tooltip"
          >
            {content}
          </span>,
          document.body
        )}
    </>
  )
}

/** Simple wrapper that adds tooltip to any element - use for quick migrations from title="" */
export function WithTooltip({
  tip,
  children,
  delay = 300,
}: {
  tip: string | undefined | null
  children: ReactNode
  delay?: number
}) {
  if (!tip) return <>{children}</>
  return (
    <Tooltip content={tip} delay={delay}>
      {children}
    </Tooltip>
  )
}
