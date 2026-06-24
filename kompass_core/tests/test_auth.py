"""Local-auth behavior: bootstrap, lockout, forced change, audit, no secrets."""

import logging

import httpx
import respx

from kompass_core.models import AuditEvent, User

PASSWORD = "correct horse battery staple"
ENGINE = "http://127.0.0.1:9280"


def _db(client):
    return client.app.state.session_factory()


def test_bootstrap_admin_created_once(client):
    db = _db(client)
    try:
        admins = db.query(User).filter(User.username == "admin").all()
        assert len(admins) == 1
        assert admins[0].role == "admin"
        assert admins[0].must_change_password is True
        # Re-running bootstrap is a no-op once users exist.
        assert client.app.state.auth_service.bootstrap_admin(db) is None
    finally:
        db.close()


def test_login_success_and_me(client, make_user, login):
    make_user("alice", PASSWORD, "viewer")
    creds = login("alice", PASSWORD)
    me = client.get("/api/auth/me", cookies=creds.cookies)
    assert me.status_code == 200
    body = me.json()
    assert body["user"]["username"] == "alice"
    assert body["user"]["role"] == "viewer"
    assert "password_hash" not in str(body)  # never leaked
    assert body["csrf_token"]


def test_lockout_after_threshold(client, make_user):
    make_user("bob", PASSWORD, "viewer")
    for _ in range(5):
        r = client.post("/api/auth/login", json={"username": "bob", "password": "wrong-pass-xx"})
        assert r.status_code == 401
    # Now locked: even the correct password is rejected during the window.
    r = client.post("/api/auth/login", json={"username": "bob", "password": PASSWORD})
    assert r.status_code == 401

    db = _db(client)
    try:
        results = [e.result for e in db.query(AuditEvent).filter(AuditEvent.action == "login").all()]
        assert "lockout" in results
        assert "locked" in results  # the post-lockout attempt
    finally:
        db.close()


@respx.mock(assert_all_mocked=False)
def test_forced_password_change_gate(respx_mock, client, make_user, login):
    respx_mock.get(f"{ENGINE}/api/topology").mock(return_value=httpx.Response(200, json={}))
    make_user("carol", PASSWORD, "admin", must_change=True)
    creds = login("carol", PASSWORD)

    # Blocked from acting until the password is changed.
    blocked = client.get("/api/engine/topology", cookies=creds.cookies)
    assert blocked.status_code == 403

    changed = client.post(
        "/api/auth/change-password",
        json={"current_password": PASSWORD, "new_password": "a-brand-new-strong-passphrase"},
        cookies=creds.cookies,
        headers={"X-CSRF-Token": creds.csrf},
    )
    assert changed.status_code == 200

    ok = client.get("/api/engine/topology", cookies=creds.cookies)
    assert ok.status_code == 200


def test_login_password_and_hash_never_logged(client, make_user, caplog):
    make_user("dave", PASSWORD, "viewer")
    db = _db(client)
    try:
        stored_hash = db.query(User).filter(User.username == "dave").one().password_hash
    finally:
        db.close()

    with caplog.at_level(logging.DEBUG):
        client.post("/api/auth/login", json={"username": "dave", "password": PASSWORD})
        client.post("/api/auth/login", json={"username": "dave", "password": "the-wrong-one"})

    blob = "\n".join(r.getMessage() for r in caplog.records)
    assert PASSWORD not in blob
    assert "the-wrong-one" not in blob
    assert stored_hash not in blob


def test_unknown_user_login_is_audited_without_enumeration(client):
    r = client.post("/api/auth/login", json={"username": "ghost", "password": "whatever-123456"})
    assert r.status_code == 401
    assert r.json()["detail"] == "Invalid credentials."
    db = _db(client)
    try:
        events = db.query(AuditEvent).filter(AuditEvent.action == "login",
                                             AuditEvent.username == "ghost").all()
        assert events and events[0].result == "failure"
    finally:
        db.close()
