"""Secret redaction for data that may inadvertently capture sensitive material.

Used for cluster-event payloads (an event message can echo a Secret's data, a
kubeconfig embedded in a ConfigMap, a bearer token, etc.). The AI context
redactor (Phase 5) is a separate, richer pipeline; this is a focused scrubber
for at-rest event text. It is deliberately conservative: it would rather redact
too much than persist a secret.
"""

from __future__ import annotations

import re

REDACTED = "[REDACTED]"

# Values following a sensitive key (token:, password=, certificate-authority-data: …).
_SENSITIVE_KEY = re.compile(
    r"(?i)\b(token|password|passwd|secret|authorization|bearer|api[-_]?key|"
    r"client-key-data|client-certificate-data|certificate-authority-data)\b"
    r"\s*[:=]\s*\S+"
)
# PEM private-key blocks.
_PEM = re.compile(r"-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----", re.DOTALL)
# Long base64-ish runs (cert/key blobs, encoded secrets).
_LONG_B64 = re.compile(r"\b[A-Za-z0-9+/]{40,}={0,2}\b")
# JWT-shaped tokens.
_JWT = re.compile(r"\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b")


def redact_text(text: str | None) -> str | None:
    if not text:
        return text
    out = _PEM.sub(REDACTED, text)
    out = _SENSITIVE_KEY.sub(lambda m: f"{m.group(1)}: {REDACTED}", out)
    out = _JWT.sub(REDACTED, out)
    out = _LONG_B64.sub(REDACTED, out)
    return out
