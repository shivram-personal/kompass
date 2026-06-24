"""Cluster registry endpoints (SPEC §6).

- list/get: any authenticated user (reads span all clusters).
- register/delete: admin only (server-enforced).
- select: any authenticated user; decrypts the kubeconfig IN MEMORY at point of
  use and forwards a cluster-targeting context switch to the engine over
  loopback. The engine never receives the kubeconfig store; the decrypted
  material is never logged or returned.
"""

from __future__ import annotations

import logging

import httpx
from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from ..auth.dependencies import AuthContext, get_db, require_active_user, require_admin
from ..models import Cluster
from .schemas import RegisterClusterRequest, cluster_public
from .service import ClusterError, ClusterService

log = logging.getLogger("kompass.clusters")
router = APIRouter(prefix="/api/clusters", tags=["clusters"])


def _service(request: Request) -> ClusterService:
    return request.app.state.cluster_service


def _get_or_404(svc: ClusterService, db: DbSession, cluster_id: str) -> Cluster:
    cluster = svc.get(db, cluster_id)
    if cluster is None:
        raise HTTPException(status_code=404, detail="Cluster not found.")
    return cluster


@router.get("")
def list_clusters(
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    return [cluster_public(c) for c in _service(request).list(db)]


@router.post("", status_code=201)
def register_cluster(
    body: RegisterClusterRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    try:
        cluster = _service(request).register(
            db, actor=ctx.user, name=body.name, env_tag=body.env_tag, kubeconfig_text=body.kubeconfig
        )
    except ClusterError as e:
        # ClusterError messages are crafted to never include kubeconfig content.
        raise HTTPException(status_code=400, detail=str(e))
    return cluster_public(cluster)


@router.get("/{cluster_id}")
def get_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    return cluster_public(_get_or_404(_service(request), db, cluster_id))


@router.delete("/{cluster_id}", status_code=204)
def delete_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    svc = _service(request)
    cluster = _get_or_404(svc, db, cluster_id)
    svc.delete(db, actor=ctx.user, cluster=cluster)


@router.post("/{cluster_id}/select")
async def select_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    svc = _service(request)
    cluster = _get_or_404(svc, db, cluster_id)

    # Point-of-use, in-memory decryption. The plaintext is used only to derive
    # the engine target context and is never logged or returned.
    try:
        svc.decrypt_kubeconfig(cluster)
    except Exception:
        # Decryption/KMS failure: clean error, no plaintext or key material.
        raise HTTPException(status_code=500, detail="Could not access cluster credentials.")

    context_name = cluster.context_name
    if not context_name:
        raise HTTPException(status_code=400, detail="Cluster has no target context.")

    # Forward a cluster-targeting context switch to the engine over loopback.
    engine: httpx.AsyncClient = request.app.state.engine_client
    try:
        resp = await engine.post(f"/api/contexts/{context_name}")
    except httpx.HTTPError:
        raise HTTPException(status_code=502, detail="Engine is unreachable.")
    if resp.status_code >= 400:
        raise HTTPException(status_code=502, detail="Engine could not switch to the selected cluster.")

    return {"status": "selected", "cluster_id": cluster.id, "context_name": context_name}
