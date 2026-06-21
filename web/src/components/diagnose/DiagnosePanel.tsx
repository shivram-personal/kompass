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
  ChevronRight,
  Send,
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

// Turn is one round of the conversation: the initial investigation (no question)
// or a follow-up (with the user's question), each with its own transcript + result.
type Turn = {
  question?: string;
  timeline: TimelineItem[];
  answer: string;
  diagnosis: Diagnosis | null;
  error: string | null;
  status: "running" | "done" | "error";
};

export function DiagnosePanel({
  kind,
  namespace,
  name,
  agentName,
  onClose,
}: Props) {
  const hasConsent =
    typeof window !== "undefined" && localStorage.getItem(CONSENT_KEY) === "1";
  const [consented, setConsented] = useState(hasConsent);
  const [turns, setTurns] = useState<Turn[]>([]);
  const [input, setInput] = useState("");
  // The CLI session id of the latest turn — resumed by the next follow-up so the
  // agent keeps full context. Kept in a ref to avoid stale closures in onEvent.
  const sessionIdRef = useRef("");
  const cancelRef = useRef<(() => void) | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);

  const running = turns[turns.length - 1]?.status === "running";

  const updateLast = (fn: (t: Turn) => Turn) =>
    setTurns((prev) => prev.map((t, i) => (i === prev.length - 1 ? fn(t) : t)));

  const runTurn = useCallback(
    (question?: string) => {
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
          question,
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
                diagnosis: ev.diagnosis!,
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

  // Kick off the first turn once the user has consented.
  useEffect(() => {
    if (consented && turns.length === 0) runTurn();
    return () => {
      cancelRef.current?.();
      cancelRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [consented]);

  // Auto-scroll unless the user scrolled up.
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
    if (nearBottom) el.scrollTop = el.scrollHeight;
  }, [turns]);

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
          {!consented ? (
            <ConsentCard
              agentName={agentName}
              onApprove={approve}
              onCancel={onClose}
            />
          ) : (
            <div className="space-y-4">
              {turns.map((t, i) => (
                <TurnView key={i} turn={t} />
              ))}
            </div>
          )}
        </div>

        {/* Footer — follow-up composer (or Stop while running) */}
        {consented && (
          <div className="border-t border-theme-border px-3 py-2.5">
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
    </div>,
    document.body,
  );
}

// TurnView renders one conversation turn: an optional user question bubble, the
// tool/reasoning transcript, the live answer while running, then the result.
function TurnView({ turn }: { turn: Turn }) {
  return (
    <div className="space-y-2">
      {turn.question && (
        <div className="flex justify-end">
          <div className="max-w-[85%] rounded-lg rounded-br-sm bg-accent/10 px-3 py-1.5 text-sm text-theme-text-primary [overflow-wrap:anywhere]">
            {turn.question}
          </div>
        </div>
      )}
      <Timeline items={turn.timeline} running={turn.status === "running"} />
      {turn.status === "running" && turn.answer && (
        <div className="whitespace-pre-wrap rounded-md border border-theme-border bg-theme-base/50 p-2 text-xs leading-relaxed text-theme-text-secondary [overflow-wrap:anywhere]">
          {stripJsonBlock(turn.answer)}
        </div>
      )}
      {turn.status === "done" && turn.diagnosis && (
        <ResultCard diagnosis={turn.diagnosis} />
      )}
      {turn.status === "error" && turn.error && (
        <div className="flex items-start gap-2 rounded-lg border border-red-500/30 bg-red-500/10 p-3 text-sm text-theme-text-primary">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-red-400" />
          <span>{turn.error}</span>
        </div>
      )}
    </div>
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

// TimelineItem is one ordered entry in the investigation transcript: either a
// chunk of the agent's reasoning, or a tool call (with its input + result).
type TimelineItem =
  | { kind: "thinking"; text: string }
  | {
      kind: "tool";
      id: string;
      tool: string;
      status: string;
      ms?: number;
      summary?: string;
      result?: string;
    };

function appendThinking(prev: TimelineItem[], text: string): TimelineItem[] {
  const last = prev[prev.length - 1];
  if (last && last.kind === "thinking") {
    const next = [...prev];
    next[next.length - 1] = { ...last, text: (last.text + text).slice(-4000) };
    return next;
  }
  return [...prev, { kind: "thinking", text }];
}

function upsertTool(prev: TimelineItem[], step: DiagnoseStep): TimelineItem[] {
  const i = prev.findIndex((it) => it.kind === "tool" && it.id === step.id);
  if (i >= 0) {
    const next = [...prev];
    const cur = next[i] as Extract<TimelineItem, { kind: "tool" }>;
    // The `done` event omits the tool name + input; keep them from `running`.
    next[i] = {
      ...cur,
      ...step,
      kind: "tool",
      tool: step.tool || cur.tool,
      summary: step.summary || cur.summary,
    };
    return next;
  }
  return [...prev, { kind: "tool", ...step }];
}

function Timeline({
  items,
  running,
}: {
  items: TimelineItem[];
  running: boolean;
}) {
  return (
    <div className="space-y-1.5">
      {items.length > 0 && (
        <div className="text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
          Investigation
        </div>
      )}
      {items.map((it, i) =>
        it.kind === "thinking" ? (
          <p
            key={i}
            className="whitespace-pre-wrap py-0.5 text-xs italic leading-relaxed text-theme-text-tertiary [overflow-wrap:anywhere]"
          >
            {it.text}
          </p>
        ) : (
          <ToolRow key={it.id} step={it} />
        ),
      )}
      {running && (
        <div className="flex items-center gap-2 pt-1 text-xs text-theme-text-tertiary">
          <Loader2 className="h-3 w-3 animate-spin" />
          {items.length === 0 ? "Starting investigation…" : "Working…"}
        </div>
      )}
    </div>
  );
}

function ToolRow({ step }: { step: Extract<TimelineItem, { kind: "tool" }> }) {
  const [open, setOpen] = useState(false);
  const hasDetail = !!(step.summary || step.result);
  return (
    <div className="rounded-md border border-theme-border/60 bg-theme-base/40">
      <button
        onClick={() => hasDetail && setOpen((v) => !v)}
        className={`flex w-full items-center gap-2 px-2 py-1.5 text-left text-sm ${
          hasDetail ? "hover:bg-theme-hover" : "cursor-default"
        }`}
      >
        {step.status === "done" ? (
          <CheckCircle2 className="h-3.5 w-3.5 shrink-0 text-emerald-400" />
        ) : (
          <Loader2 className="h-3.5 w-3.5 shrink-0 animate-spin text-accent" />
        )}
        <span className="font-mono text-xs text-theme-text-secondary">
          {prettyTool(step.tool)}
        </span>
        {step.summary && (
          <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-theme-text-tertiary">
            {compactArgs(step.summary)}
          </span>
        )}
        {step.ms != null && (
          <span className="ml-auto shrink-0 text-[11px] text-theme-text-tertiary">
            {step.ms}ms
          </span>
        )}
        {hasDetail && (
          <ChevronRight
            className={`h-3.5 w-3.5 shrink-0 text-theme-text-tertiary transition-transform ${open ? "rotate-90" : ""}`}
          />
        )}
      </button>
      {open && hasDetail && (
        <div className="space-y-2 border-t border-theme-border/60 px-2 py-2">
          {step.summary && (
            <div>
              <div className="mb-0.5 text-[10px] uppercase tracking-wide text-theme-text-tertiary">
                Input
              </div>
              <pre className="overflow-x-auto rounded bg-theme-elevated p-1.5 font-mono text-[11px] text-theme-text-secondary">
                {step.summary}
              </pre>
            </div>
          )}
          {step.result && (
            <div>
              <div className="mb-0.5 text-[10px] uppercase tracking-wide text-theme-text-tertiary">
                Result
              </div>
              <pre className="max-h-48 overflow-auto rounded bg-theme-elevated p-1.5 font-mono text-[11px] text-theme-text-secondary">
                {step.result}
              </pre>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// compactArgs renders a tool's JSON input as a compact "k=v" hint for the row.
function compactArgs(raw: string): string {
  try {
    const o = JSON.parse(raw);
    return Object.entries(o)
      .map(([k, v]) => `${k}=${typeof v === "string" ? v : JSON.stringify(v)}`)
      .join(" ");
  } catch {
    return raw;
  }
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
