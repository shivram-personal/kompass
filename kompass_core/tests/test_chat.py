"""Phase 5 verification: AI chat/troubleshoot, recommendation-only.

Covers the RBAC+per-cluster-scope matrix, the recommendation-only boundary (no
engine call / no mutation reachable from chat), provider-key handling, redaction
of the provider-bound payload AND persisted history, usage recording, and audit.
"""

import logging

import httpx
import pytest
import respx

from kompass_core.models import AiUsage, AuditEvent, ChatMessage, ClusterEvent
from kompass_core.db import utcnow

PW = "correct horse battery staple"
ANTHROPIC = "https://api.anthropic.com/v1/messages"

# Long base64-ish markers that redact_text strips (>=40 chars).
CTX_MARKER = "CTXLEAKaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
RESP_MARKER = "RESPLEAKbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
API_KEY = "sk-CHAT-PROVIDER-SECRET-aaaaaaaaaaaaaaaaaaaa"

KUBECONFIG = """apiVersion: v1
kind: Config
current-context: c
clusters:
- {name: c, cluster: {server: https://127.0.0.1:6443}}
contexts:
- {name: c, context: {cluster: c, user: u}}
users:
- {name: u, user: {token: t}}
"""


def _sse(text_with_marker: str) -> bytes:
    return (
        'event: message_start\n'
        'data: {"type":"message_start","message":{"usage":{"input_tokens":12}}}\n\n'
        'event: content_block_delta\n'
        'data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"You could scale it. "}}\n\n'
        'event: content_block_delta\n'
        f'data: {{"type":"content_block_delta","delta":{{"type":"text_delta","text":"{text_with_marker}"}}}}\n\n'
        'event: message_delta\n'
        'data: {"type":"message_delta","usage":{"output_tokens":9}}\n\n'
        'event: message_stop\n'
        'data: {}\n\n'
    ).encode()


def _db(client):
    return client.app.state.session_factory()


@pytest.fixture
def admin(make_user, login):
    make_user("ai-admin", PW, "admin")
    return login("ai-admin", PW)


