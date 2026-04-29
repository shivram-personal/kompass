import { describe, it, expect } from 'vitest'
import { computeUserInitials } from './user-initials'

// Pinning the SKY-825 bug 41 contract: the previous implementation
// only looked at separator-split segments, so any username without a
// '.', '_', or '-' (e.g. "mkohli", "alice") produced empty initials
// and the UserMenu fell back to a generic silhouette icon. The new
// helper guarantees a non-empty, uppercase 1-2-letter label whenever
// there's a usable username.

describe('computeUserInitials', () => {
  it('uses segment initials when separators are present', () => {
    expect(computeUserInitials('mary.kohli')).toBe('MK')
    expect(computeUserInitials('mary_kohli')).toBe('MK')
    expect(computeUserInitials('mary-kohli')).toBe('MK')
  })

  it('caps segment initials at 2 even with many separators', () => {
    expect(computeUserInitials('a.b.c.d')).toBe('AB')
  })

  it('falls back to leading letters when no separators (the SKY-825 bug 41 fix)', () => {
    expect(computeUserInitials('mkohli')).toBe('MK')
    expect(computeUserInitials('alice')).toBe('AL')
  })

  it('returns a single letter for single-character usernames', () => {
    expect(computeUserInitials('a')).toBe('A')
  })

  it('strips the @-domain before computing', () => {
    expect(computeUserInitials('mary.kohli@example.com')).toBe('MK')
    expect(computeUserInitials('mkohli@example.com')).toBe('MK')
  })

  it('uppercases the result', () => {
    expect(computeUserInitials('alice')).toBe('AL')
    expect(computeUserInitials('ALICE')).toBe('AL')
    expect(computeUserInitials('aLiCe')).toBe('AL')
  })

  it('returns empty string for null/undefined/empty inputs (caller falls back to silhouette)', () => {
    expect(computeUserInitials(null)).toBe('')
    expect(computeUserInitials(undefined)).toBe('')
    expect(computeUserInitials('')).toBe('')
  })

  it('handles consecutive separators without producing empty segments', () => {
    expect(computeUserInitials('mary..kohli')).toBe('MK')
    expect(computeUserInitials('mary__kohli')).toBe('MK')
  })

  it('handles email-only usernames with @ as the first character', () => {
    expect(computeUserInitials('@example.com')).toBe('')
  })
})
