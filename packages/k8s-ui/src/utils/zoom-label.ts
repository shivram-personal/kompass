/**
 * Format a zoom-window value (in hours) for display next to the
 * timeline. Pure helper so the popover and the rest of the toolbar
 * agree on the same string.
 *
 * Rules:
 *   <  1h → "<m>m"
 *   <  24h → "<n>h"
 *   >= 24h → "<n>d"
 *
 * Lives in the shared utils package (not the web/ component file)
 * so the unit test can import the production implementation
 * directly instead of duplicating it.
 */
export function formatZoomLabel(zoom: number): string {
  if (zoom < 1) return `${Math.round(zoom * 60)}m`
  if (zoom >= 24) return `${Math.round(zoom / 24)}d`
  return `${zoom}h`
}
