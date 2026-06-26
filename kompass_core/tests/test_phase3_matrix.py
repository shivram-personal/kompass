"""Role × endpoint matrix extended to the Phase 3 node and event endpoints.

Both are reads. Node stats: any authenticated user. Cluster events: viewer/admin
read any cluster; editor only in-scope clusters (out-of-scope -> 403).
"""

import httpx
import pytest
import respx

ENGINE = "http://127.0.0.1:9280"
PW = "correct horse battery staple"
NODES = "/api/nodes/stats"


@pytest.fixture
def actors(make_user, login):
    make_user("p3-viewer", PW, "viewer")
    make_user("p3-admin", PW, "admin")
    return {"viewer": login("p3-viewer", PW), "admin": login("p3-admin", PW)}


@respx.mock(assert_all_mocked=False)
def test_node_and_event_matrix(respx_mock, client, actors, make_user, login):
    respx_mock.get(f"{ENGINE}/api/resources/pods").mock(return_value=httpx.Response(200, json=[]))
    respx_mock.get(f"{ENGINE}/api/resources/nodes").mock(return_value=httpx.Response(200, json=[]))

    # Register two clusters; scope an editor to A only.
    cidA = client.post("/api/clusters", json={"name": "A", "env_tag": "dev", "kubeconfig": _KUBECONFIG},
                       cookies=actors["admin"].cookies, headers={"X-CSRF-Token": actors["admin"].csrf}).json()["id"]
    cidB = client.post("/api/clusters", json={"name": "B", "env_tag": "dev", "kubeconfig": _KUBECONFIG},
                       cookies=actors["admin"].cookies, headers={"X-CSRF-Token": actors["admin"].csrf}).json()["id"]
    make_user("p3-editor", PW, "editor")
    eid = next(u["id"] for u in client.get("/api/admin/users", cookies=actors["admin"].cookies).json()
               if u["username"] == "p3-editor")
    client.put(f"/api/admin/users/{eid}/clusters", json={"cluster_ids": [cidA]},
               cookies=actors["admin"].cookies, headers={"X-CSRF-Token": actors["admin"].csrf})
    editor = login("p3-editor", PW)

    rows = []

    def check(scenario, status, expected):
        rows.append((scenario, status, expected))
        assert status == expected, f"{scenario}: got {status}, expected {expected}"

    # GET /api/nodes/stats — any authenticated user
    check("unauth -> NODES", client.get(NODES).status_code, 401)
    check("viewer -> NODES", client.get(NODES, cookies=actors["viewer"].cookies).status_code, 200)
    check("editor -> NODES", client.get(NODES, cookies=editor.cookies).status_code, 200)
    check("admin  -> NODES", client.get(NODES, cookies=actors["admin"].cookies).status_code, 200)

    # GET /api/clusters/{id}/events — viewer/admin any; editor in-scope only
    evA, evB = f"/api/clusters/{cidA}/events", f"/api/clusters/{cidB}/events"
    check("unauth          -> EVENTS", client.get(evA).status_code, 401)
    check("viewer          -> EVENTS(A)", client.get(evA, cookies=actors["viewer"].cookies).status_code, 200)
    check("viewer          -> EVENTS(B)", client.get(evB, cookies=actors["viewer"].cookies).status_code, 200)
    check("editor in-scope -> EVENTS(A)", client.get(evA, cookies=editor.cookies).status_code, 200)
    check("editor out-scope-> EVENTS(B)", client.get(evB, cookies=editor.cookies).status_code, 403)
    check("admin           -> EVENTS(B)", client.get(evB, cookies=actors["admin"].cookies).status_code, 200)

    print("\n\nPHASE 3 NODE/EVENT RBAC MATRIX")
    print("-" * 48)
    for scenario, status, expected in rows:
        print(f"  {scenario:28s} {status} (expected {expected})")
    print("-" * 48)


_KUBECONFIG = """apiVersion: v1
kind: Config
current-context: c
clusters:
- name: c
  cluster: {server: https://127.0.0.1:6443}
contexts:
- name: c
  context: {cluster: c, user: u}
users:
- name: u
  user: {token: tok}
"""
