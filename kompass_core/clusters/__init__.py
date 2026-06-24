"""Cluster registry: the source of truth for which clusters exist and who may
write to them. kompass-core owns the encrypted kubeconfigs; the engine never
holds or sees the encrypted store (SPEC §4.2).
"""
