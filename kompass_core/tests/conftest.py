import pytest
from fastapi.testclient import TestClient

from kompass_core.config import Settings
from kompass_core.main import create_app

ENGINE_BASE = "http://127.0.0.1:9280"


@pytest.fixture
def settings() -> Settings:
    # Point static_dir somewhere absent so the test app skips UI mounting and
    # the proxy routes are exercised in isolation.
    return Settings(
        engine_base_url=ENGINE_BASE,
        static_dir="/nonexistent-kompass-static",
    )


@pytest.fixture
def client(settings: Settings):
    with TestClient(create_app(settings)) as c:
        yield c
