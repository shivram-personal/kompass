# Project Specification — "Kompass"
### An AI-augmented multi-cluster Kubernetes operations console

**Document version:** 2.0
**Audience:** Coding agents (Google Antigravity / Claude Code) and human reviewers
**Status:** Build-ready specification

---

## 0. How to read this document

This is the authoritative spec. Build strictly against it. Where this document and your own assumptions disagree, this document wins. Where this document is silent, ask before inventing behavior. Every phase has explicit **exit criteria** and **containerized test gates** — do not advance to the next phase until the current phase's tests pass inside a container.

Companion documents:
- `DEPLOYMENT_GKE.md` — how to ship the finished app to GKE.
- `AGENTS.md` (repo root) — persistent rules the coding agent must obey on every task. Mirrored to `CLAUDE.md` and `.antigravity/rules.md`.
- `KICKOFF.md` — exact steps and prompts to start the build.

---

## 1. Product summary

### 1.1 What we are building
**Kompass** — an internal, in-cluster web application giving DevOps engineers a single pane of glass to **monitor multiple Kubernetes clusters across environments**, with an **AI layer** that investigates issues from natural-language queries, explains them in context, and proposes fixes applied through a confirmed, whitelisted, audited action path.

### 1.2 Foundation & the two-container architecture
Kompass is built on a mature open-source Kubernetes-visibility engine (Apache-2.0 licensed, Go + React/TypeScript) that already provides topology, resource browsing, event timeline, logs, exec, Helm, GitOps, multi-cluster switching, informer-based caching, an SSE broadcaster, a Prometheus client, and an LLM-friendly context-minification layer. **We reuse this engine rather than rebuilding it.**

Because the team maintaining Kompass works in **Python, not Go**, we do **not** add our features inside the Go engine. Instead Kompass runs as a **two-container pod**:

```
┌────────────────────────── Kompass Pod ──────────────────────────┐
│                                                                  │
│   ┌──────────────────────┐         ┌──────────────────────────┐ │
│   │  kompass-core (NEW)   │         │  kompass-engine          │ │
│   │  Python / FastAPI     │         │  (Go engine, rebranded,  │ │
│   │                       │         │   near-upstream)         │ │
│   │  • Auth gateway       │  HTTP   │                          │ │
│   │  • Sessions & roles   │ ──────► │  • Topology, resources,  │ │
│   │  • Cluster registry   │ (loop-  │    timeline, logs, exec  │ │
│   │  • AI orchestration   │  back)  │  • Helm, GitOps          │ │
│   │  • Apply whitelist    │         │  • Multi-cluster switch  │ │
│   │  • Audit log          │         │  • Prometheus client     │ │
│   │  • Model management   │         │  • Context minifier      │ │
│   │  • Token budgets      │         │  • Write handlers        │ │
│   │  • Serves UI shell    │         │    (scale/restart/etc.)  │ │
│   └──────────┬───────────┘         └────────────┬─────────────┘ │
│              │                                   │               │
└──────────────┼───────────────────────────────────┼──────────────┘
   Browser ◄───┘ (all traffic enters here)          │
                                                     ▼
                                            K8s API (per cluster),
                                            Prometheus/GMP
```

**Traffic model:** the browser only ever talks to **kompass-core** (Python). Core is the authentication gateway and authorization boundary. For cluster reads, Core proxies to the engine over pod-local loopback. For whitelisted writes, Core calls the engine's existing write handlers. The engine is **not** exposed outside the pod.

This keeps every security-critical, business-logic, and AI component in **Python** (your team owns and reviews it), and keeps the Go engine close to upstream so it rebases cleanly.