def _setup(client, admin, name="prod"):
    """Register a cluster + an enabled keyed provider; return cluster_id."""
    cid = client.post("/api/clusters", json={"name": name, "env_tag": "prod", "kubeconfig": KUBECONFIG},
                      cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}).json()["id"]
    client.post("/api/admin/providers",
                json={"provider": "anthropic", "api_key": API_KEY, "active_model": "claude-x"},
                cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    return cid


def _chat(client, creds, cid, csrf=True, msg="what's wrong?"):
    h = {"X-CSRF-Token": creds.csrf} if csrf else {}
    return client.post("/api/ai/chat", json={"cluster_id": cid, "message": msg},
                       cookies=creds.cookies, headers=h)


# --- RBAC + per-cluster scope matrix ----------------------------------------
@respx.mock(assert_all_mocked=False)
def test_chat_rbac_scope_matrix(respx_mock, client, admin, make_user, login):
    respx_mock.post(ANTHROPIC).mock(return_value=httpx.Response(200, content=_sse("ok.")))
    cidA = _setup(client, admin, "A")
    cidB = client.post("/api/clusters", json={"name": "B", "env_tag": "dev", "kubeconfig": KUBECONFIG},
                       cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}).json()["id"]
    make_user("ai-viewer", PW, "viewer")
    make_user("ai-editor", PW, "editor")
    viewer = login("ai-viewer", PW)
    eid = next(u["id"] for u in client.get("/api/admin/users", cookies=admin.cookies).json()
               if u["username"] == "ai-editor")
    client.put(f"/api/admin/users/{eid}/clusters", json={"cluster_ids": [cidA]},
               cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    editor = login("ai-editor", PW)

    rows = []

    def check(s, st, ex):
        rows.append((s, st, ex))
        assert st == ex, f"{s}: got {st}, expected {ex}"

    check("unauth            -> CHAT", client.post("/api/ai/chat", json={"cluster_id": cidA, "message": "x"}).status_code, 401)
    check("viewer            -> CHAT", _chat(client, viewer, cidA).status_code, 200)
    check("editor in-scope   -> CHAT(A)", _chat(client, editor, cidA).status_code, 200)
    check("editor out-scope  -> CHAT(B)", _chat(client, editor, cidB).status_code, 403)
    check("admin             -> CHAT", _chat(client, admin, cidA).status_code, 200)
    check("admin no-CSRF     -> CHAT", _chat(client, admin, cidA, csrf=False).status_code, 403)

    print("\n\nPHASE 5 AI CHAT RBAC / SCOPE MATRIX")
    print("-" * 46)
    for s, st, ex in rows:
        print(f"  {s:26s} {st} (expected {ex})")
    print("-" * 46)


# --- recommendation-only boundary -------------------------------------------
@respx.mock(assert_all_mocked=False, assert_all_called=False)
def test_chat_makes_no_engine_or_mutation_call(respx_mock, client, admin):
    respx_mock.post(ANTHROPIC).mock(return_value=httpx.Response(200, content=_sse("scale it.")))
    # Any call to the engine (incl. write/injection seam routes) must NOT happen.
    engine = respx_mock.route(host="127.0.0.1", port=9280).mock(return_value=httpx.Response(200))
    cid = _setup(client, admin)

    resp = _chat(client, admin, cid)
    assert resp.status_code == 200
    assert not engine.called, "chat flow must not call the engine at all"


def test_apply_endpoint_exists_but_never_blindly_executes(client, admin):
    # Phase 6: the whitelisted apply path now EXISTS, but it is bound to a real,
    # previewed proposal — it never executes an unknown/ill-formed request.
    # Missing content_hash -> 422 (schema); unknown proposal id -> 404 (no blind exec).
    missing_body = client.post("/api/ai/proposals/whatever/apply", json={},
                               cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    assert missing_body.status_code == 422
    unknown = client.post("/api/ai/proposals/does-not-exist/apply",
                          json={"content_hash": "a" * 64}, cookies=admin.cookies,
                          headers={"X-CSRF-Token": admin.csrf})
    assert unknown.status_code == 404


# --- provider key handling ---------------------------------------------------
@respx.mock(assert_all_mocked=False)
def test_provider_key_never_logged_or_returned(respx_mock, client, admin, caplog):
    respx_mock.post(ANTHROPIC).mock(return_value=httpx.Response(200, content=_sse("ok.")))
    cid = _setup(client, admin)
    with caplog.at_level(logging.DEBUG):
        resp = _chat(client, admin, cid)
    assert API_KEY not in resp.text
    blob = "\n".join(r.getMessage() for r in caplog.records)
    assert API_KEY not in blob


# --- redaction: provider payload AND persisted history -----------------------
@respx.mock(assert_all_mocked=False)
def test_redaction_of_provider_payload_and_history(respx_mock, client, admin):
    captured = {}

    def _handler(request: httpx.Request) -> httpx.Response:
        captured["body"] = request.content.decode()
        return httpx.Response(200, content=_sse(f"leak {RESP_MARKER} end"))

    respx_mock.post(ANTHROPIC).mock(side_effect=_handler)
    cid = _setup(client, admin)

    # Insert a raw event carrying a secret marker (bypassing ingest redaction).
    db = _db(client)
    try:
        db.add(ClusterEvent(cluster_id=cid, ts=utcnow(), event_type="Warning",
                            reason="Leaky", message=f"secret {CTX_MARKER} here"))
        db.commit()
    finally:
        db.close()

    resp = _chat(client, admin, cid)
    assert resp.status_code == 200

    # 1. The cluster-data secret was redacted before being sent to the provider.
    assert CTX_MARKER not in captured["body"], "cluster secret reached the provider"
    assert "[REDACTED]" in captured["body"]

    # 2. The provider-echoed secret was redacted before persisting to history.
    db = _db(client)
    try:
        msgs = db.query(ChatMessage).filter(ChatMessage.cluster_id == cid).all()
        assert msgs, "expected persisted chat history"
        for m in msgs:
            assert RESP_MARKER not in m.content
            assert CTX_MARKER not in m.content
    finally:
        db.close()


# --- usage + audit -----------------------------------------------------------
@respx.mock(assert_all_mocked=False)
def test_usage_recorded_and_session_audited(respx_mock, client, admin):
    respx_mock.post(ANTHROPIC).mock(return_value=httpx.Response(200, content=_sse("ok.")))
    cid = _setup(client, admin)
    _chat(client, admin, cid)

    db = _db(client)
    try:
        usage = db.query(AiUsage).filter(AiUsage.cluster_id == cid).all()
        audits = db.query(AuditEvent).filter(AuditEvent.action == "ai_chat",
                                             AuditEvent.cluster_id == cid).all()
    finally:
        db.close()
    assert usage and usage[0].provider == "anthropic" and usage[0].model == "claude-x"
    assert usage[0].prompt_tokens > 0 and usage[0].completion_tokens > 0
    assert audits and audits[0].result == "attempt"


@respx.mock(assert_all_mocked=False)
def test_chat_streams_deltas_and_model_badge(respx_mock, client, admin):
    respx_mock.post(ANTHROPIC).mock(return_value=httpx.Response(200, content=_sse("done.")))
    cid = _setup(client, admin)
    resp = _chat(client, admin, cid)
    assert resp.status_code == 200
    body = resp.text
    assert "You could scale it." in body   # streamed delta
    assert "claude-x" in body              # active-model badge event
