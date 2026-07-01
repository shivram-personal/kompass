"""Per-user daily token budget enforcement (SPEC §4.5, Phase 6).

The budget meters **provider token spend** and is checked at the point of LLM
invocation (chat/troubleshoot) — before the provider call. The apply endpoint is
NOT budget-gated: applying a proposal makes no provider call (see SPEC §4.3).

The window is a calendar day in UTC; usage resets at 00:00 UTC because the check
sums only ``ai_usage`` rows with ``ts >= start-of-today-UTC``.
"""

from __future__ import annotations

from datetime import datetime, timezone

from sqlalchemy import func
from sqlalchemy.orm import Session as DbSession

from ..config import Settings
from ..models import AiUsage, User


def _effective_budget(user: User, settings: Settings) -> int | None:
    """Tokens/day for this user. None = unlimited."""
    limit = user.daily_token_budget
    if limit is None:
        limit = settings.default_daily_token_budget
    if limit is None or limit <= 0:
        return None  # unlimited
    return limit


def _start_of_utc_day(now: datetime) -> datetime:
    return now.replace(hour=0, minute=0, second=0, microsecond=0, tzinfo=None)


def tokens_used_today(db: DbSession, user_id: int, *, now: datetime | None = None) -> int:
    now = now or datetime.now(timezone.utc)
    start = _start_of_utc_day(now)
    total = (
        db.query(
            func.coalesce(func.sum(AiUsage.prompt_tokens + AiUsage.completion_tokens), 0)
        )
        .filter(AiUsage.user_id == user_id, AiUsage.ts >= start)
        .scalar()
    )
    return int(total or 0)


def remaining(db: DbSession, user: User, settings: Settings, *, now: datetime | None = None) -> int | None:
    """Tokens remaining today. None = unlimited."""
    budget = _effective_budget(user, settings)
    if budget is None:
        return None
    return max(0, budget - tokens_used_today(db, user.id, now=now))


def is_exhausted(db: DbSession, user: User, settings: Settings, *, now: datetime | None = None) -> bool:
    """True if the user has no daily budget left (blocks the provider call)."""
    left = remaining(db, user, settings, now=now)
    return left is not None and left <= 0
