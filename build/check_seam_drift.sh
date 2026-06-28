#!/usr/bin/env bash
# Seam-drift detection (Kompass engine seam #3, docs/SPEC.md ADR-001).
#
# Fails loudly if net-new engine logic escapes its sanctioned boundary:
#   1. every .go file containing a "KOMPASS SEAM" marker must be in the manifest
#   2. every kompass_seam_*.go file must be in the manifest
#   3. the only upstream (non-seam) files allowed to carry markers are the two
#      declared hook files
# This guard catches a future change quietly smearing logic into the engine.
set -euo pipefail

MANIFEST="build/seam_manifest.txt"
HOOK_FILES=("internal/k8s/client.go" "internal/server/server.go")

allowed="$(grep -vE '^\s*#|^\s*$' "$MANIFEST" | sort -u)"
in_allowed() { printf '%s\n' "$allowed" | grep -qxF "$1"; }

fail() { echo "SEAM DRIFT: $1" >&2; exit 1; }

# 1 + 3: every file with a KOMPASS SEAM marker is accounted for.
marked="$(grep -rlF "KOMPASS SEAM" --include='*.go' cmd internal pkg 2>/dev/null | sort -u || true)"
for f in $marked; do
  in_allowed "$f" || fail "$f carries a KOMPASS SEAM marker but is not in $MANIFEST"
  case "$f" in
    */kompass_seam_*.go) ;;                                   # net-new seam file: fine
    "${HOOK_FILES[0]}"|"${HOOK_FILES[1]}") ;;                 # declared upstream hook: fine
    *) fail "unexpected upstream file with a KOMPASS SEAM marker: $f" ;;
  esac
done

# 2: every kompass_seam_*.go file is declared.
seamfiles="$(find cmd internal pkg -name 'kompass_seam_*.go' 2>/dev/null | sort -u || true)"
for f in $seamfiles; do
  in_allowed "$f" || fail "seam file $f is not declared in $MANIFEST"
done

echo "seam-drift check: OK (engine seam confined to the documented surface)"
