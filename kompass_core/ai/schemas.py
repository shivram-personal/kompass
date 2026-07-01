"""Request models for the AI chat/troubleshoot endpoints."""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class ChatTurn(_Strict):
    role: str = Field(pattern="^(user|assistant)$")
    content: str = Field(min_length=1, max_length=16000)


class ChatRequest(_Strict):
    cluster_id: str = Field(min_length=1, max_length=255)
    message: str = Field(min_length=1, max_length=16000)
    provider: str | None = None
    history: list[ChatTurn] = Field(default_factory=list, max_length=40)


class TroubleshootRequest(_Strict):
    cluster_id: str = Field(min_length=1, max_length=255)
    kind: str = Field(min_length=1, max_length=128)
    name: str = Field(min_length=1, max_length=255)
    namespace: str | None = Field(default=None, max_length=255)
    message: str | None = Field(default=None, max_length=16000)
    provider: str | None = None


class ApplyRequest(_Strict):
    """Confirm-and-apply a previously previewed proposal. The content hash binds
    the apply to the exact previewed content — a changed proposal is rejected."""

    content_hash: str = Field(min_length=64, max_length=64, pattern="^[0-9a-f]{64}$")
