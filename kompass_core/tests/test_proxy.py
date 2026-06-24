import httpx
import pytest
import respx

ENGINE = "http://127.0.0.1:9280"


@pytest.fixture
def admin(make_user, login):
    make_user("prox-admin", "correct horse battery staple", "admin")
    return login("prox-admin", "correct horse battery staple")


@respx.mock(assert_all_mocked=False)
def test_proxy_strips_engine_prefix(respx_mock, client, admin):
    route = respx_mock.get(f"{ENGINE}/api/topology").mock(
        return_value=httpx.Response(200, json={"nodes": []})
    )
    resp = client.get("/api/engine/topology", cookies=admin.cookies)

    assert resp.status_code == 200
    assert resp.json() == {"nodes": []}
    assert route.called
    assert route.calls.last.request.url.path == "/api/topology"


@respx.mock(assert_all_mocked=False)
def test_proxy_forwards_query_string(respx_mock, client, admin):
    route = respx_mock.get(f"{ENGINE}/api/resources").mock(
        return_value=httpx.Response(200, json={})
    )
    client.get("/api/engine/resources?kind=Pod&namespace=default", cookies=admin.cookies)

    sent = route.calls.last.request
    assert sent.url.params["kind"] == "Pod"
    assert sent.url.params["namespace"] == "default"


@respx.mock(assert_all_mocked=False)
def test_proxy_forwards_post_body(respx_mock, client, admin):
    captured = {}

    def _handler(request: httpx.Request) -> httpx.Response:
        captured["body"] = request.content
        return httpx.Response(202, json={"accepted": True})

    respx_mock.post(f"{ENGINE}/api/context/switch").mock(side_effect=_handler)
    resp = client.post(
        "/api/engine/context/switch",
        json={"context": "kind-kind"},
        cookies=admin.cookies,
        headers={"X-CSRF-Token": admin.csrf},
    )

    assert resp.status_code == 202
    assert b"kind-kind" in captured["body"]


@respx.mock(assert_all_mocked=False)
def test_proxy_propagates_upstream_status(respx_mock, client, admin):
    respx_mock.get(f"{ENGINE}/api/missing").mock(
        return_value=httpx.Response(404, json={"error": "not found"})
    )
    resp = client.get("/api/engine/missing", cookies=admin.cookies)
    assert resp.status_code == 404
    assert resp.json() == {"error": "not found"}


@respx.mock(assert_all_mocked=False)
def test_proxy_streams_sse_without_buffering(respx_mock, client, admin):
    sse_body = b"event: topology\ndata: {}\n\nevent: heartbeat\ndata: {}\n\n"
    respx_mock.get(f"{ENGINE}/api/events/stream").mock(
        return_value=httpx.Response(
            200,
            headers={"content-type": "text/event-stream"},
            content=sse_body,
        )
    )
    resp = client.get("/api/engine/events/stream", cookies=admin.cookies)
    assert resp.status_code == 200
    assert resp.headers["content-type"].startswith("text/event-stream")
    assert b"event: topology" in resp.content


def test_proxy_requires_authentication(client):
    # No session cookie -> 401, and the engine is never contacted.
    resp = client.get("/api/engine/topology")
    assert resp.status_code == 401
