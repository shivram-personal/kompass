import { FormEvent, useEffect, useRef, useState } from 'react'
import {
  ApplyResult,
  KompassApiError,
  KompassCluster,
  KompassUser,
  ProposalCard,
  ProposalPreview,
  applyProposal,
  fetchMe,
  listClusters,
  previewProposal,
  streamChat,
} from './api'

interface Msg {
  role: 'user' | 'assistant'
  content: string
}

type ProposalStatus = 'proposed' | 'previewing' | 'previewed' | 'confirming' | 'applying' | 'applied' | 'error'

interface ProposalState {
  card: ProposalCard
  status: ProposalStatus
  preview?: ProposalPreview
  result?: ApplyResult
  error?: string
}

// Dockable AI assistant. Streams the provider response and shows the active-model
// badge. The model can RECOMMEND a whitelisted change (a "proposal"); applying it
// is a separate, human-confirmed, server-audited step (Phase 6) — the model never
// executes anything itself.
export function ChatDock() {
  const [open, setOpen] = useState(false)
  const [clusters, setClusters] = useState<KompassCluster[]>([])
  const [clusterId, setClusterId] = useState('')
  const [messages, setMessages] = useState<Msg[]>([])
  const [proposals, setProposals] = useState<ProposalState[]>([])
  const [me, setMe] = useState<KompassUser | null>(null)
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [badge, setBadge] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    listClusters()
      .then((c) => {
        setClusters(c)
        setClusterId((prev) => prev || c[0]?.id || '')
      })
      .catch(() => setError('Could not load clusters.'))
    fetchMe()
      .then((r) => setMe(r?.user ?? null))
      .catch(() => setMe(null))
  }, [open])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [messages, proposals])

  function patchProposal(id: string, patch: Partial<ProposalState>) {
    setProposals((ps) => ps.map((p) => (p.card.id === id ? { ...p, ...patch } : p)))
  }

  // editor (in-scope for the proposal's cluster) or admin — mirrors server-side
  // enforcement; the server is still the authority (this only hides the control).
  function canApply(card: ProposalCard): boolean {
    if (!me) return false
    if (me.role === 'admin') return true
    return me.role === 'editor' && me.allowed_cluster_ids.includes(card.cluster_id)
  }

  async function doPreview(id: string) {
    patchProposal(id, { status: 'previewing', error: undefined })
    try {
      const preview = await previewProposal(id)
      patchProposal(id, { status: 'previewed', preview })
    } catch (e) {
      const msg = e instanceof KompassApiError ? e.message : 'Preview failed.'
      patchProposal(id, { status: 'error', error: msg })
    }
  }

  async function doApply(card: ProposalCard) {
    patchProposal(card.id, { status: 'applying', error: undefined })
    try {
      const result = await applyProposal(card.id, card.content_hash)
      patchProposal(card.id, { status: 'applied', result })
    } catch (e) {
      const msg = e instanceof KompassApiError ? e.message : 'Apply failed.'
      patchProposal(card.id, { status: 'error', error: msg })
    }
  }

  async function send(e: FormEvent) {
    e.preventDefault()
    if (!input.trim() || !clusterId || busy) return
    const message = input.trim()
    setInput('')
    setError(null)
    setMessages((m) => [...m, { role: 'user', content: message }, { role: 'assistant', content: '' }])
    setBusy(true)
    await streamChat(
      { cluster_id: clusterId, message },
      {
        onModel: (m) => setBadge(`${m.provider} · ${m.model}`),
        onDelta: (text) =>
          setMessages((m) => {
            const copy = [...m]
            copy[copy.length - 1] = { role: 'assistant', content: copy[copy.length - 1].content + text }
            return copy
          }),
        onProposal: (card) => setProposals((ps) => [...ps, { card, status: 'proposed' }]),
        onError: (msg) => setError(msg),
        onDone: () => setBusy(false),
      },
    )
    setBusy(false)
  }

  return (
    <>
      <div className="fixed bottom-4 left-4 z-50">
        {open && (
          <div className="mb-2 w-96 h-[28rem] flex flex-col rounded-2xl border border-theme-border bg-theme-surface shadow-xl">
            <div className="flex items-center justify-between gap-2 px-3 py-2 border-b border-theme-border">
              <span className="text-sm font-semibold text-theme-text-primary">Kompass AI</span>
              {badge && (
                <span className="text-[10px] px-2 py-0.5 rounded-full bg-emerald-500/10 text-emerald-500 border border-emerald-500/20">
                  {badge}
                </span>
              )}
              <select
                value={clusterId}
                onChange={(e) => setClusterId(e.target.value)}
                className="ml-auto text-xs rounded-md border border-theme-border bg-theme-bg px-1.5 py-1 text-theme-text-primary max-w-[9rem]"
              >
                <option value="" disabled>
                  cluster…
                </option>
                {clusters.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </select>
              <button onClick={() => setOpen(false)} className="text-theme-text-tertiary hover:text-theme-text-primary text-xs">
                ✕
              </button>
            </div>

            <div ref={scrollRef} className="flex-1 overflow-auto px-3 py-2 space-y-3">
              {messages.length === 0 && (
                <p className="text-xs text-theme-text-tertiary">
                  Ask about a cluster's health. The assistant can explain and recommend a change; you review a diff
                  and apply it yourself through an audited step.
                </p>
              )}
              {messages.map((m, i) => (
                <div key={i} className={m.role === 'user' ? 'text-right' : ''}>
                  <div
                    className={`inline-block rounded-xl px-3 py-2 text-sm whitespace-pre-wrap ${
                      m.role === 'user'
                        ? 'bg-emerald-500 text-white'
                        : 'bg-theme-bg border border-theme-border text-theme-text-primary'
                    }`}
                  >
                    {m.content || (busy ? '…' : '')}
                  </div>
                </div>
              ))}

              {proposals.map((p) => (
                <ProposalCardView
                  key={p.card.id}
                  state={p}
                  canApply={canApply(p.card)}
                  onPreview={() => doPreview(p.card.id)}
                  onConfirm={() => patchProposal(p.card.id, { status: 'confirming' })}
                  onCancel={() => patchProposal(p.card.id, { status: 'previewed' })}
                  onApply={() => doApply(p.card)}
                />
              ))}

              {error && <div className="text-xs text-red-500">{error}</div>}
            </div>

            <form onSubmit={send} className="flex items-center gap-2 p-2 border-t border-theme-border">
              <input
                value={input}
                onChange={(e) => setInput(e.target.value)}
                placeholder="Ask about this cluster…"
                disabled={busy || !clusterId}
                className="flex-1 rounded-lg border border-theme-border bg-theme-bg px-3 py-1.5 text-sm text-theme-text-primary outline-none focus:border-emerald-500"
              />
              <button
                type="submit"
                disabled={busy || !clusterId || !input.trim()}
                className="rounded-lg bg-emerald-500 hover:bg-emerald-600 disabled:opacity-50 text-white text-sm px-3 py-1.5"
              >
                Send
              </button>
            </form>
          </div>
        )}
        <button
          onClick={() => setOpen((v) => !v)}
          className="flex items-center gap-2 rounded-full border border-theme-border bg-theme-surface shadow px-3 py-1.5 text-sm text-theme-text-primary hover:border-emerald-500"
        >
          <img src="/images/kompass/kompass-icon.svg" alt="" aria-hidden className="w-4 h-4" />
          Ask AI
        </button>
      </div>
    </>
  )
}

