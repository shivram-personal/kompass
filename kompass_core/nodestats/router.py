"""Node stats endpoint (SPEC §4.7, §6): GET /api/nodes/stats — read, viewer+.

Aggregates the engine's current-cluster pod/node data over loopback. Read-only:
node mutations (cordon/uncordon) belong to the Phase 6 apply-action whitelist.
"""

from __future__ import annotations

import httpx
from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException

from ..auth.dependencies import AuthContext, require_active_user
from .service import aggregate

router = APIRouter(prefix="/api/nodes", tags=["nodes"])


async def _list_resource(engine: httpx.AsyncClient, kind: str) -> list:
    try:
        resp = await engine.get(f"/api/resources/{kind}")
    except httpx.HTTPError:
        raise HTTPException(status_code=502, detail="Engine is unreachable.")
    if resp.status_code != 200:
        raise HTTPException(status_code=502, detail="Engine could not list resources.")
    try:
        payload = resp.json()
    except ValueError:
        raise HTTPException(status_code=502, detail="Engine returned an invalid response.")
    if isinstance(payload, list):
        return payload
    items = payload.get("items") if isinstance(payload, dict) else None
    return items if isinstance(items, list) else []


@router.get("/stats")
async def node_stats(request: Request, ctx: AuthContext = Depends(require_active_user)):
    engine: httpx.AsyncClient = request.app.state.engine_client
    pods = await _list_resource(engine, "pods")
    nodes = await _list_resource(engine, "nodes")
    return aggregate(pods, nodes)
