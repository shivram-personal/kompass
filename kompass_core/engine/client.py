"""Construction of the shared engine HTTP client."""

from __future__ import annotations

import httpx

from ..config import Settings


def build_engine_client(settings: Settings) -> httpx.AsyncClient:
    """Create the long-lived client used to reach the engine over loopback.

    ``read=None`` removes the read timeout so server-sent-event streams and
    log tails are not severed mid-stream; the connect/write/pool phases keep
    the configured bound.
    """
    timeout = httpx.Timeout(
        settings.request_timeout,
        read=None,
        connect=settings.request_timeout,
    )
    return httpx.AsyncClient(base_url=settings.engine_base_url, timeout=timeout)
