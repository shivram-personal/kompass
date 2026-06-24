"""Envelope encryption of arbitrary secret bytes (SPEC §4.2).

A fresh 256-bit DEK encrypts the plaintext with AES-256-GCM; the DEK is wrapped
by the configured KMS provider. The returned blob carries only ciphertext, the
wrapped DEK, and the nonce — never the plaintext or the unwrapped DEK.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

from cryptography.hazmat.primitives.ciphers.aead import AESGCM

from .kms import KmsError, KmsProvider


class EnvelopeError(Exception):
    """Encrypt/decrypt failure. Messages never include plaintext or key bytes."""


@dataclass
class Envelope:
    ciphertext: bytes
    wrapped_dek: bytes
    nonce: bytes
    kms_key_ref: str


def encrypt(plaintext: bytes, kms: KmsProvider) -> Envelope:
    dek = AESGCM.generate_key(bit_length=256)
    nonce = os.urandom(12)
    try:
        ciphertext = AESGCM(dek).encrypt(nonce, plaintext, None)
        wrapped = kms.wrap(dek)
    except KmsError:
        raise
    except Exception:
        raise EnvelopeError("encryption failed.")
    return Envelope(ciphertext=ciphertext, wrapped_dek=wrapped, nonce=nonce, kms_key_ref=kms.key_ref())


def decrypt(envelope: Envelope, kms: KmsProvider) -> bytes:
    """Recover plaintext in memory. Raises EnvelopeError on any failure without
    echoing ciphertext, plaintext, or key material."""
    try:
        dek = kms.unwrap(envelope.wrapped_dek)
        return AESGCM(dek).decrypt(envelope.nonce, envelope.ciphertext, None)
    except KmsError:
        # Surface KMS failures distinctly but still without key material.
        raise EnvelopeError("could not unwrap data key.")
    except Exception:
        raise EnvelopeError("decryption failed.")
