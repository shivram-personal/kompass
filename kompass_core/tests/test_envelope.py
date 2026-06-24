"""Unit tests for KMS envelope encryption with the local stand-in."""

import base64

import pytest

from kompass_core.secretstore.envelope import EnvelopeError, decrypt, encrypt
from kompass_core.secretstore.kms import KmsError, LocalKmsProvider

KEK_A = base64.b64encode(b"0" * 32).decode()
KEK_B = base64.b64encode(b"1" * 32).decode()


def test_roundtrip():
    kms = LocalKmsProvider(KEK_A)
    env = encrypt(b"hello-kubeconfig", kms)
    # The wrapped DEK is not the plaintext, and ciphertext != plaintext.
    assert env.ciphertext != b"hello-kubeconfig"
    assert decrypt(env, kms) == b"hello-kubeconfig"


def test_wrong_kek_fails_cleanly():
    env = encrypt(b"secret", LocalKmsProvider(KEK_A))
    with pytest.raises(EnvelopeError) as exc:
        decrypt(env, LocalKmsProvider(KEK_B))
    # No key material or plaintext in the error.
    assert "secret" not in str(exc.value)
    assert KEK_A not in str(exc.value)


def test_tampered_ciphertext_fails():
    kms = LocalKmsProvider(KEK_A)
    env = encrypt(b"secret-data", kms)
    env.ciphertext = env.ciphertext[:-1] + bytes([env.ciphertext[-1] ^ 0x01])
    with pytest.raises(EnvelopeError):
        decrypt(env, kms)


def test_missing_local_key_is_clean_error():
    with pytest.raises(KmsError) as exc:
        LocalKmsProvider("").wrap(b"0" * 32)
    assert "not configured" in str(exc.value)
