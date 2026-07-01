"""Apply-action safety model (SPEC §4.3, Phase 6) — service-level proofs.

Covers: propose ≠ apply, diff-before-apply, proposal binding (hash), single-use
replay, TTL expiry, drift rejection, audit-before-execute (incl. on failure),
wrong-active-context abort, and serialized concurrent applies to different
clusters (no cross-cluster mutation). The engine is a stateful FakeEngine.
"""

import asyncio

import pytest

from kompass_core.ai import whitelist
from kompass_core.ai.proposals import ProposalConflict, ProposalError
from kompass_core.models import AuditEvent, Proposal, ProposalStatus, User

from ._fakes import KUBECONFIG, FakeEngine

PASSWORD = "correct horse battery staple"


# --- helpers -----------------------------------------------------------------

def _sf(client):
    return client.app.state.session_factory


def _svc(client):
    return client.app.state.proposal_service


def register_cluster(client, name):
    sf, cs = _sf(client), client.app.state.cluster_service
    db = sf()
    try:
        actor = User(username=f"reg-{name}", role="admin")
        db.add(actor)
        db.commit()
        cl = cs.register(db, actor=actor, name=name, env_tag="dev", kubeconfig_text=KUBECONFIG)
        return cl.id
    finally:
        db.close()


def make_user(client, username, role, clusters=()):
    from kompass_core.models import UserCluster
    sf = _sf(client)
    db = sf()
    try:
        u = User(username=username, role=role)
        u.clusters = [UserCluster(cluster_id=c) for c in clusters]
        db.add(u)
        db.commit()
        return u.id
    finally:
        db.close()


def get_user(client, uid):
    sf = _sf(client)
    db = sf()
    try:
        u = db.get(User, uid)
        _ = (u.id, u.username, u.role)  # load attrs before detaching
        db.expunge(u)
        return u
    finally:
        db.close()


def make_scale_proposal(client, cluster_id, uid, username, name="web", replicas=3):
    validated = whitelist.validate({
        "action": "scale_deployment", "cluster_id": cluster_id,
        "params": {"kind": "Deployment", "namespace": "default", "name": name, "replicas": replicas},
    })
    db = _sf(client)()
    try:
        p = _svc(client).create(db, user_id=uid, username=username, validated=validated, rationale=None)
        return p.id, p.content_hash
    finally:
        db.close()


def get_proposal(client, pid):
    db = _sf(client)()
    try:
        return db.get(Proposal, pid)
    finally:
        db.close()


def apply_audit_rows(client, pid):
    db = _sf(client)()
    try:
        rows = db.query(AuditEvent).filter(AuditEvent.action == "ai_apply").order_by(AuditEvent.id).all()
        return [(r.result, r.params_redacted) for r in rows if r.params_redacted and pid in r.params_redacted]
    finally:
        db.close()


def do_preview(client, engine, lock, pid):
    return asyncio.run(_svc(client).preview(
        engine=engine, lock=lock, session_factory=_sf(client), proposal_id=pid))


def do_apply(client, engine, lock, pid, user, chash, rid="req1"):
    return asyncio.run(_svc(client).apply(
        engine=engine, lock=lock, session_factory=_sf(client),
        proposal_id=pid, user=user, confirmed_hash=chash, request_id=rid))


# --- tests -------------------------------------------------------------------

def test_creating_a_proposal_makes_no_mutation(client):
    """propose ≠ apply: a persisted proposal is inert; nothing reaches the engine."""
    cid = register_cluster(client, "ca")
    uid = make_user(client, "creator", "editor", clusters=[cid])
    pid, _ = make_scale_proposal(client, cid, uid, "creator")
    p = get_proposal(client, pid)
    assert p.status == ProposalStatus.proposed
    # No engine object was even involved in creation.
    fake = FakeEngine()
    assert fake.writes == [] and fake.selects == []


def test_preview_then_apply_happy_path(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "creator2", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "creator2")
    fake = FakeEngine(resource_rv="100", replicas=1)
    lock = asyncio.Lock()

    prev = do_preview(client, fake, lock, pid)
    assert prev["before"] and prev["after"] and prev["content_hash"] == chash

    res = do_apply(client, fake, lock, pid, get_user(client, uid), chash)
    assert res["result"] == "success"
    # Exactly one mutating call, to the correct route, with the target cluster active.
    assert len(fake.writes) == 1
    path, active = fake.writes[0]
    assert path == "/api/workloads/Deployment/default/web/scale"
    assert active == cid
    # Single-use: proposal is now terminal.
    assert get_proposal(client, pid).status == ProposalStatus.consumed
    # Audit: intent (attempt) BEFORE + success outcome.
    results = [r for r, _ in apply_audit_rows(client, pid)]
    assert results[0] == "attempt" and "success" in results


def test_apply_requires_a_preview_first(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c3", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c3")
    fake = FakeEngine()
    with pytest.raises(ProposalConflict):
        do_apply(client, fake, asyncio.Lock(), pid, get_user(client, uid), chash)
    assert fake.writes == []  # never mutated


