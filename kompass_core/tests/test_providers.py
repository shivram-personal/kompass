"""Phase 4 verification: provider/model registry + KMS-encrypted API keys.

Covers the admin-only RBAC matrix, ciphertext-only-at-rest (seeded-marker scan),
masking + no-leak (incl. decrypt-failure path), model picker (fetch + editable
fallback), and audit-before-execute.
"""

import logging

import httpx
import pytest
import respx

from kompass_core.models import AuditEvent, ProviderConfig

PW = "correct horse battery staple"
API_KEY = "sk-SEEDED-PROVIDER-SECRET-KEY-abcdef0123456789"
KEY_MARKERS = ["sk-SEEDED-PROVIDER-SECRET-KEY", "abcdef0123456789"]


def _db(client):
    return client.app.state.session_factory()


@pytest.fixture
def admin(make_user, login):
    make_user("prov-admin", PW, "admin")
    return login("prov-admin", PW)


def _create(client, admin, provider="anthropic", api_key=API_KEY, models=None):
    body = {"provider": provider, "api_key": api_key, "active_model": "m1",
            "models": models or ["m1", "m2"]}
    return client.post("/api/admin/providers", json=body, cookies=admin.cookies,
                       headers={"X-CSRF-Token": admin.csrf})


# --- RBAC matrix -------------------------------------------------------------
def test_provider_rbac_matrix(client, make_user, login):
    make_user("pv-viewer", PW, "viewer")
    make_user("pv-editor", PW, "editor")
    make_user("pv-admin", PW, "admin")
    viewer = login("pv-viewer", PW)
    editor = login("pv-editor", PW)
    admin = login("pv-admin", PW)

    def create(creds, csrf=True, provider="p"):
        h = {"X-CSRF-Token": creds.csrf} if csrf else {}
        return client.post("/api/admin/providers",
                           json={"provider": provider, "api_key": API_KEY},
                           cookies=creds.cookies, headers=h).status_code

    rows = []

    def check(s, st, ex):
        rows.append((s, st, ex))
        assert st == ex, f"{s}: got {st}, expected {ex}"

    # list (GET) — admin-only (provider config is global, not per-cluster)
    check("unauth -> LIST", client.get("/api/admin/providers").status_code, 401)
    check("viewer -> LIST", client.get("/api/admin/providers", cookies=viewer.cookies).status_code, 403)
    check("editor -> LIST", client.get("/api/admin/providers", cookies=editor.cookies).status_code, 403)
    check("admin  -> LIST", client.get("/api/admin/providers", cookies=admin.cookies).status_code, 200)

    # create (POST)
    check("unauth -> CREATE", client.post("/api/admin/providers", json={}).status_code, 401)
    check("viewer -> CREATE", create(viewer, provider="pv"), 403)
    check("editor -> CREATE", create(editor, provider="pe"), 403)
    check("admin  -> CREATE", create(admin, provider="anthropic"), 201)
    check("admin no-CSRF -> CREATE", create(admin, csrf=False, provider="x"), 403)

    # update (PATCH) + delete (DELETE) — admin-only
    check("viewer -> PATCH", client.patch("/api/admin/providers/anthropic", json={"enabled": False},
                                          cookies=viewer.cookies, headers={"X-CSRF-Token": viewer.csrf}).status_code, 403)
    check("admin  -> PATCH", client.patch("/api/admin/providers/anthropic", json={"enabled": False},
                                          cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}).status_code, 200)
    check("editor -> DELETE", client.delete("/api/admin/providers/anthropic",
                                            cookies=editor.cookies, headers={"X-CSRF-Token": editor.csrf}).status_code, 403)
    check("admin  -> DELETE", client.delete("/api/admin/providers/anthropic",
                                            cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf}).status_code, 204)

    print("\n\nPHASE 4 PROVIDER RBAC MATRIX")
    print("-" * 44)
    for s, st, ex in rows:
        print(f"  {s:24s} {st} (expected {ex})")
    print("-" * 44)


