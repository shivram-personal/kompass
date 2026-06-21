// Presentational pieces of the Diagnose surface — pure + prop-driven (no
// persistence, no app/routing knowledge) so they lift cleanly into k8s-ui later
// and Cloud can reuse them. The stateful controller lives in DiagnoseContext;
// the run logic in InvestigationView.
import { useState, type ReactNode } from "react";
import {
  Loader2,
  CheckCircle2,
  AlertTriangle,
  Copy,
  Check,
  ShieldCheck,
  ChevronRight,
  Wrench,
  Wand2,
  Sparkles,
} from "lucide-react";
import { DialogPortal } from "@skyhook-io/k8s-ui/components/ui/DialogPortal";
import { type Diagnosis, type DiagnoseStep } from "../../api/diagnose";
import { Markdown } from "../ui/Markdown";
import { relativeTime, type HistoryEntry } from "./history";

// Turn is one round of the conversation: the initial investigation (no question)
// or a follow-up, each with its own transcript + result.
export type Turn = {
  question?: string;
  timeline: TimelineItem[];
  answer: string;
  diagnosis: Diagnosis | null;
  error: string | null;
  status: "running" | "done" | "error";
  // apply turns execute the recommended fix (write tools) — they report an
  // outcome, not a root cause, so the UI frames them differently.
  apply?: boolean;
};

// TimelineItem is one ordered transcript entry: agent reasoning, or a tool call.
export type TimelineItem =
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

export function appendThinking(
  prev: TimelineItem[],
  text: string,
): TimelineItem[] {
  const last = prev[prev.length - 1];
  if (last && last.kind === "thinking") {
    const next = [...prev];
    next[next.length - 1] = { ...last, text: (last.text + text).slice(-4000) };
    return next;
  }
  return [...prev, { kind: "thinking", text }];
}

