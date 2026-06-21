// A single live investigation for one resource: consent → streamed transcript →
// result, multi-turn follow-ups (resumed CLI session), persisted to local
// history on completion. Pure run logic + the existing presentational parts;
// the shell (dock/resize/maximize) lives in DiagnoseSurface, the controller in
// DiagnoseContext.
import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Send, AlertTriangle } from "lucide-react";
import {
  streamDiagnose,
  type Diagnosis,
  type DiagnoseStreamEvent,
} from "../../api/diagnose";
import { getApiBase, getCredentialsMode } from "../../api/config";
import { saveEntry, type HistoryEntry, type SavedTurn } from "./history";
import { useDiagnose, type Target } from "./DiagnoseContext";
import {
  ConsentCard,
  TurnView,
  ApplyDialog,
  appendThinking,
  upsertTool,
  type Turn,
  type TimelineItem,
} from "./parts";

const CONSENT_KEY = "radar-ai-consent-v1";

// localStorage can throw (Safari private mode / disabled storage); never let that
// crash the surface — this component renders inside the always-mounted provider.
function readConsent(): boolean {
  try {
    return (
      typeof window !== "undefined" &&
      localStorage.getItem(CONSENT_KEY) === "1"
    );
  } catch {
    return false;
  }
}

// Rebuild a live Turn from a persisted one so a reopened investigation shows its
// transcript and can be continued. Saved tool entries keep only names (never raw
// results), so the timeline rows are non-expandable.
function turnFromSaved(t: SavedTurn, ti: number): Turn {
  return {
    question: t.question,
    timeline: (t.tools || []).map((tool, i) => ({
      kind: "tool",
      id: `saved-${ti}-${i}`,
      tool,
      status: "done",
    })),
    answer: "",
    diagnosis: {
      rootCause: t.rootCause,
      report: t.report,
      remediation: t.remediation,
      recommendedIndex: t.recommendedIndex,
      confidence: t.confidence,
      costUsd: t.costUsd,
    },
    error: null,
    status: "done",
    apply: t.apply,
  };
}