def test_hash_mismatch_is_rejected_and_does_not_consume(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c4", "editor", clusters=[cid])
    pid, _ = make_scale_proposal(client, cid, uid, "c4")
    fake = FakeEngine()
    lock = asyncio.Lock()
    do_preview(client, fake, lock, pid)
    with pytest.raises(ProposalConflict):
        do_apply(client, fake, lock, pid, get_user(client, uid), "0" * 64)
    assert fake.writes == []
    assert get_proposal(client, pid).status == ProposalStatus.proposed  # still applyable


def test_single_use_replay_rejected(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c5", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c5")
    fake = FakeEngine()
    lock = asyncio.Lock()
    do_preview(client, fake, lock, pid)
    do_apply(client, fake, lock, pid, get_user(client, uid), chash)
    with pytest.raises(ProposalConflict):
        do_apply(client, fake, lock, pid, get_user(client, uid), chash)  # replay
    assert len(fake.writes) == 1  # no double mutation


def test_expired_proposal_rejected(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c6", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c6")
    # Force expiry.
    db = _sf(client)()
    try:
        from datetime import datetime, timedelta, timezone
        p = db.get(Proposal, pid)
        p.expires_at = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(seconds=1)
        db.commit()
    finally:
        db.close()
    fake = FakeEngine()
    with pytest.raises(ProposalConflict):
        do_preview(client, fake, asyncio.Lock(), pid)
    assert get_proposal(client, pid).status == ProposalStatus.expired
    assert fake.writes == []


def test_drift_rejected_and_not_mutated(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c7", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c7")
    fake = FakeEngine(resource_rv="100")
    lock = asyncio.Lock()
    do_preview(client, fake, lock, pid)   # baseline 100
    fake.resource_rv = "200"              # the world changed
    with pytest.raises(ProposalConflict):
        do_apply(client, fake, lock, pid, get_user(client, uid), chash)
    assert fake.writes == []  # drift => no mutation
    results = [r for r, _ in apply_audit_rows(client, pid)]
    assert results[0] == "attempt" and "drift_rejected" in results


def test_audit_before_execute_survives_apply_failure(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c8", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c8")
    fake = FakeEngine(write_status=500)  # engine rejects the mutation
    lock = asyncio.Lock()
    do_preview(client, fake, lock, pid)
    with pytest.raises(ProposalError):
        do_apply(client, fake, lock, pid, get_user(client, uid), chash)
    results = [r for r, _ in apply_audit_rows(client, pid)]
    # Intent row exists BEFORE the (failed) mutation, plus a failure outcome.
    assert results[0] == "attempt"
    assert "failure" in results
    assert len(fake.writes) == 1  # it was attempted


def test_wrong_active_context_aborts_without_mutation(client):
    cid = register_cluster(client, "ca")
    uid = make_user(client, "c9", "editor", clusters=[cid])
    pid, chash = make_scale_proposal(client, cid, uid, "c9")
    lock = asyncio.Lock()
    # Preview against a healthy engine to establish the baseline.
    do_preview(client, FakeEngine(), lock, pid)
    # At apply time the engine reports a DIFFERENT active cluster on select ->
    # the re-assertion must abort before mutating.
    wrong = FakeEngine(wrong_select_id="some-other-cluster")
    with pytest.raises(ProposalConflict):
        do_apply(client, wrong, lock, pid, get_user(client, uid), chash)
    assert wrong.writes == []


def test_concurrent_applies_to_different_clusters_do_not_cross_execute(client):
    """The process-wide lock serializes select→mutate: each write executes while
    ITS target cluster is the active context, never the other's."""
    cid_a = register_cluster(client, "ca")
    cid_b = register_cluster(client, "cb")
    ua = make_user(client, "ua", "editor", clusters=[cid_a])
    ub = make_user(client, "ub", "editor", clusters=[cid_b])
    pid_a, ha = make_scale_proposal(client, cid_a, ua, "ua", name="app-a")
    pid_b, hb = make_scale_proposal(client, cid_b, ub, "ub", name="app-b")

    # One shared engine + one shared lock; select_sleep forces interleave windows.
    fake = FakeEngine(select_sleep=0.01)
    lock = asyncio.Lock()
    svc = _svc(client)
    sf = _sf(client)

    async def run_both():
        await fake_preview(svc, fake, lock, sf, pid_a)
        await fake_preview(svc, fake, lock, sf, pid_b)
        return await asyncio.gather(
            svc.apply(engine=fake, lock=lock, session_factory=sf, proposal_id=pid_a,
                      user=get_user(client, ua), confirmed_hash=ha, request_id="ra"),
            svc.apply(engine=fake, lock=lock, session_factory=sf, proposal_id=pid_b,
                      user=get_user(client, ub), confirmed_hash=hb, request_id="rb"),
        )

    results = asyncio.run(run_both())
    assert all(r["result"] == "success" for r in results)
    assert len(fake.writes) == 2
    for path, active in fake.writes:
        if "app-a" in path:
            assert active == cid_a, f"app-a written while {active} active (cross-cluster!)"
        elif "app-b" in path:
            assert active == cid_b, f"app-b written while {active} active (cross-cluster!)"


async def fake_preview(svc, fake, lock, sf, pid):
    await svc.preview(engine=fake, lock=lock, session_factory=sf, proposal_id=pid)
