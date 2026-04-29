import { describe, it, expect } from 'vitest'
import {
  resolveEffectiveTheme,
  isTheme,
  nextThemeForToggle,
} from './theme'

// SKY-823 bug 13: keyboard 't' toggled the theme but the Preferences
// "Theme" selector still showed "System" because the codebase had no
// 'system' value in the stored preference type. These tests pin the
// new contract: 'system' is now a first-class stored preference,
// resolved against the OS at render time, and the shortcut writes a
// deterministic explicit value so the selector stays in sync.

describe('resolveEffectiveTheme', () => {
  it('returns the explicit value when the user picks dark or light', () => {
    expect(resolveEffectiveTheme('dark', true)).toBe('dark')
    expect(resolveEffectiveTheme('dark', false)).toBe('dark')
    expect(resolveEffectiveTheme('light', true)).toBe('light')
    expect(resolveEffectiveTheme('light', false)).toBe('light')
  })

  it('follows the OS preference when the user picks system', () => {
    expect(resolveEffectiveTheme('system', true)).toBe('light')
    expect(resolveEffectiveTheme('system', false)).toBe('dark')
  })

  it('keeps the explicit choice stable across OS changes', () => {
    // Explicit "dark" must NOT silently flip to light when the OS
    // prefers-color-scheme media query changes. This is the contract
    // the keyboard shortcut depends on.
    expect(resolveEffectiveTheme('dark', true)).toBe('dark')
    expect(resolveEffectiveTheme('dark', false)).toBe('dark')
  })
})

describe('isTheme', () => {
  it('accepts dark/light/system', () => {
    expect(isTheme('dark')).toBe(true)
    expect(isTheme('light')).toBe(true)
    expect(isTheme('system')).toBe(true)
  })

  it('rejects garbage so server/disk reads can be safely narrowed', () => {
    expect(isTheme('auto')).toBe(false)
    expect(isTheme('Dark')).toBe(false)
    expect(isTheme('')).toBe(false)
    expect(isTheme(null)).toBe(false)
    expect(isTheme(undefined)).toBe(false)
    expect(isTheme(42)).toBe(false)
  })
})

describe('nextThemeForToggle', () => {
  it('flips dark and light to their opposite', () => {
    expect(nextThemeForToggle('dark')).toBe('light')
    expect(nextThemeForToggle('light')).toBe('dark')
  })

  it('always returns an explicit value (never "system")', () => {
    // The point of pressing the toggle key is to make a deliberate
    // change. Returning 'system' here would let a future OS switch
    // silently undo the user's intent — exactly the SKY-823 bug.
    const next = nextThemeForToggle('dark')
    expect(next === 'dark' || next === 'light').toBe(true)
  })
})
