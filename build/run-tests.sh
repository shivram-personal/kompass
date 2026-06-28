#!/usr/bin/env bash
# Kompass containerized test gate — runs inside the build/test.Dockerfile image
# with the host docker socket mounted. Exits non-zero on any required failure.
#
# Covers Phase 0's gate (SPEC §9/§11): both images build; engine `make test`;
# core pytest; tsc; kind smoke + two-container wiring (engine reachable only via
# core, UI served and branded). Security scans run report-only in Phase 0 and
# are tightened to fatal in Phase 8 (set SCANS_FATAL=1 to enforce early).
set -euo pipefail

ENGINE_IMAGE="kompass-engine:citest"
CORE_IMAGE="kompass-core:citest"
KIND_CLUSTER="kompass-ci"
SCANS_FATAL="${SCANS_FATAL:-0}"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
step()  { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

cleanup() {
  step "Cleanup"
  docker rm -f kompass-core kompass-engine >/dev/null 2>&1 || true
  kind delete cluster --name "$KIND_CLUSTER" >/dev/null 2>&1 || true
  docker network disconnect kind "$(hostname)" >/dev/null 2>&1 || true
}
trap cleanup EXIT

scan() {
  # report-only unless SCANS_FATAL=1
  local label="$1"; shift
  step "Scan: $label"
  if "$@"; then
    green "scan ok: $label"
  elif [ "$SCANS_FATAL" = "1" ]; then
    red "scan FAILED (fatal): $label"; exit 1
  else
    red "scan findings (report-only in Phase 0): $label"
  fi
}

# ---------------------------------------------------------------------------
step "Seam-drift check (engine seam #3 confined to its sanctioned surface)"
bash build/check_seam_drift.sh

# ---------------------------------------------------------------------------
step "Build both container images"
docker build -f build/engine.Dockerfile -t "$ENGINE_IMAGE" .
docker build -f build/core.Dockerfile   -t "$CORE_IMAGE" .

# ---------------------------------------------------------------------------
step "Engine: make test (go unit tests)"
make test

# ---------------------------------------------------------------------------
step "Frontend: install + type-check (tsc)"
npm ci --no-audit --prefer-offline
npm run tsc --workspace=web

# ---------------------------------------------------------------------------
step "kompass-core: pytest (incl. role×endpoint authorization matrix)"
pip install --no-cache-dir -r kompass_core/requirements-dev.txt
# -s surfaces the printed authorization matrix in the gate log.
( cd kompass_core && python -m pytest -v -s )

# ---------------------------------------------------------------------------
# Security scans (report-only in Phase 0).
scan "govulncheck (engine)"      bash -c 'govulncheck ./... || exit 1'
scan "pip-audit (core runtime)"  pip-audit -r kompass_core/requirements.txt
scan "npm audit (frontend, high+)" bash -c 'npm audit --audit-level=high || exit 1'
scan "trivy (engine image)"      trivy image --severity HIGH,CRITICAL --exit-code 1 --no-progress "$ENGINE_IMAGE"
scan "trivy (core image)"        trivy image --severity HIGH,CRITICAL --exit-code 1 --no-progress "$CORE_IMAGE"

# ---------------------------------------------------------------------------
step "kind smoke: create cluster + kubectl get nodes"
kind create cluster --name "$KIND_CLUSTER" --wait 120s
docker network connect kind "$(hostname)"
kind get kubeconfig --name "$KIND_CLUSTER" --internal > /tmp/kompass-kubeconfig
chmod 0644 /tmp/kompass-kubeconfig
KUBECONFIG=/tmp/kompass-kubeconfig kubectl get nodes

# ---------------------------------------------------------------------------
step "Two-container wiring smoke (pod-style shared loopback)"
# Engine on the kind network, bound to loopback inside its own netns.
docker create --name kompass-engine --network kind "$ENGINE_IMAGE" \
  --no-browser --no-mcp --host 127.0.0.1 --port 9280 --kubeconfig /config
docker cp /tmp/kompass-kubeconfig kompass-engine:/config
docker start kompass-engine

# Core SHARES the engine's network namespace — exactly like two containers in
# one pod — so it reaches the engine over 127.0.0.1 and nothing else can.
# A clearly-marked DEV KMS stand-in key is supplied at runtime (never baked into
# the image); production uses Cloud KMS instead.
docker run -d --name kompass-core --network "container:kompass-engine" \
  -e KOMPASS_LOCAL_KMS_KEY="ZGV2LW9ubHktc3RhbmQtaW4ta2V5LTMyLWJ5dGVzISE=" \
  "$CORE_IMAGE"

# The test container is on the kind network, so it resolves the engine
# container by name; core's 8080 lives in that shared netns.
BASE="http://kompass-engine:8080"
step "Wait for core to answer"
for i in $(seq 1 30); do
  if curl -fsS "$BASE/healthz" >/dev/null 2>&1; then break; fi
  sleep 2
  if [ "$i" = "30" ]; then red "core never became healthy"; docker logs kompass-core || true; exit 1; fi
done

assert() { if eval "$2"; then green "ok: $1"; else red "FAIL: $1"; docker logs kompass-core 2>&1 | tail -20 || true; exit 1; fi; }

assert "core /healthz is ok" \
  '[ "$(curl -fsS "$BASE/healthz" | jq -r .status)" = "ok" ]'

assert "core /readyz reports engine reachable on loopback" \
  '[ "$(curl -fsS "$BASE/readyz" | jq -r .status)" = "ready" ]'

assert "unauthenticated /api/engine/* is rejected with 401 (authz gate live)" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/engine/topology")" = "401" ]'

assert "unauthenticated /api/clusters is rejected with 401 (registry behind auth)" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/clusters")" = "401" ]'

assert "unauthenticated /api/nodes/stats is rejected with 401 (gate enforced)" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/nodes/stats")" = "401" ]'

assert "unauthenticated /api/clusters/{id}/events is rejected with 401" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/clusters/any/events")" = "401" ]'

assert "seam route /api/engine/kompass/* is behind the auth gate (401 unauth)" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/engine/kompass/inject")" = "401" ]'

assert "unauthenticated /api/admin/providers is rejected with 401" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" "$BASE/api/admin/providers")" = "401" ]'

assert "unauthenticated /api/ai/chat is rejected with 401" \
  '[ "$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE/api/ai/chat")" = "401" ]'

assert "engine is NOT reachable except via core (9280 not exposed)" \
  '! curl -fsS --max-time 5 "http://kompass-engine:9280/api/health" >/dev/null 2>&1'

assert "bootstrap admin credentials printed once to the core log" \
  'docker logs kompass-core 2>&1 | grep -q "INITIAL ADMIN CREDENTIALS"'

assert "UI is served and branded Kompass" \
  'curl -fsS "$BASE/" | grep -q "Kompass"'

assert "UI carries no upstream Radar branding" \
  '! curl -fsS "$BASE/" | grep -qi "radar"'

green "\nAll container gate checks passed."
