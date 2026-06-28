"""AI chat/troubleshoot endpoints (SPEC §6) — recommendation-only.

All endpoints go through the core authz gate. Cluster access follows per-cluster
scope: an editor may only target clusters in their scope (viewer/admin per §4.1).
There is no mutation path here.
"""

from __future__ import annotations

import uuid
from collections.abc import AsyncIterator

from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException
from fastapi.responses import StreamingResponse
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..auth.dependencies import AuthContext, get_db, require_active_user
from ..models import ChatMessage, Role
from .chat import ChatService
from .schemas import ChatRequest, TroubleshootRequest

router = APIRouter(prefix="/api/ai", tags=["ai"])


def _service(request: Request) -> ChatService:
    return request.app.state.chat_service


async def _sse_frames(gen: AsyncIterator[dict]) -> AsyncIterator[str]:
    """Serialize {event,data} dicts as SSE frames. Multi-line data is split into
    multiple `data:` lines per the SSE spec. (Hand-rolled to avoid event-loop
    binding issues with sse-starlette under the test client.)"""
    async for ev in gen:
        event = ev.get("event", "message")
        data = str(ev.get("data", ""))
        frame = f"event: {event}\n" + "".join(f"data: {line}\n" for line in data.split("\n")) + "\n"
        yield frame


def _sse_response(gen: AsyncIterator[dict]) -> StreamingResponse:
    return StreamingResponse(_sse_frames(gen), media_type="text/event-stream")


def _authorize_cluster(db: DbSession, ctx: AuthContext, cluster_id: str, action: str) -> None:
    """Per-cluster read scope: editors limited to their clusters; viewer/admin any."""
    user = ctx.user
    if Role(user.role) == Role.editor and cluster_id not in user.allowed_cluster_ids:
        audit.record(db, action=action, result="scope_denied", username=user.username,
                     role=user.role, cluster_id=cluster_id)
        raise HTTPException(status_code=403, detail="Cluster not in your scope.")


def _require_cluster_exists(request: Request, db: DbSession, cluster_id: str) -> None:
    if request.app.state.cluster_service.get(db, cluster_id) is None:
        raise HTTPException(status_code=404, detail="Cluster not found.")


@router.post("/chat")
def chat(
    body: ChatRequest,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    _require_cluster_exists(request, db, body.cluster_id)
    _authorize_cluster(db, ctx, body.cluster_id, "ai_chat")
    svc = _service(request)
    provider = svc.resolve_provider(db, body.provider)
    if provider is None:
        raise HTTPException(status_code=400, detail="No usable AI provider is configured.")

    generator = svc.run(
        session_factory=request.app.state.session_factory,
        user_id=ctx.user.id, username=ctx.user.username, role=ctx.user.role,
        cluster_id=body.cluster_id, provider_name=provider.provider,
        message=body.message, history=[t.model_dump() for t in body.history],
        action="ai_chat", request_id=uuid.uuid4().hex,
    )
    return _sse_response(generator)


@router.post("/troubleshoot")
def troubleshoot(
    body: TroubleshootRequest,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    _require_cluster_exists(request, db, body.cluster_id)
    _authorize_cluster(db, ctx, body.cluster_id, "ai_troubleshoot")
    svc = _service(request)
    provider = svc.resolve_provider(db, body.provider)
    if provider is None:
        raise HTTPException(status_code=400, detail="No usable AI provider is configured.")

    focus = f"{body.kind}/{body.namespace or '-'}/{body.name}"
    message = body.message or f"Why is {focus} unhealthy and what do you recommend?"
    generator = svc.run(
        session_factory=request.app.state.session_factory,
        user_id=ctx.user.id, username=ctx.user.username, role=ctx.user.role,
        cluster_id=body.cluster_id, provider_name=provider.provider,
        message=message, history=[], action="ai_troubleshoot", focus=focus,
        request_id=uuid.uuid4().hex,
    )
    return _sse_response(generator)


@router.get("/history")
def history(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    _authorize_cluster(db, ctx, cluster_id, "ai_history")
    rows = (
        db.query(ChatMessage)
        .filter(ChatMessage.user_id == ctx.user.id, ChatMessage.cluster_id == cluster_id)
        .order_by(ChatMessage.ts)
        .limit(200)
        .all()
    )
    return [
        {"role": m.role, "content": m.content, "provider": m.provider,
         "model": m.model, "ts": m.ts.isoformat()}
        for m in rows
    ]
