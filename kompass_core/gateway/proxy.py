"""Health probes, the engine reverse proxy, and static UI serving.

The proxy maps the browser-facing ``/api/engine/<path>`` onto the engine's
native ``/api/<path>``. Responses are streamed (not buffered) so server-sent
events and log tails reach the browser incrementally. Hop-by-hop headers are
stripped in both directions per RFC 7230 §6.1.

WebSocket upgrades (exec/terminal/log-stream) are NOT yet proxied here — see
the Phase 0 report. Those paths return 501 so the gap is explicit rather than
a silent failure.
"""

from __future__ import annotations

import os

import httpx
from fastapi import Depends, FastAPI, Request
from fastapi.responses import JSONResponse, StreamingResponse
from fastapi.staticfiles import StaticFiles

from ..auth.dependencies import AuthContext, authorize_engine_request
from ..config import Settings

# Hop-by-hop headers must not be forwarded by a proxy (RFC 7230 §6.1), plus
# content-length/host which we let httpx and the stream recompute.
_HOP_BY_HOP = {
    "connection",
    "keep-alive",
    "proxy-authenticate",
    "proxy-authorization",
    "te",
    "trailers",
    "transfer-encoding",
    "upgrade",
    "host",
    "content-length",
}

_PROXY_METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"]


def _filter_headers(headers) -> dict[str, str]:
    return {k: v for k, v in headers.items() if k.lower() not in _HOP_BY_HOP}


def register_gateway(app: FastAPI, settings: Settings) -> None:
    prefix = settings.engine_proxy_prefix.rstrip("/")

    @app.get("/healthz")
    async def healthz() -> dict[str, str]:
        """Liveness: the core process is up and serving."""
        return {"status": "ok"}

    @app.get("/readyz")
    async def readyz(request: Request):
        """Readiness: core is up AND the engine answers over loopback."""
        client: httpx.AsyncClient = request.app.state.engine_client
        try:
            resp = await client.get("/api/health", timeout=5.0)
            engine_ok = resp.status_code < 500
        except httpx.HTTPError:
            engine_ok = False
        if engine_ok:
            return {"status": "ready"}
        return JSONResponse({"status": "engine-unreachable"}, status_code=503)

    @app.websocket(prefix + "/{path:path}")
    async def proxy_ws(websocket):  # noqa: ANN001 - starlette WebSocket
        # WebSocket passthrough (exec, terminal, log streams) is tracked for a
        # later phase. Close with 1011 so the client sees an explicit failure.
        await websocket.close(code=1011)

    @app.api_route(prefix + "/{path:path}", methods=_PROXY_METHODS)
    async def proxy(
        path: str,
        request: Request,
        ctx: AuthContext = Depends(authorize_engine_request),
    ) -> StreamingResponse:
        # Reaching here means the request is authenticated AND authorized for
        # this method/cluster — denials raised 401/403 before this point and
        # never touched the engine.
        client: httpx.AsyncClient = request.app.state.engine_client
        target = "/api/" + path  # /api/engine/<path>  ->  engine /api/<path>

        upstream_req = client.build_request(
            request.method,
            target,
            params=request.query_params,
            headers=_filter_headers(request.headers),
            content=request.stream(),
        )
        upstream = await client.send(upstream_req, stream=True)

        async def body_iter():
            try:
                async for chunk in upstream.aiter_raw():
                    yield chunk
            finally:
                await upstream.aclose()

        return StreamingResponse(
            body_iter(),
            status_code=upstream.status_code,
            headers=_filter_headers(upstream.headers),
        )

    # Serve the rebranded UI last so explicit routes above take precedence.
    # html=True serves index.html at "/" and resolves directory roots.
    if os.path.isdir(settings.static_dir):
        app.mount(
            "/",
            StaticFiles(directory=settings.static_dir, html=True),
            name="ui",
        )
