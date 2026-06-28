"""Admin AI provider/model management endpoints (SPEC §6).

Provider config is global (not per-cluster), so every route is admin-only:
viewer and editor receive 403; writes require CSRF. The per-provider model
picker lives at GET /{provider}/models.
"""

from __future__ import annotations

from fastapi import APIRouter, Depends, Request
from fastapi.exceptions import HTTPException
from sqlalchemy.orm import Session as DbSession

from ...auth.dependencies import AuthContext, get_db, require_admin
from ...models import ProviderConfig
from .schemas import CreateProviderRequest, UpdateProviderRequest, provider_public
from .service import ProviderCredentialError, ProviderError, ProviderService

router = APIRouter(prefix="/api/admin/providers", tags=["providers"])


def _service(request: Request) -> ProviderService:
    return request.app.state.provider_service


def _get_or_404(svc: ProviderService, db: DbSession, provider: str) -> ProviderConfig:
    p = svc.get(db, provider)
    if p is None:
        raise HTTPException(status_code=404, detail="Provider not found.")
    return p


@router.get("")
def list_providers(request: Request, ctx: AuthContext = Depends(require_admin),
                   db: DbSession = Depends(get_db)):
    return [provider_public(p) for p in _service(request).list(db)]


@router.post("", status_code=201)
def create_provider(
    body: CreateProviderRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    try:
        p = _service(request).create(
            db, actor=ctx.user, provider=body.provider, base_url=body.base_url,
            api_key=body.api_key, active_model=body.active_model, enabled=body.enabled,
            models=body.models,
        )
    except ProviderError as e:
        raise HTTPException(status_code=400, detail=str(e))
    return provider_public(p)


@router.get("/{provider}")
def get_provider(provider: str, request: Request, ctx: AuthContext = Depends(require_admin),
                 db: DbSession = Depends(get_db)):
    return provider_public(_get_or_404(_service(request), db, provider))


@router.patch("/{provider}")
def update_provider(
    provider: str,
    body: UpdateProviderRequest,
    request: Request,
    ctx: AuthContext = Depends(require_admin),
    db: DbSession = Depends(get_db),
):
    svc = _service(request)
    p = _get_or_404(svc, db, provider)
    # exclude_unset so "field not sent" != "field set to null".
    svc.update(db, actor=ctx.user, p=p, fields=body.model_dump(exclude_unset=True))
    return provider_public(p)


@router.delete("/{provider}", status_code=204)
def delete_provider(provider: str, request: Request, ctx: AuthContext = Depends(require_admin),
                    db: DbSession = Depends(get_db)):
    svc = _service(request)
    p = _get_or_404(svc, db, provider)
    svc.delete(db, actor=ctx.user, p=p)


@router.get("/{provider}/models")
async def list_provider_models(provider: str, request: Request,
                               ctx: AuthContext = Depends(require_admin),
                               db: DbSession = Depends(get_db)):
    svc = _service(request)
    p = _get_or_404(svc, db, provider)
    try:
        return await svc.list_models(p)
    except ProviderCredentialError:
        # Generic — never echoes key material or decryption details.
        raise HTTPException(status_code=502, detail="Could not access provider credentials.")
