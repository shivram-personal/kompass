"""Server-side session tokens (SPEC §4.1, §8).

Tokens are 256-bit, opaque, and stored only as a SHA-256 hash — the raw token
lives solely in the client's HttpOnly cookie. Sessions carry both an idle and
an absolute expiry, plus a per-session CSRF token for double-submit protection.
"""

from __future__ import annotations

import hashlib
import secrets
from datetime import timedelta

from sqlalchemy.orm import Session as DbSession

from ..config import Settings
from ..db import utcnow
from ..models import Session, User


def _new_token() -> str:
    # 32 bytes = 256 bits of entropy.
    return secrets.token_urlsafe(32)


def hash_token(raw_token: str) -> str:
    return hashlib.sha256(raw_token.encode("utf-8")).hexdigest()


def create_session(db: DbSession, user: User, settings: Settings) -> tuple[str, str]:
    """Create a session row; return (raw_token, csrf_token).

    The raw token is returned once for the cookie and never persisted.
    """
    raw_token = _new_token()
    csrf_token = secrets.token_urlsafe(32)
    now = utcnow()
    row = Session(
        token_hash=hash_token(raw_token),
        user_id=user.id,
        csrf_token=csrf_token,
        created_at=now,
        idle_expires_at=now + timedelta(minutes=settings.session_idle_minutes),
        abs_expires_at=now + timedelta(hours=settings.session_absolute_hours),
    )
    db.add(row)
    db.commit()
    return raw_token, csrf_token


def get_valid_session(db: DbSession, raw_token: str, settings: Settings) -> Session | None:
    """Return a live session, sliding its idle expiry; else None (and purge)."""
    if not raw_token:
        return None
    row = db.get(Session, hash_token(raw_token))
    if row is None:
        return None
    now = utcnow()
    if now >= row.abs_expires_at or now >= row.idle_expires_at:
        db.delete(row)
        db.commit()
        return None
    # Slide the idle window, but never past the absolute expiry.
    new_idle = now + timedelta(minutes=settings.session_idle_minutes)
    row.idle_expires_at = min(new_idle, row.abs_expires_at)
    db.commit()
    return row


def revoke_session(db: DbSession, raw_token: str) -> None:
    row = db.get(Session, hash_token(raw_token))
    if row is not None:
        db.delete(row)
        db.commit()


def revoke_all_for_user(db: DbSession, user_id: int) -> None:
    for row in db.query(Session).filter(Session.user_id == user_id).all():
        db.delete(row)
    db.commit()
