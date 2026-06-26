"""Event store logic: ingest (redact + store), list (window + cluster), prune,
and a best-effort pull from the engine's current-cluster events."""

from __future__ import annotations

import logging
from datetime import datetime, timedelta
from typing import Any

import httpx
from sqlalchemy import delete
from sqlalchemy.orm import Session as DbSession

from ..config import Settings
from ..db import utcnow
from ..models import ClusterEvent
from ..redact import redact_text

log = logging.getLogger("kompass.events")


def _parse_ts(raw: dict[str, Any]) -> datetime:
    for key in ("lastTimestamp", "eventTime", "firstTimestamp"):
        val = raw.get(key)
        if isinstance(val, str) and val:
            try:
                return datetime.fromisoformat(val.replace("Z", "+00:00")).replace(tzinfo=None)
            except ValueError:
                continue
    meta = raw.get("metadata") or {}
    val = meta.get("creationTimestamp")
    if isinstance(val, str) and val:
        try:
            return datetime.fromisoformat(val.replace("Z", "+00:00")).replace(tzinfo=None)
        except ValueError:
            pass
    return utcnow()


class EventService:
    def __init__(self, settings: Settings) -> None:
        self.settings = settings

    # --- ingest -------------------------------------------------------------
    def ingest(self, db: DbSession, cluster_id: str, raw_events: list[dict[str, Any]]) -> int:
        """Redact and store events for a cluster. Dedupes by event uid."""
        count = 0
        for raw in raw_events:
            meta = raw.get("metadata") or {}
            uid = meta.get("uid")
            if uid and db.query(ClusterEvent).filter(
                ClusterEvent.cluster_id == cluster_id, ClusterEvent.uid == uid
            ).count() > 0:
                continue
            involved = raw.get("involvedObject") or {}
            source = raw.get("source") or {}
            db.add(
                ClusterEvent(
                    cluster_id=cluster_id,
                    ts=_parse_ts(raw),
                    event_type=str(raw.get("type") or "Normal")[:16],
                    reason=(raw.get("reason") or None),
                    # Redact: an event message can echo secret object data.
                    message=redact_text(raw.get("message")),
                    involved_kind=involved.get("kind"),
                    involved_name=involved.get("name"),
                    namespace=involved.get("namespace") or meta.get("namespace"),
                    source=source.get("component") or source.get("host"),
                    uid=uid,
                )
            )
            count += 1
        db.commit()
        return count

    # --- read ---------------------------------------------------------------
    def list(self, db: DbSession, cluster_id: str, *, limit: int = 200) -> list[dict[str, Any]]:
        cutoff = utcnow() - timedelta(days=self.settings.event_retention_days)
        rows = (
            db.query(ClusterEvent)
            .filter(ClusterEvent.cluster_id == cluster_id, ClusterEvent.ts >= cutoff)
            .order_by(ClusterEvent.ts.desc())
            .limit(limit)
            .all()
        )
        return [
            {
                "id": e.id,
                "cluster_id": e.cluster_id,
                "ts": e.ts.isoformat(),
                "type": e.event_type,
                "reason": e.reason,
                "message": e.message,
                "involved_kind": e.involved_kind,
                "involved_name": e.involved_name,
                "namespace": e.namespace,
                "source": e.source,
            }
            for e in rows
        ]

    # --- prune --------------------------------------------------------------
    def prune(self, db: DbSession, *, now: datetime | None = None) -> int:
        """Delete events older than the retention window. Returns rows removed."""
        cutoff = (now or utcnow()) - timedelta(days=self.settings.event_retention_days)
        result = db.execute(delete(ClusterEvent).where(ClusterEvent.ts < cutoff))
        db.commit()
        return result.rowcount or 0

    # --- engine pull (best effort; local/connected cluster only) -----------
    async def poll_once(self, db: DbSession, engine: httpx.AsyncClient, cluster_id: str) -> int:
        """Pull the engine's current-cluster events and ingest them. Never
        live-connects a remote registry credential (deferred seam); it only
        reads whatever the engine is already connected to."""
        try:
            resp = await engine.get("/api/resources/events")
            if resp.status_code != 200:
                return 0
            payload = resp.json()
        except (httpx.HTTPError, ValueError):
            return 0
        items = payload if isinstance(payload, list) else payload.get("items", [])
        if not isinstance(items, list):
            return 0
        return self.ingest(db, cluster_id, items)
