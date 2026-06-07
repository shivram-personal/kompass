import { describe, it, expect } from 'vitest'
import { compareVersions } from './applications'

describe('compareVersions', () => {
  it('orders semver', () => {
    expect(compareVersions('1.2.0', '1.10.0')).toBe(-1)
    expect(compareVersions('v2.0.0', 'v2.0.0')).toBe(0)
  })

  // Date-stamped CI tags are the dominant shape on real clusters
  // (main_2026-03-26_05) — semver-only made promotion lag inert on them.
  it('orders same-prefix date-stamped tags by date then sequence', () => {
    expect(compareVersions('main_2026-03-26_05', 'main_2026-06-02_03')).toBe(-1)
    expect(compareVersions('main_2026-06-02_03', 'main_2026-06-02_01')).toBe(1)
    expect(compareVersions('main_2026-06-02_03', 'main_2026-06-02_03')).toBe(0)
  })

  it('refuses date tags with different prefixes', () => {
    expect(compareVersions('main_2026-06-02_03', 'hotfix_2026-06-02_03')).toBeNull()
    expect(compareVersions('billing_main_2026-05-18_00', 'project-infra_main_2026-06-05_01')).toBeNull()
  })

  it('refuses mixed date-tag vs non-date and unparseable input', () => {
    expect(compareVersions('main_2026-06-02_03', '1.2.0')).toBeNull()
    expect(compareVersions('latest', 'abc123')).toBeNull()
    expect(compareVersions(undefined, '1.0.0')).toBeNull()
  })

  it('handles long compound prefixes as one prefix', () => {
    expect(compareVersions('billing_main_2026-05-18_00', 'billing_main_2026-06-05_01')).toBe(-1)
  })
})
