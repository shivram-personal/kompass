"""Admin user management (SPEC §6): CRUD + per-cluster scoping + budget.

Every route requires the admin role (enforced server-side); viewers/editors get
403. UI gating is cosmetic — these checks are authoritative.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from ..auth.dependencies import AuthContext, get_db, require_admin
from ..auth.schemas import (
    CreateUserRequest,
    SetBudgetRequest,
    SetClustersRequest,
    SetRoleRequest,
    user_public,
)
from ..auth.service import AuthError, AuthService
from ..models import User

router = APIRouter(prefix="/api/admin/users", tags=["admin"])


def _service(request: Request) -> AuthService:
    return request.app.state.auth_service


def _get_user_or_404(db: DbSession, user_id: int) -> User:
    user = db.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=404, detail="User not found.")
    return user


@router.get("")
def list_users(ctx: AuthContext = Depends(require_admin), db: DbSession = Depends(get_db)):
    return [user_public(u) for u in db.query(User).order_by(User.id).all()]


@router.post("", status_code=201)
def create_user(
    body: CreateUserRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    try:
        user = _service(request).create_user(
            db,
            actor=ctx.user,
            username=body.username,
            role=body.role,
            password=body.password,
            cluster_ids=body.cluster_ids,
            daily_token_budget=body.daily_token_budget,
        )
    except AuthError as e:
        raise HTTPException(status_code=400, detail=str(e))
    return user_public(user)


@router.get("/{user_id}")
def get_user(user_id: int, ctx: AuthContext = Depends(require_admin), db: DbSession = Depends(get_db)):
    return user_public(_get_user_or_404(db, user_id))


@router.patch("/{user_id}/role")
def set_role(
    user_id: int,
    body: SetRoleRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    user = _get_user_or_404(db, user_id)
    try:
        _service(request).set_role(db, actor=ctx.user, user=user, role=body.role)
    except AuthError as e:
        raise HTTPException(status_code=400, detail=str(e))
    return user_public(user)


@router.put("/{user_id}/clusters")
def set_clusters(
    user_id: int,
    body: SetClustersRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    user = _get_user_or_404(db, user_id)
    _service(request).set_clusters(db, actor=ctx.user, user=user, cluster_ids=body.cluster_ids)
    return user_public(user)


@router.patch("/{user_id}/budget")
def set_budget(
    user_id: int,
    body: SetBudgetRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    user = _get_user_or_404(db, user_id)
    _service(request).set_budget(db, actor=ctx.user, user=user, budget=body.daily_token_budget)
    return user_public(user)


@router.delete("/{user_id}", status_code=204)
def delete_user(
    user_id: int,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    user = _get_user_or_404(db, user_id)
    if user.id == ctx.user.id:
        raise HTTPException(status_code=400, detail="You cannot delete your own account.")
    _service(request).delete_user(db, actor=ctx.user, user=user)
