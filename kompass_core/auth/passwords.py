"""Argon2id password hashing with EXPLICIT parameters (SPEC §4.1).

Parameters are taken from Settings, never argon2-cffi's library defaults, so
the cost factors are auditable and documented (CONFIG_VALUES.md). Plaintext
passwords are never stored, logged, or returned.
"""

from __future__ import annotations

from argon2 import PasswordHasher, Type
from argon2.exceptions import InvalidHashError, VerifyMismatchError

from ..config import Settings


class PasswordManager:
    def __init__(self, settings: Settings) -> None:
        self._hasher = PasswordHasher(
            time_cost=settings.argon2_time_cost,
            memory_cost=settings.argon2_memory_cost,
            parallelism=settings.argon2_parallelism,
            hash_len=settings.argon2_hash_len,
            salt_len=settings.argon2_salt_len,
            type=Type.ID,  # Argon2id
        )

    def hash(self, password: str) -> str:
        return self._hasher.hash(password)

    def verify(self, password_hash: str, password: str) -> bool:
        try:
            return self._hasher.verify(password_hash, password)
        except (VerifyMismatchError, InvalidHashError, Exception):
            return False

    def needs_rehash(self, password_hash: str) -> bool:
        try:
            return self._hasher.check_needs_rehash(password_hash)
        except Exception:
            return False
