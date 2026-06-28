"""AI chat/troubleshoot orchestration — RECOMMENDATION-ONLY.

The model receives redacted, read-only cluster context and the user's message and
returns natural-language analysis. There is NO tool/function-calling and NO path
from here to any mutation: the only outbound calls are to the provider's chat API
and reads of core-owned data. The apply-action path is Phase 6 and is absent here.
"""

from __future__ import annotations

import json
import logging
import uuid
from collections.abc import AsyncIterator

import httpx
from sqlalchemy.orm import sessionmaker

from .. import audit
from ..clusters.service import ClusterService
from ..config import Settings
from ..events.service import EventService
from ..models import AiUsage, ChatMessage, ProviderConfig
from ..redact import redact_text
from .context import assemble_cluster_context
from .providers.adapters import get_adapter
from .providers.service import ProviderCredentialError, ProviderService

log = logging.getLogger("kompass.ai")

SYSTEM_PROMPT = (
    "You are Kompass's read-only Kubernetes assistant. You can explain cluster "
    "state and RECOMMEND changes in plain language, but you CANNOT perform any "
    "action, run commands, apply manifests, or modify anything. Never claim to "
    "have made a change. Describe what the user could do; the user applies changes "
    "themselves through a separate, audited path."
)


def _estimate_tokens(text: str) -> int:
    return max(1, len(text) // 4)


class ChatService:
    def __init__(
        self,
        settings: Settings,
        provider_service: ProviderService,
        cluster_service: ClusterService,
        event_service: EventService,
    ) -> None:
        self.settings = settings
        self.providers = provider_service
        self.clusters = cluster_service
        self.events = event_service

    def resolve_provider(self, db, requested: str | None) -> ProviderConfig | None:
        """Pick a usable provider: requested (if enabled, keyed, has model) else
        the first enabled provider that has a key and an active model."""
        candidates = self.providers.list(db)
        for p in candidates:
            if requested and p.provider != requested:
                continue
            if p.enabled and p.has_api_key and p.active_model:
                return p
        return None

    async def run(
        self,
        *,
        session_factory: sessionmaker,
        user_id: int,
        username: str,
        role: str,
        cluster_id: str,
        provider_name: str,
        message: str,
        history: list[dict],
        action: str,
        focus: str | None = None,
        request_id: str | None = None,
    ) -> AsyncIterator[dict]:
        request_id = request_id or uuid.uuid4().hex
        db = session_factory()
        try:
            # Audit-before-execute: the AI session occurred, against which
            # cluster — never storing secret cluster data.
            audit.record(db, action=action, result="attempt", username=username,
                         role=role, cluster_id=cluster_id, target=focus, request_id=request_id)

            provider = self.providers.get(db, provider_name)
            if provider is None or not provider.active_model:
                yield {"event": "error", "data": "AI provider is not configured."}
                return

            context = assemble_cluster_context(db, cluster_id, self.clusters, self.events)
            if focus:
                context += f"\n\nFocus resource: {focus}"

            # Decrypt the key in memory at call time only.
            try:
                api_key = self.providers.decrypt_api_key(provider)
            except ProviderCredentialError:
                yield {"event": "error", "data": "Could not access provider credentials."}
                return

            messages = [
                {"role": "user", "content": f"Read-only cluster context:\n{context}"},
                *[{"role": t["role"], "content": t["content"]} for t in history],
                {"role": "user", "content": message},
            ]
            adapter = get_adapter(provider.provider, provider.base_url)

            # Active-model badge first (SPEC §4.3 — present on every AI response).
            yield {"event": "model",
                   "data": json.dumps({"provider": provider.provider, "model": provider.active_model})}

            usage: dict = {}
            chunks: list[str] = []
            try:
                async with httpx.AsyncClient(timeout=httpx.Timeout(60.0, read=None)) as client:
                    async for delta in adapter.stream_chat(
                        client, api_key, provider.active_model, SYSTEM_PROMPT, messages, usage
                    ):
                        chunks.append(delta)
                        yield {"event": "delta", "data": delta}
            except httpx.HTTPError:
                yield {"event": "error", "data": "AI provider request failed."}
                return

            full = "".join(chunks)
            self._record(db, user_id, cluster_id, provider, usage, message, full, context, request_id)

            prompt_tokens = usage.get("prompt_tokens") or _estimate_tokens(context + message)
            completion_tokens = usage.get("completion_tokens") or _estimate_tokens(full)
            yield {"event": "usage",
                   "data": json.dumps({"prompt_tokens": prompt_tokens,
                                       "completion_tokens": completion_tokens})}
            yield {"event": "done", "data": "[DONE]"}
        finally:
            db.close()

    def _record(self, db, user_id, cluster_id, provider, usage, message, full, context, request_id):
        db.add(AiUsage(
            user_id=user_id, cluster_id=cluster_id, provider=provider.provider,
            model=provider.active_model,
            prompt_tokens=usage.get("prompt_tokens") or _estimate_tokens(context + message),
            completion_tokens=usage.get("completion_tokens") or _estimate_tokens(full),
            request_id=request_id,
        ))
        # Persist history REDACTED so no captured secret is kept in plaintext.
        db.add(ChatMessage(user_id=user_id, cluster_id=cluster_id, role="user",
                           content=redact_text(message) or "", provider=provider.provider,
                           model=provider.active_model, request_id=request_id))
        db.add(ChatMessage(user_id=user_id, cluster_id=cluster_id, role="assistant",
                           content=redact_text(full) or "", provider=provider.provider,
                           model=provider.active_model, request_id=request_id))
        db.commit()
