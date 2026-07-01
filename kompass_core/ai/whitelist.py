"""Server-side apply-action whitelist (SPEC §4.3, Phase 6) — the safety boundary.

This module is the **closed, default-deny** registry of the only action types that
may ever be applied to a cluster. It is code, not data: the AI cannot extend it,
and any proposal whose ``action`` is not a key here — or whose params fail the
per-action schema — is rejected by :func:`validate` *before* it is ever persisted
or reaches an execution path.

Each action maps 1:1 to an **already-existing engine write handler** (verified
routes; zero Go change — core proxies to them over loopback). The mapping and the
excluded-action list are mirrored in docs/SPEC.md §4.3.

Nothing here talks to the engine or mutates anything: it only validates a raw
proposal, canonicalizes it, and describes *which* engine calls a later, separately
authenticated apply step would make.
"""

from __future__ import annotations

import hashlib
import json
from collections.abc import Callable
from dataclasses import dataclass
from typing import Literal

from pydantic import BaseModel, ConfigDict, Field, ValidationError


class WhitelistError(Exception):
    """A proposal is not on the whitelist or failed validation. Client-correctable;
    never carries secrets or raw model output."""


# --- Per-action parameter schemas (strict: unknown fields rejected) ----------
# Everything action-specific lives in ``params`` so validation is total and the
# engine route can be built without free-form input. No field here is a secret
# (the v1 whitelist touches no Secret objects).


class _Params(BaseModel):
    model_config = ConfigDict(extra="forbid")


class ScaleParams(_Params):
    kind: Literal["Deployment", "StatefulSet"]
    namespace: str = Field(min_length=1, max_length=255)
    name: str = Field(min_length=1, max_length=255)
    replicas: int = Field(ge=0, le=10000)


class RestartParams(_Params):
    kind: Literal["Deployment", "StatefulSet", "DaemonSet"]
    namespace: str = Field(min_length=1, max_length=255)
    name: str = Field(min_length=1, max_length=255)


class HelmRollbackParams(_Params):
    namespace: str = Field(min_length=1, max_length=255)
    name: str = Field(min_length=1, max_length=255)
    revision: int = Field(gt=0)


class ReconcileParams(_Params):
    # Flux GitOps reconcile (SPEC gitops_reconcile).
    kind: Literal["Kustomization", "HelmRelease", "GitRepository", "OCIRepository", "HelmRepository"]
    namespace: str = Field(min_length=1, max_length=255)
    name: str = Field(min_length=1, max_length=255)


class SuspendResumeParams(_Params):
    ecosystem: Literal["flux", "argo"]
    # Flux uses a resource kind + namespace; Argo Applications live in the argocd
    # namespace and are addressed by name. `kind` is required for flux, ignored
    # for argo (Application).
    kind: str = Field(default="", max_length=64)
    namespace: str = Field(min_length=1, max_length=255)
    name: str = Field(min_length=1, max_length=255)
    suspend: bool  # True -> suspend, False -> resume


class NodeParams(_Params):
    name: str = Field(min_length=1, max_length=255)


# --- Engine-call description (built by an ActionSpec; executed later) ---------


@dataclass(frozen=True)
class EngineCall:
    method: str
    path: str
    json: dict | None = None
    query: dict | None = None


@dataclass(frozen=True)
class ActionSpec:
    action: str
    params_model: type[_Params]
    # A short human target string for audit/display, derived from params.
    target: Callable[[_Params], str]
    namespace: Callable[[_Params], str | None]
    # Read the current object to establish the drift baseline (or None if the
    # action has no meaningful pre-read). Returns an EngineCall (GET).
    read: Callable[[_Params], EngineCall | None]
    # Extract the drift token (resourceVersion / helm revision / etc.) from the
    # read response JSON. Compared preview-vs-apply; any change => reject.
    drift_token: Callable[[dict], str]
    # Build the mutating engine call.
    write: Callable[[_Params], EngineCall]
    # (before_summary, after_summary) for the diff/preview and audit.
    summarize: Callable[[dict, _Params], tuple[str, str]]


def _rv(obj: dict) -> str:
    """resourceVersion from a standard object read (`metadata.resourceVersion`)."""
    md = obj.get("metadata") if isinstance(obj, dict) else None
    rv = md.get("resourceVersion") if isinstance(md, dict) else None
    return str(rv) if rv is not None else ""


