# Kompass — Configuration Values Sheet

Fill every blank below **once**, then copy each value into the file/location noted in the right-hand column. Work top to bottom. Anything in the "Enter in the app UI after deploy" section is a runtime secret — do **not** put it in a file.

Legend: ✏️ = you fill it · 🔁 = auto-derived (just verify) · 🔒 = secret, never commit to git

---

## Part A — GitHub / fork (used in KICKOFF.md Step 1)

| # | Value | Your entry | Goes into |
|---|-------|-----------|-----------|
| A1 | ✏️ Your GitHub org/username | `____________________` | KICKOFF.md Step 1 — `YOUR_ORG` in the clone URL |
| A2 | ✏️ Upstream engine repo (org/name) | `____________________` | KICKOFF.md Step 1 — `UPSTREAM_ORG/UPSTREAM_ENGINE` in `git remote add upstream` |

---

## Part B — GCP core variables (DEPLOYMENT_GKE.md §0 "Shared variables")

Set these in the shell block; most other values derive from them.

| # | Value | Your entry | Notes |
|---|-------|-----------|-------|
| B1 | ✏️ `PROJECT_ID` | `____________________` | Your GCP project ID |
| B2 | ✏️ `REGION` | `____________________` | e.g. `us-central1` |
| B3 | ✏️ `CLUSTER_NAME` | `____________________` | Your existing GKE cluster |
| B4 | ✏️ `ZONE` | `____________________` | e.g. `us-central1-a` (zone of the cluster, for cluster commands) |
| B5 | 🔁 `NS` | `kompass` | Default is fine; change only if you have a namespace convention |
| B6 | 🔁 `AR_REPO` | `kompass` | Artifact Registry repo name; default is fine |
| B7 | ✏️ `TAG` | `v0.1.0` | Image version tag; bump per release |
| B8 | 🔁 `CORE_IMAGE` | (derives from B1/B2/B6) | Verify it resolves to `REGION-docker.pkg.dev/PROJECT_ID/AR_REPO/kompass-core` |
| B9 | 🔁 `ENGINE_IMAGE` | (derives from B1/B2/B6) | Verify it resolves to `.../kompass-engine` |

**Fixed names (already set in the docs — no change needed unless you have conventions):**
KMS key ring `kompass` · KMS key `kompass-app-secrets` · GSA `kompass-app` · KSA `kompass-ksa`.

---

## Part C — Manifest literals you must hand-edit (DEPLOYMENT_GKE.md)

These appear inside YAML/manifests where shell variables do **not** auto-substitute, so paste the resolved values directly.

| # | Value | Your entry | Location |
|---|-------|-----------|----------|
| C1 | ✏️ Full core image path | `____________________` | §6 `values.yaml` → replace `REPLACE_WITH_$CORE_IMAGE` (use B8) |
| C2 | ✏️ Full engine image path | `____________________` | §6 `values.yaml` → replace `REPLACE_WITH_$ENGINE_IMAGE` (use B9) |
| C3 | ✏️ Full KMS key resource path | `____________________` | §6 `values.yaml` → `kms.keyName`. Format: `projects/<B1>/locations/<B2>/keyRings/kompass/cryptoKeys/kompass-app-secrets` |
| C4 | ✏️ Internal hostname | `____________________` | §7.3 Ingress `host:`/`tls.hosts` **and** §7.4 SSO redirect URI. Use your real internal domain (see Part E) |
| C4a | ✏️ `KOMPASS_SUBNET` | `____________________` | §7.1 — subnet (routable to your users) used to reserve the internal IP |
| C5 | ✏️ Reserved internal IP name | `____________________` | The static internal IP you reserve (see Part E); referenced on the Ingress |
| C6 | ✏️ Subnet for the internal IP | `____________________` | Used when reserving the internal IP (`--subnet=`) |
| C7 | ✏️ Egress allow-list entries | `____________________` | §8 NetworkPolicy — replace the temporary open `egress: - {}` before prod: DNS, API server, Prometheus/GMP, provider endpoint ranges |

---

## Part D — TLS certificate decision (DEPLOYMENT_GKE.md §7.2)

