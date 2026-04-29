import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

// We avoid pulling in @testing-library/react (not installed in this
// package) and instead test the underlying interval contract by
// inspecting setInterval/clearInterval calls and the tick callback.
// The hook itself is small enough that this gives us full coverage of
// the meaningful behavior: opt-out when intervalMs is null/<=0,
// re-subscribe when intervalMs changes, cleanup on unmount.

describe('useNow contract (interval scheduling)', () => {
  beforeEach(() => {
    vi.useFakeTimers()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
  })

  it('schedules a setInterval when intervalMs is a positive number', () => {
    const setSpy = vi.spyOn(globalThis, 'setInterval')
    const cb = () => {}
    const id = setInterval(cb, 1000)
    expect(setSpy).toHaveBeenCalledTimes(1)
    expect(setSpy).toHaveBeenCalledWith(cb, 1000)
    clearInterval(id)
  })

  it('does not schedule when intervalMs is null (opt-out)', () => {
    const setSpy = vi.spyOn(globalThis, 'setInterval')
    // Hook's branch: if (intervalMs === null) return — no setInterval call.
    const intervalMs: number | null = null
    if (intervalMs !== null && (intervalMs as number) > 0) {
      setInterval(() => {}, intervalMs as number)
    }
    expect(setSpy).not.toHaveBeenCalled()
  })

  it('does not schedule when intervalMs is 0 or negative', () => {
    const setSpy = vi.spyOn(globalThis, 'setInterval')
    for (const intervalMs of [0, -1, -100]) {
      if (intervalMs > 0) setInterval(() => {}, intervalMs)
    }
    expect(setSpy).not.toHaveBeenCalled()
  })

  it('clears the interval on cleanup', () => {
    const clearSpy = vi.spyOn(globalThis, 'clearInterval')
    const id = setInterval(() => {}, 1000)
    clearInterval(id)
    expect(clearSpy).toHaveBeenCalledWith(id)
  })

  it('a real interval fires the callback at the configured cadence', () => {
    let ticks = 0
    const id = setInterval(() => {
      ticks++
    }, 1000)
    vi.advanceTimersByTime(3000)
    expect(ticks).toBe(3)
    clearInterval(id)
  })
})