def _resource_read(kind: str, namespace: str | None, name: str) -> EngineCall:
    # Cluster-scoped resources use "_" as the namespace placeholder (engine convention).
    ns = namespace if namespace else "_"
    return EngineCall("GET", f"/api/resources/{kind}/{ns}/{name}")


def _spec_int(obj: dict, key: str, default: int = 0) -> int:
    spec = obj.get("spec") if isinstance(obj, dict) else None
    val = spec.get(key) if isinstance(spec, dict) else None
    return val if isinstance(val, int) else default


def _spec_bool(obj: dict, key: str) -> bool:
    spec = obj.get("spec") if isinstance(obj, dict) else None
    return bool(spec.get(key)) if isinstance(spec, dict) else False


# --- The whitelist ------------------------------------------------------------

WHITELIST: dict[str, ActionSpec] = {
    "scale_deployment": ActionSpec(
        action="scale_deployment",
        params_model=ScaleParams,
        target=lambda p: f"{p.kind}/{p.name}",
        namespace=lambda p: p.namespace,
        read=lambda p: _resource_read(p.kind, p.namespace, p.name),
        drift_token=_rv,
        write=lambda p: EngineCall(
            "POST", f"/api/workloads/{p.kind}/{p.namespace}/{p.name}/scale",
            json={"replicas": p.replicas},
        ),
        summarize=lambda obj, p: (
            f"{p.kind}/{p.name} replicas={_spec_int(obj, 'replicas')}",
            f"{p.kind}/{p.name} replicas={p.replicas}",
        ),
    ),
    "restart_workload": ActionSpec(
        action="restart_workload",
        params_model=RestartParams,
        target=lambda p: f"{p.kind}/{p.name}",
        namespace=lambda p: p.namespace,
        read=lambda p: _resource_read(p.kind, p.namespace, p.name),
        drift_token=_rv,
        write=lambda p: EngineCall(
            "POST", f"/api/workloads/{p.kind}/{p.namespace}/{p.name}/restart",
        ),
        summarize=lambda obj, p: (
            f"{p.kind}/{p.name} (running)",
            f"{p.kind}/{p.name} rolling-restarted",
        ),
    ),
    "helm_rollback": ActionSpec(
        action="helm_rollback",
        params_model=HelmRollbackParams,
        target=lambda p: f"helm/{p.name}",
        namespace=lambda p: p.namespace,
        read=lambda p: EngineCall("GET", f"/api/helm/releases/{p.namespace}/{p.name}"),
        # Helm drift baseline is the current release revision.
        drift_token=lambda obj: str(obj.get("revision") or obj.get("version") or ""),
        write=lambda p: EngineCall(
            "POST", f"/api/helm/releases/{p.namespace}/{p.name}/rollback",
            query={"revision": str(p.revision)},
        ),
        summarize=lambda obj, p: (
            f"release {p.name} revision={obj.get('revision') or obj.get('version')}",
            f"release {p.name} -> revision={p.revision}",
        ),
    ),
    "gitops_reconcile": ActionSpec(
        action="gitops_reconcile",
        params_model=ReconcileParams,
        target=lambda p: f"{p.kind}/{p.name}",
        namespace=lambda p: p.namespace,
        read=lambda p: _resource_read(p.kind, p.namespace, p.name),
        drift_token=_rv,
        write=lambda p: EngineCall(
            "POST", f"/api/flux/{p.kind}/{p.namespace}/{p.name}/reconcile",
        ),
        summarize=lambda obj, p: (
            f"{p.kind}/{p.name} (flux)",
            f"{p.kind}/{p.name} reconcile requested",
        ),
    ),
    "gitops_suspend_resume": ActionSpec(
        action="gitops_suspend_resume",
        params_model=SuspendResumeParams,
        target=lambda p: f"{(p.kind + '/') if p.ecosystem == 'flux' else 'application/'}{p.name}",
        namespace=lambda p: p.namespace,
        read=lambda p: (
            _resource_read(p.kind, p.namespace, p.name)
            if p.ecosystem == "flux"
            else EngineCall("GET", f"/api/resources/Application/{p.namespace}/{p.name}")
        ),
        drift_token=_rv,
        write=lambda p: EngineCall(
            "POST",
            (
                f"/api/flux/{p.kind}/{p.namespace}/{p.name}/{'suspend' if p.suspend else 'resume'}"
                if p.ecosystem == "flux"
                else f"/api/argo/applications/{p.namespace}/{p.name}/{'suspend' if p.suspend else 'resume'}"
            ),
        ),
        summarize=lambda obj, p: (
            f"{p.name} suspended={_spec_bool(obj, 'suspend')}",
            f"{p.name} suspended={p.suspend}",
        ),
    ),
    "cordon_node": ActionSpec(
        action="cordon_node",
        params_model=NodeParams,
        target=lambda p: f"node/{p.name}",
        namespace=lambda p: None,
        read=lambda p: _resource_read("Node", None, p.name),
        drift_token=_rv,
        write=lambda p: EngineCall("POST", f"/api/nodes/{p.name}/cordon"),
        summarize=lambda obj, p: (
            f"node/{p.name} unschedulable={_spec_bool(obj, 'unschedulable')}",
            f"node/{p.name} unschedulable=True (cordoned)",
        ),
    ),
    "uncordon_node": ActionSpec(
        action="uncordon_node",
        params_model=NodeParams,
        target=lambda p: f"node/{p.name}",
        namespace=lambda p: None,
        read=lambda p: _resource_read("Node", None, p.name),
        drift_token=_rv,
        write=lambda p: EngineCall("POST", f"/api/nodes/{p.name}/uncordon"),
        summarize=lambda obj, p: (
            f"node/{p.name} unschedulable={_spec_bool(obj, 'unschedulable')}",
            f"node/{p.name} unschedulable=False (uncordoned)",
        ),
    ),
}


