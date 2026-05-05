/**
 * Computes the next timestamp the user-visible "Updated Xs" timer in
 * `ResourcesView` should advance to, or `null` to leave it alone.
 *
 * React Query bumps `dataUpdatedAt` on every successful fetch — even
 * when the response is byte-identical to the cached value and
 * structural sharing returns the same `data` reference. Mounting,
 * window focus, and sibling subscribers issuing the same queryKey
 * all trigger such no-op refetches. Resetting the visible timer on
 * those events is misleading: it suggests fresh data arrived when
 * nothing actually changed, and users read the "<1s" jump as
 * evidence that opening a filter drawer triggered a real network
 * round-trip.
 *
 * Hoisted out of the consuming `useEffect` so the rule
 * (reference-change AND non-zero `dataUpdatedAt`) is unit-testable
 * and survives future refactors that might otherwise drop the ref
 * guard or swap `!==` for deep equality and silently re-introduce
 * the bug. Returning `number | null` (rather than `boolean`) lets
 * the caller use the value directly, with no type assertions.
 */
export function nextLastUpdatedTimestamp(
  dataUpdatedAt: number | undefined,
  resources: unknown,
  lastRef: unknown,
): number | null {
  if (!dataUpdatedAt) return null
  if (resources === lastRef) return null
  return dataUpdatedAt
}
