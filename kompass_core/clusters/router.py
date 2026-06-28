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

from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..auth.dependencies import AuthContext, get_db, require_active_user, require_admin
from ..models import Cluster, Role
from . import injector
from .injector import InjectionError
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
async def delete_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    svc = _service(request)
    cluster = _get_or_404(svc, db, cluster_id)
    svc.delete(db, actor=ctx.user, cluster=cluster)  # audits + purges ciphertext
    # Evict any in-memory credential from the engine (seam #3 lifecycle end).
    await injector.evict(request.app.state.engine_client, cluster_id)


@router.post("/{cluster_id}/connect")
async def connect_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    """ADMIN-only credential injection (and rotation): decrypt the kubeconfig in
    memory and inject it into the engine over loopback (seam #3). Re-running
    rotates the in-memory credential. The kubeconfig is never logged or returned.
    """
    svc = _service(request)
    cluster = _get_or_404(svc, db, cluster_id)
    try:
        kubeconfig = svc.decrypt_kubeconfig(cluster)
    except Exception:
        raise HTTPException(status_code=500, detail="Could not access cluster credentials.")

    # Audit-before-execute: record the injection intent before it happens.
    audit.record(db, action="cluster_connect", result="attempt",
                 username=ctx.user.username, role=ctx.user.role, cluster_id=cluster.id,
                 target=cluster.name)
    try:
        result = await injector.inject(request.app.state.engine_client, cluster.id, kubeconfig)
    except InjectionError:
        raise HTTPException(status_code=502, detail="Engine could not load the cluster credentials.")
    return {"status": "connected", "cluster_id": cluster.id,
            "context_name": result.get("context_name")}


@router.post("/{cluster_id}/select")
async def select_cluster(
    cluster_id: str,
    request: Request,
    ctx: AuthContext = Depends(require_active_user),
    db: DbSession = Depends(get_db),
):
    """Make a connected cluster the engine's active context. Follows per-cluster
    RBAC scope: editors may select only clusters in their scope."""
    svc = _service(request)
    cluster = _get_or_404(svc, db, cluster_id)

    user = ctx.user
    if Role(user.role) == Role.editor and cluster_id not in user.allowed_cluster_ids:
        audit.record(db, action="cluster_select", result="scope_denied",
                     username=user.username, role=user.role, cluster_id=cluster_id)
        raise HTTPException(status_code=403, detail="Cluster not in editor scope.")

    engine = request.app.state.engine_client
    try:
        injector_result = await injector.select(engine, cluster.id)
    except InjectionError as e:
        if str(e) == "cluster is not connected":
            raise HTTPException(status_code=409, detail="Cluster is not connected; an admin must connect it first.")
        raise HTTPException(status_code=502, detail="Engine could not select the cluster.")

    # Now that this cluster is the active context, ingest its current events
    # into the per-cluster store (Phase 3 poll_once wired to a real injected
    # cluster). Best-effort: a poll failure must not fail selection.
    try:
        await request.app.state.event_service.poll_once(db, engine, cluster.id)
    except Exception:
        pass

    return {"status": "selected", "cluster_id": cluster.id,
            "context_name": injector_result.get("context_name") or cluster.context_name}