### 1.3 Branding & licensing (read carefully)
- **The app is fully rebranded as Kompass.** No user-facing text, logo, page title, favicon, About screen, HTTP header, or visible identifier references the upstream engine. The UI is Kompass throughout.
- **The upstream engine is Apache-2.0.** That license legally **requires** retaining, in the source distribution, the `LICENSE` file, any `NOTICE` file, and source-code copyright headers. These are **not user-facing** (they live in the repo and source files) and must remain. Keep a `THIRD_PARTY_NOTICES` / `NOTICE` file at the repo root listing the upstream project and its Apache-2.0 license. **Do not remove or alter these legal attribution artifacts** — doing so violates the license. The rule is: *rebrand everything users see; preserve the legal notices users never see.*

### 1.4 What the engine already gives us (do not rebuild — and do not drop)
All existing engine features must remain available in Kompass (rebranded):
- Cluster **topology** graph (resources + traffic modes), **resource browser** (all kinds incl. CRDs), **event timeline**, **logs**, **exec/terminal**, **image filesystem viewer**.
- **Helm** management (install/upgrade/rollback/uninstall, values, history).
- **GitOps** (FluxCD & ArgoCD) views and actions.
- **TLS certificate** expiry views, **Cluster Audit** best-practice scanner, **Cost Insights** (OpenCost), **Traffic** visualization (Hubble/Caretta/Istio).
- **Multi-cluster context switching**, keyboard shortcuts, command palette, dark/light themes.
- Informer-based caching, SSE real-time push, Prometheus auto-discovery, the context-minification layer, and the existing write handlers.

These are inherited by reusing the engine; the build must **preserve and surface all of them in the Kompass UI**, not silently disable them.

### 1.5 What Kompass adds (the work, all in Python/React)
1. **Local username/password auth** + **optional Google SSO (OIDC)** — three roles (viewer / editor / admin), editor scoped per-cluster.
2. **AI chat + per-component troubleshooting** with streamed responses and a confirmed, whitelisted, audited **apply-action** path.
3. **AI model management** — providers (any provider; org permits all), a **model/version picker per provider**, encrypted API keys (admin only).
4. **Token-usage + cost dashboard** and **configurable per-user daily token budgets** (admin only).
5. **Audit log** (30-day, admin-viewable, CSV/JSON export).
6. **Nodes view enhancements** (read-only): pods-per-node and summed pod CPU/memory **requests** per node.
7. **2-week event retention** (engine timeline config) and **on-demand metrics** via Prometheus/GMP (no custom TSDB).

---

## 2. Non-negotiable principles

1. **Python owns the brain; Go stays near-upstream.** All new logic lives in `kompass-core` (Python). The Go engine is touched only for (a) rebranding and (b) binding it to loopback. No new business logic in Go.
2. **Rebaseable engine.** Engine edits are limited to the seams in §3.3. Keep them surgical so `git rebase upstream/main` stays clean.
3. **Read-only by default; mutation is privileged and gated.** Every mutating path requires (a) editor/admin role, (b) per-cluster authorization for editors, (c) explicit user confirmation with a rendered diff, and (d) an audit record written before execution.
4. **AI never executes raw commands.** The LLM only emits a **structured action proposal** mapping to an existing engine write handler on a server-side whitelist. No free-form `kubectl`, no shelling out from model output.
5. **Secrets never leave in plaintext.** API keys and uploaded kubeconfigs are KMS-envelope-encrypted at rest. Plaintext secrets are never logged, returned to the frontend, or sent to an LLM.
6. **Data minimization to LLMs.** Cluster context is redacted before any model call. Org permits all providers, but redaction of secrets/tokens/PII is still mandatory.
7. **Every phase is test-gated in a container** mirroring the GKE runtime (§11).
8. **Full rebrand, preserved legal notices** (§1.3).

---

## 3. Architecture detail

### 3.1 kompass-core (Python — the new service)
**Stack:** Python 3.12+, FastAPI, Uvicorn, SQLAlchemy + SQLite, `httpx` (to engine + LLM providers), `argon2-cffi` (password hashing), `authlib` (OIDC), `google-cloud-kms`, `pydantic` v2 (validation), `sse-starlette` (streaming). Tests: `pytest`, `httpx` test client, `testcontainers`/kind.

