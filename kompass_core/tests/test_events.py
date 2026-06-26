"""Phase 3 event-store verification: retention prune, config window, index,
per-cluster scoping, and secret redaction at rest."""

from datetime import timedelta

import pytest
from sqlalchemy import inspect

from kompass_core.config import Settings
from kompass_core.db import build_session_factory, utcnow
from kompass_core.events.service import EventService
from kompass_core.models import ClusterEvent

PW = "correct horse battery staple"


@pytest.fixture
def svc():
    return EventService(Settings(local_kms_key="MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="))


@pytest.fixture
def db(tmp_path):
    factory = build_session_factory(f"sqlite:///{tmp_path/'ev.db'}")
    s = factory()
    yield s
    s.close()


def _evt(uid, ts, message="pod started", etype="Normal"):
    return {
        "metadata": {"uid": uid, "namespace": "default"},
        "type": etype,
        "reason": "Started",
        "message": message,
        "involvedObject": {"kind": "Pod", "name": "foo", "namespace": "default"},
        "source": {"component": "kubelet"},
        "lastTimestamp": ts.isoformat() + "Z",
    }


def test_retention_default_is_30_days():
    assert Settings().event_retention_days == 30


def test_prune_keeps_only_in_window(svc, db):
    now = utcnow()
    svc.ingest(db, "cluster-a", [
        _evt("old-1", now - timedelta(days=45)),
        _evt("old-2", now - timedelta(days=31)),
        _evt("fresh-1", now - timedelta(days=10)),
        _evt("fresh-2", now - timedelta(hours=1)),
    ])
    assert db.query(ClusterEvent).count() == 4

    removed = svc.prune(db, now=now)
    assert removed == 2
    remaining = {e.uid for e in db.query(ClusterEvent).all()}
    assert remaining == {"fresh-1", "fresh-2"}


def test_retention_window_is_read_from_config(db):
    # A 14-day window prunes the 20-day-old event that a 30-day window keeps.
    now = utcnow()
    svc14 = EventService(Settings(event_retention_days=14))
    svc14.ingest(db, "c", [_evt("e20", now - timedelta(days=20)), _evt("e5", now - timedelta(days=5))])
    assert svc14.prune(db, now=now) == 1
    assert {e.uid for e in db.query(ClusterEvent).all()} == {"e5"}


def test_composite_index_on_cluster_and_ts(db):
    insp = inspect(db.get_bind())
    indexes = insp.get_indexes("cluster_events")
    names = {tuple(ix["column_names"]) for ix in indexes}
    assert ("cluster_id", "ts") in names, f"expected (cluster_id, ts) index, got {names}"


def test_seeded_secrets_are_redacted_at_rest(svc, db):
    now = utcnow()
    leaky = (
        "ConfigMap captured: token: SUPER-SECRET-TOKEN-abc123 "
        "certificate-authority-data: SUPERSECRETCADATAbase64xxxxxxxxxxxxxxxxxxxxxxxx"
    )
    svc.ingest(db, "cluster-a", [_evt("leak-1", now, message=leaky)])
    stored = db.query(ClusterEvent).filter(ClusterEvent.uid == "leak-1").one().message
    assert "SUPER-SECRET-TOKEN-abc123" not in stored
    assert "SUPERSECRETCADATAbase64xxxxxxxxxxxxxxxxxxxxxxxx" not in stored
    assert "[REDACTED]" in stored


def test_list_is_cluster_scoped(svc, db):
    now = utcnow()
    svc.ingest(db, "cluster-a", [_evt("a1", now)])
    svc.ingest(db, "cluster-b", [_evt("b1", now)])
    a = svc.list(db, "cluster-a")
    assert [e["cluster_id"] for e in a] == ["cluster-a"]
