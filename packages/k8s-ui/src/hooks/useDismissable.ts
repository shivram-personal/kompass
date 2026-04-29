import { useEffect, type RefObject } from 'react'

/**
 * Set of dismissal triggers that should close a transient overlay
 * (popover, dropdown, account menu, ...). Each can be opted in/out
 * individually so call sites that want to keep e.g. an overlay open
 * across route changes can do so explicitly.
 */
export interface DismissableOptions {
  /** Whether the overlay is currently visible. Skips the listeners when false. */
  isOpen: boolean
  /** Callback to close the overlay. */
  onDismiss: () => void
  /**
   * Refs to elements that count as "inside" the overlay. A pointerdown
   * inside any of these refs is ignored — only clicks fully outside
   * trigger dismissal. Pass the trigger button + the popover content
   * so clicking the trigger to toggle works correctly.
   */
  containers?: ReadonlyArray<RefObject<HTMLElement | null>>
  /** Dismiss on Escape key. Default: true. */
  onEscape?: boolean
  /** Dismiss on browser back/forward (popstate). Default: true. */
  onRouteChange?: boolean
}

/**
 * Closes a transient overlay on the standard set of dismissal triggers:
 * pointerdown outside the overlay, Escape, and route changes
 * (popstate). Replaces a swarm of duplicated `useEffect` blocks across
 * the app (UserMenu, NamespaceSelector, …) and crucially fixes:
 *
 *   - Account menu lingering across page navigations because the menu
 *     component lives in the app shell and survives router transitions
 *     (SKY-822 bug 8): nothing was listening for popstate.
 *   - Dropdowns that mounted a viewport-spanning click-eating backdrop
 *     to detect outside clicks. With pointerdown-on-document the
 *     backdrop is unnecessary, and removing it lets the user's click
 *     propagate to whatever they actually targeted (e.g. clicking the
 *     Resources nav while a dropdown is open closes the dropdown AND
 *     navigates — SKY-822 bug 7).
 *
 * Listeners auto-attach on mount / `isOpen` flip and clean up on
 * unmount. Consumers don't have to remember the cleanup boilerplate.
 */
export function useDismissable({
  isOpen,
  onDismiss,
  containers = [],
  onEscape = true,
  onRouteChange = true,
}: DismissableOptions): void {
  useEffect(() => {
    if (!isOpen) return

    const isInsideContainers = (target: EventTarget | null) => {
      if (!(target instanceof Node)) return false
      for (const ref of containers) {
        if (ref.current?.contains(target)) return true
      }
      return false
    }

    // `pointerdown` rather than `click` so we beat any onClick handler
    // the user's intended target wires up; `pointerdown` rather than
    // `mousedown` so we also catch touch and pen interactions in a
    // single listener. PointerEvent is supported in every browser
    // radar targets (Chromium, Safari 13+, Firefox 59+).
    const handlePointerDown = (e: PointerEvent) => {
      if (isInsideContainers(e.target)) return
      onDismiss()
    }

    const handleKeyDown = onEscape
      ? (e: KeyboardEvent) => {
          if (e.key === 'Escape') onDismiss()
        }
      : null

    // Route changes don't go through React Router's history when the
    // user uses the browser back/forward buttons; popstate fires
    // regardless. For programmatic navigations (Link, navigate()),
    // React Router publishes a popstate too, so this single listener
    // covers both paths.
    const handlePopState = onRouteChange ? () => onDismiss() : null

    document.addEventListener('pointerdown', handlePointerDown)
    if (handleKeyDown) document.addEventListener('keydown', handleKeyDown)
    if (handlePopState) window.addEventListener('popstate', handlePopState)

    return () => {
      document.removeEventListener('pointerdown', handlePointerDown)
      if (handleKeyDown) document.removeEventListener('keydown', handleKeyDown)
      if (handlePopState) window.removeEventListener('popstate', handlePopState)
    }
    // We intentionally omit `containers` from the dep list — it's a
    // ReadonlyArray of stable refs in practice, and including it would
    // cause callers to memoize an array literal just to avoid
    // re-attaching listeners on every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isOpen, onDismiss, onEscape, onRouteChange])
}
