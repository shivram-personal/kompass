"""Role × endpoint authorization matrix (SPEC §4.1 gate).

Verifies that the core authorization gate returns the correct 401/403/200 for
viewer, editor (in-scope and out-of-scope cluster), and admin against
representative engine read/write routes and an admin-only route — and that no
denied request reaches the engine. The matrix is printed for the gate log.
"""

import httpx
import pytest
import respx

ENGINE = "http://127.0.0.1:9280"
READ = "/api/engine/topology"          # representative engine read
WRITE = "/api/engine/workloads/scale"  # representative engine write
ADMIN = "/api/admin/users"             # admin-only control-plane route

PASSWORD = "correct horse battery staple"


@pytest.fixture
def actors(make_user, login):
    make_user("m-viewer", PASSWORD, "viewer")
    make_user("m-editor", PASSWORD, "editor", clusters=["cluster-a"])
    make_user("m-admin", PASSWORD, "admin")
    return {
        "viewer": login("m-viewer", PASSWORD),
        "editor": login("m-editor", PASSWORD),
        "admin": login("m-admin", PASSWORD),
    }


def _engine_mocks(respx_mock):
    respx_mock.get(f"{ENGINE}/api/topology").mock(return_value=httpx.Response(200, json={"ok": 1}))
    respx_mock.post(f"{ENGINE}/api/workloads/scale").mock(
        return_value=httpx.Response(202, json={"scaled": True})
    )


def _read(client, creds):
    return client.get(READ, cookies=creds.cookies)


def _write(client, creds, cluster=None):
    headers = {"X-CSRF-Token": creds.csrf}
    if cluster is not None:
        headers["X-Kompass-Cluster-Id"] = cluster
    return client.post(WRITE, json={"replicas": 3}, cookies=creds.cookies, headers=headers)


@respx.mock(assert_all_mocked=False)
def test_role_endpoint_matrix(respx_mock, client, actors):
    _engine_mocks(respx_mock)

    rows = []

    def check(scenario, status, expected):
        rows.append((scenario, status, expected))
        assert status == expected, f"{scenario}: got {status}, expected {expected}"

    # --- engine READ (safe method) -------------------------------------------
    check("unauth            -> READ", client.get(READ).status_code, 401)
    check("viewer            -> READ", _read(client, actors["viewer"]).status_code, 200)
    check("editor            -> READ", _read(client, actors["editor"]).status_code, 200)
    check("admin             -> READ", _read(client, actors["admin"]).status_code, 200)

    # --- engine WRITE (unsafe method) ----------------------------------------
    check("unauth            -> WRITE", client.post(WRITE, json={}).status_code, 401)
    check("viewer            -> WRITE", _write(client, actors["viewer"], "cluster-a").status_code, 403)
    check("editor in-scope   -> WRITE", _write(client, actors["editor"], "cluster-a").status_code, 202)
    check("editor out-scope  -> WRITE", _write(client, actors["editor"], "cluster-b").status_code, 403)
    check("editor no-cluster -> WRITE", _write(client, actors["editor"], None).status_code, 403)
    check("admin             -> WRITE", _write(client, actors["admin"], "cluster-z").status_code, 202)

    # CSRF: a write without the token is rejected even for admin.
    no_csrf = client.post(WRITE, json={}, cookies=actors["admin"].cookies)
    check("admin no-CSRF     -> WRITE", no_csrf.status_code, 403)

    # --- admin-only route ----------------------------------------------------
    check("unauth            -> ADMIN", client.get(ADMIN).status_code, 401)
    check("viewer            -> ADMIN", client.get(ADMIN, cookies=actors["viewer"].cookies).status_code, 403)
    check("editor            -> ADMIN", client.get(ADMIN, cookies=actors["editor"].cookies).status_code, 403)
    check("admin             -> ADMIN", client.get(ADMIN, cookies=actors["admin"].cookies).status_code, 200)

    # Print the matrix for the gate log.
    print("\n\nROLE × ENDPOINT AUTHORIZATION MATRIX")
    print("-" * 48)
    for scenario, status, expected in rows:
        print(f"  {scenario:24s}  {status}  (expected {expected})")
    print("-" * 48)


@respx.mock(assert_all_mocked=False, assert_all_called=False)
def test_denied_write_never_reaches_engine(respx_mock, client, make_user, login):
    route = respx_mock.post(f"{ENGINE}/api/workloads/scale").mock(
        return_value=httpx.Response(202, json={})
    )
    make_user("nr-viewer", PASSWORD, "viewer")
    creds = login("nr-viewer", PASSWORD)
    resp = client.post(
        WRITE, json={}, cookies=creds.cookies, headers={"X-CSRF-Token": creds.csrf}
    )
    assert resp.status_code == 403
    assert not route.called  # engine handler was never invoked
