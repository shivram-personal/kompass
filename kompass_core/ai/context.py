"""Assemble grounding context for the AI from CORE-OWNED, per-cluster data only.

Phase 5 deliberately makes NO engine calls (see the phase report): reading a
specific cluster's live engine state would require selecting it (an injection-
family route the recommendation-only boundary keeps out of chat) and risks
returning the wrong active context's data. The Phase 3 event store is already
per-cluster scoped and redacted-at-ingest; we redact again here defensively
before anything reaches the provider.
"""

from __future__ import annotations

from sqlalchemy.orm import Session as DbSession

from ..clusters.service import ClusterService
from ..events.service import EventService
from ..redact import redact_text


def assemble_cluster_context(
    db: DbSession,
    cluster_id: str,
    cluster_service: ClusterService,
    event_service: EventService,
    *,
    max_events: int = 40,
) -> str:
    """Return a redacted, human-readable context block for a cluster."""
    parts: list[str] = []

    cluster = cluster_service.get(db, cluster_id)
    if cluster is not None:
        parts.append(
            f"Cluster: {cluster.name} (id={cluster.id}, environment={cluster.env_tag})"
        )

    events = event_service.list(db, cluster_id, limit=max_events)
    if events:
        lines = [
            f"- [{e['type']}] {e.get('reason') or ''} "
            f"{e.get('involved_kind') or ''}/{e.get('involved_name') or ''} "
            f"({e.get('namespace') or ''}): {e.get('message') or ''}"
            for e in events
        ]
        parts.append("Recent cluster events:\n" + "\n".join(lines))
    else:
        parts.append("Recent cluster events: none recorded.")

    # Defense-in-depth: redact again even though events are redacted at ingest.
    return redact_text("\n\n".join(parts)) or ""
