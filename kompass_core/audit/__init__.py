"""Append-only audit log (SPEC §4.6).

Phase 1 records authentication and authorization events (login success/failure,
lockout, role/scope denial) and user/role/cluster config changes. Records are
written BEFORE the corresponding action proceeds (audit-before-execute). The
admin-facing viewer and CSV/JSON export arrive in Phase 7; the store is built
append-only now. Never write secrets here.
"""

from __future__ import annotations

import json
from typing import Any

from sqlalchemy.orm import Session as DbSession

from ..models import AuditEvent


def record(
    db: DbSession,
    *,
    action: str,
    result: str,
    username: str | None = None,
    role: str | None = None,
    cluster_id: str | None = None,
    target: str | None = None,
    params: dict[str, Any] | None = None,
    before_summary: str | None = None,
    after_summary: str | None = None,
    request_id: str | None = None,
) -> AuditEvent:
    """Append one audit event and commit immediately.

    ``params`` is serialized as already-redacted JSON; callers must never pass
    secrets. Committing here makes the record durable before the action runs.
    """
    event = AuditEvent(
        action=action,
        result=result,
        username=username,
        role=role,
        cluster_id=cluster_id,
        target=target,
        params_redacted=json.dumps(params, sort_keys=True) if params is not None else None,
        before_summary=before_summary,
        after_summary=after_summary,
        request_id=request_id,
    )
    db.add(event)
    db.commit()
    return event
