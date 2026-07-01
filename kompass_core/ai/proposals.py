"""Apply-action proposal lifecycle (SPEC §4.3, Phase 6) — propose ≠ preview ≠ apply.

Three distinct, separately-authenticated steps:

1. **create** — a validated, whitelisted proposal parsed from model output is
   persisted (inert). Creation never mutates anything.
2. **preview** — reads the target's *current* state from the engine to build the
   before/after diff and capture the drift baseline. This is the mutation-preview
   live read permitted in Phase 6 (ADR-002 carve-out).
3. **apply** — the only mutating step. Bound to the proposal id + confirmed content
   hash; single-use; TTL-bounded; drift-checked; audit-before-execute; and run
   inside the process-wide engine-context lock so ``select -> re-assert -> mutate``
   is atomic against every other context switch (ADR-001 single-active-context).

All decryption is in-memory (seam #3); no secret is logged or returned.
"""

from __future__ import annotations

import asyncio
import json
import logging
import uuid
from datetime import datetime, timedelta, timezone

import httpx
from sqlalchemy.orm import Session as DbSession

from .. import audit
from ..clusters import injector
from ..clusters.service import ClusterService
from ..config import Settings
from ..models import Proposal, ProposalStatus, Role, User
from ..redact import redact_text
from . import whitelist
from .whitelist import EngineCall, ValidatedProposal, WhitelistError

log = logging.getLogger("kompass.ai.apply")


class ProposalError(Exception):
    """Client-correctable proposal error (bad/unknown/forbidden). Never carries secrets."""


class ProposalConflict(Exception):
    """Proposal can no longer be applied as previewed: consumed, expired, drifted,
    hash mismatch, or wrong active context. Maps to HTTP 409."""


def _now() -> datetime:
    return datetime.now(timezone.utc).replace(tzinfo=None)


