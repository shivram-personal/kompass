"""FastAPI application factory for kompass-core."""

from __future__ import annotations

from contextlib import asynccontextmanager

from fastapi import FastAPI

from .config import Settings, get_settings
from .engine.client import build_engine_client
from .gateway.proxy import register_gateway


def create_app(settings: Settings | None = None) -> FastAPI:
    settings = settings or get_settings()

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        app.state.engine_client = build_engine_client(settings)
        try:
            yield
        finally:
            await app.state.engine_client.aclose()

    # No upstream branding in any user-visible identifier; docs endpoints are
    # disabled (no public API schema is part of the product surface).
    app = FastAPI(
        title="Kompass",
        version="0.1.0",
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
        lifespan=lifespan,
    )
    app.state.settings = settings
    register_gateway(app, settings)
    return app


app = create_app()
