"""Single-worker boot guard (SPEC §4.3, load-bearing).

The select->apply serialization uses one in-process asyncio.Lock; more than one
worker would silently break wrong-cluster protection. Core must REFUSE TO BOOT.
"""

import pytest

from kompass_core.config import Settings
from kompass_core.main import create_app


def _settings(tmp_path, **over):
    return Settings(
        static_dir="/nonexistent-kompass-static",
        cookie_secure=False,
        db_url=f"sqlite:///{tmp_path/'k.db'}",
        kms_provider="local",
        local_kms_key="MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
        **over,
    )


def test_single_worker_boots(tmp_path):
    # workers=1 (default) must create the app fine.
    app = create_app(_settings(tmp_path, workers=1))
    assert app is not None


def test_multi_worker_refuses_to_boot(tmp_path):
    with pytest.raises(RuntimeError, match="single worker"):
        create_app(_settings(tmp_path, workers=2))


def test_web_concurrency_env_refuses_to_boot(tmp_path, monkeypatch):
    monkeypatch.setenv("WEB_CONCURRENCY", "4")
    with pytest.raises(RuntimeError, match="single worker"):
        create_app(_settings(tmp_path, workers=1))
