# AGENTS.md — Operating rules for coding agents on the Kompass repository

> This file is authoritative for **how** you work in this repo. `docs/SPEC.md` is authoritative for **what** to build. Read both before acting, plus `/mnt/skills/public/frontend-design/SKILL.md` before any UI work. Mirror this file to `CLAUDE.md` and `.antigravity/rules.md`.

## Project in one line
**Kompass** is a two-container app: a near-upstream **Go cluster-visibility engine** (reused, rebranded) plus a new **Python/FastAPI service `kompass-core`** that owns auth, AI orchestration, the apply-action whitelist, audit, model management, budgets, and the auth gateway. We add local + Google-SSO auth, AI chat/troubleshoot with whitelisted apply-actions, model management, a token/cost dashboard with per-user budgets, an audit log, and read-only Nodes enhancements — for in-cluster GKE deployment.

## The rules

### 1. Python owns the brain; Go stays near-upstream
- **All new logic goes in `kompass-core` (Python).** No new business logic, auth, or AI code in the Go engine.
- The Go engine may be edited **only** to: (a) bind to pod loopback, (b) rebrand user-facing strings/assets/title, (c) toggle existing engine flags. Nothing else.
- If you think a feature needs Go logic, first try to do it in Python against the engine's existing API. If truly impossible, **stop and ask** before editing Go.
- Keep the engine rebaseable: surgical edits only, so `git rebase upstream/main` stays clean.

### 2. Branding & licensing — rebrand everything users see, keep the legal notices
- No user-facing text, logo, favicon, page title, HTTP header, About screen, or visible identifier may reference the upstream engine. The product is **Kompass** everywhere a user can see.
- The upstream engine is **Apache-2.0**: you **must retain** `LICENSE`, any `NOTICE`, and source-code copyright headers, and keep a `THIRD_PARTY_NOTICES`/`NOTICE` file at repo root. These are not user-facing. **Never remove or alter them** — that violates the license. Rebrand the UI; preserve the notices.

### 3. Security is not optional (SPEC §8)
- **Never** log, return to the frontend, or send to an LLM any secret: passwords, API keys, kubeconfigs, OIDC client secret, tokens. Mask on display.
- KMS-envelope-encrypt kubeconfigs, API keys, and the OIDC secret at rest (`kompass_core/secrets/`). No plaintext at rest.
- **Authorization is server-side, in kompass-core.** UI hiding is cosmetic. Every endpoint checks role; editor writes check per-cluster scope.
- The **Go engine must never be reachable except through the authenticated kompass-core proxy** (loopback bind + NetworkPolicy no-ingress).
- The AI may only emit **structured action proposals** validated against the server-side whitelist (`kompass_core/ai/whitelist.py`), each mapping to an existing engine write handler. No free-form commands, no shelling out from model output. Apply requires role + per-cluster auth + budget check + confirm + **audit-before-execute**.
- Redact cluster context before any LLM call (`kompass_core/ai/redact.py`).

### 4. Preserve every inherited engine feature
- Topology, resources, timeline, logs, exec, image FS viewer, Helm, GitOps, TLS certs, cluster audit, cost insights, traffic, multi-cluster switching, shortcuts, themes — all must remain available in the Kompass UI via the `/api/engine/*` proxy. Do not silently drop or disable them (except where an explicit policy flag requires it).

### 5. Test-gate every phase in a container (SPEC §9–§11)
- A phase is done **only** when `make test-container` passes and the phase's acceptance criteria are met.
- **Do not advance phases until the gate is green.** Commit at each green gate (`feat:`/`fix:`/`test:`/`chore:`).
- If you cannot run containers, **stop and report** — never declare a phase done on unverified code.
- Add/extend tests for every feature: Python unit, TS type+unit, kind integration, role×endpoint 403 matrix, AI whitelist/redaction/audit-ordering/budget tests, security scans (Trivy, pip-audit, npm audit, govulncheck).

### 6. Match the stack and quality bar
- kompass-core: Python 3.12+, FastAPI, SQLAlchemy+SQLite, httpx, argon2-cffi, authlib (OIDC), google-cloud-kms, pydantic v2, sse-starlette.
- Frontend: React 19, TypeScript, Tailwind v4, shadcn/ui, TanStack Query. UI must feel native and seamless, especially the AI surfaces (SPEC §7).
- Engine: leave Go style as upstream; format any touched Go with gofmt.

### 7. Build in the specified order
Follow SPEC §9 phases 0→9 in order. Do not start apply-actions (Phase 6) before model management (Phase 4) and recommendation-only chat (Phase 5) are green. Do not build metrics retention beyond SPEC §4.8 (no custom TSDB).

### 8. When unsure, ask — don't invent
- Spec explicit → follow exactly. Spec silent + material choice (security, data model, external dep, any Go edit) → pause and ask. Spec silent + trivial/local → proceed and note the assumption in the commit.

### 9. Keep secrets out of the repo
- No real API keys, kubeconfigs, OIDC secrets, or credentials in code/fixtures/tests. Use fakes/mocks (mock provider server, fake KMS) in tests.

### 10. Definition of done for any task
Code + tests written; `make test-container` green; acceptance criteria met; no secret leakage; engine reachable only via core; no user-facing upstream branding; LICENSE/NOTICE intact; conventional commit; Go edits within the allowed seams only; assumptions documented.

## Quick references
- What to build: `docs/SPEC.md`
- How to deploy: `docs/DEPLOYMENT_GKE.md`
- Phase gates & tests: `docs/SPEC.md` §9–§11
- Security checklist: `docs/SPEC.md` §8