@dataclass(frozen=True)
class ValidatedProposal:
    """A proposal that passed the whitelist. ``params`` is the validated model;
    ``content`` is the canonical, redacted dict that is hashed and persisted."""

    action: str
    cluster_id: str
    namespace: str | None
    target: str
    params: _Params
    content: dict
    content_hash: str

    @property
    def spec(self) -> ActionSpec:
        return WHITELIST[self.action]


def canonical_content(action: str, cluster_id: str, params: _Params) -> dict:
    """The exact, order-independent content that identifies this action and is hashed.

    The params are the validated, schema-constrained fields (kinds, names,
    namespaces, ints, bools) — non-secret by construction (the v1 whitelist touches
    no Secret objects and forbids free-form fields), so they are hashed and stored
    verbatim. This is also what apply re-derives the engine call from, so the
    executed action is exactly the one the user confirmed.

    Free-text ``rationale`` is intentionally NOT part of this content: it is not
    part of the action identity and could echo redacted context, so it is stored
    separately (redacted) and never hashed. Keeping it out of the hash is what makes
    "hash over redacted-but-semantically-complete content" hold without mangling the
    executable params.
    """
    return {
        "action": action,
        "cluster_id": cluster_id,
        "params": params.model_dump(),
    }


def hash_content(content: dict) -> str:
    blob = json.dumps(content, sort_keys=True, separators=(",", ":")).encode("utf-8")
    return hashlib.sha256(blob).hexdigest()


def validate(raw: object) -> ValidatedProposal:
    """Validate a raw proposal (e.g. parsed from model output) against the whitelist.

    Default-deny: raises :class:`WhitelistError` for anything that is not a known
    action with schema-valid params. Never executes or persists — pure validation.
    """
    if not isinstance(raw, dict):
        raise WhitelistError("proposal is not an object")
    action = raw.get("action")
    if not isinstance(action, str) or action not in WHITELIST:
        raise WhitelistError("action is not on the whitelist")
    cluster_id = raw.get("cluster_id")
    if not isinstance(cluster_id, str) or not cluster_id:
        raise WhitelistError("cluster_id is required")
    spec = WHITELIST[action]
    raw_params = raw.get("params")
    if not isinstance(raw_params, dict):
        raise WhitelistError("params is required")
    try:
        params = spec.params_model(**raw_params)
    except ValidationError as e:
        # Never echo raw params/model output; summarize the shape of the failure.
        raise WhitelistError(f"params failed validation for {action} ({e.error_count()} errors)")
    content = canonical_content(action, cluster_id, params)
    return ValidatedProposal(
        action=action,
        cluster_id=cluster_id,
        namespace=spec.namespace(params),
        target=spec.target(params),
        params=params,
        content=content,
        content_hash=hash_content(content),
    )


def is_whitelisted(action: object) -> bool:
    return isinstance(action, str) and action in WHITELIST