**Modules (`kompass_core/`):**
| Module | Responsibility |
|---|---|
| `auth/` | Local users, Argon2id hashing, sessions, **OIDC/Google SSO**, role + per-cluster authorization middleware, bootstrap admin. |
| `clusters/` | Cluster registry: store/list/select/remove clusters; encrypted kubeconfig storage; health. |
| `ai/` | Chat & troubleshoot orchestration: pull minified context from the engine, redact, call provider, stream via SSE, parse structured proposals. |
| `ai/providers/` | Provider abstraction (any provider) with a **model/version picker**; token accounting. |
| `ai/whitelist.py` | Validate proposals; map each allowed action to an engine write handler. |
| `audit/` | Append-only audit store, 30-day retention job, CSV/JSON export. |
| `budgets/` | Per-user daily token budgets (admin-configurable defaults + per-user overrides) and enforcement. |
| `secrets/` | GCP KMS envelope encryption helpers. |
| `engine/` | Typed client to the Go engine (read proxy + write-handler calls) over loopback. |
| `gateway/` | Reverse-proxy/auth-gateway: authenticate, authorize, then proxy engine reads and serve the UI shell. |
| `nodestats/` | If not exposed by the engine, compute pods-per-node + summed requests from engine resource data. (Prefer doing the aggregation in Python over engine JSON to avoid Go edits.) |

### 3.2 Frontend (React/TS — extends the engine's existing web app)
Same stack as the engine (React 19, Tailwind v4, shadcn/ui, TanStack Query). New areas under `web/src/`:
| Area | Responsibility |
|---|---|
| `components/auth/` | Login (local + "Sign in with Google"), forced first-login password change, session handling. |
| `components/ai/` | Global chat dock + per-resource Troubleshoot tab; streamed messages; action-proposal cards with diff + confirm; active-model badge. |
| `components/admin/` | User management, model/provider management + model picker, token/cost dashboard, budget config, audit-log viewer + export. |
| `components/nodes/` | Pods-per-node and summed-requests columns in the Nodes view. |
| `branding/` | Kompass name, logo, favicon, theme tokens, page titles — replacing all upstream branding. |

All admin surfaces gated via the existing capabilities-context pattern (server-side enforced regardless).

### 3.3 Allowed edits to the Go engine (surgical, rebrand + bind only)
1. **Bind to loopback** so the engine is reachable only by kompass-core within the pod (config/flag; no code logic change ideally).
2. **Rebranding:** replace user-facing strings, logo/favicon/assets, page title, and any visible product identifiers with Kompass equivalents. Add Kompass NOTICE handling. Do **not** alter LICENSE/NOTICE/copyright headers (§1.3).
3. Optionally enable existing engine flags (e.g. sqlite timeline storage, disable-exec where policy requires).
**No new business logic, no auth, no AI code in Go.** If a needed capability seems to require Go logic, prefer doing it in Python against the engine's existing API; if truly impossible, **stop and ask**.

---

## 4. Feature specifications

### 4.1 Authentication & authorization (`kompass_core/auth/`)

**Roles**
- **viewer** — read-only across all registered clusters. No mutations, no admin pages. May use AI chat/troubleshoot in *recommendation-only* mode (apply disabled and server-rejected).
- **editor** — viewer capabilities **plus** applying whitelisted actions, but **only on clusters in their `allowed_cluster_ids`**. No admin pages.
- **admin** — full access: all clusters, all actions, AI model & key config, user management, token/cost dashboard, **budget configuration**, audit view + export.

**Per-cluster editor scoping** — enforced **server-side** in kompass-core middleware: every mutating request carries a target `cluster_id`; reject 403 if not in the editor's allowed set. UI hiding is cosmetic only.

**Local auth** — Argon2id (memory 64 MiB, iterations 3, parallelism 1, 32-byte output, 16-byte salt). No plaintext stored/logged. Lockout after 5 failed attempts for 15 min (configurable).

