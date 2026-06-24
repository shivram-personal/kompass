"""kompass-core — the Python/FastAPI service that fronts the Kompass app.

kompass-core is the authentication gateway and authorization boundary for
Kompass. The browser only ever talks to this service; it serves the rebranded
UI shell and proxies cluster reads to the near-upstream Go engine over pod
loopback under ``/api/engine/*`` (SPEC §6). All auth, AI orchestration, the
apply-action whitelist, audit, model management, and budgets live here in
later phases — never in the Go engine.
"""

__version__ = "0.1.0"
