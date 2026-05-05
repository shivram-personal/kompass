import { describe, it, expect } from 'vitest'
import { nextLastUpdatedTimestamp } from './last-updated'

describe('nextLastUpdatedTimestamp', () => {
  it('returns null when dataUpdatedAt is falsy (no successful fetch yet)', () => {
    const resources = [{ kind: 'Pod' }]
    expect(nextLastUpdatedTimestamp(0, resources, undefined)).toBe(null)
    expect(nextLastUpdatedTimestamp(undefined, resources, undefined)).toBe(null)
  })

  it('returns null on byte-identical refetch (same data reference)', () => {
    // Structural sharing: React Query returns the same reference when
    // the response is byte-identical. The visible timer must NOT
    // reset on those no-op refetches.
    const resources = [{ kind: 'Pod' }]
    expect(nextLastUpdatedTimestamp(1700000000000, resources, resources)).toBe(null)
  })

  it('returns the new timestamp when the data reference changes', () => {
    const before = [{ kind: 'Pod' }]
    const after = [{ kind: 'Pod' }, { kind: 'Service' }]
    expect(nextLastUpdatedTimestamp(1700000000000, after, before)).toBe(1700000000000)
    // Initial mount: lastRef is undefined and resources is anything.
    expect(nextLastUpdatedTimestamp(1700000005000, before, undefined)).toBe(1700000005000)
  })
})