**Google SSO (OIDC)** — admin-configurable in Settings: client ID/secret (secret KMS-encrypted), allowed hosted domain(s), and **role mapping** (default new SSO users to viewer; admin can elevate). Local and SSO coexist; SSO is additive so users can be migrated gradually. An SSO login still maps to a Kompass user record (created on first login if the domain is allowed) so roles, per-cluster scoping, budgets, and audit all work identically.

**Bootstrap admin** — on first start with an empty user table, generate a strong random password, create user `admin`, print credentials **once** to the core container log (clearly marked). Force change on first login before any other action.

**Sessions** — server-side tokens (256-bit), stored hashed; `HttpOnly`/`Secure`/`SameSite=Strict` cookies; idle + absolute expiry; CSRF protection on all state-changing requests.

**Acceptance criteria**
- Bootstrap admin runs exactly once.
- viewer cannot mutate (API returns 403, not merely hidden buttons).
- editor mutates assigned clusters only; 403 elsewhere.
- admin-only endpoints 403 for viewer/editor.
- Local Argon2id verify + lockout work; **SSO login creates/maps a user and respects role + scoping**.

### 4.2 Cluster registry & kubeconfig management (`kompass_core/clusters/`, `secrets/`)
- Admin adds a cluster by uploading/pasting a kubeconfig with a friendly name + environment tag (`prod`/`staging`/`dev`).
- Kubeconfig **KMS-envelope-encrypted at rest**; plaintext never returned to frontend or logged.
- Prefer short-lived creds: document/encourage GKE Workload Identity over long-lived static keys; where static keys are used, scope target-cluster RBAC read-only by default.
- Cluster switching mirrors the engine's context-switch UX; health surfaced. Stable `cluster_id` used by auth scoping, audit, and AI targeting.
- The engine performs the actual multi-cluster connection; kompass-core supplies the (decrypted-in-memory) kubeconfig/context to the engine over loopback per request/selection, and is the source of truth for *which* clusters exist and *who* may write to them.

**Acceptance:** add→encrypted-at-rest (verify ciphertext in DB)→connect→list; remove purges credential; kubeconfig never in any response/log (scanned).

### 4.3 AI chat & troubleshooting (`kompass_core/ai/`)
**Entry points:** (1) global chat aware of selected cluster/namespace; (2) per-component Troubleshoot tab pre-loaded with a resource's context.

**Pipeline:** gather minified context from the engine (topology/health/deduped events/filtered logs) + on-demand Prometheus metrics → **redact** (`ai/redact.py`: secret values, tokens/bearer strings, connection strings, emails, configurable regexes) → build prompt with strict system instruction → call selected provider via `ai/providers/` → **stream** to browser via SSE → record usage (`ai_usage`) and **decrement the user's daily token budget**.

**Structured action contract (the safety boundary):** when recommending a change the model returns:
```json
{ "proposal": { "action": "scale_deployment", "cluster_id": "…", "namespace": "…",
  "target": "deployment/foo", "params": { "replicas": 3 }, "rationale": "…", "reversible": true } }
```
kompass-core validates against the server-side whitelist (`ai/whitelist.py`); each allowed action maps 1:1 to an **existing engine write handler**. Unknown/malformed/out-of-whitelist proposals are rejected and never surface an apply button.

**v1 whitelist:** `scale_deployment`, `restart_workload`, `helm_rollback`, `gitops_reconcile`, `gitops_suspend_resume`, `cordon_node`/`uncordon_node`. **Excluded v1:** any `delete`, `exec`, raw `patch`, arbitrary YAML apply, secret edits.

**Apply flow:** editor/admin role → per-cluster auth → UI diff + explicit confirm → **audit row written before execution** → call engine handler → record result.

**Active-model badge** on every AI response (all roles).

**Acceptance:** non-whitelisted proposal can never apply; redaction removes seeded secrets; streaming works; every apply yields exactly one audit row written before mutation; usage recorded and budget decremented for every call.

