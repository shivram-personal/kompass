import { useEffect, useState } from 'react'

/**
 * Returns the current wall-clock time, refreshed every `intervalMs`.
 *
 * Use this when you have UI that derives a relative time string (e.g.
 * "Updated 8s", "24d") from a fixed timestamp. Without it, the relative
 * label only changes when the parent re-renders for an unrelated reason
 * — which makes the label feel frozen, and worse: the next unrelated
 * re-render makes it jump forward by however many seconds passed in
 * silence. SKY-820 captured users perceiving that as a "data re-fetch
 * was triggered" because the displayed age suddenly updated.
 *
 * The interval is opt-in per call site so cells in tight tables don't
 * pay the cost of a 1Hz tick when they only need a 60Hz update for
 * "minutes" granularity.
 *
 * @param intervalMs — how often to advance the clock. Defaults to 1000ms.
 *                     Pass `null` to disable ticking (returns the time at
 *                     mount and never updates).
 * @returns the current `Date.now()` value.
 */
export function useNow(intervalMs: number | null = 1000): number {
  const [now, setNow] = useState(() => Date.now())

  useEffect(() => {
    if (intervalMs === null) return
    if (intervalMs <= 0) return
    // Use the global setInterval rather than `window.setInterval` so the
    // hook works under SSR / Node-based tests too. In browsers they're
    // the same function; in Node, `window` is undefined.
    const id = setInterval(() => setNow(Date.now()), intervalMs)
    return () => {
      clearInterval(id)
    }
  }, [intervalMs])

  return now
}
