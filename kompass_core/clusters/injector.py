"""Thin client for the engine's seam #3 loopback injection routes.

kompass-core decrypts a kubeconfig in memory (ClusterService) and hands the
already-decrypted bytes to the engine over loopback. The engine holds them in
process memory only (see docs/SPEC.md ADR-001). This module never logs the
kubeconfig and never persists it.
"""

from __future__ import annotations

import httpx


class InjectionError(Exception):
    """Engine-side injection/selection failure. Never carries kubeconfig bytes."""


async def inject(engine: httpx.AsyncClient, cluster_id: str, kubeconfig_text: str) -> dict:
    """Load a decrypted kubeconfig into the engine's in-memory store."""
    try:
        resp = await engine.post(
            "/api/kompass/inject",
            json={"cluster_id": cluster_id, "kubeconfig": kubeconfig_text},
        )
    except httpx.HTTPError:
        raise InjectionError("engine unreachable")
    if resp.status_code >= 400:
        raise InjectionError("engine rejected the kubeconfig")
    return resp.json()


async def select(engine: httpx.AsyncClient, cluster_id: str) -> dict:
    """Make an already-injected cluster the engine's active context."""
    try:
        resp = await engine.post(f"/api/kompass/select/{cluster_id}")
    except httpx.HTTPError:
        raise InjectionError("engine unreachable")
    if resp.status_code == 404:
        raise InjectionError("cluster is not connected")
    if resp.status_code >= 400:
        raise InjectionError("engine could not select the cluster")
    return resp.json()


async def evict(engine: httpx.AsyncClient, cluster_id: str) -> None:
    """Remove a cluster's credential from engine memory (best-effort)."""
    try:
        await engine.delete(f"/api/kompass/inject/{cluster_id}")
    except httpx.HTTPError:
        pass  # eviction is best-effort cleanup; the encrypted store is the SoT
