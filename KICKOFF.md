# KICKOFF.md — How to start the Kompass build with Google Antigravity / Claude Code

Operator runbook. Do these in order.

---

## Step 1 — Create the repository from the engine fork

```bash
# Fork the upstream engine on GitHub UI into your org as "kompass", then:
git clone git@github.com:shivram-personal/kompass.git
cd kompass

# Track upstream so you can rebase the engine later
git remote add upstream https://github.com/skyhook-io/radar.git
git fetch upstream

# Confirm the engine builds before changing anything
make deps && make build
```
> Forking (not copying) lets you pull upstream engine fixes via `git fetch upstream && git rebase upstream/main`. Because all *your* code lives in the separate Python service, rebases stay clean.

## Step 2 — Add docs, rules, and legal notices

```
kompass/
├── AGENTS.md                 <- agent operating rules
├── CLAUDE.md                 <- copy of AGENTS.md (Claude Code reads this)
├── .antigravity/rules.md     <- copy of AGENTS.md (Antigravity reads this)
├── LICENSE                   <- KEEP (upstream Apache-2.0 — required)
├── NOTICE / THIRD_PARTY_NOTICES  <- KEEP/ADD (attribution — required)
└── docs/
    ├── SPEC.md
    └── DEPLOYMENT_GKE.md
```
```bash
mkdir -p docs .antigravity
# place SPEC.md and DEPLOYMENT_GKE.md in docs/, AGENTS.md at root, then:
cp AGENTS.md CLAUDE.md
cp AGENTS.md .antigravity/rules.md
git add . && git commit -m "docs: add spec, deployment guide, agent rules, third-party notices"
```
Each tool reads its own rules file: **Claude Code → `CLAUDE.md`**, **Antigravity → `.antigravity/rules.md`** (also respects `AGENTS.md`). Keep `AGENTS.md` as the single source of truth and re-copy whenever you edit it. If a tool's current docs specify a different filename, follow the tool and keep the content identical.

> **Legal note:** the upstream engine is Apache-2.0. Keep `LICENSE`, any `NOTICE`, and source copyright headers; add a `THIRD_PARTY_NOTICES` listing the engine. These are not user-facing. The UI is fully rebranded to Kompass; the legal files stay. Do not let the agent strip them.

## Step 3 — Test-gate scaffolding first

Before features, ensure the container gate exists (SPEC §11): `build/test.Dockerfile` (Go 1.26+, Node 20+, Python 3.12+, kind, kubectl, Trivy, govulncheck, pip-audit) and a `make test-container` target running engine `make test`, core `pytest`, `tsc`, kind smoke, and scans — exiting non-zero on any failure. This is part of Phase 0's gate.

## Step 4 — The kickoff prompt

Paste this as the agent's first instruction (Antigravity or Claude Code):

---

> **You are building the application described in `docs/SPEC.md` (Kompass). Before doing anything else, read `AGENTS.md`, `docs/SPEC.md`, and `docs/DEPLOYMENT_GKE.md` in full, plus `/mnt/skills/public/frontend-design/SKILL.md` before any UI work.**
>
> **Architecture:** two containers in one pod. A near-upstream **Go cluster engine** (already forked; upstream remote configured) that we only rebrand and bind to loopback, plus a new **Python/FastAPI service `kompass-core`** that owns auth (local + Google SSO), the cluster registry, AI orchestration, the apply-action whitelist, audit, model management, and per-user token budgets, and that acts as the auth gateway proxying `/api/engine/*` to the engine. All browser traffic enters via kompass-core.
>
> **Hard constraints (from `AGENTS.md` — repeat them back to confirm understanding):**
> 1. All new logic is **Python** in `kompass-core`. The Go engine is edited only to bind to loopback, rebrand user-facing strings/assets, and toggle existing flags. If you think a feature needs Go logic, try Python against the engine API first; if impossible, stop and ask.
> 2. Full rebrand to **Kompass** in everything users see; but **keep** `LICENSE`, `NOTICE`/`THIRD_PARTY_NOTICES`, and source copyright headers (Apache-2.0 requirement). Never strip them.
> 3. Security: never log/return/send-to-LLM any secret; KMS-encrypt kubeconfigs, API keys, OIDC secret; authorization is server-side in core; the engine is never reachable except via the authenticated core proxy; the AI emits only structured proposals validated against a server-side whitelist mapping to existing engine handlers; apply requires role + per-cluster auth + budget check + confirm + audit-before-execute; redact context before any LLM call.
> 4. Preserve every inherited engine feature (topology, resources, timeline, logs, exec, Helm, GitOps, etc.) in the rebranded UI.
> 5. Test-gate every phase in a container: a phase is done only when `make test-container` passes and acceptance criteria are met. Do not advance phases until green. If you can't run containers, stop and report.
>
> **Start with Phase 0 only** (SPEC §9): fork builds and runs against kind; bind engine to loopback; scaffold `kompass-core` (FastAPI) proxying `/api/engine/*` and serving the UI; apply Kompass branding; add NOTICE/THIRD_PARTY_NOTICES and preserve LICENSE; build both container images and the `make test-container` gate. Do not begin Phase 1 until Phase 0's gate is green and committed.
>
> First reply with: (a) the five hard constraints in your own words, (b) the exact files you'll create or modify in Phase 0 (and confirmation that Go edits stay within the allowed seams), and (c) any clarifying questions. Wait for my confirmation before writing code.

---

## Step 5 — Drive the build phase by phase
- Approve the Phase 0 plan; require `make test-container` passing and "no user-facing upstream branding + LICENSE/NOTICE intact" before accepting.
- For each next phase, a short prompt, e.g.:
  > "Phase 0 gate is green and committed. Proceed to Phase 1 (local auth & roles) per SPEC §4.1 and §9. Same rules. Show the 403 matrix tests passing in-container before declaring done."
- Never let it batch phases. The per-phase container gate is what prevents GKE-deploy surprises.

Phase reminders:
- Phase 4 must include the **per-provider model/version picker**.
- Phase 6 must include **budget enforcement** and the **admin budget-default UI**.
- Phase 7 adds **Google SSO** (local auth keeps working alongside it).

## Step 6 — Keep the engine fresh
```bash
git fetch upstream
git rebase upstream/main      # surgical engine edits keep this clean
make test-container           # must pass before trusting the rebase
```

## Step 7 — Deploy
Once Phase 8 is green, follow `docs/DEPLOYMENT_GKE.md` to non-prod GKE, run the §9 post-deploy validation (incl. the SSO and no-branding checks), then promote to prod.

---

## Tool notes
- **Claude Code:** ensure `CLAUDE.md` present; paste the Step 4 prompt.
- **Google Antigravity:** ensure `.antigravity/rules.md` (and `AGENTS.md`) present; paste the Step 4 prompt; verify the tool's current rules-file location and mirror content if it differs.

One source of truth (`AGENTS.md`), copied to the tool-specific filenames, and a kickoff prompt that anchors the agent to `docs/SPEC.md`.
