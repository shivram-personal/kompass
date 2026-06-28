import { FormEvent, useCallback, useEffect, useState } from 'react'
import {
  createProvider,
  deleteProvider,
  KompassApiError,
  KompassProvider,
  listProviderModels,
  listProviders,
  updateProvider,
} from './api'

// Admin AI provider + model management (SPEC §4.4 / §7). API keys are sent once
// on add/rotate and immediately KMS-encrypted server-side; responses only ever
// show a masked …last4.
export function ProvidersAdmin({ onClose }: { onClose: () => void }) {
  const [providers, setProviders] = useState<KompassProvider[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  const reload = useCallback(async () => {
    setLoading(true)
    try {
      setProviders(await listProviders())
      setError(null)
    } catch (err) {
      setError(err instanceof KompassApiError ? err.message : 'Failed to load providers.')
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
          <h2 className="text-lg font-semibold text-theme-text-primary">AI providers &amp; models</h2>
          <button onClick={onClose} className="text-theme-text-tertiary hover:text-theme-text-primary text-sm">
            Close
          </button>
        </div>

        {error && (
          <div role="alert" className="mb-3 text-sm rounded-lg px-3 py-2 bg-red-500/10 text-red-500 border border-red-500/20">
            {error}
          </div>
        )}

        <AddProviderForm onAdd={(input) => guard(() => createProvider(input).then(() => undefined))} />

        <div className="mt-5 space-y-3">
          {loading && <p className="text-sm text-theme-text-tertiary py-3">Loading…</p>}
          {!loading && providers.length === 0 && (
            <p className="text-sm text-theme-text-tertiary py-3">No providers configured yet.</p>
          )}
          {providers.map((p) => (
            <ProviderRow
              key={p.id}
              provider={p}
              onToggle={() => guard(() => updateProvider(p.provider, { enabled: !p.enabled }).then(() => undefined))}
              onActiveModel={(m) => guard(() => updateProvider(p.provider, { active_model: m }).then(() => undefined))}
              onRotate={(k) => guard(() => updateProvider(p.provider, { api_key: k }).then(() => undefined))}
              onDelete={() => guard(() => deleteProvider(p.provider))}
              onError={setError}
            />
          ))}
        </div>
      </div>
    </div>
  )
}

function ProviderRow({
  provider,
  onToggle,
  onActiveModel,
  onRotate,
  onDelete,
  onError,
}: {
  provider: KompassProvider
  onToggle: () => void
  onActiveModel: (m: string) => void
  onRotate: (key: string) => void
  onDelete: () => void
  onError: (m: string) => void
}) {
  const [models, setModels] = useState<string[]>(provider.configured_models)
  const [rotateKey, setRotateKey] = useState('')

  async function loadModels() {
    try {
      const res = await listProviderModels(provider.provider)
      setModels(res.models)
    } catch (err) {
      onError(err instanceof KompassApiError ? err.message : 'Could not list models.')
    }
  }

  return (
    <div className="border border-theme-border rounded-xl p-3">
      <div className="flex flex-wrap items-center gap-3">
        <span className="font-medium text-theme-text-primary min-w-[7rem]">{provider.provider}</span>
        <button
          onClick={onToggle}
          className={`text-xs px-2 py-0.5 rounded-full border ${
            provider.enabled ? 'border-emerald-500 text-emerald-500' : 'border-theme-border text-theme-text-tertiary'
          }`}
        >
          {provider.enabled ? 'enabled' : 'disabled'}
        </button>
        <span className="text-xs text-theme-text-tertiary">
          key: {provider.api_key_masked ?? '(none)'}
        </span>
        <button onClick={loadModels} className="text-xs text-emerald-500 hover:text-emerald-600">
          Load models
        </button>
        <select
          value={provider.active_model ?? ''}
          onChange={(e) => onActiveModel(e.target.value)}
          className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-sm text-theme-text-primary"
        >
          <option value="" disabled>
            active model…
          </option>
          {models.map((m) => (
            <option key={m} value={m}>
              {m}
            </option>
          ))}
        </select>
        <button onClick={onDelete} className="ml-auto text-xs text-red-500 hover:text-red-600">
          Remove
        </button>
      </div>
      <div className="mt-2 flex items-center gap-2">
        <input
          type="password"
          placeholder="rotate API key…"
          value={rotateKey}
          onChange={(e) => setRotateKey(e.target.value)}
          className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-xs text-theme-text-primary w-64"
        />
        <button
          onClick={() => {
            onRotate(rotateKey)
            setRotateKey('')
          }}
          disabled={!rotateKey}
          className="text-xs text-emerald-500 hover:text-emerald-600 disabled:opacity-50"
        >
          Rotate
        </button>
      </div>
    </div>
  )
}

function AddProviderForm({
  onAdd,
}: {
  onAdd: (input: { provider: string; base_url: string | null; api_key: string | null; models: string[] }) => void
}) {
  const [provider, setProvider] = useState('')
  const [baseUrl, setBaseUrl] = useState('')
  const [apiKey, setApiKey] = useState('')

  function submit(e: FormEvent) {
    e.preventDefault()
    onAdd({ provider, base_url: baseUrl || null, api_key: apiKey || null, models: [] })
    setProvider('')
    setBaseUrl('')
    setApiKey('')
  }

  return (
    <form onSubmit={submit} className="flex flex-wrap items-end gap-2 bg-theme-bg border border-theme-border rounded-xl p-3">
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Provider
        <input
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          placeholder="anthropic"
          value={provider}
          onChange={(e) => setProvider(e.target.value)}
          required
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        Base URL (optional)
        <input
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          value={baseUrl}
          onChange={(e) => setBaseUrl(e.target.value)}
        />
      </label>
      <label className="flex flex-col gap-1 text-xs text-theme-text-secondary">
        API key
        <input
          type="password"
          className="rounded-md border border-theme-border bg-theme-surface px-2 py-1 text-sm text-theme-text-primary"
          value={apiKey}
          onChange={(e) => setApiKey(e.target.value)}
        />
      </label>
      <button type="submit" className="rounded-md bg-emerald-500 hover:bg-emerald-600 text-white text-sm px-3 py-1.5">
        Add provider
      </button>
    </form>
  )
}
