import { describe, it, expect, vi } from 'vitest'
import { shouldScheduleNow, scheduleNowTicks } from './useNow'

// This package's vitest config has no @testing-library/react and no
// jsdom — we can't render hooks here. The hook's effect body is
// hoisted into the pure `scheduleNowTicks` driver, and we exercise
// THAT directly: same opt-out rules, same setInterval call, same
// cleanup contract that the hook installs at runtime.

describe('shouldScheduleNow', () => {
  it('schedules when intervalMs is a positive number', () => {
    expect(shouldScheduleNow(1)).toBe(true)
    expect(shouldScheduleNow(1000)).toBe(true)
    expect(shouldScheduleNow(60_000)).toBe(true)
  })

  it('opts out when intervalMs is null', () => {
    expect(shouldScheduleNow(null)).toBe(false)
  })

  it('opts out when intervalMs is zero', () => {
    expect(shouldScheduleNow(0)).toBe(false)
  })

  it('opts out when intervalMs is negative', () => {
    expect(shouldScheduleNow(-1)).toBe(false)
    expect(shouldScheduleNow(-1000)).toBe(false)
  })
})

describe('scheduleNowTicks (the pure body of useNow)', () => {
  function fakeTimers() {
    // Type the spies explicitly so TS knows .mock.calls has the
    // (cb, ms) shape — vi.fn() inference defaults to a 0-arity
    // signature otherwise.
    const setInterval = vi.fn<(cb: () => void, ms: number) => unknown>(() => 'TIMER_ID' as unknown)
    const clearInterval = vi.fn<(id: unknown) => void>()
    const now = vi.fn<() => number>(() => 1700000000000)
    return {
      timers: { setInterval, clearInterval, now },
      setInterval,
      clearInterval,
      now,
    }
  }

  it('installs no timer and returns a no-op cleanup when interval is null', () => {
    const setNow = vi.fn()
    const { timers, setInterval, clearInterval } = fakeTimers()
    const cleanup = scheduleNowTicks(null, setNow, timers)
    expect(setInterval).not.toHaveBeenCalled()
    cleanup()
    expect(clearInterval).not.toHaveBeenCalled()
  })

  it('installs no timer when interval is zero or negative', () => {
    const setNow = vi.fn()
    for (const bad of [0, -1, -1000]) {
      const { timers, setInterval } = fakeTimers()
      scheduleNowTicks(bad, setNow, timers)
      expect(setInterval, `interval=${bad}`).not.toHaveBeenCalled()
    }
  })

  it('installs an interval that calls setNow with the current time', () => {
    const setNow = vi.fn()
    const { timers, setInterval, now } = fakeTimers()
    scheduleNowTicks(1000, setNow, timers)
    expect(setInterval).toHaveBeenCalledTimes(1)
    expect(setInterval.mock.calls[0][1]).toBe(1000)

    // Fire the registered tick — the hook must update state with
    // the latest wall-clock value, NOT the value captured at
    // mount.
    const tick = setInterval.mock.calls[0][0] as () => void
    now.mockReturnValue(1700000005000)
    tick()
    expect(setNow).toHaveBeenCalledWith(1700000005000)
    now.mockReturnValue(1700000010000)
    tick()
    expect(setNow).toHaveBeenLastCalledWith(1700000010000)
  })

  it('returns a cleanup that clears the registered timer id', () => {
    const setNow = vi.fn()
    const { timers, setInterval, clearInterval } = fakeTimers()
    setInterval.mockReturnValue(42)
    const cleanup = scheduleNowTicks(1000, setNow, timers)
    cleanup()
    expect(clearInterval).toHaveBeenCalledWith(42)
  })

  it('passes the interval through unchanged (1Hz vs 60Hz callers)', () => {
    const setNow = vi.fn()
    const { timers, setInterval } = fakeTimers()
    scheduleNowTicks(60_000, setNow, timers)
    expect(setInterval.mock.calls[0][1]).toBe(60_000)
  })
})