### 4.4 AI model management (`kompass_core/ai/providers/`, admin only)
- Enable/disable any provider; **model/version picker per provider** (the model changer you requested); per-provider API config (base URL, key, optional org/project).
- **Do not hardcode model lists.** Fetch from each provider's models endpoint where available; otherwise an **admin-editable** list. Names drift — never bake in.
- Keys KMS-encrypted; never returned (masked `…last4`); never logged.
- Clean provider abstraction so adding a provider is one adapter; a provider that fronts many models (returning per-request cost) may be used to simplify the cost dashboard.

**Acceptance:** switching active model changes badge + routing; keys masked everywhere and never logged; disabled provider unusable; **model picker lists and selects versions per provider**.

### 4.5 Token usage, cost & budgets (`kompass_core/ai/usage`, `budgets/`, admin only)
- Charts: usage over time and total per model; filter by provider/model/user/cluster/date.
- **Rough** cost = tokens × per-model price from an **admin-editable pricing table** (labelled "estimate"); prefer provider-reported cost where available.
- **Per-user daily token budget:** an **admin-configurable default in the UI**, plus per-user overrides. When a user hits their budget, AI calls are blocked for the day with a clear message; admins can raise/reset. All configurable from the admin UI (no code change).

**Acceptance:** dashboard reconciles with `ai_usage`; pricing edits recompute without code changes; **budget default editable in UI**, enforced, and overridable per user.

### 4.6 Audit log (`kompass_core/audit/`)
- Append-only `audit_events`: `id, ts, user, role, cluster_id, action, target, params_redacted, result, before_summary, after_summary, request_id`.
- Written for: applied AI actions, manual mutations routed through Kompass, user/role/cluster/model/key/budget config changes, logins/SSO logins/lockouts.
- Written **before** the mutation in the same path. 30-day retention via scheduled job. Admin-only viewer with filtering; **CSV + JSON export** from UI.

**Acceptance:** every mutating endpoint yields a row; export well-formed and matches filtered view; >30-day rows purged.

### 4.7 Nodes view enhancements (`kompass_core/nodestats/`, read-only)
- Per node: **pod count** scheduled, and **summed CPU & memory `requests`** across those pods' containers; optionally summed limits and requests-vs-allocatable %.
- Computed in Python from the engine's already-cached pod data grouped by `spec.nodeName`. **No new cluster permissions, no new data source, no Go edits.**

**Acceptance:** aggregates match `kubectl describe node` on a kind fixture; pods with no requests counted as zero; updates live via the engine's data path.

### 4.8 Metrics & event retention
- **Events:** configure the engine timeline for **14-day** retention (sqlite storage + time-based retention). Do not build a new store.
- **Metrics:** query Prometheus/GMP **on demand** via the engine, short-lived cache. No raw-sample storage, no TSDB. (Future: 1-min rollups only if required — out of scope v1.)

---

## 5. Data model (SQLite, owned by kompass-core)
- `users(id, username UNIQUE, password_hash NULLABLE, auth_source[local|oidc], oidc_subject NULLABLE, role, must_change_password, failed_attempts, locked_until, daily_token_budget NULLABLE, created_at, updated_at)`
- `user_clusters(user_id, cluster_id)`
- `sessions(token_hash, user_id, created_at, idle_expires_at, abs_expires_at)`
- `clusters(id, name, env_tag, kubeconfig_ciphertext, kms_key_ref, created_by, created_at)`
- `provider_config(id, provider, enabled, base_url, api_key_ciphertext, kms_key_ref, active_model, extra_json, updated_by, updated_at)`
- `oidc_config(enabled, client_id, client_secret_ciphertext, kms_key_ref, allowed_domains, default_role, updated_by, updated_at)`
- `model_pricing(provider, model, input_price_per_1k, output_price_per_1k, updated_by, updated_at)`
- `app_settings(key, value)` — incl. `default_daily_token_budget`
- `ai_usage(id, ts, user_id, cluster_id, provider, model, prompt_tokens, completion_tokens, est_cost, request_id)`
- `audit_events(…)` per §4.6

All `*_ciphertext` columns hold KMS-envelope-encrypted blobs. No plaintext secret stored.

