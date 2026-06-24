"""FastAPI dependencies that enforce authn/authz server-side (SPEC §4.1, §8).

Nothing reaches the engine proxy without passing through here: a missing/invalid
session is 401; an authenticated-but-under-privileged request is 403; and every
denial is audited before the request is rejected.
"""

from __future__ import annotations

from dataclasses import dataclass

from fastapi import Depends, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..config import Settings
from ..models import Role, Session, User
from .sessions import get_valid_session

SAFE_METHODS = {"GET", "HEAD", "OPTIONS"}
_ROLE_RANK = {Role.viewer: 0, Role.editor: 1, Role.admin: 2}

# Endpoints reachable even while a forced password change is pending.
PW_CHANGE_EXEMPT_PATHS = {"/api/auth/change-password", "/api/auth/logout", "/api/auth/me"}


@dataclass
class AuthContext:
    user: User
    session: Session


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


def get_db(request: Request):
    factory = request.app.state.session_factory
    db = factory()
    try:
        yield db
    finally:
        db.close()


def _auth_context(request: Request, db: DbSession, settings: Settings) -> AuthContext | None:
    token = request.cookies.get(settings.cookie_name, "")
    sess = get_valid_session(db, token, settings)
    if sess is None:
        return None
    user = db.get(User, sess.user_id)
    if user is None:
        return None
    return AuthContext(user=user, session=sess)


def optional_user(
    request: Request,
    db: DbSession = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> AuthContext | None:
    return _auth_context(request, db, settings)


def require_user(
    request: Request,
    db: DbSession = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> AuthContext:
    ctx = _auth_context(request, db, settings)
    if ctx is None:
        raise HTTPException(status_code=401, detail="Authentication required.")
    return ctx


def _check_csrf(request: Request, ctx: AuthContext, settings: Settings) -> None:
    if request.method in SAFE_METHODS:
        return
    sent = request.headers.get(settings.csrf_header, "")
    if not sent or sent != ctx.session.csrf_token:
        raise HTTPException(status_code=403, detail="Invalid or missing CSRF token.")


def require_active_user(
    request: Request,
    ctx: AuthContext = Depends(require_user),
    settings: Settings = Depends(get_settings),
) -> AuthContext:
    """Authenticated, CSRF-valid (on writes), and not pending a forced change."""
    _check_csrf(request, ctx, settings)
    if ctx.user.must_change_password and request.url.path not in PW_CHANGE_EXEMPT_PATHS:
        raise HTTPException(status_code=403, detail="Password change required.")
    return ctx


def require_user_with_csrf(
    request: Request,
    ctx: AuthContext = Depends(require_user),
    settings: Settings = Depends(get_settings),
) -> AuthContext:
    """Authenticated + CSRF-valid, but WITHOUT the forced-change gate — used by
    logout and the change-password endpoint itself."""
    _check_csrf(request, ctx, settings)
    return ctx


def require_min_role(min_role: Role):
    def _dep(
        request: Request,
        ctx: AuthContext = Depends(require_active_user),
        db: DbSession = Depends(get_db),
    ) -> AuthContext:
        if _ROLE_RANK[Role(ctx.user.role)] < _ROLE_RANK[min_role]:
            audit.record(
                db,
                action="authz",
                result="role_denied",
                username=ctx.user.username,
                role=ctx.user.role,
                target=request.url.path,
                params={"required": str(min_role)},
            )
            raise HTTPException(status_code=403, detail="Insufficient role.")
        return ctx

    return _dep


require_admin = require_min_role(Role.admin)


def authorize_engine_request(
    request: Request,
    db: DbSession = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> AuthContext:
    """Authorization gate in front of EVERY proxied engine route.

    - unauthenticated            -> 401
    - reads (safe methods)       -> any authenticated, active user
    - writes by viewer           -> 403
    - writes by editor           -> require in-scope target cluster header
    - writes by admin            -> allowed
    Denials are audited before rejection; nothing reaches the engine on denial.
    """
    ctx = _auth_context(request, db, settings)
    if ctx is None:
        audit.record(db, action="authz", result="unauthenticated", target=request.url.path)
        raise HTTPException(status_code=401, detail="Authentication required.")

    user = ctx.user
    if user.must_change_password:
        raise HTTPException(status_code=403, detail="Password change required.")

    if request.method in SAFE_METHODS:
        return ctx  # reads allowed for any authenticated user

    # State-changing engine request: CSRF first, then role/scope.
    _check_csrf(request, ctx, settings)
    role = Role(user.role)

    if role == Role.admin:
        return ctx

    if role == Role.viewer:
        audit.record(db, action="authz", result="role_denied", username=user.username,
                     role=user.role, target=request.url.path, params={"reason": "viewer_write"})
        raise HTTPException(status_code=403, detail="Viewers cannot perform writes.")

    # editor: must target an in-scope cluster.
    cluster_id = request.headers.get(settings.cluster_header, "")
    if not cluster_id or cluster_id not in user.allowed_cluster_ids:
        audit.record(db, action="authz", result="scope_denied", username=user.username,
                     role=user.role, cluster_id=cluster_id or None, target=request.url.path)
        raise HTTPException(status_code=403, detail="Cluster not in editor scope.")
    return ctx
