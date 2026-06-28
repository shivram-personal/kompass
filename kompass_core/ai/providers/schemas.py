"""Provider request/response models. Responses never carry the API key — only
a masked `…last4` hint and whether a key is set."""

from __future__ import annotations

import json

from pydantic import BaseModel, ConfigDict, Field

from ...models import ProviderConfig


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class CreateProviderRequest(_Strict):
    provider: str = Field(min_length=1, max_length=64)
    base_url: str | None = Field(default=None, max_length=512)
    api_key: str | None = Field(default=None, max_length=4096)
    active_model: str | None = Field(default=None, max_length=255)
    enabled: bool = True
    # Admin-editable fallback model list (used when the provider has no
    # fetchable models endpoint or a fetch fails).
    models: list[str] = Field(default_factory=list)


class UpdateProviderRequest(_Strict):
    base_url: str | None = Field(default=None, max_length=512)
    api_key: str | None = Field(default=None, max_length=4096)  # rotate when provided
    active_model: str | None = Field(default=None, max_length=255)
    enabled: bool | None = None
    models: list[str] | None = None


def _editable_models(p: ProviderConfig) -> list[str]:
    if not p.extra_json:
        return []
    try:
        return json.loads(p.extra_json).get("models", [])
    except (ValueError, AttributeError):
        return []


def provider_public(p: ProviderConfig) -> dict:
    return {
        "id": p.id,
        "provider": p.provider,
        "enabled": p.enabled,
        "base_url": p.base_url,
        "active_model": p.active_model,
        "has_api_key": p.has_api_key,
        "api_key_masked": f"…{p.api_key_last4}" if p.has_api_key and p.api_key_last4 else None,
        "configured_models": _editable_models(p),
        "updated_by": p.updated_by,
        "updated_at": p.updated_at.isoformat(),
    }