§7.2 now uses your own / internal-CA cert as a K8s TLS secret (the public Google-managed-cert annotation has been removed because it can't validate an internal hostname).

| # | Decision | Your entry | Action |
|---|----------|-----------|--------|
| D1 | ✏️ TLS source for internal host | `____________________` | Choose: (a) internal/corporate CA cert loaded as a K8s TLS secret, or (b) cert-manager with an internal issuer. Replace the managed-cert annotation accordingly. |
| D2 | ✏️ TLS secret name (if using a secret) | `____________________` | Reference it on the Ingress `tls:` block |

---

## Part E — DNS & IP (your earlier question — answers baked in)

Use **private DNS**, not public. Reserve a static internal IP so the name→IP mapping stays constant.

| # | Value | Your entry | Notes |
|---|-------|-----------|-------|
| E1 | ✏️ Internal DNS zone / domain | `____________________` | Your private zone (Cloud DNS private zone, or internal corporate DNS). Replaces the `example.com` part of C4 |
| E2 | ✏️ Reserved internal static IP | `____________________` | `gcloud compute addresses create <C5> --region=<B2> --subnet=<C6> --purpose=GCE_ENDPOINT`, then read the assigned IP |
| E3 | ✏️ A record | `____________________` | One A record: `<C4>` → `<E2>` in the private zone |
| E4 | ✏️ Network reachability | `____________________` | Confirm users reach the internal IP via VPN / interconnect / same-VPC. Resolve with your network team |

---

## Part F — Legal notices (KICKOFF.md Step 2)

Source these from the upstream repo after you fork — do not hand-write them.

| # | Value | Your entry | Notes |
|---|-------|-----------|-------|
| F1 | 🔁 `LICENSE` | (copy from upstream) | Keep upstream's Apache-2.0 LICENSE as-is |
| F2 | 🔁 `NOTICE` | (copy from upstream if present) | Keep as-is |
| F3 | ✏️ `THIRD_PARTY_NOTICES` content | `____________________` | List the upstream project name + its Apache-2.0 license; copy attribution text from upstream's LICENSE/NOTICE |

---

## Part G — Enter in the app UI AFTER deploy (🔒 NEVER put these in files or git)

These are KMS-encrypted at runtime by the app. No placeholders exist for them in any config file — that is intentional.

| # | Value | Where to enter |
|---|-------|----------------|
| G1 | 🔒 Initial admin password | **Not set by you.** Printed once to the core container log on first start: `kubectl logs -n kompass deploy/kompass -c kompass-core | grep -A2 "INITIAL ADMIN CREDENTIALS"`. Log in, then change it. |
| G2 | 🔒 LLM provider API key(s) + model selection | Admin Settings → Model Management (per provider; pick model/version) |
| G3 | 🔒 Google OAuth client ID | Admin Settings → SSO |
| G4 | 🔒 Google OAuth client secret | Admin Settings → SSO |
| G5 | ✏️ SSO allowed domain(s) + default role | Admin Settings → SSO |
| G6 | 🔒 Cluster kubeconfig(s) | Admin Settings → cluster registry (upload per cluster, with friendly name + env tag) |
| G7 | ✏️ Default daily token budget (+ per-user overrides) | Admin Settings → Budget config |

**Google OAuth redirect URI to register on the Google side:** `https://<C4>/api/auth/oidc/callback` (must exactly match C4).

---

## Quick fill order
1. Part A → fork the repo.
2. Part B → export the shell variables.
3. Part E → reserve IP + create the A record (do this early; DNS can take time to propagate).
4. Part C + D → edit the manifests.
5. Part F → copy legal files from the fork.
6. Deploy (DEPLOYMENT_GKE.md), then Part G in the running app.

---

## Part H — kompass-core auth & security parameters (Phase 1)

All read from `KOMPASS_`-prefixed environment variables (`kompass_core/config.py`).
The defaults are production-sound; override only with reason. **Secrets are never
configured here** — the bootstrap admin password is generated at first start and
printed once to the core log (Part G1).

### H.1 Argon2id password hashing (explicitly set — not library defaults, SPEC §4.1)

| Env var | Default | Meaning |
|---|---|---|
| `KOMPASS_ARGON2_MEMORY_COST` | `65536` | Memory in **KiB** = **64 MiB** |
| `KOMPASS_ARGON2_TIME_COST` | `3` | Iterations |
| `KOMPASS_ARGON2_PARALLELISM` | `1` | Lanes |
| `KOMPASS_ARGON2_HASH_LEN` | `32` | Derived hash length (bytes) |
| `KOMPASS_ARGON2_SALT_LEN` | `16` | Salt length (bytes) |

Type Argon2**id**. Encoded hashes advertise `$argon2id$v=19$m=65536,t=3,p=1$…`.
Plaintext passwords are never stored, logged, or returned.

### H.2 Sessions, cookies & CSRF

| Env var | Default | Notes |
|---|---|---|
| `KOMPASS_COOKIE_NAME` | `kompass_session` | Session cookie name |
| `KOMPASS_COOKIE_SECURE` | `true` | Requires HTTPS; set `false` only for plain-HTTP local/dev |
| `KOMPASS_COOKIE_SAMESITE` | `strict` | CSRF hardening |
| `KOMPASS_SESSION_IDLE_MINUTES` | `60` | Idle expiry (slides on use) |
| `KOMPASS_SESSION_ABSOLUTE_HOURS` | `12` | Hard cap regardless of activity |
| `KOMPASS_CSRF_HEADER` | `X-CSRF-Token` | Double-submit token header for writes |

Session tokens are 256-bit, stored only as a SHA-256 hash; cookies are `HttpOnly`.

### H.3 Authentication policy & scoping

| Env var | Default | Notes |
|---|---|---|
| `KOMPASS_LOCKOUT_THRESHOLD` | `5` | Failed attempts before lockout |
| `KOMPASS_LOCKOUT_MINUTES` | `15` | Lockout duration |
| `KOMPASS_BOOTSTRAP_ADMIN_USERNAME` | `admin` | First-run admin username |
| `KOMPASS_CLUSTER_HEADER` | `X-Kompass-Cluster-Id` | Target cluster for editor per-cluster scope on writes |
| `KOMPASS_DB_URL` | `sqlite:////app/data/kompass.db` | App DB (PVC-backed in GKE) |

### H.4 KMS envelope encryption for kubeconfigs & secrets (Phase 2, SPEC §4.2)

Kubeconfigs (and, later, provider API keys + the OIDC secret) are envelope-encrypted:
a random data key (DEK) encrypts the secret with AES-256-GCM, and the DEK is wrapped
by a KMS key. The DB stores **only** ciphertext + wrapped DEK + nonce — never plaintext
and never the unwrapped DEK.

| Env var | Default | Notes |
|---|---|---|
| `KOMPASS_KMS_PROVIDER` | `local` | `gcp` for production (Cloud KMS); `local` is a dev/test stand-in |
| `KOMPASS_KMS_KEY_NAME` | `""` | **GCP:** full key resource name (see below) |
| `KOMPASS_LOCAL_KMS_KEY` | `""` | **Local stand-in only:** base64 of a 32-byte KEK, supplied out-of-band |

**Production (GCP), set `KOMPASS_KMS_PROVIDER=gcp` and `KOMPASS_KMS_KEY_NAME` to the key
resource created in `DEPLOYMENT_GKE.md` §2:**
```
projects/<PROJECT_ID>/locations/<REGION>/keyRings/kompass/cryptoKeys/kompass-app-secrets
```
Authentication is via **Workload Identity** — no static keys (`DEPLOYMENT_GKE.md` §3):
the GSA `kompass-app@<PROJECT_ID>.iam.gserviceaccount.com` holds
`roles/cloudkms.cryptoKeyEncrypterDecrypter` **on that key only**, and the KSA
`kompass-ksa` (namespace `kompass`) is bound to it via `roles/iam.workloadIdentityUser`.
The core container needs no key file — the Cloud KMS client uses the pod's Workload
Identity credentials.

**Local/kind/test stand-in (`KOMPASS_KMS_PROVIDER=local`):** the DEK is wrapped with a
local KEK from `KOMPASS_LOCAL_KMS_KEY`. This is **clearly marked, not for production**
(core logs a warning), and it preserves "no plaintext at rest": the DB holds only
ciphertext + wrapped DEK; the KEK lives **outside** the database (env/secret). The
`make test-container` gate supplies a throwaway dev KEK at runtime — never baked into
the image, never a real GCP credential.

### H.5 Event retention (Phase 3)

Cluster events are persisted in a core-owned store, indexed on `(cluster_id, ts)`, and
pruned to a retention window by a background loop (also callable directly). Event
messages are redacted before storage so secrets an event might capture are not persisted.

| Env var | Default | Notes |
|---|---|---|
| `KOMPASS_EVENT_RETENTION_DAYS` | `30` | Retention window. Per the Phase 3 directive; set `14` to match SPEC §4.8. |
| `KOMPASS_EVENT_PRUNE_SECONDS` | `3600` | How often the prune loop runs |
| `KOMPASS_EVENT_POLL_ENABLED` | `false` | Background ingestion of the engine's current-cluster events. **Off** until the deferred multi-cluster injection seam lands (engine-context → registry-cluster-id mapping + remote ingestion). The store/retention/endpoints are complete and tested independently. |
| `KOMPASS_EVENT_POLL_SECONDS` | `30` | Poll interval when enabled |