---

## 6. API surface (kompass-core, Python/FastAPI)
All browser traffic hits kompass-core. Prefix `/api/`. Role-gated, CSRF-protected on writes, rate-limited on AI endpoints.
- `POST /api/auth/login`, `POST /api/auth/logout`, `POST /api/auth/change-password`, `GET /api/auth/me`
- `GET /api/auth/oidc/login`, `GET /api/auth/oidc/callback` — Google SSO
- `GET/POST/PATCH/DELETE /api/admin/users` (+ `/{id}/clusters`, `/{id}/budget`) — admin
- `GET/PATCH /api/admin/oidc` — admin
- `GET/POST/DELETE /api/clusters` (+ `/{id}/select`) — list all; mutate admin
- `POST /api/ai/chat` (SSE), `POST /api/ai/troubleshoot` (SSE)
- `POST /api/ai/proposals/{id}/apply` — editor/admin + per-cluster auth + budget check
- `GET/PATCH /api/admin/providers`, `GET /api/admin/providers/{p}/models` — admin (model picker)
- `GET/PATCH /api/admin/pricing`, `GET/PATCH /api/admin/budget-defaults` — admin
- `GET /api/admin/usage` — admin
- `GET /api/admin/audit`, `GET /api/admin/audit/export?format=csv|json` — admin
- `GET /api/nodes/stats` — read-only aggregates
- `ANY /api/engine/*` — authenticated, authorized **reverse proxy** to the Go engine for all inherited read features (topology, resources, timeline, logs, helm, gitops, etc.)

The `/api/engine/*` proxy is how all the engine's existing features remain available while staying behind the Python auth gateway.

---

## 7. UI / UX requirements (this must WOW)
Stack: React 19 + Tailwind v4 + shadcn/ui + TanStack Query. **Read `/mnt/skills/public/frontend-design/SKILL.md` before building any new component.**

**Principles**
- **Preserve every inherited feature**, fully rebranded as Kompass; nothing user-facing references the upstream project.
- **Seamless AI:** persistent dockable chat + inline Troubleshoot tab; streamed responses with typing indicator; markdown + copy-able code/YAML; ever-present active-model badge.
- **Action proposals as first-class cards:** human summary, real before/after **diff**, reversibility indicator, single primary **Apply** → confirm dialog. Disabled with tooltip for viewers / unauthorized clusters.
- **Coherent design language:** one spacing/type scale; consistent empty/loading/error states; skeleton loaders; dark/light parity.
- **Speed:** preserve perceived performance; SSE live updates; no full reload on cluster switch.
- **Accessibility:** keyboard nav (extend existing shortcuts), focus states, ARIA, contrast.
- **Login:** clean local form + prominent "Sign in with Google" when SSO is enabled.
- **Admin area:** calm, grouped, searchable; masked-secret affordances; the **model picker** and **budget default** controls are explicit and obvious.

**Required UI elements:** login (+ Google SSO + forced first-login change); cluster switcher with env tags + health dots; Nodes view with new sortable columns; global chat dock + Troubleshoot tab; admin pages (Users, Models w/ picker, Token/Cost dashboard, Budget config, Audit viewer w/ export); active-model badge.

---

