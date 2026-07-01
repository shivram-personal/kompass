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
from . import budget
from .chat import ChatService
from .proposals import ProposalConflict, ProposalError, ProposalService
from .schemas import ApplyRequest, ChatRequest, TroubleshootRequest

router = APIRouter(prefix="/api/ai", tags=["ai"])


def _service(request: Request) -> ChatService:
    return request.app.state.chat_service


def _proposals(request: Request) -> ProposalService:
    return request.app.state.proposal_service


def _enforce_budget(request: Request, db: DbSession, ctx: AuthContext) -> None:
    """Refuse the LLM call when the user's daily token budget is exhausted — BEFORE
    the provider is contacted (SPEC §4.5). Apply is not budget-gated (no provider call)."""
    settings = request.app.state.settings
    if budget.is_exhausted(db, ctx.user, settings):
        audit.record(db, action="ai_budget", result="budget_exceeded",
                     username=ctx.user.username, role=ctx.user.role)
        raise HTTPException(status_code=429, detail="Daily AI token budget exhausted. Try again tomorrow or ask an admin to raise your budget.")


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
    _enforce_budget(request, db, ctx)
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
    _enforce_budget(request, db, ctx)
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


def _load_proposal_or_404(svc: ProposalService, db: DbSession, proposal_id: str):
    proposal = svc.get(db, proposal_id)
    if proposal is None:
        raise HTTPException(status_code=404, detail="Proposal not found.")
    return proposal


@router.get("/proposals/{proposal_id}/preview")
async def preview_proposal(
    proposal_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    """Compute the before/after diff and capture the drift baseline (a live read of
    the target's current state). Restricted to the proposal's creator or an admin."""
    svc = _proposals(request)
    proposal = _load_proposal_or_404(svc, db, proposal_id)
    if not svc.can_apply(proposal, ctx.user):  # creator or admin
        raise HTTPException(status_code=403, detail="Not authorized to preview this proposal.")
    try:
        return await svc.preview(
            engine=request.app.state.engine_client,
            lock=request.app.state.engine_context_lock,
            session_factory=request.app.state.session_factory,
            proposal_id=proposal_id,
        )
    except ProposalConflict as e:
        raise HTTPException(status_code=409, detail=str(e))
    except ProposalError as e:
        raise HTTPException(status_code=400, detail=str(e))


@router.post("/proposals/{proposal_id}/apply")
async def apply_proposal(
    proposal_id: str,
    body: ApplyRequest,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),  # authenticated + CSRF (write) + active
    db: DbSession = Depends(get_db),
):
    """Apply a previewed proposal — the only mutating AI path. Requires editor
    (in-scope for the target cluster) or admin, AND the proposal's creator or an
    admin (separation of duties). Bound to the proposal id + confirmed content hash."""
    svc = _proposals(request)
    proposal = _load_proposal_or_404(svc, db, proposal_id)
    user = ctx.user
    role = Role(user.role)

    # RBAC on apply (server-side; UI hiding is cosmetic).
    if role == Role.viewer:
        audit.record(db, action="ai_apply", result="role_denied", username=user.username,
                     role=user.role, cluster_id=proposal.cluster_id, target=proposal.target,
                     params={"proposal_id": proposal.id})
        raise HTTPException(status_code=403, detail="Viewers cannot apply actions.")
    if role == Role.editor and proposal.cluster_id not in user.allowed_cluster_ids:
        audit.record(db, action="ai_apply", result="scope_denied", username=user.username,
                     role=user.role, cluster_id=proposal.cluster_id, target=proposal.target,
                     params={"proposal_id": proposal.id})
        raise HTTPException(status_code=403, detail="Target cluster not in your scope.")
    if not svc.can_apply(proposal, user):  # creator or admin
        audit.record(db, action="ai_apply", result="ownership_denied", username=user.username,
                     role=user.role, cluster_id=proposal.cluster_id, target=proposal.target,
                     params={"proposal_id": proposal.id})
        raise HTTPException(status_code=403, detail="Only the proposal's creator or an admin may apply it.")

    try:
        return await svc.apply(
            engine=request.app.state.engine_client,
            lock=request.app.state.engine_context_lock,
            session_factory=request.app.state.session_factory,
            proposal_id=proposal_id,
            user=user,
            confirmed_hash=body.content_hash,
            request_id=uuid.uuid4().hex,
        )
    except ProposalConflict as e:
        raise HTTPException(status_code=409, detail=str(e))
    except ProposalError as e:
        raise HTTPException(status_code=400, detail=str(e))
