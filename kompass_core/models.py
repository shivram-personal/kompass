"""ORM models (SPEC §5).

Phase 1 defines users, per-cluster scoping, sessions, and the append-only
audit log. The user record is SSO-ready: ``auth_source`` + nullable
``oidc_subject`` + nullable ``password_hash`` let Phase 7 map a Google identity
onto the same role/scoping/session machinery with no schema rewrite.
"""

from __future__ import annotations

from datetime import datetime
from enum import StrEnum

from sqlalchemy import DateTime, ForeignKey, Integer, String, Text
from sqlalchemy.orm import Mapped, mapped_column, relationship

from .db import Base, utcnow


class Role(StrEnum):
    viewer = "viewer"
    editor = "editor"
    admin = "admin"


class AuthSource(StrEnum):
    local = "local"
    oidc = "oidc"


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    username: Mapped[str] = mapped_column(String(255), unique=True, index=True)
    # Nullable: SSO users have no local password.
    password_hash: Mapped[str | None] = mapped_column(String(255), nullable=True)
    auth_source: Mapped[str] = mapped_column(String(16), default=AuthSource.local)
    oidc_subject: Mapped[str | None] = mapped_column(String(255), nullable=True, index=True)
    role: Mapped[str] = mapped_column(String(16), default=Role.viewer)
    must_change_password: Mapped[bool] = mapped_column(default=False)
    failed_attempts: Mapped[int] = mapped_column(Integer, default=0)
    locked_until: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    daily_token_budget: Mapped[int | None] = mapped_column(Integer, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)

    clusters: Mapped[list["UserCluster"]] = relationship(
        back_populates="user", cascade="all, delete-orphan"
    )

    @property
    def allowed_cluster_ids(self) -> set[str]:
        return {c.cluster_id for c in self.clusters}


class UserCluster(Base):
    __tablename__ = "user_clusters"

    user_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), primary_key=True
    )
    cluster_id: Mapped[str] = mapped_column(String(255), primary_key=True)

    user: Mapped[User] = relationship(back_populates="clusters")


class Session(Base):
    __tablename__ = "sessions"

    # Only the SHA-256 of the opaque token is stored — never the token itself.
    token_hash: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    csrf_token: Mapped[str] = mapped_column(String(64))
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    idle_expires_at: Mapped[datetime] = mapped_column(DateTime)
    abs_expires_at: Mapped[datetime] = mapped_column(DateTime)


class AuditEvent(Base):
    __tablename__ = "audit_events"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    ts: Mapped[datetime] = mapped_column(DateTime, default=utcnow, index=True)
    username: Mapped[str | None] = mapped_column(String(255), nullable=True)
    role: Mapped[str | None] = mapped_column(String(16), nullable=True)
    cluster_id: Mapped[str | None] = mapped_column(String(255), nullable=True)
    action: Mapped[str] = mapped_column(String(64), index=True)
    target: Mapped[str | None] = mapped_column(String(255), nullable=True)
    params_redacted: Mapped[str | None] = mapped_column(Text, nullable=True)
    result: Mapped[str] = mapped_column(String(32))
    before_summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    after_summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    request_id: Mapped[str | None] = mapped_column(String(64), nullable=True)
