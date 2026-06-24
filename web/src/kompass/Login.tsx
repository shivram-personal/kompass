import { FormEvent, useState } from 'react'
import { useKompassAuth } from './AuthContext'
import { KompassApiError } from './api'

export function Login() {
  const { login } = useKompassAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(username, password)
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Login failed.')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="min-h-screen flex items-center justify-center bg-theme-bg p-4">
      <form
        onSubmit={onSubmit}
        className="w-full max-w-sm bg-theme-surface border border-theme-border rounded-2xl shadow-lg p-8 flex flex-col gap-5"
      >
        <div className="flex flex-col items-center gap-3">
          <img src="/images/kompass/kompass-icon.svg" alt="" aria-hidden className="w-12 h-12" />
          <h1 className="text-xl font-semibold tracking-tight text-theme-text-primary">Sign in to Kompass</h1>
        </div>

        {error && (
          <div role="alert" className="text-sm rounded-lg px-3 py-2 bg-red-500/10 text-red-500 border border-red-500/20">
            {error}
          </div>
        )}

        <label className="flex flex-col gap-1 text-sm text-theme-text-secondary">
          Username
          <input
            className="rounded-lg border border-theme-border bg-theme-bg px-3 py-2 text-theme-text-primary outline-none focus:border-emerald-500"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
          />
        </label>

        <label className="flex flex-col gap-1 text-sm text-theme-text-secondary">
          Password
          <input
            type="password"
            className="rounded-lg border border-theme-border bg-theme-bg px-3 py-2 text-theme-text-primary outline-none focus:border-emerald-500"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
          />
        </label>

        <button
          type="submit"
          disabled={busy}
          className="mt-1 rounded-lg bg-emerald-500 hover:bg-emerald-600 disabled:opacity-60 text-white font-medium py-2 transition-colors"
        >
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