class ProposalService:
    def __init__(self, settings: Settings, cluster_service: ClusterService) -> None:
        self.settings = settings
        self.clusters = cluster_service

    # --- create ----------------------------------------------------------------

    def create(
        self,
        db: DbSession,
        *,
        user_id: int | None,
        username: str,
        validated: ValidatedProposal,
        rationale: str | None,
        request_id: str | None = None,
    ) -> Proposal:
        """Persist a validated proposal. Inert — nothing executes here."""
        proposal = Proposal(
            id=uuid.uuid4().hex,
            created_by_user_id=user_id,
            created_by=username,
            cluster_id=validated.cluster_id,
            action=validated.action,
            namespace=validated.namespace,
            target=validated.target,
            params_redacted=json.dumps(validated.content, sort_keys=True),
            content_hash=validated.content_hash,
            rationale=redact_text(rationale) if rationale else None,
            status=ProposalStatus.proposed,
            expires_at=_now() + timedelta(seconds=self.settings.proposal_ttl_seconds),
            request_id=request_id,
        )
        db.add(proposal)
        db.commit()
        return proposal

    # --- lookup / lifecycle helpers -------------------------------------------

    def get(self, db: DbSession, proposal_id: str) -> Proposal | None:
        return db.get(Proposal, proposal_id)

    def _expire_if_due(self, db: DbSession, proposal: Proposal) -> bool:
        """Flip a stale proposed->expired (terminal). Returns True if now expired."""
        if proposal.status == ProposalStatus.proposed and _now() >= proposal.expires_at:
            proposal.status = ProposalStatus.expired
            db.commit()
        return proposal.status == ProposalStatus.expired

    def _revalidate(self, proposal: Proposal) -> ValidatedProposal:
        """Re-validate the *stored* content against the whitelist and recompute its
        hash. Defense in depth: a tampered/DB-mutated proposal (including one whose
        action was swapped to a non-whitelisted value) is rejected here, before any
        engine call."""
        try:
            content = json.loads(proposal.params_redacted)
            raw = {"action": content["action"], "cluster_id": content["cluster_id"],
                   "params": content["params"]}
            vp = whitelist.validate(raw)
        except (WhitelistError, KeyError, json.JSONDecodeError):
            raise ProposalConflict("proposal content is no longer valid")
        if vp.content_hash != proposal.content_hash:
            # Stored content and stored hash disagree -> integrity failure.
            raise ProposalConflict("proposal integrity check failed")
        return vp

    def can_apply(self, proposal: Proposal, user: User) -> bool:
        """Separation of duties: only the proposal's creator or an admin may apply."""
        if Role(user.role) == Role.admin:
            return True
        return proposal.created_by_user_id is not None and proposal.created_by_user_id == user.id

    # --- engine interaction (under the caller-held lock) -----------------------

    async def _connect(self, engine: httpx.AsyncClient, cluster) -> dict:
        """Inject (in-memory) + select the target cluster, and RE-ASSERT the engine's
        active context is exactly this cluster. Must be called with the context lock
        held. Returns the select result on success; raises ProposalConflict on any
        mismatch so a wrong-cluster mutation is impossible."""
        kubeconfig = self.clusters.decrypt_kubeconfig(cluster)
        try:
            await injector.inject(engine, cluster.id, kubeconfig)
            result = await injector.select(engine, cluster.id)
        except injector.InjectionError:
            raise ProposalConflict("engine could not select the target cluster")
        finally:
            del kubeconfig  # drop the plaintext reference promptly
        if result.get("status") != "selected" or result.get("cluster_id") != cluster.id:
            # Active context is not what we intend — refuse to mutate.
            raise ProposalConflict("engine active context did not match the target cluster")
        return result

    async def _call(self, engine: httpx.AsyncClient, call: EngineCall) -> httpx.Response:
        return await engine.request(call.method, call.path, json=call.json, params=call.query)

    async def _read_drift(self, engine: httpx.AsyncClient, vp: ValidatedProposal) -> tuple[str, str, str]:
        """Read current state -> (drift_token, before_summary, after_summary)."""
        read = vp.spec.read(vp.params)
        obj: dict = {}
        if read is not None:
            resp = await self._call(engine, read)
            if resp.status_code == 404:
                raise ProposalConflict("target resource no longer exists")
            if resp.status_code >= 400:
                raise ProposalConflict("could not read current state of the target")
            try:
                obj = resp.json()
            except ValueError:
                obj = {}
        token = vp.spec.drift_token(obj)
        before, after = vp.spec.summarize(obj, vp.params)
        return token, before, after

    # --- preview ---------------------------------------------------------------

    async def preview(
        self,
        *,
        engine: httpx.AsyncClient,
        lock: asyncio.Lock,
        session_factory,
        proposal_id: str,
    ) -> dict:
        """Compute the before/after diff and capture the drift baseline.

        Serialized under the engine-context lock: select the target, re-assert the
        active context, then read. Persists the baseline drift token so apply can
        reject if the world changed since the user reviewed the diff.
        """
        db: DbSession = session_factory()
        try:
            proposal = self.get(db, proposal_id)
            if proposal is None:
                raise ProposalError("proposal not found")
            if self._expire_if_due(db, proposal):
                raise ProposalConflict("proposal has expired")
            if proposal.status != ProposalStatus.proposed:
                raise ProposalConflict("proposal is no longer pending")
            vp = self._revalidate(proposal)
            cluster = self.clusters.get(db, proposal.cluster_id)
            if cluster is None:
                raise ProposalError("cluster not found")

            async with lock:
                await self._connect(engine, cluster)
                token, before, after = await self._read_drift(engine, vp)

            proposal.preview_drift_token = token
            proposal.before_summary = before
            proposal.after_summary = after
            db.commit()
            return {
                "id": proposal.id,
                "action": proposal.action,
                "cluster_id": proposal.cluster_id,
                "target": proposal.target,
                "content_hash": proposal.content_hash,
                "before": before,
                "after": after,
                "expires_at": proposal.expires_at.isoformat(),
            }
        finally:
            db.close()

    # --- apply -----------------------------------------------------------------

    async def apply(
        self,
        *,
        engine: httpx.AsyncClient,
        lock: asyncio.Lock,
        session_factory,
        proposal_id: str,
        user: User,
        confirmed_hash: str,
        request_id: str | None = None,
    ) -> dict:
        """Apply a previewed proposal. The only mutating path.

        Order (all mandatory): validate binding -> single-use claim -> AUDIT the
        intent (before any mutation) -> under the context lock: select + re-assert
        active context + re-read & compare drift -> mutate -> AUDIT the outcome.
        """
        request_id = request_id or uuid.uuid4().hex
        db: DbSession = session_factory()
        try:
            proposal = self.get(db, proposal_id)
            if proposal is None:
                raise ProposalError("proposal not found")

            # Ownership / separation of duties (creator or admin only).
            if not self.can_apply(proposal, user):
                audit.record(db, action="ai_apply", result="ownership_denied",
                             username=user.username, role=user.role,
                             cluster_id=proposal.cluster_id, target=proposal.target,
                             params={"proposal_id": proposal.id}, request_id=request_id)
                raise ProposalError("only the proposal's creator or an admin may apply it")

            # TTL + status (single-use): reject consumed/expired before anything.
            if self._expire_if_due(db, proposal):
                raise ProposalConflict("proposal has expired")
            if proposal.status != ProposalStatus.proposed:
                raise ProposalConflict("proposal has already been applied or is not pending")

            # Binding: the confirmed hash must equal the previewed content hash.
            if not confirmed_hash or confirmed_hash != proposal.content_hash:
                raise ProposalConflict("proposal content hash mismatch (was it changed since preview?)")
            vp = self._revalidate(proposal)  # re-checks whitelist + integrity

            # Diff-before-apply: a preview must have established the baseline.
            if proposal.preview_drift_token is None:
                raise ProposalConflict("a preview is required before apply")

            cluster = self.clusters.get(db, proposal.cluster_id)
            if cluster is None:
                raise ProposalError("cluster not found")

            # SINGLE-USE CLAIM (atomic, guarded): flip proposed->consumed. If a
            # concurrent apply already claimed it, rowcount is 0 -> reject (replay).
            claimed = (
                db.query(Proposal)
                .filter(Proposal.id == proposal.id, Proposal.status == ProposalStatus.proposed)
                .update({Proposal.status: ProposalStatus.consumed, Proposal.applied_at: _now()},
                        synchronize_session=False)
            )
            db.commit()
            if claimed != 1:
                raise ProposalConflict("proposal has already been applied")

            # AUDIT-BEFORE-EXECUTE: the intent row is durable before any mutation,
            # so the trail records who/what/which cluster/proposal id + hash even
            # if the apply then fails.
            audit.record(
                db, action="ai_apply", result="attempt",
                username=user.username, role=user.role, cluster_id=proposal.cluster_id,
                target=proposal.target,
                params={"proposal_id": proposal.id, "content_hash": proposal.content_hash,
                        "proposal_action": proposal.action},
                before_summary=proposal.before_summary, after_summary=proposal.after_summary,
                request_id=request_id,
            )

            # MUTATION critical section — serialized against every context switch.
            outcome_result = "failure"
            detail = "apply failed"
            try:
                async with lock:
                    await self._connect(engine, cluster)  # select + re-assert active context
                    # Re-read and reject on drift since the previewed baseline.
                    token, _before, _after = await self._read_drift(engine, vp)
                    if token != proposal.preview_drift_token:
                        raise ProposalConflict(
                            "target changed since preview (drift) — re-preview and confirm again")
                    resp = await self._call(engine, vp.spec.write(vp.params))
                    if resp.status_code >= 400:
                        detail = f"engine rejected the action (status {resp.status_code})"
                        raise ProposalError(detail)
                outcome_result = "success"
                detail = "applied"
            except ProposalConflict as e:
                outcome_result = "drift_rejected" if "drift" in str(e) else "aborted"
                detail = str(e)
                raise
            except (injector.InjectionError, httpx.HTTPError) as e:
                outcome_result = "failure"
                detail = "engine error during apply"
                log.warning("apply engine error for proposal %s: %s", proposal.id, type(e).__name__)
                raise ProposalError("engine error during apply")
            finally:
                # OUTCOME audit (append-only; never overwrites the intent row).
                audit.record(
                    db, action="ai_apply", result=outcome_result,
                    username=user.username, role=user.role, cluster_id=proposal.cluster_id,
                    target=proposal.target,
                    params={"proposal_id": proposal.id, "detail": detail},
                    request_id=request_id,
                )

            return {"id": proposal.id, "result": "success", "target": proposal.target,
                    "cluster_id": proposal.cluster_id, "before": proposal.before_summary,
                    "after": proposal.after_summary}
        finally:
            db.close()
