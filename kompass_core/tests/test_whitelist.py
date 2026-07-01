"""Whitelist validation (SPEC §4.3, Phase 6): default-deny + binding hash."""

import pytest

from kompass_core.ai import whitelist
from kompass_core.ai.whitelist import WhitelistError


def test_all_seven_v1_actions_present_and_no_more():
    assert set(whitelist.WHITELIST) == {
        "scale_deployment", "restart_workload", "helm_rollback", "gitops_reconcile",
        "gitops_suspend_resume", "cordon_node", "uncordon_node",
    }


@pytest.mark.parametrize("action", [
    "delete_deployment", "exec_pod", "apply_yaml", "patch_resource",
    "edit_secret", "drain_node", "", "scale_deployment ",  # trailing space != match
])
def test_non_whitelisted_action_is_rejected(action):
    raw = {"action": action, "cluster_id": "c1", "params": {}}
    with pytest.raises(WhitelistError):
        whitelist.validate(raw)


def test_valid_scale_proposal_validates_and_maps_to_engine_route():
    raw = {"action": "scale_deployment", "cluster_id": "c1",
           "params": {"kind": "Deployment", "namespace": "default", "name": "web", "replicas": 3}}
    vp = whitelist.validate(raw)
    assert vp.action == "scale_deployment"
    assert vp.cluster_id == "c1"
    assert vp.namespace == "default"
    write = vp.spec.write(vp.params)
    assert write.method == "POST"
    assert write.path == "/api/workloads/Deployment/default/web/scale"
    assert write.json == {"replicas": 3}


def test_helm_rollback_uses_revision_query_param():
    raw = {"action": "helm_rollback", "cluster_id": "c1",
           "params": {"namespace": "prod", "name": "api", "revision": 4}}
    vp = whitelist.validate(raw)
    write = vp.spec.write(vp.params)
    assert write.path == "/api/helm/releases/prod/api/rollback"
    assert write.query == {"revision": "4"}


def test_cordon_node_read_uses_cluster_scoped_placeholder():
    raw = {"action": "cordon_node", "cluster_id": "c1", "params": {"name": "node-1"}}
    vp = whitelist.validate(raw)
    read = vp.spec.read(vp.params)
    assert read.path == "/api/resources/Node/_/node-1"
    assert vp.spec.write(vp.params).path == "/api/nodes/node-1/cordon"


def test_bad_params_rejected_without_echoing_them():
    # replicas out of range
    raw = {"action": "scale_deployment", "cluster_id": "c1",
           "params": {"kind": "Deployment", "namespace": "default", "name": "web", "replicas": -1}}
    with pytest.raises(WhitelistError) as e:
        whitelist.validate(raw)
    assert "web" not in str(e.value)  # never echoes params


def test_extra_params_field_rejected():
    raw = {"action": "cordon_node", "cluster_id": "c1",
           "params": {"name": "node-1", "sneaky": "x"}}
    with pytest.raises(WhitelistError):
        whitelist.validate(raw)


def test_content_hash_is_stable_and_order_independent():
    a = {"action": "scale_deployment", "cluster_id": "c1",
         "params": {"kind": "Deployment", "namespace": "default", "name": "web", "replicas": 3}}
    b = {"cluster_id": "c1", "action": "scale_deployment",
         "params": {"replicas": 3, "name": "web", "kind": "Deployment", "namespace": "default"}}
    assert whitelist.validate(a).content_hash == whitelist.validate(b).content_hash


def test_content_hash_changes_when_params_change():
    a = whitelist.validate({"action": "scale_deployment", "cluster_id": "c1",
        "params": {"kind": "Deployment", "namespace": "default", "name": "web", "replicas": 3}})
    b = whitelist.validate({"action": "scale_deployment", "cluster_id": "c1",
        "params": {"kind": "Deployment", "namespace": "default", "name": "web", "replicas": 9}})
    assert a.content_hash != b.content_hash
