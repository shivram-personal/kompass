import { describe, it, expect } from 'vitest'
import { formatZoomLabel } from './zoom-label'

// Pin the timeline-window label format. The picker and the
// previous-render label MUST format identically or the user sees
// flicker between "60m" (label) and "1h" (popover option) for the
// same zoom.
describe('formatZoomLabel (timeline window picker)', () => {
  it('formats sub-hour zooms as minutes', () => {
    expect(formatZoomLabel(0.25)).toBe('15m')
    expect(formatZoomLabel(0.5)).toBe('30m')
  })

  it('formats hour-scale zooms as hours', () => {
    expect(formatZoomLabel(1)).toBe('1h')
    expect(formatZoomLabel(2)).toBe('2h')
    expect(formatZoomLabel(12)).toBe('12h')
  })

  it('formats day-scale zooms as days', () => {
    expect(formatZoomLabel(24)).toBe('1d')
    expect(formatZoomLabel(48)).toBe('2d')
    expect(formatZoomLabel(72)).toBe('3d')
    expect(formatZoomLabel(168)).toBe('7d')
  })

  it('rounds (does not truncate) values at the bucket boundaries', () => {
    expect(formatZoomLabel(0.49)).toBe('29m')
    expect(formatZoomLabel(35)).toBe('1d')
  })
})
