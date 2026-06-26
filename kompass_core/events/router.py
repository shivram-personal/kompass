"""Per-cluster event listing (Phase 3).

Read endpoint behind the core authz gate. Per-cluster visibility: admins and
viewers read any cluster's events; an editor may read only events for clusters
in their scope (out-of-scope -> 403), satisfying "editor scoped to A cannot
read B's events".
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, Query, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..auth.dependencies import AuthContext, get_db, require_active_user
from ..events.service import EventService
from ..models import Role

router = APIRouter(prefix="/api/clusters", tags=["events"])


def _service(request: Request) -> EventService:
    return request.app.state.event_service


@router.get("/{cluster_id}/events")
def list_cluster_events(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
    limit: int = Query(default=200, le=1000, ge=1),
):
    user = ctx.user
    if Role(user.role) == Role.editor and cluster_id not in user.allowed_cluster_ids:
        audit.record(db, action="event_read", result="scope_denied", username=user.username,
                     role=user.role, cluster_id=cluster_id)
        raise HTTPException(status_code=403, detail="Cluster not in editor scope.")
    return _service(request).list(db, cluster_id, limit=limit)
