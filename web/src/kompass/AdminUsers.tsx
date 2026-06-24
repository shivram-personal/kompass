import { FormEvent, useCallback, useEffect, useState } from 'react'
import {
  createUser,
  deleteUser,
  KompassApiError,
  KompassUser,
  listUsers,
  Role,
  setUserClusters,
  setUserRole,
} from './api'

const ROLES: Role[] = ['viewer', 'editor', 'admin']

export function AdminUsers({ onClose }: { onClose: () => void }) {
  const [users, setUsers] = useState<KompassUser[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setUsers(await listUsers())
      setError(null)
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Failed to load users.')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void reload()
  }, [reload])

  async function guard(fn: () => Promise<void>) {
    try {
      await fn()
      await reload()
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Action failed.')
    }
  }

  return (
    <div className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4" onClick={onClose}>
      <div
        className="w-full max-w-3xl max-h-[85vh] overflow-auto bg-theme-surface border border-theme-border rounded-2xl shadow-xl p-6"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between mb-4">
          <h2 className="text-lg font-semibold text-theme-text-primary">User management</h2>
          <button onClick={onClose} className="text-theme-text-tertiary hover:text-theme-text-primary text-sm">
            Close
          </button>
        </div>

        {error && (
          <div role="alert" className="mb-3 text-sm rounded-lg px-3 py-2 bg-red-500/10 text-red-500 border border-red-500/20">
            {error}
          </div>
        )}

        <CreateUserForm onCreate={(input) => guard(() => createUser(input).then(() => undefined))} />

        <div className="mt-5 divide-y divide-theme-border">
          {loading && <p className="text-sm text-theme-text-tertiary py-3">Loading…</p>}
          {users.map((u) => (
            <div key={u.id} className="py-3 flex flex-wrap items-center gap-3">
              <span className="font-medium text-theme-text-primary min-w-[8rem]">{u.username}</span>
              <select
                value={u.role}
                onChange={(e) => guard(() => setUserRole(u.id, e.target.value as Role).then(() => undefined))}
                className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-sm text-theme-text-primary"
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
              <ClusterEditor
                user={u}
                onSave={(ids) => guard(() => setUserClusters(u.id, ids).then(() => undefined))}
              />
              <span className="text-xs text-theme-text-tertiary">{u.auth_source}</span>
              <button
                onClick={() => guard(() => deleteUser(u.id))}
                className="ml-auto text-xs text-red-500 hover:text-red-600"
              >
                Delete
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function ClusterEditor({ user, onSave }: { user: KompassUser; onSave: (ids: string[]) => void }) {
  const [value, setValue] = useState(user.allowed_cluster_ids.join(', '))
  if (user.role !== 'editor') {
    return <span className="text-xs text-theme-text-tertiary italic">scope n/a</span>
  }
  return (
    <span className="flex items-center gap-1">
      <input
        className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-xs text-theme-text-primary w-48"
        placeholder="cluster ids, comma-separated"
        value={value}
        onChange={(e) => setValue(e.target.value)}
      />
      <button
        onClick={() =>
          onSave(
            value
              .split(',')
              .map((s) => s.trim())
              .filter(Boolean),
          )
        }
        className="text-xs text-emerald-500 hover:text-emerald-600"
      >
        Save
      </button>
    </span>
  )
}

function CreateUserForm({ onCreate }: { onCreate: (input: { username: string; password: string; role: Role; cluster_ids: string[]; daily_token_budget: number | null }) => void }) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [role, setRole] = useState<Role>('viewer')

  function submit(e: FormEvent) {
    e.preventDefault()
    onCreate({ username, password, role, cluster_ids: [], daily_token_budget: null })
    setUsername('')
    setPassword('')
    setRole('viewer')
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-end gap-2 bg-theme-bg border border-theme-border rounded-xl p-3">
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Username
        <input
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          value={username}
          onChange={(e) => setUsername(e.target.value)}
          required
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Temp password
        <input
          type="password"
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          required
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Role
        <select
          value={role}
          onChange={(e) => setRole(e.target.value as Role)}
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
        >
          {ROLES.map((r) => (
            <option key={r} value={r}>
              {r}
            </option>
          ))}
        </select>
      </label>
      <button type="submit" className="rounded-md bg-emerald-500 hover:bg-emerald-600 text-white text-sm px-3 py-1.5">
        Add user
      </button>
    </form>
  )
}
