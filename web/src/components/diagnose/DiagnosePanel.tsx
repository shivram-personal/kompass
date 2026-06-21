import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import {
  Sparkles,
  X,
  Loader2,
  CheckCircle2,
  AlertTriangle,
  Copy,
  Check,
  ShieldCheck,
} from "lucide-react";
import {
  streamDiagnose,
  type Diagnosis,
  type DiagnoseStep,
  type DiagnoseStreamEvent,
} from "../../api/diagnose";
import { Markdown } from "../ui/Markdown";

const CONSENT_KEY = "radar-ai-consent-v1";

interface Props {
  kind: string;
  namespace: string;
  name: string;
  agentName: string; // e.g. "Claude Code"
  onClose: () => void;
}

type Phase = "consent" | "running" | "done" | "error";

export function DiagnosePanel({
  kind,
  namespace,
  name,
  agentName,
  onClose,
}: Props) {
  const hasConsent =
    typeof window !== "undefined" && localStorage.getItem(CONSENT_KEY) === "1";
  const [phase, setPhase] = useState<Phase>(hasConsent ? "running" : "consent");
  const [steps, setSteps] = useState<DiagnoseStep[]>([]);
  const [narration, setNarration] = useState("");
  const [diagnosis, setDiagnosis] = useState<Diagnosis | null>(null);
  const [error, setError] = useState<string | null>(null);
  const cancelRef = useRef<(() => void) | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const start = useCallback(() => {
    setPhase("running");
    setSteps([]);
    setNarration("");
    setDiagnosis(null);
    setError(null);
    cancelRef.current = streamDiagnose(
      { kind, namespace, name },
      {
        onEvent: (ev: DiagnoseStreamEvent) => {
          if (ev.type === "step" && ev.step) {
            setSteps((prev) => {
              const i = prev.findIndex((s) => s.id === ev.step!.id);
              if (i >= 0) {
                const next = [...prev];
                // The `done` event omits the tool name; keep the running one.
                next[i] = {
                  ...next[i],
                  ...ev.step!,
                  tool: ev.step!.tool || next[i].tool,
                };
                return next;
              }
              return [...prev, ev.step!];
            });
          } else if (ev.type === "token" && ev.token) {
            setNarration((prev) => (prev + ev.token).slice(-4000));
          } else if (ev.type === "done" && ev.diagnosis) {
            setDiagnosis(ev.diagnosis);
            setPhase("done");
          } else if (ev.type === "error") {
            setError(ev.error || "The investigation failed.");
            setPhase("error");
          }
        },
      },
    );
  }, [kind, namespace, name]);

  useEffect(() => {
    if (phase === "running" && !cancelRef.current) start();
    return () => {
      cancelRef.current?.();
      cancelRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Auto-scroll the activity log unless the user scrolled up.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [steps, narration]);

  const approve = () => {
    localStorage.setItem(CONSENT_KEY, "1");
    start();
  };

  const cancel = () => {
    cancelRef.current?.();
    cancelRef.current = null;
    if (phase === "running") {
      setError("Investigation cancelled.");
      setPhase("error");
    }
  };

  const resourceLabel = namespace
    ? `${kind} ${namespace}/${name}`
    : `${kind} ${name}`;

  return createPortal(
    <div
      className="fixed inset-0 z-50 flex justify-end"
      onMouseDown={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="absolute inset-0 bg-black/40 backdrop-blur-sm" />
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Diagnose with AI"
        className="relative flex h-full w-full max-w-[560px] flex-col border-l border-theme-border bg-theme-surface shadow-2xl"
        style={{
          animation: "slide-in-from-right 0.22s cubic-bezier(0.32,0.72,0,1)",
        }}
      >
        {/* Header */}
        <div className="flex items-center justify-between border-b border-theme-border px-4 py-3">
          <div className="flex items-center gap-2 min-w-0">
            <Sparkles className="h-4 w-4 shrink-0 text-accent" />
            <div className="min-w-0">
              <div className="text-sm font-medium text-theme-text-primary">
                Diagnose with AI
              </div>
              <div className="flex items-center gap-1.5 text-xs text-theme-text-tertiary">
                <span className="truncate">{resourceLabel}</span>
                <span className="shrink-0 opacity-60">·</span>
                <span className="shrink-0">{agentName}</span>
              </div>
            </div>
          </div>
          <button
            onClick={onClose}
            className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
            aria-label="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>

        {/* Body */}
        <div
          ref={scrollRef}
          className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-3"
        >
          {phase === "consent" && (
            <ConsentCard
              agentName={agentName}
              onApprove={approve}
              onCancel={onClose}
            />
          )}

          {phase !== "consent" && (
            <>
              <ActivityLog
                steps={steps}
                narration={narration}
                running={phase === "running"}
              />
              {phase === "done" && diagnosis && (
                <ResultCard diagnosis={diagnosis} />
              )}
              {phase === "error" && error && (
                <div className="mt-3 flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-theme-text-primary">
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
                  <span>{error}</span>
                </div>
              )}
            </>
          )}
        </div>

        {/* Footer */}
        {phase === "running" && (
          <div className="border-t border-theme-border px-4 py-2.5">
            <button
              onClick={cancel}
              className="w-full rounded-lg border border-theme-border py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover"
            >
              Stop investigation
            </button>
          </div>
        )}
        {(phase === "done" || phase === "error") && (
          <div className="border-t border-theme-border px-4 py-2.5">
            <button
              onClick={start}
              className="w-full rounded-lg border border-theme-border py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover"
            >
              Investigate again
            </button>
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}

function ConsentCard({
  agentName,
  onApprove,
  onCancel,
}: {
  agentName: string;
  onApprove: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="rounded-lg border border-theme-border bg-theme-elevated p-4">
      <div className="mb-2 flex items-center gap-2">
        <ShieldCheck className="h-4 w-4 text-accent" />
        <div className="text-sm font-medium text-theme-text-primary">
          Run a read-only AI investigation?
        </div>
      </div>
      <p className="text-sm leading-relaxed text-theme-text-secondary">
        Radar will send this resource&apos;s spec, recent events, and pod logs
        to{" "}
        <span className="font-medium text-theme-text-primary">{agentName}</span>
        , running on your own machine and subscription. The agent can only{" "}
        <span className="font-medium">read</span> — it cannot change your
        cluster.
      </p>
      <ul className="mt-2 space-y-1 text-xs text-theme-text-tertiary">
        <li>• Runs locally — no Radar cloud, no API key needed.</li>
        <li>• Read-only: investigation tools only.</li>
      </ul>
      <div className="mt-4 flex gap-2">
        <button
          onClick={onCancel}
          className="flex-1 rounded-lg border border-theme-border py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover"
        >
          Cancel
        </button>
        <button
          onClick={onApprove}
          className="flex-1 rounded-lg btn-brand py-1.5 text-sm"
        >
          Approve &amp; investigate
        </button>
      </div>
    </div>
  );
}

function ActivityLog({
  steps,
  narration,
  running,
}: {
  steps: DiagnoseStep[];
  narration: string;
  running: boolean;
}) {
  return (
    <div className="space-y-1.5">
      {steps.length > 0 && (
        <div className="text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
          Investigation
        </div>
      )}
      {steps.map((s) => (
        <div key={s.id} className="flex items-center gap-2 text-sm">
          {s.status === "done" ? (
            <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-emerald-400" />
          ) : (
            <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-accent" />
          )}
          <span className="font-mono text-xs text-theme-text-secondary">
            {prettyTool(s.tool)}
          </span>
          {s.ms != null && (
            <span className="text-xs text-theme-text-tertiary">{s.ms}ms</span>
          )}
        </div>
      ))}
      {running && (
        <div className="flex items-center gap-2 pt-1 text-xs text-theme-text-tertiary">
          <Loader2 className="h-3 w-3 animate-spin" />
          {steps.length === 0 ? "Starting investigation…" : "Analyzing…"}
        </div>
      )}
      {/* Live reasoning trace — only while running; the structured result cards
          replace it once done (otherwise it duplicates the answer + raw json). */}
      {running && narration && (
        <div className="mt-2 max-h-32 overflow-y-auto whitespace-pre-wrap rounded-md border border-theme-border bg-theme-base/50 p-2 font-mono text-[11px] leading-relaxed text-theme-text-tertiary [overflow-wrap:anywhere]">
          {stripJsonBlock(narration)}
        </div>
      )}
    </div>
  );
}

function ResultCard({ diagnosis }: { diagnosis: Diagnosis }) {
  // The agent emits rich markdown in `report` (## headings, fenced code blocks,
  // bold, inline code); the JSON-extracted rootCause/remediation are plain text.
  // Render the report so commands/fields/yaml are formatted; fall back to the
  // plain root cause if a run somehow produced no report.
  const body = diagnosis.report || diagnosis.rootCause;
  return (
    <div className="mt-3 space-y-2">
      <div className="rounded-lg border border-theme-border bg-theme-elevated p-3">
        <div className="mb-1 flex items-center justify-end gap-2">
          {diagnosis.confidence != null && (
            <span className="text-[11px] text-theme-text-tertiary">
              {Math.round(diagnosis.confidence * 100)}% confident
            </span>
          )}
          <CopyButton text={body} />
        </div>
        <Markdown className="text-sm [overflow-wrap:anywhere] [&_h2:first-child]:mt-0 [&_h2]:mb-1.5 [&_h2]:mt-3 [&_h2]:text-xs [&_h2]:font-semibold [&_h2]:uppercase [&_h2]:tracking-wide [&_h2]:text-theme-text-tertiary [&_h3]:text-sm [&_li]:text-theme-text-secondary [&_p]:my-1.5 [&_p]:text-theme-text-secondary">
          {body}
        </Markdown>
      </div>

      <div className="flex items-center justify-between gap-2 px-0.5 text-[11px] text-theme-text-tertiary">
        <span className="flex min-w-0 items-center gap-1">
          <ShieldCheck className="h-3 w-3 shrink-0" />
          <span className="truncate">
            Read-only · AI-generated — verify before acting
          </span>
        </span>
        {diagnosis.costUsd != null && (
          <span className="shrink-0">${diagnosis.costUsd.toFixed(3)}</span>
        )}
      </div>
    </div>
  );
}

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      onClick={() => {
        navigator.clipboard?.writeText(text);
        setCopied(true);
        setTimeout(() => setCopied(false), 1200);
      }}
      className="shrink-0 rounded p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
      aria-label="Copy"
    >
      {copied ? (
        <Check className="h-3.5 w-3.5 text-emerald-400" />
      ) : (
        <Copy className="h-3.5 w-3.5" />
      )}
    </button>
  );
}

function prettyTool(tool: string): string {
  return tool.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

// Hide the trailing fenced ```json result block from the live reasoning trace —
// it's machine output the result cards already render cleanly.
function stripJsonBlock(text: string): string {
  return text.replace(/```json[\s\S]*?```/g, "").trimEnd();
}
