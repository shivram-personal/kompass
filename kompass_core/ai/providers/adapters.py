"""Provider adapters: a thin per-shape interface for listing models.

Model names drift constantly, so we fetch them from each provider's models
endpoint at call time rather than hardcoding. The API key is passed in at call
time (decrypted in memory by the caller) and is never logged.
"""

from __future__ import annotations

import json
from collections.abc import AsyncIterator

import httpx

# Messages are [{"role": "user"|"assistant", "content": str}]; `system` is the
# system prompt. `usage_out` is filled with prompt/completion token counts when
# the provider reports them.
Messages = list[dict[str, str]]

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

    async def stream_chat(
        self, client: httpx.AsyncClient, api_key: str, model: str,
        system: str, messages: Messages, usage_out: dict,
    ) -> AsyncIterator[str]:
        raise NotImplementedError
        yield ""  # pragma: no cover (makes this an async generator)


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

    async def stream_chat(
        self, client: httpx.AsyncClient, api_key: str, model: str,
        system: str, messages: Messages, usage_out: dict,
    ) -> AsyncIterator[str]:
        base = self.base_url or _DEFAULT_BASE["anthropic"]
        payload = {
            "model": model, "max_tokens": 1024, "system": system,
            "messages": messages, "stream": True,
        }
        headers = {"x-api-key": api_key, "anthropic-version": "2023-06-01"}
        async with client.stream("POST", f"{base}/v1/messages", json=payload, headers=headers) as resp:
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if not line.startswith("data:"):
                    continue
                raw = line[5:].strip()
                if not raw:
                    continue
                try:
                    obj = json.loads(raw)
                except ValueError:
                    continue
                t = obj.get("type")
                if t == "content_block_delta":
                    txt = (obj.get("delta") or {}).get("text")
                    if txt:
                        yield txt
                elif t == "message_start":
                    usage_out["prompt_tokens"] = (
                        (obj.get("message") or {}).get("usage") or {}).get("input_tokens", 0)
                elif t == "message_delta":
                    usage_out["completion_tokens"] = (obj.get("usage") or {}).get(
                        "output_tokens", usage_out.get("completion_tokens", 0))


class OpenAICompatibleAdapter(ProviderAdapter):
    """Covers OpenAI and the many OpenAI-compatible APIs (the common shape)."""

    name = "openai"

    async def list_models(self, client: httpx.AsyncClient, api_key: str) -> list[str]:
        base = self.base_url or _DEFAULT_BASE["openai"]
        resp = await client.get(f"{base}/v1/models", headers={"Authorization": f"Bearer {api_key}"})
        resp.raise_for_status()
        data = resp.json().get("data", [])
        return [m["id"] for m in data if isinstance(m, dict) and "id" in m]

    async def stream_chat(
        self, client: httpx.AsyncClient, api_key: str, model: str,
        system: str, messages: Messages, usage_out: dict,
    ) -> AsyncIterator[str]:
        base = self.base_url or _DEFAULT_BASE["openai"]
        payload = {
            "model": model,
            "messages": [{"role": "system", "content": system}, *messages],
            "stream": True,
            "stream_options": {"include_usage": True},
        }
        headers = {"Authorization": f"Bearer {api_key}"}
        async with client.stream("POST", f"{base}/v1/chat/completions", json=payload, headers=headers) as resp:
            resp.raise_for_status()
            async for line in resp.aiter_lines():
                if not line.startswith("data:"):
                    continue
                raw = line[5:].strip()
                if not raw or raw == "[DONE]":
                    continue
                try:
                    obj = json.loads(raw)
                except ValueError:
                    continue
                for choice in obj.get("choices") or []:
                    txt = (choice.get("delta") or {}).get("content")
                    if txt:
                        yield txt
                if obj.get("usage"):
                    usage_out["prompt_tokens"] = obj["usage"].get("prompt_tokens", 0)
                    usage_out["completion_tokens"] = obj["usage"].get("completion_tokens", 0)


def get_adapter(provider: str, base_url: str | None) -> ProviderAdapter:
    """Resolve an adapter by provider name. Unknown providers default to the
    OpenAI-compatible shape (with their configured base_url)."""
    if provider.lower() == "anthropic":
        return AnthropicAdapter(base_url)
    return OpenAICompatibleAdapter(base_url)
