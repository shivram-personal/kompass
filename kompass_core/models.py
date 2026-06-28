"""ORM models (SPEC §5).

Phase 1 defines users, per-cluster scoping, sessions, and the append-only
audit log. The user record is SSO-ready: ``auth_source`` + nullable
``oidc_subject`` + nullable ``password_hash`` let Phase 7 map a Google identity
onto the same role/scoping/session machinery with no schema rewrite.
"""

from __future__ import annotations

from datetime import datetime
from enum import StrEnum

from sqlalchemy import DateTime, ForeignKey, Index, Integer, LargeBinary, String, Text
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


class EnvTag(StrEnum):
    prod = "prod"
    staging = "staging"
    dev = "dev"


class Cluster(Base):
    __tablename__ = "clusters"

    # Stable, opaque id referenced by user_clusters scoping, audit, AI targeting.
    id: Mapped[str] = mapped_column(String(36), primary_key=True)
    name: Mapped[str] = mapped_column(String(255))
    env_tag: Mapped[str] = mapped_column(String(16))
    # Non-secret metadata the engine uses to target this cluster (kubeconfig
    # current-context name). Safe to store/return; not a credential.
    context_name: Mapped[str | None] = mapped_column(String(255), nullable=True)

    # Envelope-encrypted kubeconfig. The DB NEVER holds plaintext kubeconfig or
    # the unwrapped DEK: `kubeconfig_ciphertext` is AES-GCM(DEK), `wrapped_dek`
    # is KMS-wrapped(DEK), `nonce` is the AES-GCM nonce, and `kms_key_ref`
    # records which KMS key wrapped the DEK.
    kubeconfig_ciphertext: Mapped[bytes] = mapped_column(LargeBinary)
    wrapped_dek: Mapped[bytes] = mapped_column(LargeBinary)
    nonce: Mapped[bytes] = mapped_column(LargeBinary)
    kms_key_ref: Mapped[str] = mapped_column(String(512))

    created_by: Mapped[str] = mapped_column(String(255))
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


class ProviderConfig(Base):
    """An AI provider configuration (SPEC §4.4, §5).

    The API key is envelope-encrypted via the same KMS path as kubeconfigs
    (Phase 2): the DB holds only ciphertext + wrapped DEK + nonce + key ref,
    never plaintext. `api_key_last4` is the non-secret masked hint shown on
    read so display never requires decryption. Decryption happens in memory
    only at call time (model listing / chat).
    """

    __tablename__ = "provider_config"

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    provider: Mapped[str] = mapped_column(String(64), unique=True, index=True)
    enabled: Mapped[bool] = mapped_column(default=True)
    base_url: Mapped[str | None] = mapped_column(String(512), nullable=True)

    api_key_ciphertext: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    wrapped_dek: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    nonce: Mapped[bytes | None] = mapped_column(LargeBinary, nullable=True)
    kms_key_ref: Mapped[str | None] = mapped_column(String(512), nullable=True)
    api_key_last4: Mapped[str | None] = mapped_column(String(8), nullable=True)

    active_model: Mapped[str | None] = mapped_column(String(255), nullable=True)
    # Non-secret extras: admin-editable model list, org/project, etc. (JSON).
    extra_json: Mapped[str | None] = mapped_column(Text, nullable=True)

    updated_by: Mapped[str] = mapped_column(String(255))
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)

    @property
    def has_api_key(self) -> bool:
        return self.api_key_ciphertext is not None


class Session(Base):
    __tablename__ = "sessions"

    # Only the SHA-256 of the opaque token is stored — never the token itself.
    token_hash: Mapped[str] = mapped_column(String(64), primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    csrf_token: Mapped[str] = mapped_column(String(64))
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    idle_expires_at: Mapped[datetime] = mapped_column(DateTime)
    abs_expires_at: Mapped[datetime] = mapped_column(DateTime)


class ClusterEvent(Base):
    """A cluster event in the core-owned store (SPEC §4.8 / Phase 3).

    Scoped per cluster and pruned to the configured retention window. The
    composite index on (cluster_id, ts) keeps "recent events for cluster X"
    queries off a full table scan. `message` is redacted before storage so no
    secret material that an event might capture is persisted.
    """

    __tablename__ = "cluster_events"
    __table_args__ = (Index("ix_cluster_events_cluster_ts", "cluster_id", "ts"),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    cluster_id: Mapped[str] = mapped_column(String(255))
    ts: Mapped[datetime] = mapped_column(DateTime)  # event time (UTC)
    event_type: Mapped[str] = mapped_column(String(16))  # Normal | Warning
    reason: Mapped[str | None] = mapped_column(String(255), nullable=True)
    message: Mapped[str | None] = mapped_column(Text, nullable=True)  # redacted
    involved_kind: Mapped[str | None] = mapped_column(String(128), nullable=True)
    involved_name: Mapped[str | None] = mapped_column(String(255), nullable=True)
    namespace: Mapped[str | None] = mapped_column(String(255), nullable=True)
    source: Mapped[str | None] = mapped_column(String(255), nullable=True)
    uid: Mapped[str | None] = mapped_column(String(255), nullable=True, index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


class AiUsage(Base):
    """Per-call token accounting (SPEC §5). Foundation for the Phase 7 cost
    dashboard; no cost/budget logic is applied here."""

    __tablename__ = "ai_usage"
    __table_args__ = (Index("ix_ai_usage_user_ts", "user_id", "ts"),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    ts: Mapped[datetime] = mapped_column(DateTime, default=utcnow, index=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"), nullable=True)
    cluster_id: Mapped[str | None] = mapped_column(String(255), nullable=True)
    provider: Mapped[str] = mapped_column(String(64))
    model: Mapped[str | None] = mapped_column(String(255), nullable=True)
    prompt_tokens: Mapped[int] = mapped_column(Integer, default=0)
    completion_tokens: Mapped[int] = mapped_column(Integer, default=0)
    request_id: Mapped[str | None] = mapped_column(String(64), nullable=True)


class ChatMessage(Base):
    """Persisted chat history. `content` is REDACTED before storage so no secret
    captured in cluster state is kept in plaintext (SPEC §4.3 redaction)."""

    __tablename__ = "chat_messages"
    __table_args__ = (Index("ix_chat_messages_user_cluster_ts", "user_id", "cluster_id", "ts"),)

    id: Mapped[int] = mapped_column(Integer, primary_key=True)
    ts: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    cluster_id: Mapped[str | None] = mapped_column(String(255), nullable=True)
    role: Mapped[str] = mapped_column(String(16))  # user | assistant
    content: Mapped[str] = mapped_column(Text)  # redacted
    provider: Mapped[str | None] = mapped_column(String(64), nullable=True)
    model: Mapped[str | None] = mapped_column(String(255), nullable=True)
    request_id: Mapped[str | None] = mapped_column(String(64), nullable=True)


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
