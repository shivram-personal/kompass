"""FastAPI application factory for kompass-core."""

from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager

from fastapi import FastAPI

from .admin.users_router import router as admin_users_router
from .auth.router import router as auth_router
from .auth.service import AuthService
from .clusters.router import router as clusters_router
from .clusters.service import ClusterService
from .config import Settings, get_settings
from .db import build_session_factory
from .engine.client import build_engine_client
from .events.router import router as events_router
from .events.service import EventService
from .gateway.proxy import register_gateway
from .nodestats.router import router as nodestats_router
from .secretstore.kms import build_kms_provider

log = logging.getLogger("kompass")


async def _prune_loop(app: FastAPI, settings: Settings) -> None:
    """Periodically prune events older than the retention window."""
    svc: EventService = app.state.event_service
    while True:
        try:
            db = app.state.session_factory()
            try:
                removed = svc.prune(db)
                if removed:
                    log.info("event retention prune removed %d events", removed)
            finally:
                db.close()
        except Exception:  # never let the loop die on a transient error
            log.exception("event prune loop error")
        await asyncio.sleep(settings.event_prune_seconds)


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or get_settings()

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        # Persistence + auth + registry wiring.
        app.state.session_factory = build_session_factory(settings.db_url)
        app.state.auth_service = AuthService(settings)
        app.state.cluster_service = ClusterService(build_kms_provider(settings))
        app.state.event_service = EventService(settings)

        # Bootstrap admin exactly once (empty user table). Print the one-time
        # password to the core log, clearly marked — never elsewhere.
        db = app.state.session_factory()
        try:
            password = app.state.auth_service.bootstrap_admin(db)
        finally:
            db.close()
        if password:
            log.warning(
                "INITIAL ADMIN CREDENTIALS — username=%s password=%s "
                "(change immediately; this is printed once)",
                settings.bootstrap_admin_username,
                password,
            )

        app.state.engine_client = build_engine_client(settings)

        # Background event retention pruning.
        prune_task = asyncio.create_task(_prune_loop(app, settings))
        try:
            yield
        finally:
            prune_task.cancel()
            try:
                await prune_task
            except (asyncio.CancelledError, Exception):
                pass
            await app.state.engine_client.aclose()

    app = FastAPI(
        title="Kompass",
        version="0.1.0",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
        lifespan=lifespan,
    )
    app.state.settings = settings

    # Core control-plane routes first; the engine proxy + static UI mount last
    # (inside register_gateway) so explicit routes always take precedence.
    app.include_router(auth_router)
    app.include_router(admin_users_router)
    app.include_router(clusters_router)
    app.include_router(events_router)
    app.include_router(nodestats_router)
    register_gateway(app, settings)
    return app


app = create_app()