## 8. Security & production-hardening checklist
- **Secrets:** KMS envelope encryption for kubeconfigs, API keys, OIDC client secret; no plaintext at rest/in logs/in responses; masked display.
- **Transport:** TLS at ingress; `Secure`/`HttpOnly`/`SameSite=Strict` cookies.
- **AuthZ:** server-side on every endpoint in kompass-core; per-cluster editor scoping in middleware; engine not reachable except via the authenticated proxy.
- **Engine isolation:** Go engine binds to pod loopback only; never exposed via Service/Ingress.
- **AI safety:** whitelist-only structured proposals; confirm+diff; audit-before-execute; redaction before every LLM call; per-user daily budget.
- **Input validation:** pydantic models; reject unknown fields; size-limit kubeconfig/chat payloads.
- **Rate limiting & quotas:** per-user AI rate limits + daily token budgets.
- **Audit & observability:** audit log + structured logs (no secrets) + app `/metrics` + health/readiness.
- **Container hardening (both containers):** minimal/distroless base; non-root; read-only rootfs; drop all caps; no privilege escalation; resource requests/limits; seccomp `RuntimeDefault`.
- **K8s hardening:** dedicated namespace; least-privilege ServiceAccount; NetworkPolicy egress allow-list (K8s API, Prometheus/GMP, provider endpoints, DNS) — and **no ingress to the engine container**; PDB; probes.
- **Supply chain:** pin deps; `pip-audit`, `npm audit`, `govulncheck` (engine), Trivy image scans; SBOM.
- **Data governance:** document what cluster data may go to providers; redaction rules reviewable.
- **Backups:** back up the SQLite app DB (PVC snapshot/scheduled export); document restore.

---

## 9. Build phases (each test-gated; do not skip the gate)

> **Gate rule:** a phase is complete only when `make test-container` passes **and** its acceptance criteria are met. Commit at each green gate (conventional commits). Do not start the next phase until green.

**Phase 0 — Foundation, rebrand, two-container skeleton**
- Fork the engine; get it building + running against kind. Bind engine to loopback. Scaffold `kompass-core` (FastAPI) that proxies `/api/engine/*` to the engine and serves the UI. Apply Kompass branding (name/logo/favicon/title); add `NOTICE`/`THIRD_PARTY_NOTICES`; preserve LICENSE. Build the two-container image(s) and the `make test-container` gate.
- **Gate:** both containers build; engine reachable only via core proxy; UI loads as "Kompass" with no upstream branding visible; LICENSE/NOTICE present; `make test-container` green (engine `make test`, core `pytest`, `tsc`, kind smoke).

**Phase 1 — Local auth & roles**
- `auth/` (local), bootstrap admin, login UI, forced password change, role middleware, admin User Management (CRUD + per-cluster scoping model).
- **Gate:** §4.1 local criteria; 403 matrix via API tests; in-container.

**Phase 2 — Cluster registry & kubeconfig encryption**
- `clusters/`, `secrets/` (KMS; fake KMS in tests), add/list/select/remove, encrypted storage, cluster switcher UI, per-cluster scoping end-to-end.
- **Gate:** §4.2 incl. "no plaintext kubeconfig anywhere"; in-container.

**Phase 3 — Nodes enhancements & event retention**
- `nodestats/` aggregates; Nodes columns; 14-day event retention config.
- **Gate:** §4.7 aggregates match `kubectl describe node` on kind; retention verified.

**Phase 4 — AI providers & model management (admin)**
- `ai/providers/` abstraction + adapters; admin Model Management with **model/version picker**; encrypted keys; dynamic/editable model lists.
- **Gate:** §4.4 (keys masked/never logged; switching works; disabled provider unusable; picker works); mock provider server; in-container.

**Phase 5 — AI chat & troubleshoot (recommendation-only)**
- `ai/` context assembly via engine, redaction, SSE streaming, chat dock + Troubleshoot tab, active-model badge, usage recording. **Apply disabled this phase.**
- **Gate:** §4.3 minus apply; redaction unit tests; streaming e2e; usage recorded; in-container.

**Phase 6 — Apply-actions (whitelist + confirm + audit) & budgets**
- `ai/whitelist.py`, proposal validation, diff/confirm UI, `audit/`, apply endpoint → engine write handlers, audit-before-execute; `budgets/` enforcement + admin budget-default UI.
- **Gate:** §4.3 apply + §4.6 audit + §4.5 budget criteria; "non-whitelisted can never apply" and "audit-before-mutation" green; in-container.

**Phase 7 — Token/cost dashboard, audit export & Google SSO**
- Usage charts, admin-editable pricing, CSV/JSON audit export, **Google SSO (OIDC)** config + login + user mapping.
- **Gate:** §4.5 + §4.6 export + §4.1 SSO criteria; in-container.

