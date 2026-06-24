"""Argon2id parameters must be EXPLICIT (not library defaults) — SPEC §4.1."""

from kompass_core.auth.passwords import PasswordManager
from kompass_core.config import Settings


def test_argon2_parameters_are_explicit_and_documented_values():
    settings = Settings()
    assert settings.argon2_time_cost == 3
    assert settings.argon2_memory_cost == 65536  # 64 MiB in KiB
    assert settings.argon2_parallelism == 1
    assert settings.argon2_hash_len == 32
    assert settings.argon2_salt_len == 16


def test_hash_encodes_argon2id_with_configured_costs():
    pm = PasswordManager(Settings())
    h = pm.hash("a-sufficiently-long-password")
    # Encoded hash advertises the algorithm + cost factors.
    assert h.startswith("$argon2id$")
    assert "m=65536,t=3,p=1" in h
    assert pm.verify(h, "a-sufficiently-long-password") is True
    assert pm.verify(h, "not-the-password") is False
