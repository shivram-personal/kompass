"""Local-auth business logic: login with lockout, bootstrap admin, password
change, and admin user management. All decisions are server-side (SPEC §4.1).
"""

from __future__ import annotations

import logging
import secrets
from datetime import timedelta

from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..config import Settings
from ..db import utcnow
from ..models import AuthSource, Role, User, UserCluster
from .passwords import PasswordManager
from .sessions import revoke_all_for_user

log = logging.getLogger("kompass.auth")

MIN_PASSWORD_LENGTH = 12


class AuthError(Exception):
    """Raised for client-correctable auth problems (bad input, weak password)."""


def validate_password(password: str) -> None:
    if len(password) < MIN_PASSWORD_LENGTH:
        raise AuthError(f"Password must be at least {MIN_PASSWORD_LENGTH} characters.")


class AuthService:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings
        self.pw = PasswordManager(settings)

    # --- bootstrap ----------------------------------------------------------
    def bootstrap_admin(self, db: DbSession) -> str | None:
        """On an empty user table, create the admin and return its one-time
        password (caller logs it exactly once). Returns None if users exist."""
        if db.query(User).count() > 0:
            return None
        password = secrets.token_urlsafe(18)
        admin = User(
            username=self.settings.bootstrap_admin_username,
            password_hash=self.pw.hash(password),
            auth_source=AuthSource.local,
            role=Role.admin,
            must_change_password=True,
        )
        db.add(admin)
        db.commit()
        audit.record(
            db,
            action="bootstrap_admin",
            result="success",
            username=admin.username,
            role=Role.admin,
        )
        return password

    # --- login --------------------------------------------------------------
    def authenticate(self, db: DbSession, username: str, password: str) -> User | None:
        """Verify credentials with lockout. Returns the user on success, else
        None. Every outcome is audited before returning. Never logs secrets."""
        user = db.query(User).filter(User.username == username).one_or_none()
        now = utcnow()

        # Generic failure for unknown user / SSO-only account (no enumeration).
        if user is None or user.auth_source != AuthSource.local or not user.password_hash:
            audit.record(db, action="login", result="failure", username=username)
            return None

        if user.locked_until is not None and now < user.locked_until:
            audit.record(db, action="login", result="locked", username=username, role=user.role)
            return None

        if not self.pw.verify(user.password_hash, password):
            user.failed_attempts += 1
            result = "failure"
            if user.failed_attempts >= self.settings.lockout_threshold:
                user.locked_until = now + timedelta(minutes=self.settings.lockout_minutes)
                user.failed_attempts = 0
                result = "lockout"
            db.commit()
            audit.record(db, action="login", result=result, username=username, role=user.role)
            return None

        # Success: reset counters, opportunistically rehash.
        user.failed_attempts = 0
        user.locked_until = None
        if self.pw.needs_rehash(user.password_hash):
            user.password_hash = self.pw.hash(password)
        db.commit()
        audit.record(db, action="login", result="success", username=username, role=user.role)
        return user

    # --- password change ----------------------------------------------------
    def change_password(self, db: DbSession, user: User, new_password: str) -> None:
        validate_password(new_password)
        user.password_hash = self.pw.hash(new_password)
        user.must_change_password = False
        db.commit()
        audit.record(db, action="password_change", result="success",
                     username=user.username, role=user.role)

    # --- admin user management ---------------------------------------------
    def create_user(
        self,
        db: DbSession,
        *,
        actor: User,
        username: str,
        role: str,
        password: str,
        cluster_ids: list[str] | None = None,
        daily_token_budget: int | None = None,
    ) -> User:
        if role not in set(Role):
            raise AuthError(f"Unknown role: {role}")
        if db.query(User).filter(User.username == username).count() > 0:
            raise AuthError("Username already exists.")
        validate_password(password)
        # Audit the intent before the mutation (audit-before-execute).
        audit.record(db, action="user_create", result="attempt",
                     username=actor.username, role=actor.role, target=username,
                     params={"role": role, "clusters": sorted(cluster_ids or [])})
        user = User(
            username=username,
            password_hash=self.pw.hash(password),
            auth_source=AuthSource.local,
            role=role,
            must_change_password=True,
            daily_token_budget=daily_token_budget,
        )
        user.clusters = [UserCluster(cluster_id=c) for c in (cluster_ids or [])]
        db.add(user)
        db.commit()
        return user

    def set_role(self, db: DbSession, *, actor: User, user: User, role: str) -> User:
        if role not in set(Role):
            raise AuthError(f"Unknown role: {role}")
        audit.record(db, action="user_set_role", result="attempt",
                     username=actor.username, role=actor.role, target=user.username,
                     params={"role": role})
        user.role = role
        db.commit()
        return user

    def set_clusters(self, db: DbSession, *, actor: User, user: User, cluster_ids: list[str]) -> User:
        audit.record(db, action="user_set_clusters", result="attempt",
                     username=actor.username, role=actor.role, target=user.username,
                     params={"clusters": sorted(cluster_ids)})
        user.clusters = [UserCluster(cluster_id=c) for c in cluster_ids]
        db.commit()
        return user

    def set_budget(self, db: DbSession, *, actor: User, user: User, budget: int | None) -> User:
        audit.record(db, action="user_set_budget", result="attempt",
                     username=actor.username, role=actor.role, target=user.username,
                     params={"daily_token_budget": budget})
        user.daily_token_budget = budget
        db.commit()
        return user

    def delete_user(self, db: DbSession, *, actor: User, user: User) -> None:
        audit.record(db, action="user_delete", result="attempt",
                     username=actor.username, role=actor.role, target=user.username)
        revoke_all_for_user(db, user.id)
        db.delete(user)
        db.commit()