export function upsertTool(
  prev: TimelineItem[],
  step: DiagnoseStep,
): TimelineItem[] {
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

export function TurnView({
  turn,
  onApply,
}: {
  turn: Turn;
  onApply?: () => void;
}) {
  return (
    <div className="space-y-2">
      {turn.question && (
        <div className="flex justify-end">
          <div className="max-w-[85%] rounded-lg rounded-br-sm bg-accent/10 px-3 py-1.5 text-sm text-theme-text-primary [overflow-wrap:anywhere]">
            {turn.question}
          </div>
        </div>
      )}
      <Timeline
        items={turn.timeline}
        running={turn.status === "running"}
        applyMode={turn.apply}
      />
      {turn.status === "running" && turn.answer && (
        <div className="whitespace-pre-wrap rounded-md border border-theme-border bg-theme-base/50 p-2 text-xs leading-relaxed text-theme-text-secondary [overflow-wrap:anywhere]">
          {stripJsonBlock(turn.answer)}
        </div>
      )}
      {turn.status === "done" && turn.diagnosis && (
        <ResultCard
          diagnosis={turn.diagnosis}
          onApply={onApply}
          apply={turn.apply}
        />
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

export function ConsentCard({
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

// The Apply confirmation — wider than a generic confirm so the recommended fix
// (rendered markdown) is legible, making it unambiguous what the one click does.
export function ApplyDialog({
  open,
  onClose,
  onConfirm,
  agentLabel,
  resourceLabel,
  recommendedFix,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  agentLabel: string;
  resourceLabel: string;
  recommendedFix?: string;
}) {
  const fix = recommendedFix?.trim();
  return (
    <DialogPortal open={open} onClose={onClose} className="max-w-lg w-full">
      <div className="flex items-start gap-3 border-b border-theme-border p-4">
        <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-full bg-amber-500/20">
          <AlertTriangle className="h-5 w-5 text-amber-500" />
        </div>
        <div className="min-w-0 flex-1">
          <h3 className="text-lg font-semibold text-theme-text-primary">
            Apply this fix?
          </h3>
          <p className="mt-1 text-sm text-theme-text-secondary">
            Let {agentLabel} apply the recommended change to{" "}
            <span className="font-medium text-theme-text-primary">
              {resourceLabel}
            </span>
            .
          </p>
        </div>
      </div>

      {fix && (
        <div className="border-b border-theme-border p-4">
          <div className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-accent">
            <Sparkles className="h-3.5 w-3.5" />
            What will happen
          </div>
          <Markdown className="max-h-48 overflow-auto text-sm text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_p]:my-0 [&_p]:text-theme-text-primary [&_pre]:my-1.5">
            {fix}
          </Markdown>
        </div>
      )}

      <div className="p-4">
        <div className="flex items-start gap-2 rounded border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-theme-text-secondary">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <span>
            {agentLabel} will change your cluster using your kubeconfig
            credentials. For GitOps/Helm-managed resources a direct change may be
            reverted — the agent will flag that and prefer the managed path.
          </span>
        </div>
      </div>

      <div className="flex items-center justify-end gap-3 border-t border-theme-border p-4">
        <button
          onClick={onClose}
          className="rounded-lg px-4 py-2 text-sm font-medium text-theme-text-secondary transition-colors hover:bg-theme-elevated hover:text-theme-text-primary"
        >
          Cancel
        </button>
        <button
          onClick={onConfirm}
          className="flex items-center gap-1.5 rounded-lg btn-brand px-4 py-2 text-sm font-medium"
        >
          <Wand2 className="h-4 w-4" />
          Apply fix
        </button>
      </div>
    </DialogPortal>
  );
}

export function Timeline({
  items,
  running,
  applyMode,
}: {
  items: TimelineItem[];
  running: boolean;
  applyMode?: boolean;
}) {
  return (
    <div className="space-y-1.5">
      {items.length > 0 && (
        <div className="text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
          {applyMode ? "Applying fix" : "Investigation"}
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
          {applyMode
            ? "Applying the fix…"
            : items.length === 0
              ? "Starting investigation…"
              : "Working…"}
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
      {hasDetail && (
        <Collapse open={open}>
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
        </Collapse>
      )}
    </div>
  );
}

// Collapse — the Radar-standard expand/collapse motion (grid-template-rows
// 0fr↔1fr) used across issue rows. Children stay mounted so close animates too.
function Collapse({ open, children }: { open: boolean; children: ReactNode }) {
  return (
    <div
      className={`issue-details-motion ${open ? "issue-details-motion-open" : ""}`}
    >
      <div className="overflow-hidden">{children}</div>
    </div>
  );
}

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

export function SavedReportView({ entry }: { entry: HistoryEntry }) {
  const now = Date.now();
  return (
    <div className="space-y-4">
      <div className="text-[11px] text-theme-text-tertiary">
        {entry.kind} {entry.namespace}/{entry.name} · saved{" "}
        {relativeTime(entry.ts, now)}
        {entry.ctx ? ` · ${entry.ctx}` : ""}
      </div>
      {entry.turns.map((t, i) => (
        <div key={i} className="space-y-2">
          {t.question && (
            <div className="flex justify-end">
              <div className="max-w-[85%] rounded-lg rounded-br-sm bg-accent/10 px-3 py-1.5 text-sm text-theme-text-primary [overflow-wrap:anywhere]">
                {t.question}
              </div>
            </div>
          )}
          {t.tools.length > 0 && (
            <div className="text-[11px] text-theme-text-tertiary">
              <span className="font-medium uppercase tracking-wide">
                Investigation
              </span>{" "}
              {t.tools.map(prettyTool).join(" · ")}
            </div>
          )}
          <ResultCard
            diagnosis={{
              rootCause: t.rootCause,
              report: t.report,
              remediation: t.remediation,
              recommendedFix: t.recommendedFix,
              confidence: t.confidence,
              costUsd: t.costUsd,
            }}
            apply={t.apply}
          />
        </div>
      ))}
    </div>
  );
}

function ResultCard({
  diagnosis,
  onApply,
  apply,
}: {
  diagnosis: Diagnosis;
  onApply?: () => void;
  apply?: boolean;
}) {
  // Apply turns report what changed — an outcome, not a diagnosis. Frame as a
  // success confirmation (emerald) rather than the amber root-cause anchor.
  if (apply) return <ApplyOutcomeCard diagnosis={diagnosis} />;

  const [showAnalysis, setShowAnalysis] = useState(false);
  const rootCause = diagnosis.rootCause || diagnosis.report;
  const remediation = diagnosis.remediation || [];
  const hasRemediation = remediation.length > 0;
  const recommended = diagnosis.recommendedFix?.trim();
  // Apply is only offered for a concrete, named fix — so the user knows exactly
  // what the one click will do before it happens.
  const canApply = !!onApply && !!recommended;
  return (
    <div className="mt-3 space-y-2">
      {/* Root cause — the anchor: distinct tone + heavier type so it pops. */}
      {rootCause && (
        <div className="rounded-lg border border-amber-500/30 bg-amber-500/5 p-3">
          <div className="mb-1 flex items-center justify-between gap-2">
            <div className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-amber-500">
              <AlertTriangle className="h-3.5 w-3.5" />
              Root cause
            </div>
            <div className="flex items-center gap-2">
              {diagnosis.confidence != null && (
                <span className="text-[11px] text-theme-text-tertiary">
                  {Math.round(diagnosis.confidence * 100)}% confident
                </span>
              )}
              <CopyButton text={rootCause} />
            </div>
          </div>
          <Markdown className="text-sm font-medium text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_p]:my-0 [&_p]:text-theme-text-primary">
            {rootCause}
          </Markdown>
        </div>
      )}

      {/* Remediation — discrete, copyable steps. */}
      {hasRemediation && (
        <div className="rounded-lg border border-theme-border bg-theme-elevated p-3">
          <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-theme-text-tertiary">
            <Wrench className="h-3.5 w-3.5 text-accent" />
            Remediation
          </div>
          <ol className="space-y-2">
            {remediation.map((r, i) => (
              <li key={i} className="flex items-start justify-between gap-2">
                <div className="flex min-w-0 flex-1 gap-2">
                  <span className="mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full bg-theme-base text-[10px] text-theme-text-tertiary">
                    {i + 1}
                  </span>
                  <Markdown className="min-w-0 flex-1 text-sm [overflow-wrap:anywhere] [&_p]:my-0 [&_pre]:my-1.5">
                    {r}
                  </Markdown>
                </div>
                <CopyButton text={r} />
              </li>
            ))}
          </ol>
        </div>
      )}

      {/* Recommended fix — the one action Apply will take, named up front so the
          user knows the outcome before clicking. */}
      {recommended && (
        <div className="rounded-lg border border-accent/40 bg-accent/5 p-3">
          <div className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-accent">
            <Sparkles className="h-3.5 w-3.5" />
            Recommended fix
          </div>
          <Markdown className="text-sm text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_p]:my-0 [&_p]:text-theme-text-primary [&_pre]:my-1.5">
            {recommended}
          </Markdown>
          {canApply && (
            <button
              onClick={onApply}
              className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg btn-brand py-2 text-sm font-medium"
            >
              <Wand2 className="h-4 w-4" />
              Apply recommended fix
            </button>
          )}
        </div>
      )}

      {/* Full analysis — the agent's detailed evidence, on demand. */}
      {diagnosis.report && (
        <div className="rounded-lg border border-theme-border bg-theme-elevated">
          <button
            onClick={() => setShowAnalysis((v) => !v)}
            className="flex w-full items-center gap-1.5 px-3 py-2 text-xs font-medium uppercase tracking-wide text-theme-text-tertiary hover:text-theme-text-primary"
          >
            <ChevronRight
              className={`h-3.5 w-3.5 transition-transform ${showAnalysis ? "rotate-90" : ""}`}
            />
            Full analysis
          </button>
          <Collapse open={showAnalysis}>
            <div className="border-t border-theme-border/60 px-3 py-2">
              <Markdown className="text-sm [overflow-wrap:anywhere] [&_h2:first-child]:mt-0 [&_h2]:mb-1.5 [&_h2]:mt-3 [&_h2]:text-xs [&_h2]:font-semibold [&_h2]:uppercase [&_h2]:tracking-wide [&_h2]:text-theme-text-tertiary [&_h3]:text-sm [&_li]:text-theme-text-secondary [&_p]:my-1.5 [&_p]:text-theme-text-secondary">
                {diagnosis.report}
              </Markdown>
            </div>
          </Collapse>
        </div>
      )}

      <div className="flex items-center gap-1 px-0.5 text-[11px] text-theme-text-tertiary">
        <ShieldCheck className="h-3 w-3 shrink-0" />
        <span className="truncate">AI-generated — review before applying</span>
      </div>
    </div>
  );
}

// The result of an apply turn: a success confirmation of what changed, not a
// diagnosis. Emerald + checkmark so it reads as an outcome.
function ApplyOutcomeCard({ diagnosis }: { diagnosis: Diagnosis }) {
  const outcome = diagnosis.report || diagnosis.rootCause;
  return (
    <div className="mt-3 space-y-2">
      <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/5 p-3">
        <div className="mb-1 flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-emerald-500">
            <CheckCircle2 className="h-3.5 w-3.5" />
            Applied
          </div>
          {outcome && <CopyButton text={outcome} />}
        </div>
        {outcome && (
          <Markdown className="text-sm text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_li]:text-theme-text-primary [&_p]:my-1 [&_p]:text-theme-text-primary [&_p:first-child]:mt-0 [&_p:last-child]:mb-0">
            {outcome}
          </Markdown>
        )}
      </div>
      <div className="flex items-center gap-1 px-0.5 text-[11px] text-theme-text-tertiary">
        <ShieldCheck className="h-3 w-3 shrink-0" />
        <span className="truncate">
          Applied by AI — verify the change took effect
        </span>
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

export function prettyTool(tool: string): string {
  return tool.replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());
}

function stripJsonBlock(text: string): string {
  return text.replace(/```json[\s\S]*?```/g, "").trimEnd();
}
