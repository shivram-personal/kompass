"""KMS providers that wrap/unwrap data-encryption keys.

Two implementations:

* ``GcpKmsProvider`` — production. Wraps the DEK with Google Cloud KMS; the KEK
  never leaves the KMS HSM. ``google-cloud-kms`` is imported lazily so the
  test/dev path needs neither the package nor GCP credentials.

* ``LocalKmsProvider`` — a CLEARLY-MARKED dev/test STAND-IN. It wraps the DEK
  with a 32-byte KEK provided out-of-band via ``KOMPASS_LOCAL_KMS_KEY`` (base64).
  Crucially it preserves the "no plaintext at rest" invariant: the database
  holds only ciphertext + the KEK-wrapped DEK, and the KEK lives outside the DB.
  It is NOT a substitute for KMS in production.

Errors raise ``KmsError`` with messages that never include key material.
"""

from __future__ import annotations

import base64
import logging
import os

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from ..config import Settings

log = logging.getLogger("kompass.kms")


class KmsError(Exception):
    """KMS wrap/unwrap failure. Messages never carry key or plaintext material."""


class KmsProvider:
    def key_ref(self) -> str:
        raise NotImplementedError

    def wrap(self, dek: bytes) -> bytes:
        raise NotImplementedError

    def unwrap(self, wrapped: bytes) -> bytes:
        raise NotImplementedError


class LocalKmsProvider(KmsProvider):
    """DEV/TEST STAND-IN — wraps the DEK with a local KEK (AES-GCM)."""

    KEY_REF = "local-kms-standin"

    def __init__(self, key_b64: str) -> None:
        self._key_b64 = key_b64
        log.warning(
            "kompass-core is using the LOCAL KMS STAND-IN (dev/test only) — "
            "do not use in production; configure Cloud KMS instead."
        )

    def _kek(self) -> bytes:
        if not self._key_b64:
            raise KmsError("local KMS key is not configured (KOMPASS_LOCAL_KMS_KEY).")
        try:
            kek = base64.b64decode(self._key_b64)
        except Exception:
            raise KmsError("local KMS key is not valid base64.")
        if len(kek) != 32:
            raise KmsError("local KMS key must decode to exactly 32 bytes.")
        return kek

    def key_ref(self) -> str:
        return self.KEY_REF

    def wrap(self, dek: bytes) -> bytes:
        nonce = os.urandom(12)
        ct = AESGCM(self._kek()).encrypt(nonce, dek, b"kompass-dek")
        return nonce + ct  # prepend nonce; opaque blob

    def unwrap(self, wrapped: bytes) -> bytes:
        try:
            nonce, ct = wrapped[:12], wrapped[12:]
            return AESGCM(self._kek()).decrypt(nonce, ct, b"kompass-dek")
        except KmsError:
            raise
        except Exception:
            raise KmsError("failed to unwrap data key.")


class GcpKmsProvider(KmsProvider):
    """Production — wraps the DEK with Google Cloud KMS."""

    def __init__(self, key_name: str) -> None:
        if not key_name:
            raise KmsError("KMS key name is not configured (KOMPASS_KMS_KEY_NAME).")
        self._key_name = key_name
        self._client = None

    def _kms(self):
        if self._client is None:
            try:
                from google.cloud import kms  # lazy: prod-only dependency
            except ImportError:
                raise KmsError("google-cloud-kms is not installed.")
            self._client = kms.KeyManagementServiceClient()
        return self._client

    def key_ref(self) -> str:
        return self._key_name

    def wrap(self, dek: bytes) -> bytes:
        try:
            resp = self._kms().encrypt(request={"name": self._key_name, "plaintext": dek})
            return resp.ciphertext
        except KmsError:
            raise
        except Exception:
            raise KmsError("KMS encrypt (wrap) failed.")

    def unwrap(self, wrapped: bytes) -> bytes:
        try:
            resp = self._kms().decrypt(request={"name": self._key_name, "ciphertext": wrapped})
            return resp.plaintext
        except KmsError:
            raise
        except Exception:
            raise KmsError("KMS decrypt (unwrap) failed.")


def build_kms_provider(settings: Settings) -> KmsProvider:
    provider = settings.kms_provider.lower()
    if provider == "gcp":
        return GcpKmsProvider(settings.kms_key_name)
    if provider == "local":
        return LocalKmsProvider(settings.local_kms_key)
    raise KmsError(f"unknown KMS provider: {settings.kms_provider!r}")
