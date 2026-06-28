"""Provider adapters: a thin per-shape interface for listing models.

Model names drift constantly, so we fetch them from each provider's models
endpoint at call time rather than hardcoding. The API key is passed in at call
time (decrypted in memory by the caller) and is never logged.
"""

from __future__ import annotations

import httpx

# Default API endpoints; overridable per-provider via base_url (e.g. proxies,
# Azure/OpenAI-compatible gateways, self-hosted).
_DEFAULT_BASE = {
    "anthropic": "https://api.anthropic.com",
    "openai": "https://api.openai.com",
}


class ProviderAdapter:
    name = "generic"

    def __init__(self, base_url: str | None) -> None:
        self.base_url = (base_url or "").rstrip("/")

    async def list_models(self, client: httpx.AsyncClient, api_key: str) -> list[str]:
        raise NotImplementedError


class AnthropicAdapter(ProviderAdapter):
    name = "anthropic"

    async def list_models(self, client: httpx.AsyncClient, api_key: str) -> list[str]:
        base = self.base_url or _DEFAULT_BASE["anthropic"]
        resp = await client.get(
            f"{base}/v1/models",
            headers={"x-api-key": api_key, "anthropic-version": "2023-06-01"},
        )
        resp.raise_for_status()
        data = resp.json().get("data", [])
        return [m["id"] for m in data if isinstance(m, dict) and "id" in m]


class OpenAICompatibleAdapter(ProviderAdapter):
    """Covers OpenAI and the many OpenAI-compatible APIs (the common shape)."""

    name = "openai"

    async def list_models(self, client: httpx.AsyncClient, api_key: str) -> list[str]:
        base = self.base_url or _DEFAULT_BASE["openai"]
        resp = await client.get(f"{base}/v1/models", headers={"Authorization": f"Bearer {api_key}"})
        resp.raise_for_status()
        data = resp.json().get("data", [])
        return [m["id"] for m in data if isinstance(m, dict) and "id" in m]


def get_adapter(provider: str, base_url: str | None) -> ProviderAdapter:
    """Resolve an adapter by provider name. Unknown providers default to the
    OpenAI-compatible shape (with their configured base_url)."""
    if provider.lower() == "anthropic":
        return AnthropicAdapter(base_url)
    return OpenAICompatibleAdapter(base_url)
