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
