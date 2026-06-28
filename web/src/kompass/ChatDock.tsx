import { FormEvent, useEffect, useRef, useState } from 'react'
import { KompassCluster, listClusters, streamChat } from './api'

interface Msg {
  role: 'user' | 'assistant'
  content: string
}

// Dockable, recommendation-only AI assistant. Streams the provider response and
// shows the active-model badge. It can only read + recommend — there is no
// apply/mutation control here (that path arrives in a later phase).
export function ChatDock() {
  const [open, setOpen] = useState(false)
  const [clusters, setClusters] = useState<KompassCluster[]>([])
  const [clusterId, setClusterId] = useState('')
  const [messages, setMessages] = useState<Msg[]>([])
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
  }, [open])

  useEffect(() => {
    scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight })
  }, [messages])

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
                  Ask about a cluster's health. The assistant can explain and recommend — it cannot make changes.
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
