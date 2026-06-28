"""Phase 2 verification: cluster registry, KMS envelope encryption, RBAC, audit.

Covers the required gate evidence: ciphertext-only at rest, no kubeconfig in
responses/logs/error paths, registry RBAC matrix, editor scope referencing real
registry IDs, and audit-before-execute on registry mutations.
"""

import logging

import httpx
import pytest
import respx

from kompass_core.models import AuditEvent, Cluster

ENGINE = "http://127.0.0.1:9280"
PW = "correct horse battery staple"

# A kubeconfig carrying two obvious secrets we assert never leak at rest.
CA_SECRET = "SUPERSECRETCADATA"
TOKEN_SECRET = "SUPER-SECRET-TOKEN-abc123"
KUBECONFIG = f"""apiVersion: v1
kind: Config
current-context: kind-kompass
clusters:
- name: kind-kompass
  cluster:
    server: https://127.0.0.1:6443
    certificate-authority-data: {CA_SECRET}
contexts:
- name: kind-kompass
  context:
    cluster: kind-kompass
    user: kind-user
users:
- name: kind-user
  user:
    token: {TOKEN_SECRET}
"""

SECRET_MARKERS = [CA_SECRET, TOKEN_SECRET, "BEGIN", "token:", "apiVersion"]


@pytest.fixture
def admin(make_user, login):
    make_user("c-admin", PW, "admin")
    return login("c-admin", PW)


def _db(client):
    return client.app.state.session_factory()


def _register(client, admin, name="prod-eu"):
    return client.post(
        "/api/clusters",
        json={"name": name, "env_tag": "prod", "kubeconfig": KUBECONFIG},
        cookies=admin.cookies,
        headers={"X-CSRF-Token": admin.csrf},
    )


def test_kubeconfig_stored_only_as_ciphertext(client, admin):
    resp = _register(client, admin)
    assert resp.status_code == 201
    cid = resp.json()["id"]

    db = _db(client)
    try:
        row = db.get(Cluster, cid)
        at_rest = bytes(row.kubeconfig_ciphertext) + bytes(row.wrapped_dek) + bytes(row.nonce)
    finally:
        db.close()

    # No plaintext kubeconfig / secret markers anywhere in the stored bytes.
    for marker in SECRET_MARKERS:
        assert marker.encode() not in at_rest, f"{marker!r} leaked into at-rest blob"

    # For the gate log: show the at-rest representation is opaque ciphertext.
    print("\nAT-REST cluster row (no plaintext):")
    print(f"  kms_key_ref      = {row.kms_key_ref}")
    print(f"  ciphertext[0:16] = {bytes(row.kubeconfig_ciphertext)[:16].hex()}…")
    print(f"  wrapped_dek[0:16]= {bytes(row.wrapped_dek)[:16].hex()}…")


def test_no_kubeconfig_in_api_responses(client, admin):
    cid = _register(client, admin).json()["id"]
    for resp in (
        _register(client, admin, name="prod-us"),
        client.get("/api/clusters", cookies=admin.cookies),
        client.get(f"/api/clusters/{cid}", cookies=admin.cookies),
    ):
        body = resp.text
        for marker in SECRET_MARKERS:
            assert marker not in body, f"{marker!r} leaked into an API response"


def test_decrypt_failure_path_leaks_nothing(client, admin):
    cid = _register(client, admin).json()["id"]
    # Tamper the wrapped DEK so unwrap fails.
    db = _db(client)
    try:
        row = db.get(Cluster, cid)
        row.wrapped_dek = b"\x00" * len(row.wrapped_dek)
        db.commit()
    finally:
        db.close()

    # Decryption happens at connect (admin injection). Tampered DEK -> clean 500.
    with respx.mock(assert_all_mocked=False, assert_all_called=False):
        resp = client.post(
            f"/api/clusters/{cid}/connect", cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}
        )
    assert resp.status_code == 500
    body = resp.text
    for marker in SECRET_MARKERS:
        assert marker not in body
    # Generic message, no key/plaintext material.
    assert "Could not access cluster credentials" in resp.json()["detail"]


def test_no_kubeconfig_in_logs(client, admin, caplog):
    with caplog.at_level(logging.DEBUG):
        _register(client, admin)
    blob = "\n".join(r.getMessage() for r in caplog.records)
    for marker in SECRET_MARKERS:
        assert marker not in blob


