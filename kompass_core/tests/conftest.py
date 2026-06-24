from __future__ import annotations

from dataclasses import dataclass, field

import pytest
from fastapi.testclient import TestClient

from kompass_core.config import Settings
from kompass_core.main import create_app
from kompass_core.models import User, UserCluster

ENGINE_BASE = "http://127.0.0.1:9280"


@pytest.fixture
def settings(tmp_path) -> Settings:
    db_path = tmp_path / "kompass-test.db"
    return Settings(
        engine_base_url=ENGINE_BASE,
        static_dir="/nonexistent-kompass-static",
        cookie_secure=False,  # tests run over plain HTTP
        db_url=f"sqlite:///{db_path}",
        # Local KMS stand-in: a fixed 32-byte test KEK (base64). NOT a real key.
        kms_provider="local",
        local_kms_key="MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
    )


@pytest.fixture
def app(settings: Settings):
    return create_app(settings)


@pytest.fixture
def client(app):
    # TestClient runs lifespan: builds the DB and bootstraps the admin.
    with TestClient(app) as c:
        yield c


@dataclass
class Creds:
    """Captured login credentials for a user — used per-request so multiple
    identities can share one TestClient without cookie-jar cross-talk."""
    cookies: dict = field(default_factory=dict)
    csrf: str = ""


@pytest.fixture
def make_user(client):
    """Create a local user directly in the DB with a known password."""
    def _make(username, password, role, clusters=(), must_change=False):
        factory = client.app.state.session_factory
        svc = client.app.state.auth_service
        db = factory()
        try:
            user = User(
                username=username,
                password_hash=svc.pw.hash(password),
                role=role,
                must_change_password=must_change,
            )
            user.clusters = [UserCluster(cluster_id=c) for c in clusters]
            db.add(user)
            db.commit()
            return user.id
        finally:
            db.close()
    return _make


@pytest.fixture
def login(client):
    """Log in and return Creds (session cookie + CSRF token), leaving the
    shared client's cookie jar clean for explicit per-request use."""
    def _login(username, password):
        resp = client.post("/api/auth/login", json={"username": username, "password": password})
        assert resp.status_code == 200, resp.text
        cookie_name = client.app.state.settings.cookie_name
        token = resp.cookies.get(cookie_name)
        client.cookies.clear()
        return Creds(cookies={cookie_name: token}, csrf=resp.json()["csrf_token"])
    return _login
