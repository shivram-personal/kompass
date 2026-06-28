"""Phase 3.5 verification: kubeconfig injection seam (core side).

Covers the connect/select RBAC matrix, CSRF, proxy block of /api/engine/kompass/*,
credential rotation, poll_once wired to an injected cluster, and audit.
"""

import httpx
import pytest
import respx

from kompass_core.models import AuditEvent

ENGINE = "http://127.0.0.1:9280"
PW = "correct horse battery staple"

_KUBECONFIG = """apiVersion: v1
kind: Config
current-context: kind-kompass
clusters:
- name: kind-kompass
  cluster: {server: https://127.0.0.1:6443}
contexts:
- name: kind-kompass
  context: {cluster: kind-kompass, user: u}
users:
- name: u
  user: {token: tok}
"""


def _db(client):
    return client.app.state.session_factory()


def _register(client, admin, name="prod"):
    return client.post("/api/clusters", json={"name": name, "env_tag": "prod", "kubeconfig": _KUBECONFIG},
                       cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}).json()["id"]


@respx.mock(assert_all_mocked=False)
def test_connect_and_select_rbac_matrix(respx_mock, client, make_user, login):
    respx_mock.post(url__regex=rf"{ENGINE}/api/kompass/inject").mock(
        return_value=httpx.Response(200, json={"cluster_id": "x", "context_name": "kind-kompass"})
    )
    respx_mock.post(url__regex=rf"{ENGINE}/api/kompass/select/.*").mock(
        return_value=httpx.Response(200, json={"status": "selected"})
    )
    respx_mock.get(f"{ENGINE}/api/resources/events").mock(return_value=httpx.Response(200, json=[]))

    make_user("i-viewer", PW, "viewer")
    make_user("i-admin", PW, "admin")
    viewer = login("i-viewer", PW)
    admin = login("i-admin", PW)

    cidA = _register(client, admin, "A")
    cidB = _register(client, admin, "B")
    make_user("i-editor", PW, "editor")
    eid = next(u["id"] for u in client.get("/api/admin/users", cookies=admin.cookies).json()
               if u["username"] == "i-editor")
    client.put(f"/api/admin/users/{eid}/clusters", json={"cluster_ids": [cidA]},
               cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    editor = login("i-editor", PW)

    rows = []

    def check(scenario, status, expected):
        rows.append((scenario, status, expected))
        assert status == expected, f"{scenario}: got {status}, expected {expected}"

    def connect(creds, cid, csrf=True):
        h = {"X-CSRF-Token": creds.csrf} if csrf else {}
        return client.post(f"/api/clusters/{cid}/connect", cookies=creds.cookies, headers=h).status_code

    def select(creds, cid):
        return client.post(f"/api/clusters/{cid}/select", cookies=creds.cookies,
                           headers={"X-CSRF-Token": creds.csrf}).status_code

    # connect = ADMIN ONLY (credential injection)
    check("unauth -> CONNECT", client.post(f"/api/clusters/{cidA}/connect").status_code, 401)
    check("viewer -> CONNECT", connect(viewer, cidA), 403)
    check("editor -> CONNECT", connect(editor, cidA), 403)
    check("admin  -> CONNECT", connect(admin, cidA), 200)
    check("admin no-CSRF -> CONNECT", connect(admin, cidA, csrf=False), 403)

    # select = per-cluster scope
    check("unauth          -> SELECT", client.post(f"/api/clusters/{cidA}/select").status_code, 401)
    check("editor in-scope -> SELECT(A)", select(editor, cidA), 200)
    check("editor out-scope-> SELECT(B)", select(editor, cidB), 403)
    check("viewer          -> SELECT(A)", select(viewer, cidA), 200)
    check("admin           -> SELECT(B)", select(admin, cidB), 200)

    print("\n\nPHASE 3.5 INJECTION RBAC MATRIX")
    print("-" * 48)
    for s, st, ex in rows:
        print(f"  {s:28s} {st} (expected {ex})")
    print("-" * 48)


def test_proxy_blocks_browser_access_to_seam_routes(client, make_user, login):
    # Even an admin cannot reach /api/engine/kompass/* through the generic proxy.
    make_user("pb-admin", PW, "admin")
    admin = login("pb-admin", PW)
    resp = client.post("/api/engine/kompass/inject", json={"cluster_id": "x", "kubeconfig": "y"},
                       cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    assert resp.status_code == 404


@respx.mock(assert_all_mocked=False)
def test_connect_is_audited_and_rotation_reinjects(respx_mock, client, make_user, login):
    injected = []

    def _capture(request: httpx.Request) -> httpx.Response:
        injected.append(request.content)
        return httpx.Response(200, json={"context_name": "kind-kompass"})

    respx_mock.post(f"{ENGINE}/api/kompass/inject").mock(side_effect=_capture)
    make_user("rot-admin", PW, "admin")
    admin = login("rot-admin", PW)
    cid = _register(client, admin)

    # Connect, then connect again (rotation) -> engine inject called twice.
    assert client.post(f"/api/clusters/{cid}/connect", cookies=admin.cookies,
                       headers={"X-CSRF-Token": admin.csrf}).status_code == 200
    assert client.post(f"/api/clusters/{cid}/connect", cookies=admin.cookies,
                       headers={"X-CSRF-Token": admin.csrf}).status_code == 200
    assert len(injected) == 2

    db = _db(client)
    try:
        connects = db.query(AuditEvent).filter(AuditEvent.action == "cluster_connect",
                                               AuditEvent.cluster_id == cid).count()
    finally:
        db.close()
    assert connects == 2  # audit-before-execute on injection + rotation


@respx.mock(assert_all_mocked=False)
def test_poll_once_ingests_injected_cluster_events_on_select(respx_mock, client, make_user, login):
    respx_mock.post(url__regex=rf"{ENGINE}/api/kompass/select/.*").mock(
        return_value=httpx.Response(200, json={"status": "selected"})
    )
    respx_mock.get(f"{ENGINE}/api/resources/events").mock(
        return_value=httpx.Response(200, json=[{
            "metadata": {"uid": "evt-1", "namespace": "default"},
            "type": "Warning", "reason": "BackOff", "message": "pod crashlooping",
            "involvedObject": {"kind": "Pod", "name": "foo"}, "source": {"component": "kubelet"},
            "lastTimestamp": "2026-06-01T00:00:00Z",
        }])
    )
    make_user("pp-admin", PW, "admin")
    admin = login("pp-admin", PW)
    cid = _register(client, admin)

    client.post(f"/api/clusters/{cid}/select", cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})

    # The injected cluster's events were ingested into the per-cluster store.
    events = client.get(f"/api/clusters/{cid}/events", cookies=admin.cookies).json()
    assert any(e["reason"] == "BackOff" and e["cluster_id"] == cid for e in events)