// A proposed whitelisted action rendered as a card: recommend → preview diff →
// explicit confirm → apply. Apply is disabled (with a reason) unless the current
// user is authorized; the server enforces this regardless.
function ProposalCardView(props: {
  state: ProposalState
  canApply: boolean
  onPreview: () => void
  onConfirm: () => void
  onCancel: () => void
  onApply: () => void
}) {
  const { state, canApply, onPreview, onConfirm, onCancel, onApply } = props
  const { card, status, preview, result, error } = state
  const busy = status === 'previewing' || status === 'applying'

  return (
    <div className="rounded-xl border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-sm text-theme-text-primary">
      <div className="flex items-center gap-2">
        <span className="text-[10px] px-1.5 py-0.5 rounded bg-amber-500/15 text-amber-500 border border-amber-500/20">
          proposed action
        </span>
        <span className="font-mono text-xs">{card.action}</span>
        {!card.reversible && (
          <span className="text-[10px] text-amber-500" title="This action may be hard to reverse">
            ⚠ not easily reversible
          </span>
        )}
      </div>
      <div className="mt-1 text-xs text-theme-text-secondary">
        target <span className="font-mono">{card.target}</span>
      </div>

      {preview && (
        <div className="mt-2 rounded-lg bg-theme-bg border border-theme-border p-2 text-xs font-mono">
          <div className="text-red-500">- {preview.before}</div>
          <div className="text-emerald-500">+ {preview.after}</div>
        </div>
      )}

      {status === 'applied' && result && (
        <div className="mt-2 text-xs text-emerald-500">✓ applied — {result.after ?? 'done'}</div>
      )}
      {error && <div className="mt-2 text-xs text-red-500">{error}</div>}

      <div className="mt-2 flex items-center gap-2">
        {(status === 'proposed' || status === 'previewing' || status === 'error') && (
          <button
            onClick={onPreview}
            disabled={busy}
            className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-xs hover:border-emerald-500 disabled:opacity-50"
          >
            {status === 'previewing' ? 'Previewing…' : 'Preview diff'}
          </button>
        )}

        {status === 'previewed' && (
          <button
            onClick={onConfirm}
            disabled={!canApply}
            title={canApply ? 'Apply this change' : 'You are not authorized to apply changes to this cluster'}
            className="rounded-md bg-amber-500 hover:bg-amber-600 disabled:opacity-40 disabled:cursor-not-allowed text-white px-2 py-1 text-xs"
          >
            Apply…
          </button>
        )}

        {status === 'confirming' && (
          <>
            <span className="text-xs text-theme-text-secondary">Apply this exact change?</span>
            <button
              onClick={onApply}
              className="rounded-md bg-amber-600 hover:bg-amber-700 text-white px-2 py-1 text-xs"
            >
              Confirm
            </button>
            <button
              onClick={onCancel}
              className="rounded-md border border-theme-border bg-theme-bg px-2 py-1 text-xs hover:border-theme-text-tertiary"
            >
              Cancel
            </button>
          </>
        )}

        {status === 'applying' && <span className="text-xs text-theme-text-secondary">Applying…</span>}
      </div>
    </div>
  )
}
