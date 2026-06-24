import { FormEvent, useState } from 'react'
import { useKompassAuth } from './AuthContext'
import { changePassword, KompassApiError } from './api'

const MIN_LENGTH = 12

export function ChangePassword() {
  const { refresh, logout } = useKompassAuth()
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  async function onSubmit(e: FormEvent) {
    e.preventDefault()
    setError(null)
    if (next.length < MIN_LENGTH) {
      setError(`New password must be at least ${MIN_LENGTH} characters.`)
      return
    }
    if (next !== confirm) {
      setError('New password and confirmation do not match.')
      return
    }
    setBusy(true)
    try {
      await changePassword(current, next)
      await refresh()
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Could not change password.')
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
        <div className="flex flex-col items-center gap-2">
          <img src="/images/kompass/kompass-icon.svg" alt="" aria-hidden className="w-10 h-10" />
          <h1 className="text-lg font-semibold tracking-tight text-theme-text-primary">Set a new password</h1>
          <p className="text-xs text-theme-text-tertiary text-center">
            You must change your password before continuing.
          </p>
        </div>

        {error && (
          <div role="alert" className="text-sm rounded-lg px-3 py-2 bg-red-500/10 text-red-500 border border-red-500/20">
            {error}
          </div>
        )}

        {[
          { label: 'Current password', value: current, set: setCurrent, ac: 'current-password' },
          { label: 'New password', value: next, set: setNext, ac: 'new-password' },
          { label: 'Confirm new password', value: confirm, set: setConfirm, ac: 'new-password' },
        ].map((f) => (
          <label key={f.label} className="flex flex-col gap-1 text-sm text-theme-text-secondary">
            {f.label}
            <input
              type="password"
              className="rounded-lg border border-theme-border bg-theme-bg px-3 py-2 text-theme-text-primary outline-none focus:border-emerald-500"
              value={f.value}
              onChange={(e) => f.set(e.target.value)}
              autoComplete={f.ac}
              required
            />
          </label>
        ))}

        <button
          type="submit"
          disabled={busy}
          className="mt-1 rounded-lg bg-emerald-500 hover:bg-emerald-600 disabled:opacity-60 text-white font-medium py-2 transition-colors"
        >
          {busy ? 'Saving…' : 'Change password'}
        </button>
        <button
          type="button"
          onClick={() => void logout()}
          className="text-xs text-theme-text-tertiary hover:text-theme-text-secondary"
        >
          Sign out instead
        </button>
      </form>
    </div>
  )
}
