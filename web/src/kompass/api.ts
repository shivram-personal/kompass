// Client for kompass-core's own endpoints (/api/auth, /api/admin). These are
// NOT under the engine proxy prefix — they are core control-plane routes, so
// they bypass the apiBase singleton and hit the origin directly. The session
// cookie is HttpOnly and sent automatically; the CSRF token is held in memory
// and attached to state-changing requests.

export type Role = 'viewer' | 'editor' | 'admin'

export interface KompassUser {
  id: number
  username: string
  role: Role
  auth_source: string
  must_change_password: boolean
  locked: boolean
  allowed_cluster_ids: string[]
  daily_token_budget: number | null
}

export interface MeResponse {
  user: KompassUser
  csrf_token: string
}

let csrfToken = ''

export class KompassApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function req(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers)
  const method = (init.method ?? 'GET').toUpperCase()
  if (method !== 'GET' && method !== 'HEAD') {
    headers.set('Content-Type', 'application/json')
    if (csrfToken) headers.set('X-CSRF-Token', csrfToken)
  }
  return fetch(path, { credentials: 'same-origin', ...init, headers })
}

async function parse<T>(r: Response): Promise<T> {
  if (!r.ok) {
    let detail = r.statusText
    try {
      const body = await r.json()
      if (body && typeof body.detail === 'string') detail = body.detail
    } catch {
      /* non-JSON error body */
    }
    throw new KompassApiError(r.status, detail)
  }
  if (r.status === 204) return undefined as T
  return (await r.json()) as T
}

export async function fetchMe(): Promise<MeResponse | null> {
  const r = await req('/api/auth/me')
  if (r.status === 401) return null
  const data = await parse<MeResponse>(r)
  csrfToken = data.csrf_token
  return data
}

