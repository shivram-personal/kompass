export { TracePanel } from './TracePanel'
// ResourceRef intentionally NOT re-exported from the package root — it would
// collide with the global ResourceRef in types.ts. Trace consumers import it
// from the panel module directly when they need the typed shape.
export type { Trace, Hop, Finding, FindingSeverity, Verdict } from './types'
