import { FormEvent, useCallback, useEffect, useState } from 'react'
import {
  deleteCluster,
  EnvTag,
  KompassApiError,
  KompassCluster,
  listClusters,
  registerCluster,
} from './api'

const ENV_TAGS: EnvTag[] = ['prod', 'staging', 'dev']

// Admin-only cluster registry management. The kubeconfig is sent once on
// register and immediately envelope-encrypted server-side; it is never read
// back (responses carry only non-secret metadata).
export function ClustersAdmin({ onClose }: { onClose: () => void }) {
  const [clusters, setClusters] = useState<KompassCluster[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setClusters(await listClusters())
      setError(null)
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Failed to load clusters.')
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
          <h2 className="text-lg font-semibold text-theme-text-primary">Cluster registry</h2>
          <button onClick={onClose} className="text-theme-text-tertiary hover:text-theme-text-primary text-sm">
            Close
          </button>
        </div>

        {error && (
          <div role="alert" className="mb-3 text-sm rounded-lg px-3 py-2 bg-red-500/10 text-red-500 border border-red-500/20">
            {error}
          </div>
        )}

        <RegisterForm onRegister={(input) => guard(() => registerCluster(input).then(() => undefined))} />

        <div className="mt-5 divide-y divide-theme-border">
          {loading && <p className="text-sm text-theme-text-tertiary py-3">Loading…</p>}
          {!loading && clusters.length === 0 && (
            <p className="text-sm text-theme-text-tertiary py-3">No clusters registered yet.</p>
          )}
          {clusters.map((c) => (
            <div key={c.id} className="py-3 flex flex-wrap items-center gap-3">
              <span className="font-medium text-theme-text-primary min-w-[10rem]">{c.name}</span>
              <span className="text-xs px-2 py-0.5 rounded-full border border-theme-border text-theme-text-secondary">
                {c.env_tag}
              </span>
              <span className="text-xs text-theme-text-tertiary">{c.context_name ?? '—'}</span>
              <code className="text-[11px] text-theme-text-tertiary">{c.id.slice(0, 8)}…</code>
              <button
                onClick={() => guard(() => deleteCluster(c.id))}
                className="ml-auto text-xs text-red-500 hover:text-red-600"
              >
                Remove
              </button>
            </div>
          ))}
        </div>
      </div>
    </div>
  )
}

function RegisterForm({ onRegister }: { onRegister: (input: { name: string; env_tag: EnvTag; kubeconfig: string }) => void }) {
  const [name, setName] = useState('')
  const [envTag, setEnvTag] = useState<EnvTag>('dev')
  const [kubeconfig, setKubeconfig] = useState('')

  function submit(e: FormEvent) {
    e.preventDefault()
    onRegister({ name, env_tag: envTag, kubeconfig })
    setName('')
    setEnvTag('dev')
    setKubeconfig('')
  }

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 bg-theme-bg border border-theme-border rounded-xl p-3">
      <div className="flex flex-wrap items-end gap-2">
        <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
          Display name
          <input
            className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
            value={name}
            onChange={(e) => setName(e.target.value)}
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
          Environment
          <select
            value={envTag}
            onChange={(e) => setEnvTag(e.target.value as EnvTag)}
            className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          >
            {ENV_TAGS.map((t) => (
              <option key={t} value={t}>
                {t}
              </option>
            ))}
          </select>
        </label>
      </div>
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Kubeconfig (encrypted at rest; never shown again)
        <textarea
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-xs font-mono text-theme-text-primary h-32"
          value={kubeconfig}
          onChange={(e) => setKubeconfig(e.target.value)}
          required
        />
      </label>
      <button type="submit" className="self-start rounded-md bg-emerald-500 hover:bg-emerald-600 text-white text-sm px-3 py-1.5">
        Register cluster
      </button>
    </form>
  )
}
