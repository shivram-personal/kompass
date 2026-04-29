/**
 * Theme preference + resolution. Lifted out of the radar-app
 * `ThemeContext` so the rules are unit-testable in this package
 * (where vitest runs in CI) and so external consumers — notably the
 * `radar-hub-web` Preferences page — can reuse the same vocabulary
 * instead of inventing their own.
 *
 * Background: previously `Theme = 'dark' | 'light'` only — there was
 * no way to express "follow the OS" in the stored preference. The
 * radar-hub Preferences page therefore had a `'System'` selector
 * option that didn't correspond to anything in the app's state, so
 * the keyboard shortcut `t` could toggle dark/light without the
 * selector ever updating. (SKY-823 bug 13)
 */

export type Theme = 'dark' | 'light' | 'system'
export type EffectiveTheme = 'dark' | 'light'

/**
 * Resolves a stored preference + the OS preference into the concrete
 * theme that should be applied. Pure.
 */
export function resolveEffectiveTheme(
  theme: Theme,
  prefersLight: boolean,
): EffectiveTheme {
  if (theme === 'system') return prefersLight ? 'light' : 'dark'
  return theme
}

/** Type guard / narrowing helper for stored values from disk or HTTP. */
export function isTheme(value: unknown): value is Theme {
  return value === 'dark' || value === 'light' || value === 'system'
}

/**
 * Computes the *next stored preference* for a "toggle theme"
 * keyboard shortcut. The toggle should always switch what the user
 * sees, so:
 *   - if currently effective dark → next is 'light' (explicit override)
 *   - if currently effective light → next is 'dark' (explicit override)
 *   - we deliberately do NOT preserve 'system' across a toggle:
 *     the user pressed the key to make a deliberate change; staying
 *     on 'system' would let a future OS switch silently undo their
 *     intent.
 */
export function nextThemeForToggle(effective: EffectiveTheme): Theme {
  return effective === 'dark' ? 'light' : 'dark'
}
