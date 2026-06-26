"""Core-owned, per-cluster event store with configurable retention (Phase 3).

kompass-core persists cluster events in its own SQLite table (the zero-Go-logic
constraint precludes configuring the engine's Go timeline, and per-cluster RBAC
+ redaction + our own index require a core-owned store). Events are redacted
before storage and pruned to the configured window.
"""
