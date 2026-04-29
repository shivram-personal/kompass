import { createContext, useContext, useEffect, useMemo, useState, ReactNode } from 'react'
import {
  resolveEffectiveTheme,
  isTheme,
  nextThemeForToggle,
  type Theme,
  type EffectiveTheme,
} from '@skyhook-io/k8s-ui/utils/theme'
import { apiUrl, getAuthHeaders, getCredentialsMode } from '../api/config'

// Re-export so existing radar-app imports of `Theme` from this module
// keep compiling. The canonical types now live in @skyhook-io/k8s-ui
// so the radar-hub-web Preferences page can bind to the same vocab.
export type { Theme, EffectiveTheme }

interface ThemeContextType {
  /** User's stored preference (may be 'system'). */
  theme: Theme
  /** What's actually applied right now ('dark' or 'light'). */
  effectiveTheme: EffectiveTheme
  setTheme: (theme: Theme) => void
  /**
   * Toggles between explicit dark and light based on what's currently
   * visible. Always writes an explicit ('dark' or 'light') value so a
   * subsequent OS theme change doesn't silently fight the user's
   * deliberate keyboard-shortcut choice. (SKY-823 bug 13)
   */
  toggleTheme: () => void
}

const ThemeContext = createContext<ThemeContextType | undefined>(undefined)

const THEME_STORAGE_KEY = 'radar-theme'

function readStoredTheme(): Theme {
  if (typeof window === 'undefined') return 'system'
  const stored = localStorage.getItem(THEME_STORAGE_KEY)
  return isTheme(stored) ? stored : 'system'
}

function readPrefersLight(): boolean {
  if (typeof window === 'undefined') return false
  return window.matchMedia('(prefers-color-scheme: light)').matches
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(readStoredTheme)
  const [prefersLight, setPrefersLight] = useState<boolean>(readPrefersLight)

  const effectiveTheme = useMemo(
    () => resolveEffectiveTheme(theme, prefersLight),
    [theme, prefersLight],
  )

  const setTheme = (newTheme: Theme) => {
    setThemeState(newTheme)
    localStorage.setItem(THEME_STORAGE_KEY, newTheme)
    fetch(apiUrl('/settings'), {
      method: 'PUT',
      credentials: getCredentialsMode(),
      headers: { 'Content-Type': 'application/json', ...getAuthHeaders() },
      body: JSON.stringify({ theme: newTheme }),
    }).then((res) => {
      if (!res.ok) console.warn('[settings] Failed to persist theme:', res.status)
    }).catch((err) => console.warn('[settings] Failed to persist theme:', err))
  }

  const toggleTheme = () => {
    setTheme(nextThemeForToggle(effectiveTheme))
  }

  // Apply effective theme to document
  useEffect(() => {
    document.documentElement.classList.toggle('dark', effectiveTheme === 'dark')
    document.documentElement.style.colorScheme = effectiveTheme
  }, [effectiveTheme])

  // Sync theme from server (persisted settings survive port changes in desktop app)
  useEffect(() => {
    fetch(apiUrl('/settings'), { credentials: getCredentialsMode(), headers: getAuthHeaders() })
      .then((res) => res.ok ? res.json() : null)
      .then((data) => {
        if (isTheme(data?.theme) && data.theme !== theme) {
          setThemeState(data.theme)
          localStorage.setItem(THEME_STORAGE_KEY, data.theme)
        }
      })
      .catch((err) => console.warn('[settings] Failed to load theme from server:', err))
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  // Track OS theme. In 'system' mode this updates `effectiveTheme`
  // immediately; in explicit dark/light mode the value is still
  // tracked so a future switch back to 'system' picks up the latest
  // OS state without a page refresh.
  useEffect(() => {
    const mediaQuery = window.matchMedia('(prefers-color-scheme: light)')
    const handleChange = (e: MediaQueryListEvent) => {
      setPrefersLight(e.matches)
    }
    mediaQuery.addEventListener('change', handleChange)
    return () => mediaQuery.removeEventListener('change', handleChange)
  }, [])

  return (
    <ThemeContext.Provider value={{ theme, effectiveTheme, setTheme, toggleTheme }}>
      {children}
    </ThemeContext.Provider>
  )
}

export function useTheme() {
  const context = useContext(ThemeContext)
  if (context === undefined) {
    throw new Error('useTheme must be used within a ThemeProvider')
  }
  return context
}
