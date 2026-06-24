"""Local authentication, sessions, and role/per-cluster authorization.

All authorization decisions are made here in Python and enforced in front of
the engine proxy. The engine has no auth of its own and is reachable only via
this gateway.
"""
