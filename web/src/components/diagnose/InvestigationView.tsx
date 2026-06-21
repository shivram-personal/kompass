// A single live investigation for one resource: consent → streamed transcript →
// result, multi-turn follow-ups (resumed CLI session), persisted to local
// history on completion. Pure run logic + the existing presentational parts;
// the shell (dock/resize/maximize) lives in DiagnoseSurface, the controller in
// DiagnoseContext.
import { useCallback, useEffect, useRef, useState } from "react";
import { Send } from "lucide-react";
import {
  streamDiagnose,
  type Diagnosis,
  type DiagnoseStreamEvent,
} from "../../api/diagnose";
import { getApiBase, getCredentialsMode } from "../../api/config";
import { saveEntry } from "./history";
import { ConfirmDialog } from "../ui/ConfirmDialog";
import { useDiagnose, type Target } from "./DiagnoseContext";
import {
  ConsentCard,
  TurnView,
  appendThinking,
  upsertTool,
  type Turn,
  type TimelineItem,
} from "./parts";

const CONSENT_KEY = "radar-ai-consent-v1";

export function InvestigationView({
  target,
  agentLabel,
  maximized,
}: {
  target: Target;
  agentLabel: string;
  maximized: boolean;
}) {
  const { kind, namespace, name } = target;
  const { close } = useDiagnose();
  const hasConsent =
    typeof window !== "undefined" && localStorage.getItem(CONSENT_KEY) === "1";
  const [consented, setConsented] = useState(hasConsent);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  const ctxRef = useRef("");
  const sessionIdRef = useRef("");
  const cancelRef = useRef<(() => void) | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const running = turns[turns.length - 1]?.status === "running";
  const updateLast = (fn: (t: Turn) => Turn) =>
    setTurns((prev) => prev.map((t, i) => (i === prev.length - 1 ? fn(t) : t)));

  const runTurn = useCallback(
    (question?: string, apply?: boolean) => {
      setTurns((prev) => [
        ...prev,
        {
          question,
          timeline: [],
          answer: "",
          diagnosis: null,
          error: null,
          status: "running",
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
    [kind, namespace, name],
  );

  // Kick off the first turn once consented.
  useEffect(() => {
    if (consented && turns.length === 0) runTurn();
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

  // Best-effort cluster/context label for history.
  useEffect(() => {
    fetch(`${getApiBase()}/connection`, { credentials: getCredentialsMode() })
      .then((r) => (r.ok ? r.json() : null))
      .then((d) => {
        if (d) ctxRef.current = d.contextName || d.context || d.cluster || "";
      })
      .catch(() => {});
  }, []);

  // Persist the thread to local history whenever its latest turn finishes.
  useEffect(() => {
    const last = turns[turns.length - 1];
    if (!last || last.status === "running" || !sessionIdRef.current) return;
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
        confidence: t.diagnosis?.confidence,
        costUsd: t.diagnosis?.costUsd,
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
    localStorage.setItem(CONSENT_KEY, "1");
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
  const runApply = () => {
    setConfirmApply(false);
    runTurn("Apply the recommended fix", true);
  };

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
              {turns.map((t, i) => {
                const canApply =
                  i === turns.length - 1 &&
                  t.status === "done" &&
                  (t.diagnosis?.remediation?.length ?? 0) > 0;
                return (
                  <TurnView
                    key={i}
                    turn={t}
                    onApply={canApply ? () => setConfirmApply(true) : undefined}
                  />
                );
              })}
            </div>
          )}
        </div>
      </div>

      <ConfirmDialog
        open={confirmApply}
        onClose={() => setConfirmApply(false)}
        onConfirm={runApply}
        variant="warning"
        title="Apply the AI's fix?"
        message={`Let ${agentLabel} apply its recommended remediation to ${kind} ${namespace}/${name}.`}
        details="The agent will change your cluster using your kubeconfig credentials. Review the remediation steps first. For GitOps/Helm-managed resources a direct change may be reverted — the agent will flag that and prefer the managed path."
        confirmLabel="Apply fix"
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
              Stop investigation
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
