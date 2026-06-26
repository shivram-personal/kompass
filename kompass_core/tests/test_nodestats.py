"""Nodestats aggregation correctness (SPEC §4.7)."""

from kompass_core.nodestats.service import aggregate, parse_cpu_millicores, parse_memory_bytes


def test_cpu_parsing():
    assert parse_cpu_millicores("100m") == 100
    assert parse_cpu_millicores("2") == 2000
    assert parse_cpu_millicores("1.5") == 1500
    assert parse_cpu_millicores(None) == 0


def test_memory_parsing():
    assert parse_memory_bytes("128Mi") == 128 * 1024**2
    assert parse_memory_bytes("1Gi") == 1024**3
    assert parse_memory_bytes("500M") == 500 * 1000**2
    assert parse_memory_bytes("") == 0


def _pod(node, *containers):
    return {"spec": {"nodeName": node, "containers": list(containers)}}


def _c(cpu=None, mem=None):
    req = {}
    if cpu:
        req["cpu"] = cpu
    if mem:
        req["memory"] = mem
    return {"resources": {"requests": req}} if req else {"resources": {}}


def test_aggregate_counts_and_sums():
    pods = [
        _pod("node-1", _c("100m", "128Mi"), _c("200m", "256Mi")),
        _pod("node-1", _c("500m", "512Mi")),
        _pod("node-2", _c()),            # pod with no requests -> zero
        _pod(None, _c("999m")),          # unscheduled -> ignored
    ]
    nodes = [
        {"metadata": {"name": "node-1"}, "status": {"allocatable": {"cpu": "4", "memory": "8Gi"}}},
        {"metadata": {"name": "node-2"}, "status": {"allocatable": {"cpu": "2", "memory": "4Gi"}}},
        {"metadata": {"name": "node-3"}, "status": {"allocatable": {"cpu": "2", "memory": "4Gi"}}},
    ]
    result = {r["node"]: r for r in aggregate(pods, nodes)}

    assert result["node-1"]["pod_count"] == 2
    assert result["node-1"]["cpu_requests_millicores"] == 800        # 100+200+500
    assert result["node-1"]["memory_requests_bytes"] == (128 + 256 + 512) * 1024**2
    assert result["node-1"]["allocatable_cpu_millicores"] == 4000
    assert result["node-1"]["cpu_request_pct"] == 20.0               # 800/4000

    assert result["node-2"]["pod_count"] == 1
    assert result["node-2"]["cpu_requests_millicores"] == 0

    # Node with no scheduled pods still appears.
    assert result["node-3"]["pod_count"] == 0
