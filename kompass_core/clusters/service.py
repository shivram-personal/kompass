"""Cluster registry business logic: CRUD + envelope encryption + audit.

A raw kubeconfig is never logged, returned, or persisted in plaintext. It is
envelope-encrypted on register and decrypted in memory only at point of use.
"""

from __future__ import annotations

import uuid

import yaml
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..models import Cluster, EnvTag, User
from ..secretstore import envelope
from ..secretstore.envelope import Envelope
from ..secretstore.kms import KmsProvider


class ClusterError(Exception):
    """Client-correctable registry error (bad input). Never echoes secrets."""


def _parse_context_name(kubeconfig_text: str) -> str | None:
    """Extract the (non-secret) current-context for engine targeting.

    Raises ClusterError if the kubeconfig is not parseable as a kubeconfig.
    Never includes kubeconfig content in the error.
    """
    try:
        doc = yaml.safe_load(kubeconfig_text)
    except Exception:
        raise ClusterError("kubeconfig is not valid YAML.")
    if not isinstance(doc, dict) or "contexts" not in doc:
        raise ClusterError("not a valid kubeconfig (no contexts).")
    current = doc.get("current-context")
    if isinstance(current, str) and current:
        return current
    contexts = doc.get("contexts") or []
    if isinstance(contexts, list) and contexts and isinstance(contexts[0], dict):
        name = contexts[0].get("name")
        if isinstance(name, str):
            return name
    return None


class ClusterService:
    def __init__(self, kms: KmsProvider) -> None:
        self.kms = kms

    def register(
        self,
        db: DbSession,
        *,
        actor: User,
        name: str,
        env_tag: str,
        kubeconfig_text: str,
    ) -> Cluster:
        if env_tag not in set(EnvTag):
            raise ClusterError(f"env_tag must be one of {[e.value for e in EnvTag]}.")
        if not name.strip():
            raise ClusterError("name is required.")
        context_name = _parse_context_name(kubeconfig_text)

        cluster_id = uuid.uuid4().hex
        # Audit the intent before persisting (audit-before-execute). Never log
        # the kubeconfig — only non-secret metadata.
        audit.record(
            db,
            action="cluster_create",
            result="attempt",
            username=actor.username,
            role=actor.role,
            cluster_id=cluster_id,
            target=name,
            params={"env_tag": env_tag, "context_name": context_name},
        )

        env = envelope.encrypt(kubeconfig_text.encode("utf-8"), self.kms)
        cluster = Cluster(
            id=cluster_id,
            name=name,
            env_tag=env_tag,
            context_name=context_name,
            kubeconfig_ciphertext=env.ciphertext,
            wrapped_dek=env.wrapped_dek,
            nonce=env.nonce,
            kms_key_ref=env.kms_key_ref,
            created_by=actor.username,
        )
        db.add(cluster)
        db.commit()
        return cluster

    def list(self, db: DbSession) -> list[Cluster]:
        return db.query(Cluster).order_by(Cluster.created_at).all()

    def get(self, db: DbSession, cluster_id: str) -> Cluster | None:
        return db.get(Cluster, cluster_id)

    def delete(self, db: DbSession, *, actor: User, cluster: Cluster) -> None:
        audit.record(
            db,
            action="cluster_delete",
            result="attempt",
            username=actor.username,
            role=actor.role,
            cluster_id=cluster.id,
            target=cluster.name,
        )
        db.delete(cluster)  # purges ciphertext + wrapped DEK
        db.commit()

    def decrypt_kubeconfig(self, cluster: Cluster) -> str:
        """In-memory decryption at point of use. The result is never logged,
        returned in an API response, or written to disk."""
        env = Envelope(
            ciphertext=cluster.kubeconfig_ciphertext,
            wrapped_dek=cluster.wrapped_dek,
            nonce=cluster.nonce,
            kms_key_ref=cluster.kms_key_ref,
        )
        return envelope.decrypt(env, self.kms).decode("utf-8")