export async function login(username: string, password: string): Promise<MeResponse> {
  const data = await parse<MeResponse>(
    await req('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) }),
  )
  csrfToken = data.csrf_token
  return data
}

export async function logout(): Promise<void> {
  await req('/api/auth/logout', { method: 'POST' })
  csrfToken = ''
}

export async function changePassword(current_password: string, new_password: string): Promise<void> {
  await parse<void>(
    await req('/api/auth/change-password', {
      method: 'POST',
      body: JSON.stringify({ current_password, new_password }),
    }),
  )
}

// --- admin user management ---------------------------------------------------
export interface CreateUserInput {
  username: string
  password: string
  role: Role
  cluster_ids: string[]
  daily_token_budget: number | null
}

export async function listUsers(): Promise<KompassUser[]> {
  return parse<KompassUser[]>(await req('/api/admin/users'))
}

export async function createUser(input: CreateUserInput): Promise<KompassUser> {
  return parse<KompassUser>(await req('/api/admin/users', { method: 'POST', body: JSON.stringify(input) }))
}

export async function deleteUser(id: number): Promise<void> {
  await parse<void>(await req(`/api/admin/users/${id}`, { method: 'DELETE' }))
}

export async function setUserRole(id: number, role: Role): Promise<KompassUser> {
  return parse<KompassUser>(
    await req(`/api/admin/users/${id}/role`, { method: 'PATCH', body: JSON.stringify({ role }) }),
  )
}

export async function setUserClusters(id: number, cluster_ids: string[]): Promise<KompassUser> {
  return parse<KompassUser>(
    await req(`/api/admin/users/${id}/clusters`, { method: 'PUT', body: JSON.stringify({ cluster_ids }) }),
  )
}

// --- cluster registry --------------------------------------------------------
export type EnvTag = 'prod' | 'staging' | 'dev'

export interface KompassCluster {
  id: string
  name: string
  env_tag: EnvTag
  context_name: string | null
  created_by: string
  created_at: string
}

export async function listClusters(): Promise<KompassCluster[]> {
  return parse<KompassCluster[]>(await req('/api/clusters'))
}

export async function registerCluster(input: {
  name: string
  env_tag: EnvTag
  kubeconfig: string
}): Promise<KompassCluster> {
  return parse<KompassCluster>(await req('/api/clusters', { method: 'POST', body: JSON.stringify(input) }))
}

export async function deleteCluster(id: string): Promise<void> {
  await parse<void>(await req(`/api/clusters/${id}`, { method: 'DELETE' }))
}

// --- AI provider / model management ------------------------------------------
export interface KompassProvider {
  id: number
  provider: string
  enabled: boolean
  base_url: string | null
  active_model: string | null
  has_api_key: boolean
  api_key_masked: string | null
  configured_models: string[]
  updated_by: string
  updated_at: string
}

export interface ProviderModels {
  source: 'provider' | 'configured'
  models: string[]
  active_model: string | null
}

export async function listProviders(): Promise<KompassProvider[]> {
  return parse<KompassProvider[]>(await req('/api/admin/providers'))
}

export async function createProvider(input: {
  provider: string
  base_url?: string | null
  api_key?: string | null
  active_model?: string | null
  models?: string[]
}): Promise<KompassProvider> {
  return parse<KompassProvider>(await req('/api/admin/providers', { method: 'POST', body: JSON.stringify(input) }))
}

export async function updateProvider(
  provider: string,
  patch: Partial<{ enabled: boolean; base_url: string; api_key: string; active_model: string; models: string[] }>,
): Promise<KompassProvider> {
  return parse<KompassProvider>(
    await req(`/api/admin/providers/${provider}`, { method: 'PATCH', body: JSON.stringify(patch) }),
  )
}

export async function deleteProvider(provider: string): Promise<void> {
  await parse<void>(await req(`/api/admin/providers/${provider}`, { method: 'DELETE' }))
}

export async function listProviderModels(provider: string): Promise<ProviderModels> {
  return parse<ProviderModels>(await req(`/api/admin/providers/${provider}/models`))
}

// --- AI apply-action proposals (Phase 6) -------------------------------------
// A proposal is a whitelisted, human-confirmed, audited change. The model only
// *recommends* it (emitted as an SSE `proposal` event); nothing executes until a
// privileged user previews the diff and explicitly applies.
export interface ProposalCard {
  id: string
  action: string
  cluster_id: string
  target: string
  content_hash: string
  reversible: boolean
}

export interface ProposalPreview {
  id: string
  action: string
  cluster_id: string
  target: string
  content_hash: string
  before: string
  after: string
  expires_at: string
}

export interface ApplyResult {
  id: string
  result: string
  target: string
  cluster_id: string
  before: string | null
  after: string | null
}

export async function previewProposal(id: string): Promise<ProposalPreview> {
  return parse<ProposalPreview>(await req(`/api/ai/proposals/${id}/preview`))
}

export async function applyProposal(id: string, content_hash: string): Promise<ApplyResult> {
  return parse<ApplyResult>(
    await req(`/api/ai/proposals/${id}/apply`, { method: 'POST', body: JSON.stringify({ content_hash }) }),
  )
}

// --- AI chat (SSE streaming) --------------------------------------------------
export interface ChatHandlers {
  onModel?: (m: { provider: string; model: string }) => void
  onDelta?: (text: string) => void
  onUsage?: (u: { prompt_tokens: number; completion_tokens: number }) => void
  onProposal?: (p: ProposalCard) => void
  onError?: (message: string) => void
  onDone?: () => void
}

function _dispatchFrame(frame: string, h: ChatHandlers): void {
  let event = 'message'
  const dataLines: string[] = []
  for (const line of frame.split('\n')) {
    if (line.startsWith('event:')) event = line.slice(6).trim()
    else if (line.startsWith('data:')) dataLines.push(line.slice(5).replace(/^ /, ''))
  }
  const data = dataLines.join('\n')
  if (event === 'delta') h.onDelta?.(data)
  else if (event === 'model') { try { h.onModel?.(JSON.parse(data)) } catch { /* ignore */ } }
  else if (event === 'usage') { try { h.onUsage?.(JSON.parse(data)) } catch { /* ignore */ } }
  else if (event === 'proposal') { try { h.onProposal?.(JSON.parse(data)) } catch { /* ignore */ } }
  else if (event === 'error') h.onError?.(data)
  else if (event === 'done') h.onDone?.()
}

export async function streamChat(
  input: { cluster_id: string; message: string; provider?: string | null },
  handlers: ChatHandlers,
): Promise<void> {
  const resp = await req('/api/ai/chat', { method: 'POST', body: JSON.stringify(input) })
  if (resp.status === 403) { handlers.onError?.('You are not allowed to query this cluster.'); return }
  if (resp.status === 429) { handlers.onError?.('Daily AI token budget exhausted. Try again tomorrow or ask an admin to raise your budget.'); return }
  if (!resp.ok || !resp.body) {
    let detail = 'AI request failed.'
    try { const b = await resp.json(); if (b?.detail) detail = b.detail } catch { /* non-JSON */ }
    handlers.onError?.(detail)
    return
  }
  const reader = resp.body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buf += decoder.decode(value, { stream: true })
    let idx: number
    while ((idx = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, idx)
      buf = buf.slice(idx + 2)
      if (frame.trim()) _dispatchFrame(frame, handlers)
    }
  }
}