@respx.mock(assert_all_mocked=False)
def test_registry_rbac_matrix(respx_mock, client, make_user, login):
    make_user("rbac-viewer", PW, "viewer")
    make_user("rbac-editor", PW, "editor")
    make_user("rbac-admin", PW, "admin")
    viewer = login("rbac-viewer", PW)
    editor = login("rbac-editor", PW)
    admin = login("rbac-admin", PW)

    def reg(creds):
        return client.post(
            "/api/clusters",
            json={"name": "x", "env_tag": "dev", "kubeconfig": KUBECONFIG},
            cookies=creds.cookies,
            headers={"X-CSRF-Token": creds.csrf},
        ).status_code

    rows = []

    def check(scenario, status, expected):
        rows.append((scenario, status, expected))
        assert status == expected, f"{scenario}: got {status}, expected {expected}"

    # list (GET) — any authenticated user; unauth 401
    check("unauth -> LIST", client.get("/api/clusters").status_code, 401)
    check("viewer -> LIST", client.get("/api/clusters", cookies=viewer.cookies).status_code, 200)
    check("editor -> LIST", client.get("/api/clusters", cookies=editor.cookies).status_code, 200)
    check("admin  -> LIST", client.get("/api/clusters", cookies=admin.cookies).status_code, 200)

    # register (POST) — admin only
    check("unauth -> REGISTER", client.post("/api/clusters", json={}).status_code, 401)
    check("viewer -> REGISTER", reg(viewer), 403)
    check("editor -> REGISTER", reg(editor), 403)
    check("admin  -> REGISTER", reg(admin), 201)
    check(
        "admin no-CSRF -> REGISTER",
        client.post("/api/clusters", json={"name": "y", "env_tag": "dev", "kubeconfig": KUBECONFIG},
                    cookies=admin.cookies).status_code,
        403,
    )

    # delete (DELETE) — admin only
    cid = client.get("/api/clusters", cookies=admin.cookies).json()[0]["id"]
    check("viewer -> DELETE", client.delete(f"/api/clusters/{cid}", cookies=viewer.cookies,
                                            headers={"X-CSRF-Token": viewer.csrf}).status_code, 403)
    check("editor -> DELETE", client.delete(f"/api/clusters/{cid}", cookies=editor.cookies,
                                            headers={"X-CSRF-Token": editor.csrf}).status_code, 403)
    check("admin  -> DELETE", client.delete(f"/api/clusters/{cid}", cookies=admin.cookies,
                                            headers={"X-CSRF-Token": admin.csrf}).status_code, 204)

    print("\n\nCLUSTER REGISTRY RBAC MATRIX")
    print("-" * 44)
    for scenario, status, expected in rows:
        print(f"  {scenario:26s} {status} (expected {expected})")
    print("-" * 44)


@respx.mock(assert_all_mocked=False)
def test_editor_scope_references_registry_ids(respx_mock, client, make_user, login):
    respx_mock.post(f"{ENGINE}/api/workloads/scale").mock(return_value=httpx.Response(202, json={}))
    make_user("s-admin", PW, "admin")
    make_user("s-editor", PW, "editor")
    admin = login("s-admin", PW)

    cid = _register(client, admin, name="scoped").json()["id"]
    editor_id = next(u["id"] for u in client.get("/api/admin/users", cookies=admin.cookies).json()
                     if u["username"] == "s-editor")

    # Assigning an unknown cluster id is rejected (scope must reference registry).
    bad = client.put(f"/api/admin/users/{editor_id}/clusters", json={"cluster_ids": ["does-not-exist"]},
                     cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    assert bad.status_code == 400

    # Assigning the real registry id succeeds.
    ok = client.put(f"/api/admin/users/{editor_id}/clusters", json={"cluster_ids": [cid]},
                    cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    assert ok.status_code == 200

    editor = login("s-editor", PW)
    # Write targeting the in-scope registry id -> allowed; another id -> denied.
    in_scope = client.post("/api/engine/workloads/scale", json={}, cookies=editor.cookies,
                           headers={"X-CSRF-Token": editor.csrf, "X-Kompass-Cluster-Id": cid})
    out_scope = client.post("/api/engine/workloads/scale", json={}, cookies=editor.cookies,
                            headers={"X-CSRF-Token": editor.csrf, "X-Kompass-Cluster-Id": "other-id"})
    assert in_scope.status_code == 202
    assert out_scope.status_code == 403


def test_cluster_crud_is_audited(client, admin):
    cid = _register(client, admin).json()["id"]
    client.delete(f"/api/clusters/{cid}", cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    db = _db(client)
    try:
        actions = {e.action for e in db.query(AuditEvent).filter(AuditEvent.cluster_id == cid).all()}
    finally:
        db.close()
    assert "cluster_create" in actions
    assert "cluster_delete" in actions


@respx.mock(assert_all_mocked=False)
def test_select_uses_seam_route_and_switches_engine(respx_mock, client, admin):
    cid = _register(client, admin).json()["id"]
    route = respx_mock.post(f"{ENGINE}/api/kompass/select/{cid}").mock(
        return_value=httpx.Response(200, json={"status": "selected", "cluster_id": cid})
    )
    respx_mock.get(f"{ENGINE}/api/resources/events").mock(return_value=httpx.Response(200, json=[]))
    resp = client.post(f"/api/clusters/{cid}/select", cookies=admin.cookies,
                       headers={"X-CSRF-Token": admin.csrf})
    assert resp.status_code == 200
    assert resp.json()["context_name"] == "kind-kompass"  # falls back to registry metadata
    assert route.called  # engine selected via the seam route over loopback
    for marker in SECRET_MARKERS:
        assert marker not in resp.text
