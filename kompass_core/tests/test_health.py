import httpx
import respx


def test_healthz_is_liveness_only(client):
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


@respx.mock(assert_all_mocked=False)
def test_readyz_ready_when_engine_answers(respx_mock, client):
    respx_mock.get("http://127.0.0.1:9280/api/health").mock(
        return_value=httpx.Response(200, json={"ok": True})
    )
    resp = client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ready"}


@respx.mock(assert_all_mocked=False)
def test_readyz_503_when_engine_unreachable(respx_mock, client):
    respx_mock.get("http://127.0.0.1:9280/api/health").mock(
        side_effect=httpx.ConnectError("refused")
    )
    resp = client.get("/readyz")
    assert resp.status_code == 503
    assert resp.json() == {"status": "engine-unreachable"}