# --- secrets at rest ---------------------------------------------------------
def test_api_key_stored_only_as_ciphertext(client, admin):
    assert _create(client, admin).status_code == 201
    db = _db(client)
    try:
        p = db.query(ProviderConfig).filter(ProviderConfig.provider == "anthropic").one()
        at_rest = bytes(p.api_key_ciphertext) + bytes(p.wrapped_dek) + bytes(p.nonce)
    finally:
        db.close()
    for marker in KEY_MARKERS:
        assert marker.encode() not in at_rest, f"{marker!r} leaked into at-rest blob"
    print("\nAT-REST provider row (no plaintext key):")
    print(f"  kms_key_ref       = {p.kms_key_ref}")
    print(f"  ciphertext[0:16]  = {bytes(p.api_key_ciphertext)[:16].hex()}…")
    print(f"  api_key_last4     = {p.api_key_last4}")


def test_api_key_masked_in_responses(client, admin):
    _create(client, admin)
    for resp in (
        client.get("/api/admin/providers", cookies=admin.cookies),
        client.get("/api/admin/providers/anthropic", cookies=admin.cookies),
    ):
        body = resp.text
        for marker in KEY_MARKERS:
            assert marker not in body, f"{marker!r} leaked into a response"
        assert "…6789" in body  # masked last4 shown


def test_api_key_never_logged(client, admin, caplog):
    with caplog.at_level(logging.DEBUG):
        _create(client, admin)
        client.patch("/api/admin/providers/anthropic", json={"api_key": API_KEY + "X2"},
                     cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    blob = "\n".join(r.getMessage() for r in caplog.records)
    for marker in KEY_MARKERS:
        assert marker not in blob


def test_decrypt_failure_returns_generic_error(client, admin):
    _create(client, admin)
    db = _db(client)
    try:
        p = db.query(ProviderConfig).filter(ProviderConfig.provider == "anthropic").one()
        p.wrapped_dek = b"\x00" * len(p.wrapped_dek)  # tamper
        db.commit()
    finally:
        db.close()
    resp = client.get("/api/admin/providers/anthropic/models", cookies=admin.cookies)
    assert resp.status_code == 502
    body = resp.text
    for marker in KEY_MARKERS:
        assert marker not in body
    assert "Could not access provider credentials" in resp.json()["detail"]


# --- model picker ------------------------------------------------------------
@respx.mock(assert_all_mocked=False)
def test_model_picker_fetches_from_provider(respx_mock, client, admin):
    respx_mock.get("https://api.anthropic.com/v1/models").mock(
        return_value=httpx.Response(200, json={"data": [{"id": "claude-x"}, {"id": "claude-y"}]})
    )
    _create(client, admin)
    resp = client.get("/api/admin/providers/anthropic/models", cookies=admin.cookies)
    assert resp.status_code == 200
    body = resp.json()
    assert body["source"] == "provider"
    assert body["models"] == ["claude-x", "claude-y"]


@respx.mock(assert_all_mocked=False, assert_all_called=False)
def test_model_picker_falls_back_to_editable_list(respx_mock, client, admin):
    respx_mock.get("https://api.anthropic.com/v1/models").mock(side_effect=httpx.ConnectError("down"))
    _create(client, admin, models=["edit-1", "edit-2"])
    resp = client.get("/api/admin/providers/anthropic/models", cookies=admin.cookies)
    assert resp.status_code == 200
    body = resp.json()
    assert body["source"] == "configured"
    assert body["models"] == ["edit-1", "edit-2"]


# --- audit -------------------------------------------------------------------
def test_provider_crud_is_audited(client, admin):
    _create(client, admin)
    client.patch("/api/admin/providers/anthropic", json={"api_key": API_KEY + "X3"},
                 cookies=admin.cookies, headers={"X-CSRF-Token": admin.csrf})
    client.delete("/api/admin/providers/anthropic", cookies=admin.cookies,
                  headers={"X-CSRF-Token": admin.csrf})
    db = _db(client)
    try:
        actions = {e.action for e in db.query(AuditEvent).filter(AuditEvent.target == "anthropic").all()}
    finally:
        db.close()
    assert {"provider_create", "provider_rotate_key", "provider_delete"} <= actions
