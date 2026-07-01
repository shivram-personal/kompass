"""Per-user daily token budget enforcement (SPEC §4.5, Phase 6)."""

from datetime import datetime, timedelta, timezone

from kompass_core.ai import budget
from kompass_core.config import Settings
from kompass_core.models import AiUsage, User

PASSWORD = "correct horse battery staple"


def _user(client, username, override=None):
    factory = client.app.state.session_factory
    db = factory()
    try:
        u = User(username=username, role="editor", daily_token_budget=override)
        db.add(u)
        db.commit()
        return u.id
    finally:
        db.close()


def _add_usage(client, user_id, prompt, completion, *, ts=None):
    factory = client.app.state.session_factory
    db = factory()
    try:
        row = AiUsage(user_id=user_id, provider="p", model="m",
                      prompt_tokens=prompt, completion_tokens=completion)
        if ts is not None:
            row.ts = ts
        db.add(row)
        db.commit()
    finally:
        db.close()


def _check(client, user_id, settings, now=None):
    factory = client.app.state.session_factory
    db = factory()
    try:
        user = db.get(User, user_id)
        return budget.is_exhausted(db, user, settings, now=now)
    finally:
        db.close()


def test_under_budget_proceeds(client):
    settings = Settings(default_daily_token_budget=1000)
    uid = _user(client, "b-under")
    _add_usage(client, uid, 300, 300)  # 600 < 1000
    assert _check(client, uid, settings) is False


def test_over_budget_is_exhausted(client):
    settings = Settings(default_daily_token_budget=1000)
    uid = _user(client, "b-over")
    _add_usage(client, uid, 700, 400)  # 1100 >= 1000
    assert _check(client, uid, settings) is True


def test_window_resets_next_day(client):
    settings = Settings(default_daily_token_budget=1000)
    uid = _user(client, "b-reset")
    yesterday = datetime.now(timezone.utc).replace(tzinfo=None) - timedelta(days=1)
    _add_usage(client, uid, 900, 900, ts=yesterday)  # huge, but yesterday
    # Today has no usage -> not exhausted; yesterday's spend does not count.
    assert _check(client, uid, settings) is False


def test_per_user_override_beats_default(client):
    settings = Settings(default_daily_token_budget=1_000_000)
    uid = _user(client, "b-override", override=500)
    _add_usage(client, uid, 300, 300)  # 600 >= 500 override
    assert _check(client, uid, settings) is True


def test_zero_default_means_unlimited(client):
    settings = Settings(default_daily_token_budget=0)
    uid = _user(client, "b-unlimited")
    _add_usage(client, uid, 10_000, 10_000)
    assert _check(client, uid, settings) is False


def test_remaining_reports_none_for_unlimited(client):
    settings = Settings(default_daily_token_budget=0)
    uid = _user(client, "b-rem-unlimited")
    factory = client.app.state.session_factory
    db = factory()
    try:
        user = db.get(User, uid)
        assert budget.remaining(db, user, settings) is None
    finally:
        db.close()
