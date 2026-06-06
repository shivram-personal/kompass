import { seriesColor, seriesFill, SERIES_COLORS } from '../components/charts/colors'

// Per-workload color encoding for the application topology graph. A workload's
// exclusive satellites (its Service, config, pods) carry its hue; shared and
// unattached resources stay neutral. Reuses SERIES_COLORS — the codebase's
// categorical palette for multi-series charts (10 well-separated 500-level
// shades, vetted on both themes) — so workload colors match the rest of the UI.
//
// Solid swatch for the rail legend; faint fill for the node card background. The
// node wash is applied only to healthy/unknown cards (see K8sResourceNode), so a
// warm hue here never competes with the red/amber a degraded card owns.

export interface WorkloadHue {
  /** Solid — the rail legend chip. */
  swatch: string
  /** Faint fill (~13% alpha) — the node card tint, layered over the surface. */
  wash: string
}

export const WORKLOAD_HUE_COUNT = SERIES_COLORS.length

/** Sentinel owner for shared / unattached nodes — they get no hue (neutral). */
export const NEUTRAL_OWNER = '__neutral__'

const NEUTRAL_FALLBACK = '#64748b' // slate-500

export function workloadHue(index: number): WorkloadHue {
  return {
    swatch: seriesColor(index, NEUTRAL_FALLBACK),
    wash: seriesFill(index, NEUTRAL_FALLBACK),
  }
}
