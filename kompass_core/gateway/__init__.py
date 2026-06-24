"""The auth gateway: health probes, the engine reverse proxy, and UI serving.

Phase 0 wires the transport: it proxies ``/api/engine/*`` to the engine and
serves the rebranded UI. Authentication and authorization are layered on top
of this gateway in later phases — every engine call already flows through here,
so there is a single choke point to enforce them.
"""
