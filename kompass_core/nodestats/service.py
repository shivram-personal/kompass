"""Aggregate pods-per-node and summed requests vs allocatable (SPEC §4.7)."""

from __future__ import annotations

from typing import Any

# Binary (Ki) and decimal (k) memory suffixes -> multiplier in bytes.
_MEM_SUFFIX = {
    "Ki": 1024, "Mi": 1024**2, "Gi": 1024**3, "Ti": 1024**4, "Pi": 1024**5, "Ei": 1024**6,
    "k": 1000, "M": 1000**2, "G": 1000**3, "T": 1000**4, "P": 1000**5, "E": 1000**6,
}


def parse_cpu_millicores(q: str | None) -> int:
    """'100m' -> 100; '2' -> 2000; '1.5' -> 1500. Empty/None -> 0."""
    if not q:
        return 0
    q = str(q).strip()
    try:
        if q.endswith("m"):
            return int(float(q[:-1]))
        return int(float(q) * 1000)
    except ValueError:
        return 0


def parse_memory_bytes(q: str | None) -> int:
    """'128Mi' -> 134217728; '1Gi'; '500M'; plain bytes. Empty/None -> 0."""
    if not q:
        return 0
    q = str(q).strip()
    for suffix, mult in _MEM_SUFFIX.items():
        if q.endswith(suffix):
            try:
                return int(float(q[: -len(suffix)]) * mult)
            except ValueError:
                return 0
    try:
        return int(float(q))
    except ValueError:
        return 0


def _pct(used: int, total: int) -> float | None:
    if total <= 0:
        return None
    return round(used / total * 100, 1)


def aggregate(pods: list[dict[str, Any]], nodes: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Group pods by spec.nodeName; sum container requests; join with node
    allocatable. Pods with no requests count as zero (SPEC §4.7 acceptance)."""
    by_node: dict[str, dict[str, int]] = {}

    def bucket(name: str) -> dict[str, int]:
        return by_node.setdefault(name, {"pod_count": 0, "cpu_m": 0, "mem_b": 0})

    for pod in pods:
        spec = pod.get("spec") or {}
        node_name = spec.get("nodeName")
        if not node_name:
            continue  # unscheduled pods are not on any node
        b = bucket(node_name)
        b["pod_count"] += 1
        for container in spec.get("containers") or []:
            requests = ((container.get("resources") or {}).get("requests")) or {}
            b["cpu_m"] += parse_cpu_millicores(requests.get("cpu"))
            b["mem_b"] += parse_memory_bytes(requests.get("memory"))

    # Ensure nodes with zero scheduled pods still appear, and capture allocatable.
    alloc: dict[str, tuple[int, int]] = {}
    for node in nodes:
        name = (node.get("metadata") or {}).get("name")
        if not name:
            continue
        a = (node.get("status") or {}).get("allocatable") or {}
        alloc[name] = (parse_cpu_millicores(a.get("cpu")), parse_memory_bytes(a.get("memory")))
        bucket(name)

    out = []
    for name in sorted(by_node):
        b = by_node[name]
        alloc_cpu, alloc_mem = alloc.get(name, (0, 0))
        out.append(
            {
                "node": name,
                "pod_count": b["pod_count"],
                "cpu_requests_millicores": b["cpu_m"],
                "memory_requests_bytes": b["mem_b"],
                "allocatable_cpu_millicores": alloc_cpu,
                "allocatable_memory_bytes": alloc_mem,
                "cpu_request_pct": _pct(b["cpu_m"], alloc_cpu),
                "memory_request_pct": _pct(b["mem_b"], alloc_mem),
            }
        )
    return out
