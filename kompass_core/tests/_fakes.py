"""Test doubles for the engine, used by the Phase 6 apply-action tests.

FakeEngine is a stateful stand-in for the Go engine reached over loopback. It
models the ONE active context (ADR-001): a select flips ``active``; a write
records the active context at write time so a cross-cluster mutation is
detectable. All methods mirror the httpx.AsyncClient surface the code uses
(``post`` for the seam inject/select routes, ``request`` for reads/writes).
"""

from __future__ import annotations

import asyncio


class Resp:
    def __init__(self, status_code: int, payload: dict | None = None) -> None:
        self.status_code = status_code
        self._payload = payload if payload is not None else {}

    def json(self) -> dict:
        return self._payload


class FakeEngine:
    def __init__(
        self,
        *,
        resource_rv: str = "100",
        replicas: int = 1,
        revision: int = 1,
        unschedulable: bool = False,
        suspend: bool = False,
        write_status: int = 202,
        select_sleep: float = 0.0,
        connected: bool = True,
        wrong_select_id: str | None = None,
    ) -> None:
        self.active: str | None = None
        self.selects: list[str] = []
        self.reads: list[str] = []
        self.writes: list[tuple[str, str | None]] = []  # (path, active-at-write)
        self.injects: list[str] = []
        self.resource_rv = resource_rv
        self.replicas = replicas
        self.revision = revision
        self.unschedulable = unschedulable
        self.suspend = suspend
        self.write_status = write_status
        self.select_sleep = select_sleep
        self.connected = connected
        self.wrong_select_id = wrong_select_id

    async def post(self, path: str, json: dict | None = None):
        if path == "/api/kompass/inject":
            self.injects.append(json["cluster_id"] if json else "")
            return Resp(200, {"cluster_id": json["cluster_id"] if json else "", "context_name": "ctx"})
        if path.startswith("/api/kompass/select/"):
            cid = path.rsplit("/", 1)[1]
            self.selects.append(cid)
            if not self.connected:
                return Resp(404, {})
            if self.select_sleep:
                await asyncio.sleep(self.select_sleep)
            self.active = cid
            reported = self.wrong_select_id or cid
            return Resp(200, {"status": "selected", "cluster_id": reported})
        return Resp(200, {})

    async def request(self, method: str, path: str, json: dict | None = None, params: dict | None = None):
        if method == "GET":
            self.reads.append(path)
            if path.startswith("/api/helm/releases/"):
                return Resp(200, {"revision": self.revision})
            return Resp(200, {
                "metadata": {"resourceVersion": self.resource_rv},
                "spec": {"replicas": self.replicas, "unschedulable": self.unschedulable,
                         "suspend": self.suspend},
            })
        # mutating call
        self.writes.append((path, self.active))
        return Resp(self.write_status, {"ok": True})

    async def delete(self, path: str):
        return Resp(204, {})

    async def aclose(self) -> None:
        # Match httpx.AsyncClient so lifespan shutdown can close a swapped-in fake.
        return None


KUBECONFIG = """apiVersion: v1
kind: Config
current-context: ctx
contexts:
- name: ctx
  context: {cluster: c, user: u}
clusters:
- name: c
  cluster: {server: https://example.invalid}
users:
- name: u
  user: {token: fake-not-a-real-secret}
"""
