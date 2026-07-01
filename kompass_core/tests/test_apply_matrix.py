"""Role × endpoint authorization matrix for the Phase 6 propose/apply surface.

Verifies 401/403/200 for GET preview and POST apply across viewer / editor
(in-scope, out-of-scope, non-creator) / admin, plus CSRF and separation-of-duties
(creator-or-admin). The matrix is printed for the gate log.
"""

import json

import pytest

from kompass_core.ai import whitelist
from kompass_core.models import Proposal, User

from ._fakes import KUBECONFIG, FakeEngine

PASSWORD = "correct horse battery staple"
FAKE_HASH = "a" * 64


def register_cluster(client, name):
    cs = client.app.state.cluster_service
    db = client.app.state.session_factory()
    try:
        actor = User(username=f"reg-{name}", role="admin")
        db.add(actor)
        db.commit()
        return cs.register(db, actor=actor, name=name, env_tag="dev", kubeconfig_text=KUBECONFIG).id
    finally:
        db.close()


def seed_proposal(client, cluster_id, creator_uid, creator_username, name="web"):
    validated = whitelist.validate({
        "action": "scale_deployment", "cluster_id": cluster_id,
        "params": {"kind": "Deployment", "namespace": "default", "name": name, "replicas": 3},
    })
    db = client.app.state.session_factory()
    try:
        p = client.app.state.proposal_service.create(
            db, user_id=creator_uid, username=creator_username, validated=validated, rationale=None)
        # Pretend a preview already ran so apply can proceed for the 200 cases.
        row = db.get(Proposal, p.id)
        row.preview_drift_token = "100"
        row.before_summary, row.after_summary = "before", "after"
        db.commit()
        return p.id, p.content_hash
    finally:
        db.close()


def _apply(client, pid, creds=None, chash=FAKE_HASH, csrf=True):
    headers, cookies = {}, {}
    if creds is not None:
        cookies = creds.cookies
        if csrf:
            headers["X-CSRF-Token"] = creds.csrf
    return client.post(f"/api/ai/proposals/{pid}/apply", json={"content_hash": chash},
                       cookies=cookies, headers=headers)


def _preview(client, pid, creds=None):
    cookies = creds.cookies if creds else {}
    return client.get(f"/api/ai/proposals/{pid}/preview", cookies=cookies)


@pytest.fixture
def env(client, make_user, login):
    # Engine stand-in so the 200 apply/preview paths have a working "cluster".
    client.app.state.engine_client = FakeEngine(resource_rv="100")
    cid_a = register_cluster(client, "cluster-a")
    cid_b = register_cluster(client, "cluster-b")
    ids = {
        "viewer": make_user("mx-viewer", PASSWORD, "viewer"),
        "editor": make_user("mx-editor", PASSWORD, "editor", clusters=[cid_a]),
        "editor2": make_user("mx-editor2", PASSWORD, "editor", clusters=[cid_a]),
        "admin": make_user("mx-admin", PASSWORD, "admin"),
    }
    creds = {
        "viewer": login("mx-viewer", PASSWORD),
        "editor": login("mx-editor", PASSWORD),
        "editor2": login("mx-editor2", PASSWORD),
        "admin": login("mx-admin", PASSWORD),
    }
    return {"cid_a": cid_a, "cid_b": cid_b, "ids": ids, "creds": creds}


def test_apply_preview_matrix(client, env):
    cid_a, cid_b, ids, creds = env["cid_a"], env["cid_b"], env["ids"], env["creds"]
    rows = []

    def check(scenario, status, expected):
        rows.append((scenario, status, expected))
        assert status == expected, f"{scenario}: got {status}, expected {expected}"

    # Proposals created by the editor (in cluster-a and out-of-scope cluster-b).
    p_a, h_a = seed_proposal(client, cid_a, ids["editor"], "mx-editor")
    p_b, _ = seed_proposal(client, cid_b, ids["editor"], "mx-editor")
    p_admin, h_admin = seed_proposal(client, cid_a, ids["editor"], "mx-editor", name="svc2")

    # --- APPLY ---------------------------------------------------------------
    check("unauth              -> APPLY", _apply(client, p_a).status_code, 401)
    check("no-CSRF (editor)    -> APPLY", _apply(client, p_a, creds["editor"], csrf=False).status_code, 403)
    check("viewer              -> APPLY", _apply(client, p_a, creds["viewer"]).status_code, 403)
    check("editor out-of-scope -> APPLY", _apply(client, p_b, creds["editor"], chash=FAKE_HASH).status_code, 403)
    check("editor in-scope non-creator -> APPLY", _apply(client, p_a, creds["editor2"]).status_code, 403)
    # creator editor, in-scope, correct hash -> success (consumes p_a).
    check("editor creator      -> APPLY", _apply(client, p_a, creds["editor"], chash=h_a).status_code, 200)
    # admin can apply another user's proposal (separate proposal, correct hash).
    check("admin (non-creator) -> APPLY", _apply(client, p_admin, creds["admin"], chash=h_admin).status_code, 200)

    # --- PREVIEW -------------------------------------------------------------
    p_prev, _ = seed_proposal(client, cid_a, ids["editor"], "mx-editor", name="prevsvc")
    check("unauth              -> PREVIEW", _preview(client, p_prev).status_code, 401)
    check("in-scope non-creator-> PREVIEW", _preview(client, p_prev, creds["editor2"]).status_code, 403)
    check("creator             -> PREVIEW", _preview(client, p_prev, creds["editor"]).status_code, 200)

    print("\n\nAPPLY/PREVIEW ROLE × ENDPOINT MATRIX")
    print("-" * 52)
    for scenario, status, expected in rows:
        print(f"  {scenario:34s}  {status}  (expected {expected})")
    print("-" * 52)


def test_apply_unknown_proposal_is_404(client, env):
    assert _apply(client, "does-not-exist", env["creds"]["admin"], chash=FAKE_HASH).status_code == 404
