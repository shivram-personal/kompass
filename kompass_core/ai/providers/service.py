"""Provider/credential management. API keys are envelope-encrypted via the
Phase 2 KMS path (reused, not reinvented), decrypted in memory only at call
time, masked on read, and never logged."""

from __future__ import annotations

import json
from typing import Any

import httpx
from sqlalchemy.orm import Session as DbSession

from ... import audit
from ...models import ProviderConfig, User
from ...secretstore import envelope
from ...secretstore.envelope import Envelope, EnvelopeError
from ...secretstore.kms import KmsProvider
from .adapters import get_adapter


class ProviderError(Exception):
    """Client-correctable provider error (bad input). Never echoes secrets."""


class ProviderCredentialError(Exception):
    """Credential decryption/KMS failure. Never carries key material."""


def _last4(api_key: str) -> str:
    return api_key[-4:] if len(api_key) >= 4 else "****"


class ProviderService:
    def __init__(self, kms: KmsProvider) -> None:
        self.kms = kms

    def list(self, db: DbSession) -> list[ProviderConfig]:
        return db.query(ProviderConfig).order_by(ProviderConfig.provider).all()

    def get(self, db: DbSession, provider: str) -> ProviderConfig | None:
        return db.query(ProviderConfig).filter(ProviderConfig.provider == provider).one_or_none()

    def _set_api_key(self, p: ProviderConfig, api_key: str) -> None:
        env = envelope.encrypt(api_key.encode("utf-8"), self.kms)
        p.api_key_ciphertext = env.ciphertext
        p.wrapped_dek = env.wrapped_dek
        p.nonce = env.nonce
        p.kms_key_ref = env.kms_key_ref
        p.api_key_last4 = _last4(api_key)

    def create(
        self,
        db: DbSession,
        *,
        actor: User,
        provider: str,
        base_url: str | None,
        api_key: str | None,
        active_model: str | None,
        enabled: bool,
        models: list[str],
    ) -> ProviderConfig:
        if not provider.strip():
            raise ProviderError("provider name is required.")
        if self.get(db, provider) is not None:
            raise ProviderError("provider already configured.")
        # Audit-before-execute. Never include the API key.
        audit.record(db, action="provider_create", result="attempt",
                     username=actor.username, role=actor.role, target=provider,
                     params={"enabled": enabled, "has_key": bool(api_key)})
        p = ProviderConfig(
            provider=provider,
            enabled=enabled,
            base_url=base_url,
            active_model=active_model,
            extra_json=json.dumps({"models": models}),
            updated_by=actor.username,
        )
        if api_key:
            self._set_api_key(p, api_key)
        db.add(p)
        db.commit()
        return p

    def update(self, db: DbSession, *, actor: User, p: ProviderConfig, fields: dict[str, Any]) -> ProviderConfig:
        rotating = "api_key" in fields and fields["api_key"]
        audit.record(db, action=("provider_rotate_key" if rotating else "provider_update"),
                     result="attempt", username=actor.username, role=actor.role, target=p.provider,
                     params={k: v for k, v in fields.items() if k != "api_key"})
        if "enabled" in fields and fields["enabled"] is not None:
            p.enabled = fields["enabled"]
        if "base_url" in fields and fields["base_url"] is not None:
            p.base_url = fields["base_url"]
        if "active_model" in fields and fields["active_model"] is not None:
            p.active_model = fields["active_model"]
        if "models" in fields and fields["models"] is not None:
            p.extra_json = json.dumps({"models": fields["models"]})
        if rotating:
            self._set_api_key(p, fields["api_key"])
        p.updated_by = actor.username
        db.commit()
        return p

    def delete(self, db: DbSession, *, actor: User, p: ProviderConfig) -> None:
        audit.record(db, action="provider_delete", result="attempt",
                     username=actor.username, role=actor.role, target=p.provider)
        db.delete(p)  # purges ciphertext + wrapped DEK
        db.commit()

    def decrypt_api_key(self, p: ProviderConfig) -> str:
        """In-memory decryption at point of use. Never logged or returned."""
        if not p.has_api_key:
            raise ProviderCredentialError("no API key configured")
        env = Envelope(
            ciphertext=p.api_key_ciphertext,
            wrapped_dek=p.wrapped_dek,
            nonce=p.nonce,
            kms_key_ref=p.kms_key_ref or "",
        )
        try:
            return envelope.decrypt(env, self.kms).decode("utf-8")
        except EnvelopeError:
            raise ProviderCredentialError("could not access provider credentials")

    async def list_models(self, p: ProviderConfig) -> dict[str, Any]:
        """Model picker: fetch from the provider when a key is set, else return
        the admin-editable list. Decrypt failure is surfaced as a clean error;
        a fetch failure falls back to the configured list."""
        editable = []
        if p.extra_json:
            try:
                editable = json.loads(p.extra_json).get("models", [])
            except ValueError:
                editable = []

        if not p.has_api_key:
            return {"source": "configured", "models": editable, "active_model": p.active_model}

        api_key = self.decrypt_api_key(p)  # raises ProviderCredentialError on tamper
        adapter = get_adapter(p.provider, p.base_url)
        try:
            async with httpx.AsyncClient(timeout=15.0) as client:
                models = await adapter.list_models(client, api_key)
            return {"source": "provider", "models": models, "active_model": p.active_model}
        except (httpx.HTTPError, KeyError, ValueError):
            # Network/parse failure → fall back to the admin-editable list.
            return {"source": "configured", "models": editable, "active_model": p.active_model}
