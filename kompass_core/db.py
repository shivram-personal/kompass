"""SQLite persistence for kompass-core (SPEC §5).

The engine + session factory are built per Settings and stashed on app.state,
so tests can spin up isolated databases without global state.
"""

from __future__ import annotations

import os
from datetime import datetime, timezone

from sqlalchemy import create_engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker


class Base(DeclarativeBase):
    pass


def utcnow() -> datetime:
    """Naive UTC timestamp — SQLite stores naive; we keep everything in UTC."""
    return datetime.now(timezone.utc).replace(tzinfo=None)


def _ensure_sqlite_dir(db_url: str) -> None:
    prefix = "sqlite:///"
    if db_url.startswith(prefix):
        path = db_url[len(prefix) - 1 :]  # keep leading slash for absolute paths
        directory = os.path.dirname(path)
        if directory:
            os.makedirs(directory, exist_ok=True)


def build_session_factory(db_url: str) -> sessionmaker[Session]:
    _ensure_sqlite_dir(db_url)
    connect_args = {"check_same_thread": False} if db_url.startswith("sqlite") else {}
    engine = create_engine(db_url, connect_args=connect_args, future=True)

    # Import models so their tables register on Base before create_all.
    from . import models  # noqa: F401

    Base.metadata.create_all(engine)
    return sessionmaker(bind=engine, expire_on_commit=False, future=True)
