"""Cluster registry request/response models. Responses carry only non-secret
metadata — never the kubeconfig, ciphertext, DEK, or nonce."""

from __future__ import annotations

from pydantic import BaseModel, ConfigDict, Field

from ..models import Cluster


class _Strict(BaseModel):
    model_config = ConfigDict(extra="forbid")


class RegisterClusterRequest(_Strict):
    name: str = Field(min_length=1, max_length=255)
    env_tag: str
    # Raw kubeconfig — accepted once, immediately envelope-encrypted, never
    # echoed back. Size-limited to reject absurd payloads (SPEC §8).
    kubeconfig: str = Field(min_length=1, max_length=1_048_576)


def cluster_public(cluster: Cluster) -> dict:
    return {
        "id": cluster.id,
        "name": cluster.name,
        "env_tag": cluster.env_tag,
        "context_name": cluster.context_name,
        "created_by": cluster.created_by,
        "created_at": cluster.created_at.isoformat(),
    }
