"""Runtime configuration for kompass-core.

Values are read from ``KOMPASS_``-prefixed environment variables so the same
image runs unchanged across local dev, the test container, and GKE.
"""

from __future__ import annotations

from pydantic_settings import BaseSettings, SettingsConfigDict


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="KOMPASS_", extra="ignore")

    # Base URL of the Go engine. In the pod the engine binds to loopback, so
    # core reaches it at 127.0.0.1. Never exposed outside the pod.
    engine_base_url: str = "http://127.0.0.1:9280"

    # The browser-facing prefix that maps onto the engine's native /api. The
    # frontend's apiBase is set to this value (web/src/api/config.ts) so every
    # REST/SSE/WebSocket call is funnelled through the authenticated proxy.
    engine_proxy_prefix: str = "/api/engine"

    # Directory holding the built React UI (index.html + assets). Served at /.
    # If the directory is absent (e.g. unit tests), static serving is skipped.
    static_dir: str = "/app/web"

    # Upstream request timeout in seconds. Streaming responses (SSE, log
    # tails) disable the read timeout so long-lived streams are not cut off.
    request_timeout: float = 60.0

    # --- Persistence (SQLite app DB, owned by kompass-core) -----------------
    db_url: str = "sqlite:////app/data/kompass.db"

    # --- Sessions & cookies -------------------------------------------------
    cookie_name: str = "kompass_session"
    # Secure cookies require HTTPS; ingress terminates TLS in prod. Tests and
    # plain-HTTP local runs set this False.
    cookie_secure: bool = True
    cookie_samesite: str = "strict"
    session_idle_minutes: int = 60
    session_absolute_hours: int = 12
    # Double-submit CSRF token required on state-changing requests.
    csrf_header: str = "X-CSRF-Token"

    # --- Authentication policy ----------------------------------------------
    lockout_threshold: int = 5
    lockout_minutes: int = 15
    bootstrap_admin_username: str = "admin"

    # Argon2id parameters — EXPLICIT, not library defaults (SPEC §4.1).
    # memory in KiB (65536 KiB = 64 MiB).
    argon2_time_cost: int = 3
    argon2_memory_cost: int = 65536
    argon2_parallelism: int = 1
    argon2_hash_len: int = 32
    argon2_salt_len: int = 16

    # --- Per-cluster scoping -------------------------------------------------
    # Mutating engine requests carry the target cluster id in this header so
    # core can enforce editor per-cluster scope before proxying (SPEC §4.1).
    cluster_header: str = "X-Kompass-Cluster-Id"


def get_settings() -> Settings:
    return Settings()
