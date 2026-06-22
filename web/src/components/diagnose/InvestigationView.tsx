// A view over one durable, server-side investigation run. It SUBSCRIBES to the
// run's event stream (replay + live) and reconstructs the transcript; it does not
// own the run's lifetime — the server does. So closing the panel or navigating
// away just unsubscribes; the run keeps going and re-subscribing replays it.
import { useCallback, useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Send, AlertTriangle } from "lucide-react";
import {
  subscribeRun,
  addTurn,
  stopRun,
  DiagnoseError,
  type Diagnosis,
  type DiagnoseStreamEvent,
  type RunSummary,
} from "../../api/diagnose";
import { useDiagnose } from "./DiagnoseContext";
import {
  TurnView,
  ApplyDialog,
  appendThinking,
  upsertTool,
  type Turn,
} from "./parts";

export function InvestigationView({
  run,
  agentLabel,
  maximized,
}: {
  run: RunSummary;
  agentLabel: string;
  maximized: boolean;
}) {
  const { kind, namespace, name } = run;
  const { refreshRuns } = useDiagnose();
  const queryClient = useQueryClient();
  const [turns, setTurns] = useState<Turn[]>([]);
  const [busy, setBusy] = useState(false);
  const [input, setInput] = useState("");
  const [actionError, setActionError] = useState<string | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const pendingApplyRef = useRef(false);

  // After a successful apply, refresh the cluster-state views so the fix shows in
  // the surrounding UI (Issues, the resource, topology, …), not just the transcript.
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

  const updateLast = (fn: (t: Turn) => Turn) =>
    setTurns((prev) => prev.map((t, i) => (i === prev.length - 1 ? fn(t) : t)));

  // Subscribe to the run's event stream; rebuild the transcript from scratch on
  // (re)subscribe — the server replays everything, so a fresh tab reconstructs the
  // whole conversation.
  useEffect(() => {
    setTurns([]);
    setBusy(false);
    setActionError(null);
    pendingApplyRef.current = false;
    const cancel = subscribeRun(run.id, {
      onEvent: (ev: DiagnoseStreamEvent) => {
        switch (ev.type) {
          case "turn":
            if (ev.apply) pendingApplyRef.current = true;
            setBusy(true);
            setTurns((prev) => [
              ...prev,
              {
                question: ev.question,
                timeline: [],
                answer: "",
                diagnosis: null,
                error: null,
                status: "running",
                apply: ev.apply,
              },
            ]);
            break;
          case "thinking":
            if (ev.token)
              updateLast((t) => ({
                ...t,
                timeline: appendThinking(t.timeline, ev.token!),
              }));
            break;
          case "step":
            if (ev.step)
              updateLast((t) => ({
                ...t,
                timeline: upsertTool(t.timeline, ev.step!),
              }));
            break;
          case "token":
            if (ev.token)
              updateLast((t) => ({
                ...t,
                answer: (t.answer + ev.token).slice(-4000),
              }));
            break;
          case "done":
            updateLast((t) => ({
              ...t,
              diagnosis: (ev.diagnosis ?? null) as Diagnosis | null,
              status: "done",
            }));
            setBusy(false);
            if (pendingApplyRef.current) {
              pendingApplyRef.current = false;
              refreshClusterState();
            }
            refreshRuns();
            break;
          case "error":
            updateLast((t) => ({
              ...t,
              error: ev.error || "The investigation failed.",
              status: "error",
            }));
            setBusy(false);
            pendingApplyRef.current = false;
            refreshRuns();
            break;
        }
      },
    });
    return cancel;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run.id]);

  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [turns]);

  const stale = run.status === "stale";

  const submitFollowup = () => {
    const q = input.trim();
    if (!q || busy || stale) return;
    setInput("");
    setActionError(null);
    addTurn(run.id, { question: q }).catch((e) =>
      setActionError(e instanceof DiagnoseError ? e.message : "Couldn't send."),
    );
  };
  const stop = () => stopRun(run.id);

  // Apply: a user-confirmed remediation turn. Any step is applyable; the chosen
  // step's text is sent so the server binds the apply to it.
  const [confirmApply, setConfirmApply] = useState(false);
  const [pendingFix, setPendingFix] = useState("");
  const requestApply = (fix: string) => {
    setPendingFix(fix);
    setConfirmApply(true);
  };
  const runApply = () => {
    setConfirmApply(false);
    setActionError(null);
    addTurn(run.id, { apply: true, fix: pendingFix }).catch((e) =>
      setActionError(
        e instanceof DiagnoseError ? e.message : "Couldn't apply.",
      ),
    );
  };
  const checkStatus = () =>
    addTurn(run.id, {
      question:
        "Did the fix resolve the issue? Re-check the resource's current status and health now, and say whether it's healthy.",
    }).catch(() => {});

  // Apply tracks the latest turn that produced remediation (so follow-ups don't
  // strip it) and is blocked on a stale (context-switched) run.
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
        className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-3 [scrollbar-gutter:stable]"
      >
        <div className={maximized ? "mx-auto max-w-3xl" : ""}>
          <div className="space-y-4">
            {stale && (
              <div className="flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-theme-text-secondary">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
                <span>
                  This investigation ran against{" "}
                  <span className="font-medium text-theme-text-primary">
                    {run.context || "a different cluster"}
                  </span>
                  . The cluster context has changed — it's read-only now; re-run
                  Diagnose to analyze the current cluster.
                </span>
              </div>
            )}
            {turns.map((t, i) => {
              const isLast = i === turns.length - 1;
              const canApply = i === lastRemediationIdx && !stale;
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
            {actionError && (
              <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-theme-text-primary">
                <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
                <span>{actionError}</span>
              </div>
            )}
          </div>
        </div>
      </div>

      <ApplyDialog
        open={confirmApply}
        onClose={() => setConfirmApply(false)}
        onConfirm={runApply}
        agentLabel={agentLabel}
        resourceLabel={`${kind} ${namespace ? `${namespace}/` : ""}${name}`}
        fix={pendingFix}
        managedBy={run.managedBy}
        confidence={turns[lastRemediationIdx]?.diagnosis?.confidence}
      />

      <div
        className={`border-t border-theme-border px-3 py-2.5 ${maximized ? "[&>*]:mx-auto [&>*]:max-w-3xl" : ""}`}
      >
        {busy ? (
          <button
            onClick={stop}
            className="w-full rounded-lg border border-theme-border py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover"
          >
            Stop
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
              disabled={stale}
              placeholder={
                stale
                  ? "Cluster changed — re-run Diagnose"
                  : "Ask a follow-up or refine…"
              }
              className="max-h-32 min-h-[38px] flex-1 resize-none rounded-lg border border-theme-border bg-theme-base px-3 py-2 text-sm text-theme-text-primary placeholder:text-theme-text-tertiary focus:border-accent focus:outline-none disabled:opacity-50"
            />
            <button
              onClick={submitFollowup}
              disabled={!input.trim() || stale}
              className="shrink-0 rounded-lg btn-brand p-2 disabled:opacity-40"
              aria-label="Send follow-up"
            >
              <Send className="h-4 w-4" />
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
