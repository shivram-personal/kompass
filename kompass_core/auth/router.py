"""Authentication endpoints (SPEC §6): login, logout, change-password, me."""

from __future__ import annotations

from fastapi import APIRouter, Depends, Request, Response
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from ..config import Settings
from .dependencies import (
    AuthContext,
    get_db,
    get_settings,
    require_user,
    require_user_with_csrf,
)
from .schemas import ChangePasswordRequest, LoginRequest, user_public
from .service import AuthError, AuthService
from .sessions import create_session, revoke_session

router = APIRouter(prefix="/api/auth", tags=["auth"])


def _service(request: Request) -> AuthService:
    return request.app.state.auth_service


def _set_session_cookie(response: Response, token: str, settings: Settings) -> None:
    response.set_cookie(
        key=settings.cookie_name,
        value=token,
        httponly=True,
        secure=settings.cookie_secure,
        samesite=settings.cookie_samesite,
        path="/",
    )


@router.post("/login")
def login(
    body: LoginRequest,
    request: Request,
    response: Response,
    db: DbSession = Depends(get_db),
    settings: Settings = Depends(get_settings),
):
    user = _service(request).authenticate(db, body.username, body.password)
    if user is None:
        # Uniform error — no account-existence or lockout disclosure.
        raise HTTPException(status_code=401, detail="Invalid credentials.")
    token, csrf = create_session(db, user, settings)
    _set_session_cookie(response, token, settings)
    return {"user": user_public(user), "csrf_token": csrf}


@router.post("/logout")
def logout(
    request: Request,
    response: Response,
    ctx: AuthContext = Depends(require_user_with_csrf),
    db: DbSession = Depends(get_db),
    settings: Settings = Depends(get_settings),
):
    token = request.cookies.get(settings.cookie_name, "")
    revoke_session(db, token)
    response.delete_cookie(settings.cookie_name, path="/")
    return {"status": "logged_out"}


@router.post("/change-password")
def change_password(
    body: ChangePasswordRequest,
    request: Request,
    ctx: AuthContext = Depends(require_user_with_csrf),
    db: DbSession = Depends(get_db),
):
    svc = _service(request)
    # Re-verify the current password even mid-session.
    if not svc.pw.verify(ctx.user.password_hash or "", body.current_password):
        raise HTTPException(status_code=400, detail="Current password is incorrect.")
    try:
        svc.change_password(db, ctx.user, body.new_password)
    except AuthError as e:
        raise HTTPException(status_code=400, detail=str(e))
    return {"status": "password_changed"}


@router.get("/me")
def me(ctx: AuthContext = Depends(require_user)):
    return {"user": user_public(ctx.user), "csrf_token": ctx.session.csrf_token}