export function InvestigationView({
  target,
  agentLabel,
  maximized,
  seed,
}: {
  target: Target;
  agentLabel: string;
  maximized: boolean;
  // A persisted entry to rehydrate: shows its saved turns and resumes the
  // agent's session (claude --resume) on follow-up / apply, instead of starting
  // a fresh investigation.
  seed?: HistoryEntry;
}) {
  const { kind, namespace, name } = target;
  const { close } = useDiagnose();
  const queryClient = useQueryClient();
  // After a successful apply, refresh the cluster-state views so the fix shows
  // in the surrounding UI (Issues, the resource, topology, …) — not just in the
  // agent's word. Prefix-match invalidation covers the namespace-keyed variants.
  const refreshClusterState = useCallback(() => {
    for (const key of [
      ["issues"],
      ["dashboard"],
      ["topology"],
      ["applications"],
      ["audit"],
      ["gitops-insights"],
      ["gitops-tree"],
      ["resource", kind, namespace, name],
    ]) {
      queryClient.invalidateQueries({ queryKey: key });
    }
  }, [queryClient, kind, namespace, name]);
  const [consented, setConsented] = useState(readConsent() || !!seed);
  // Cluster-mismatch guard for Apply. baselineCtx = the kube-context this
  // investigation is ABOUT (a seed's saved ctx, or the live cluster captured
  // when it first ran); currentCtx = the cluster we're connected to now,
  // refreshed on focus. Apply is allowed only on a positive match, so it can't
  // fire while the cluster is still loading or after a context switch.
  const [baselineCtx, setBaselineCtx] = useState(seed?.ctx || "");
  const [currentCtx, setCurrentCtx] = useState("");
  const [turns, setTurns] = useState<Turn[]>(() =>
    seed ? seed.turns.map(turnFromSaved) : [],
  );
  const [input, setInput] = useState("");
  const ctxRef = useRef(seed?.ctx || "");
  const sessionIdRef = useRef(seed?.id || "");
  // A seeded view shouldn't re-persist (bumping its timestamp) just for being
  // opened — only once the user actually continues it.
  const mutatedRef = useRef(!seed);
  const cancelRef = useRef<(() => void) | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const lastTurn = turns[turns.length - 1];
  const running = lastTurn?.status === "running";
  const applying = running && !!lastTurn?.apply;
  const updateLast = (fn: (t: Turn) => Turn) =>
    setTurns((prev) => prev.map((t, i) => (i === prev.length - 1 ? fn(t) : t)));

  const runTurn = useCallback(
    (question?: string, apply?: boolean, fix?: string) => {
      mutatedRef.current = true;
      // Close any stream still open from a prior turn before starting a new one,
      // so its late events can't land on this turn.
      cancelRef.current?.();
      setTurns((prev) => [
        ...prev,
        {
          question,
          timeline: [],
          answer: "",
          diagnosis: null,
          error: null,
          status: "running",
          apply,
        },
      ]);
      cancelRef.current = streamDiagnose(
        {
          kind,
          namespace,
          name,
          sessionId: sessionIdRef.current || undefined,
          question: apply ? undefined : question,
          apply,
          fix,
        },
        {
          onEvent: (ev: DiagnoseStreamEvent) => {
            if (ev.type === "thinking" && ev.token) {
              updateLast((t) => ({
                ...t,
                timeline: appendThinking(t.timeline, ev.token!),
              }));
            } else if (ev.type === "step" && ev.step) {
              updateLast((t) => ({
                ...t,
                timeline: upsertTool(t.timeline, ev.step!),
              }));
            } else if (ev.type === "token" && ev.token) {
              updateLast((t) => ({
                ...t,
                answer: (t.answer + ev.token).slice(-4000),
              }));
            } else if (ev.type === "done" && ev.diagnosis) {
              if (ev.diagnosis.sessionId)
                sessionIdRef.current = ev.diagnosis.sessionId;
              updateLast((t) => ({
                ...t,
                diagnosis: ev.diagnosis as Diagnosis,
                status: "done",
              }));
              // An apply turn just mutated the cluster — refresh the views that
              // should now reflect it.
              if (apply) refreshClusterState();
            } else if (ev.type === "error") {
              updateLast((t) => ({
                ...t,
                error: ev.error || "The investigation failed.",
                status: "error",
              }));
            }
          },
        },
      );
    },
    [kind, namespace, name, refreshClusterState],
  );

  // Kick off the first turn once consented. startedRef guards against React
  // Strict Mode's double effect invocation (which would otherwise leave an
  // orphaned cancelled turn behind the real one).
  const startedRef = useRef(false);
  useEffect(() => {
    if (consented && !startedRef.current && turns.length === 0) {
      startedRef.current = true;
      runTurn();
    }
    return () => {
      cancelRef.current?.();
      cancelRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [consented]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [turns]);

  // Track the connected cluster: on mount and again on window focus, so a
  // kube-context switch made while the panel stays open is detected (and the
  // mismatch guard re-evaluates) rather than going stale.
  useEffect(() => {
    let live = true;
    const refresh = () => {
      fetch(`${getApiBase()}/connection`, { credentials: getCredentialsMode() })
        .then((r) => (r.ok ? r.json() : null))
        .then((d) => {
          if (!live || !d) return;
          const ctx = d.contextName || d.context || d.cluster || "";
          ctxRef.current = ctx;
          setCurrentCtx(ctx);
          // Freeze the investigation's baseline cluster the first time we learn
          // it (a live run is about whatever cluster it started against). A
          // seed already pinned baselineCtx to its saved context.
          setBaselineCtx((prev) => prev || ctx);
        })
        .catch(() => {});
    };
    refresh();
    window.addEventListener("focus", refresh);
    return () => {
      live = false;
      window.removeEventListener("focus", refresh);
    };
  }, []);

  // Persist the thread to local history whenever its latest turn finishes.
  useEffect(() => {
    const last = turns[turns.length - 1];
    if (!last || last.status === "running" || !sessionIdRef.current) return;
    // Don't re-save a freshly reopened entry until the user continues it.
    if (!mutatedRef.current) return;
    saveEntry({
      id: sessionIdRef.current,
      ctx: ctxRef.current,
      kind,
      namespace,
      name,
      ts: Date.now(),
      status: last.status === "error" ? "error" : "done",
      turns: turns.map((t) => ({
        question: t.question,
        rootCause: t.diagnosis?.rootCause || "",
        report: t.diagnosis?.report || "",
        remediation: t.diagnosis?.remediation || [],
        recommendedIndex: t.diagnosis?.recommendedIndex,
        confidence: t.diagnosis?.confidence,
        costUsd: t.diagnosis?.costUsd,
        apply: t.apply,
        tools: t.timeline
          .filter(
            (it): it is Extract<TimelineItem, { kind: "tool" }> =>
              it.kind === "tool",
          )
          .map((it) => it.tool),
      })),
    });
  }, [turns, kind, namespace, name]);

  const approve = () => {
    try {
      localStorage.setItem(CONSENT_KEY, "1");
    } catch {
      /* storage disabled — consent holds for this session only */
    }
    setConsented(true);
  };
  const stop = () => {
    cancelRef.current?.();
    cancelRef.current = null;
    updateLast((t) =>
      t.status === "running"
        ? { ...t, status: "error", error: "Investigation cancelled." }
        : t,
    );
  };
  const submitFollowup = () => {
    const q = input.trim();
    if (!q || running) return;
    setInput("");
    runTurn(q);
  };

  // Apply: a user-confirmed remediation turn (the agent gets write tools and
  // applies the fix it proposed). Shown as its own turn in the transcript.
  const [confirmApply, setConfirmApply] = useState(false);
  // The remediation step the user chose to apply (any step, not just the
  // recommended one); drives the confirm dialog + binds the apply turn.
  const [pendingFix, setPendingFix] = useState("");
  const requestApply = (fix: string) => {
    setPendingFix(fix);
    setConfirmApply(true);
  };
  const runApply = () => {
    setConfirmApply(false);
    // Bind the apply to the exact step the user confirmed.
    runTurn("Apply this fix", true, pendingFix);
  };
  // Post-apply verification: a read-only follow-up that re-checks the resource.
  const checkStatus = () =>
    runTurn(
      "Did the fix resolve the issue? Re-check the resource's current status and health now, and say whether it's healthy.",
    );

  // Cluster guard: Apply may write only when we positively confirm we're still
  // connected to the cluster the investigation is about. If the baseline is
  // unknown (connection probe failed) we can't guard, so we don't block.
  const ctxConfirmed = baselineCtx === "" || currentCtx === baselineCtx;
  // Explicit mismatch (vs. merely still-loading) — drives the warning banner.
  const ctxMismatch =
    baselineCtx !== "" && currentCtx !== "" && baselineCtx !== currentCtx;

  // The diagnosis to apply from = the latest turn that produced remediation.
  // Asking a follow-up (which has no remediation) must NOT strip Apply from the
  // diagnosis above it — so Apply tracks this turn, not merely the last one.
  let lastRemediationIdx = -1;
  turns.forEach((t, i) => {
    if (
      t.status === "done" &&
      !t.apply &&
      (t.diagnosis?.remediation?.length ?? 0) > 0
    )
      lastRemediationIdx = i;
  });

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div
        ref={scrollRef}
        className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-3"
      >
        <div className={maximized ? "mx-auto max-w-3xl" : ""}>
          {!consented ? (
            <ConsentCard
              agentName={agentLabel}
              onApprove={approve}
              onCancel={close}
            />
          ) : (
            <div className="space-y-4">
              {ctxMismatch && (
                <div className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-theme-text-secondary">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                  <span>
                    This investigation ran against{" "}
                    <span className="font-medium text-theme-text-primary">
                      {baselineCtx}
                    </span>
                    , but you&apos;re now connected to{" "}
                    <span className="font-medium text-theme-text-primary">
                      {currentCtx}
                    </span>
                    . Apply is disabled — re-run Diagnose to analyze the current
                    cluster.
                  </span>
                </div>
              )}
              {turns.map((t, i) => {
                const isLast = i === turns.length - 1;
                // Apply (any remediation step) stays on the diagnosis turn even
                // after follow-ups, as long as we're still on its cluster.
                const canApply = i === lastRemediationIdx && ctxConfirmed;
                // After an apply, offer a one-tap verification follow-up.
                const canCheck = isLast && t.status === "done" && !!t.apply;
                return (
                  <TurnView
                    key={i}
                    turn={t}
                    onApply={canApply ? requestApply : undefined}
                    onCheckStatus={canCheck ? checkStatus : undefined}
                  />
                );
              })}
            </div>
          )}
        </div>
      </div>

      <ApplyDialog
        open={confirmApply}
        onClose={() => setConfirmApply(false)}
        onConfirm={runApply}
        agentLabel={agentLabel}
        resourceLabel={`${kind} ${namespace ? `${namespace}/` : ""}${name}`}
        fix={pendingFix}
      />

      {consented && (
        <div
          className={`border-t border-theme-border px-3 py-2.5 ${maximized ? "[&>*]:mx-auto [&>*]:max-w-3xl" : ""}`}
        >
          {running ? (
            <button
              onClick={stop}
              className="w-full rounded-lg border border-theme-border py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover"
            >
              {applying ? "Stop applying" : "Stop investigation"}
            </button>
          ) : (
            <div className="flex items-end gap-2">
              <textarea
                value={input}
                onChange={(e) => setInput(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    submitFollowup();
                  }
                }}
                rows={1}
                placeholder="Ask a follow-up or refine…"
                className="max-h-32 min-h-[38px] flex-1 resize-none rounded-lg border border-theme-border bg-theme-base px-3 py-2 text-sm text-theme-text-primary placeholder:text-theme-text-tertiary focus:border-accent focus:outline-none"
              />
              <button
                onClick={submitFollowup}
                disabled={!input.trim()}
                className="shrink-0 rounded-lg btn-brand p-2 disabled:opacity-40"
                aria-label="Send follow-up"
              >
                <Send className="h-4 w-4" />
              </button>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
