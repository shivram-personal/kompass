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


def get_settings() -> Settings:
    return Settings()
