"""Typed client to the Go engine over pod loopback.

Phase 0 only needs a shared ``httpx.AsyncClient`` for the reverse proxy and a
readiness probe. Richer typed read/write helpers land in later phases.
"""
