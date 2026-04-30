import { describe, it, expect } from 'vitest'

// Inline copy of the helper from web/src/components/timeline/TimelineSwimlanes.tsx
// (same module — re-declared here to avoid pulling the swimlanes
// React component into the unit-test boundary). If the rules drift,
// update both. The shared rule it pins:
//
//   <  1h → "<m>m"
//   <  24h → "<n>h"
//   >= 24h → "<n>d"
//
// SKY-826 bug 12 turned a static "1h window" label into a real
// dropdown of preset windows. The picker and the previous-render
// label have to format identically or the user sees flicker between
// "60m" (label) and "1h" (popover option) for the same zoom.
function formatZoomLabel(zoom: number): string {
  if (zoom < 1) return `${Math.round(zoom * 60)}m`
  if (zoom >= 24) return `${Math.round(zoom / 24)}d`
  return `${zoom}h`
}

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
    // 0.49 * 60 = 29.4 → rounds to 29
    expect(formatZoomLabel(0.49)).toBe('29m')
    // 35h / 24 = 1.458... → rounds to 1d (closer to 1 than 2)
    expect(formatZoomLabel(35)).toBe('1d')
  })
})