**Phase 8 — Hardening & production readiness**
- Everything in §8 for both containers; NetworkPolicy (incl. engine no-ingress); rate limits/quotas; scans; SBOM; backups; probes; PDB.
- **Gate:** checklist verified; Trivy/`pip-audit`/`npm audit`/`govulncheck` clean or triaged; load/smoke in-container.

**Phase 9 — GKE deployment & UAT**
- Follow `DEPLOYMENT_GKE.md` to non-prod GKE; run UAT; then prod.
- **Gate:** acceptance suite passes against real GKE; sign-off.

---

## 10. Testing strategy
- **Python unit (`pytest`):** auth (hashing, lockout, sessions, OIDC mapping), redaction, whitelist validation, nodestats aggregation, cost computation, budget enforcement, KMS wrapper (fake KMS).
- **Frontend:** `tsc`; component tests for proposal card, login (local+SSO), admin forms, model picker; mock SSE.
- **Integration:** kind cluster inside the test container; resource listing via the engine proxy, node aggregates vs `kubectl describe node`, event retention, cluster add/select.
- **API/contract:** full **role × endpoint 403 matrix**; CSRF; rate limits; "secret never in response/log" scanners; "engine not reachable except via core" check.
- **AI:** mock provider returning whitelisted + non-whitelisted proposals; assert apply impossible for non-whitelisted and unauthorized role/cluster; assert audit-before-execute; assert redaction on fixtures; assert budget blocks at limit.
- **Security:** Trivy (both images), `pip-audit`, `npm audit`, `govulncheck` (engine); dependency pinning check.
- **E2E smoke:** login → switch cluster → Nodes aggregates → ask AI → streamed answer → (editor) apply a scale on kind → audit row precedes change → export audit CSV → confirm no upstream branding anywhere.
- All runnable via `make test-container`.

---

## 11. Containerized test gating (mandatory)
- Provide `build/test.Dockerfile` (or a test stage) with Go 1.26+, Node 20+, Python 3.12+, kind, kubectl, Trivy, govulncheck, pip-audit.
- `make test-container` builds the test image and runs: engine `make test`; core `pytest`; `tsc` + FE unit; kind integration; API/contract; AI tests; security scans. Exits non-zero on any failure.
- **The agent must run `make test-container` and see it pass before declaring any phase complete and before moving on.** If it cannot run containers, it must stop and report rather than proceed on unverified code.
- CI (GitHub Actions) runs `make test-container` on every PR; merges blocked on green.

---

## 12. Deliverables checklist
- Two-container repo: near-upstream rebranded Go engine + new Python `kompass-core` + extended React UI.
- `AGENTS.md` (+ `CLAUDE.md`, `.antigravity/rules.md`), `NOTICE`/`THIRD_PARTY_NOTICES`, preserved `LICENSE`.
- `make test-container` green for all phases.
- Helm chart / manifests for the two-container in-cluster deployment.
- `DEPLOYMENT_GKE.md` followed and validated.
- Security checklist (§8) completed and documented.
- Demo script for the showcase (login + SSO → multi-cluster → Nodes insight → AI troubleshoot → confirmed apply → audit → cost/budget dashboard).

---

## 13. Confirmed decisions (from stakeholder)
1. **Name:** Kompass.
2. **Providers:** all providers permitted; redaction still mandatory; NetworkPolicy egress allow-lists the configured provider endpoints.
3. **KMS naming:** key ring `kompass`, key `kompass-app-secrets`, GSA `kompass-app`, KSA `kompass-ksa`, namespace `kompass` (see `DEPLOYMENT_GKE.md`).
4. **Google SSO:** yes — offered alongside local auth for gradual migration (Phase 7).
5. **Token budgets:** admin-configurable default in the UI + per-user overrides; provider model picker required.
6. **Branding:** full rebrand to Kompass; no user-facing reference to the upstream engine; legal LICENSE/NOTICE attribution retained in-repo (license requirement).
