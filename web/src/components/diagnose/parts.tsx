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
  RefreshCw,
} from "lucide-react";
import { DialogPortal } from "@skyhook-io/k8s-ui/components/ui/DialogPortal";
import {
  type Diagnosis,
  type DiagnoseStep,
  type AgentInfo,
} from "../../api/diagnose";
import { Markdown } from "../ui/Markdown";

// Segmented two-or-more-way selector — shared shape for the agent picker and the
// isolation toggle.
function Segmented<T extends string | boolean>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: T; label: string }[];
  value: T;
  onChange: (v: T) => void;
}) {
  return (
    <div>
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
        {label}
      </div>
      <div className="flex gap-1 rounded-lg border border-theme-border bg-theme-base p-1">
        {options.map((o) => (
          <button
            key={String(o.value)}
            onClick={() => onChange(o.value)}
            className={`flex-1 rounded-md px-2 py-1.5 text-xs font-medium transition-colors ${
              o.value === value
                ? "selection-strong selection-text selection-ring"
                : "text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary"
            }`}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

// Claude Code's --model takes version-stable ALIASES that always resolve to the
// user's installed latest of that tier (per `claude --help`), so this list never
// rots across model updates. "" = the agent's own default.
const CLAUDE_MODEL_OPTIONS = [
  { value: "", label: "Default" },
  { value: "opus", label: "Opus" },
  { value: "sonnet", label: "Sonnet" },
  { value: "haiku", label: "Haiku" },
  { value: "fable", label: "Fable" },
];
// Codex has no stable alias set and no way to enumerate models, and slugs change
// across versions — so we take a free-text override rather than a list that rots.
const EFFORT_OPTIONS = [
  { value: "", label: "Default" },
  { value: "minimal", label: "Minimal" },
  { value: "low", label: "Low" },
  { value: "medium", label: "Medium" },
  { value: "high", label: "High" },
];

function TextField({
  label,
  value,
  placeholder,
  onChange,
  hint,
}: {
  label: string;
  value: string;
  placeholder?: string;
  onChange: (v: string) => void;
  hint?: string;
}) {
  return (
    <div>
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
        {label}
      </div>
      <input
        type="text"
        value={value}
        placeholder={placeholder}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-theme-border bg-theme-base px-2 py-1.5 text-xs text-theme-text-primary placeholder:text-theme-text-tertiary"
      />
      {hint && (
        <p className="mt-1 text-[11px] leading-snug text-theme-text-tertiary">
          {hint}
        </p>
      )}
    </div>
  );
}

function Dropdown({
  label,
  value,
  options,
  onChange,
  hint,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onChange: (v: string) => void;
  hint?: string;
}) {
  return (
    <div>
      <div className="mb-1.5 text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
        {label}
      </div>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className="w-full rounded-md border border-theme-border bg-theme-base px-2 py-1.5 text-xs text-theme-text-primary"
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {hint && (
        <p className="mt-1 text-[11px] leading-snug text-theme-text-tertiary">
          {hint}
        </p>
      )}
    </div>
  );
}

// AgentControls is the full AI-diagnosis config block (agent, isolation, model,
// effort) — pure + prop-driven. It lives in Settings, not the investigation panel,
// since these are set-once preferences rather than per-run knobs.
export function AgentControls({
  agents,
  selectedAgent,
  onSelectAgent,
  isolated,
  onSetIsolated,
  model,
  onSetModel,
  effort,
  onSetEffort,
}: {
  agents: AgentInfo[];
  selectedAgent: string;
  onSelectAgent: (name: string) => void;
  isolated: boolean;
  onSetIsolated: (v: boolean) => void;
  model: string;
  onSetModel: (v: string) => void;
  effort: string;
  onSetEffort: (v: string) => void;
}) {
  const isCodex = selectedAgent === "codex";
  return (
    <div className="space-y-3">
      {agents.length >= 2 && (
        <Segmented
          label="Agent"
          value={selectedAgent}
          onChange={onSelectAgent}
          options={agents.map((a) => ({
            value: a.name,
            label: a.label || a.name,
          }))}
        />
      )}
      {isCodex && (
        <div>
          <Segmented<boolean>
            label="Environment"
            value={isolated}
            onChange={onSetIsolated}
            options={[
              { value: true, label: "Isolated" },
              { value: false, label: "My setup" },
            ]}
          />
          <p className="mt-1.5 text-[11px] leading-snug text-theme-text-tertiary">
            {isolated
              ? "Runs Codex on its own — no access to your other MCP servers, guidelines, or project files."
              : "Runs Codex with your full setup (your other MCP servers + guidelines). It can also read local files."}
          </p>
        </div>
      )}
      {isCodex ? (
        <TextField
          label="Model"
          value={model}
          placeholder="Default (e.g. gpt-5-codex, o3)"
          onChange={onSetModel}
          hint={
            !isolated
              ? "“My setup” uses your own Codex config's model; set a slug here to override it."
              : "Leave empty for Codex's default, or enter a model your Codex version supports."
          }
        />
      ) : (
        <Dropdown
          label="Model"
          value={model}
          options={CLAUDE_MODEL_OPTIONS}
          onChange={onSetModel}
          hint="Aliases always resolve to the latest of that tier; Default uses Claude Code's own."
        />
      )}
      {isCodex && (
        <Dropdown
          label="Reasoning effort"
          value={effort}
          options={EFFORT_OPTIONS}
          onChange={onSetEffort}
        />
      )}
    </div>
  );
}

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
  onCheckStatus,
}: {
  turn: Turn;
  onApply?: (fix: string) => void;
  onCheckStatus?: () => void;
}) {
  // A follow-up (a turn the user asked a question on) is a conversational reply,
  // not a fresh diagnosis — render it as a plain answer, never the root-cause
  // anchor or a remediation card.
  const followup = !!turn.question && !turn.apply;
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
        followup={followup}
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
          followup={followup}
          onCheckStatus={onCheckStatus}
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
  isolated = true,
  onOpenSettings,
  onApprove,
  onCancel,
}: {
  agentName: string;
  isolated?: boolean;
  onOpenSettings?: () => void;
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
        , running on your own machine and subscription.
        {isolated && (
          <>
            {" "}
            Through Radar the agent can only{" "}
            <span className="font-medium">read</span> — it cannot change your
            cluster.
          </>
        )}
      </p>
      <ul className="mt-2 space-y-1 text-xs text-theme-text-tertiary">
        <li>• Runs locally — no Radar cloud, no API key needed.</li>
        {isolated ? (
          <li>
            • Isolated: only Radar&apos;s read-only investigation tools — your
            other CLI config and MCP servers are excluded.
          </li>
        ) : (
          <li>
            • &ldquo;My setup&rdquo;: the agent also runs with your own CLI
            config + MCP servers and can read local files. Only Radar&apos;s own
            tools are read-only.
          </li>
        )}
      </ul>
      {onOpenSettings && (
        <button
          onClick={onOpenSettings}
          className="mt-3 text-xs text-accent hover:underline"
        >
          Change the agent and how it runs in Settings
        </button>
      )}
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
  fix,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  agentLabel: string;
  resourceLabel: string;
  fix?: string;
}) {
  const fixText = fix?.trim();
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

      {fixText && (
        <div className="border-b border-theme-border p-4">
          <div className="mb-1.5 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-accent">
            <Sparkles className="h-3.5 w-3.5" />
            What will happen
          </div>
          <AIMarkdown className="max-h-48 overflow-auto text-sm text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_p]:my-0 [&_p]:text-theme-text-primary [&_pre]:my-1.5">
            {fixText}
          </AIMarkdown>
        </div>
      )}

      <div className="p-4">
        <div className="flex items-start gap-2 rounded border border-amber-500/30 bg-amber-500/10 p-3 text-sm text-theme-text-secondary">
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-500" />
          <span>
            Review the change above before applying. {agentLabel} will change
            your cluster using your kubeconfig credentials; if you&apos;re not
            sure, ask a follow-up first. For GitOps/Helm-managed resources a
            direct change may be reverted — the agent will flag that and prefer
            the managed path.
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
  followup,
}: {
  items: TimelineItem[];
  running: boolean;
  applyMode?: boolean;
  followup?: boolean;
}) {
  const heading = applyMode
    ? "Applying fix"
    : followup
      ? "Working"
      : "Investigation";
  const runningLabel = applyMode
    ? "Applying the fix…"
    : items.length > 0
      ? "Working…"
      : followup
        ? "Thinking…"
        : "Starting investigation…";
  return (
    <div className="space-y-1.5">
      {items.length > 0 && (
        <div className="text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
          {heading}
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
          {runningLabel}
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

function ResultCard({
  diagnosis,
  onApply,
  apply,
  followup,
  onCheckStatus,
}: {
  diagnosis: Diagnosis;
  onApply?: (fix: string) => void;
  apply?: boolean;
  followup?: boolean;
  onCheckStatus?: () => void;
}) {
  // Apply turns report what changed — an outcome, not a diagnosis. Frame as a
  // success confirmation (emerald) rather than the amber root-cause anchor.
  if (apply)
    return (
      <ApplyOutcomeCard diagnosis={diagnosis} onCheckStatus={onCheckStatus} />
    );
  // Follow-ups are conversational replies, not fresh diagnoses — plain answer.
  if (followup) return <FollowupAnswer diagnosis={diagnosis} />;

  return <DiagnosisResult diagnosis={diagnosis} onApply={onApply} />;
}

// The diagnosis result: root cause + remediation (any step applyable) + the
// agent's full analysis on demand.
function DiagnosisResult({
  diagnosis,
  onApply,
}: {
  diagnosis: Diagnosis;
  onApply?: (fix: string) => void;
}) {
  const [showAnalysis, setShowAnalysis] = useState(false);
  const rootCause = diagnosis.rootCause || diagnosis.report;
  const remediation = diagnosis.remediation || [];
  const hasRemediation = remediation.length > 0;
  // The recommended step is a pointer into the list (1-based) — used to highlight
  // the suggested default. Any step can be applied, though, so the pointer only
  // drives emphasis, not whether Apply is offered.
  const recIdx = diagnosis.recommendedIndex;
  const recValid =
    recIdx != null && recIdx >= 1 && recIdx <= remediation.length;
  // The host decides whether this turn is applyable (latest result, right cluster,
  // etc.); when it is, every step gets an Apply affordance.
  const canApply = !!onApply;
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
                  {confidenceLabel(diagnosis.confidence)} confidence
                </span>
              )}
              <CopyButton text={rootCause} />
            </div>
          </div>
          <AIMarkdown className="text-sm font-medium text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_p]:my-0 [&_p]:text-theme-text-primary">
            {rootCause}
          </AIMarkdown>
        </div>
      )}

      {/* Remediation — copyable steps; the recommended one is highlighted as the
          default, and any step can be applied (Apply binds to that step's text). */}
      {hasRemediation && (
        <div className="rounded-lg border border-theme-border bg-theme-elevated p-3">
          <div className="mb-2 flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-theme-text-tertiary">
            <Wrench className="h-3.5 w-3.5 text-accent" />
            Remediation
          </div>
          <ol className="space-y-2">
            {remediation.map((r, i) => {
              const isRec = recValid && i === recIdx! - 1;
              return (
                <li
                  key={i}
                  className={
                    isRec
                      ? "rounded-lg border border-accent/40 bg-accent/5 p-2.5"
                      : ""
                  }
                >
                  <div className="flex items-start gap-2">
                    <span
                      className={`mt-0.5 flex h-4 w-4 shrink-0 items-center justify-center rounded-full text-[10px] ${
                        isRec
                          ? "bg-accent/20 text-accent"
                          : "bg-theme-base text-theme-text-tertiary"
                      }`}
                    >
                      {i + 1}
                    </span>
                    <div className="min-w-0 flex-1">
                      {isRec && (
                        <div className="mb-1 flex items-center gap-1 text-[10px] font-semibold uppercase tracking-wide text-accent">
                          <Sparkles className="h-3 w-3" />
                          Recommended
                        </div>
                      )}
                      <AIMarkdown className="text-sm [overflow-wrap:anywhere] [&_p]:my-0 [&_pre]:my-1.5">
                        {r}
                      </AIMarkdown>
                    </div>
                    {/* Action cluster: compact Apply (recommended = subtly
                        filled, others = ghost) sits next to Copy so each row's
                        actions stay together. The ellipsis signals a confirm
                        dialog follows — it doesn't apply immediately. */}
                    <div className="flex shrink-0 items-center gap-0.5">
                      {canApply && (
                        <button
                          onClick={() => onApply!(r)}
                          className={`inline-flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-accent transition-colors ${
                            isRec
                              ? "border border-accent/40 bg-accent/10 hover:bg-accent/20"
                              : "hover:bg-accent/10"
                          }`}
                        >
                          <Wand2 className="h-3 w-3" />
                          Apply…
                        </button>
                      )}
                      <CopyButton text={r} />
                    </div>
                  </div>
                </li>
              );
            })}
          </ol>
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
              <AIMarkdown className="text-sm [overflow-wrap:anywhere] [&_h2:first-child]:mt-0 [&_h2]:mb-1.5 [&_h2]:mt-3 [&_h2]:text-xs [&_h2]:font-semibold [&_h2]:uppercase [&_h2]:tracking-wide [&_h2]:text-theme-text-tertiary [&_h3]:text-sm [&_li]:text-theme-text-secondary [&_p]:my-1.5 [&_p]:text-theme-text-secondary">
                {diagnosis.report}
              </AIMarkdown>
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

// A follow-up reply: the agent answering a question, not re-diagnosing. Plain
// neutral block — no root-cause anchor, no remediation/apply.
function FollowupAnswer({ diagnosis }: { diagnosis: Diagnosis }) {
  const text = diagnosis.report || diagnosis.rootCause;
  if (!text) return null;
  return (
    <div className="mt-1 rounded-lg border border-theme-border bg-theme-elevated p-3">
      <div className="mb-1.5 flex items-center justify-between gap-2">
        <div className="flex items-center gap-1.5 text-xs font-semibold uppercase tracking-wide text-theme-text-tertiary">
          <Sparkles className="h-3.5 w-3.5 text-accent" />
          Answer
        </div>
        <CopyButton text={text} />
      </div>
      <AIMarkdown className="text-sm [overflow-wrap:anywhere] [&_code]:font-normal [&_h2:first-child]:mt-0 [&_h2]:mb-1.5 [&_h2]:mt-3 [&_h2]:text-xs [&_h2]:font-semibold [&_h2]:uppercase [&_h2]:tracking-wide [&_h2]:text-theme-text-tertiary [&_h3]:text-sm [&_li]:text-theme-text-secondary [&_p]:my-1.5 [&_p]:text-theme-text-secondary [&_p:first-child]:mt-0">
        {text}
      </AIMarkdown>
    </div>
  );
}

// The result of an apply turn: a success confirmation of what changed, not a
// diagnosis. Emerald + checkmark so it reads as an outcome.
function ApplyOutcomeCard({
  diagnosis,
  onCheckStatus,
}: {
  diagnosis: Diagnosis;
  onCheckStatus?: () => void;
}) {
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
          <AIMarkdown className="text-sm text-theme-text-primary [overflow-wrap:anywhere] [&_code]:font-normal [&_li]:text-theme-text-primary [&_p]:my-1 [&_p]:text-theme-text-primary [&_p:first-child]:mt-0 [&_p:last-child]:mb-0">
            {outcome}
          </AIMarkdown>
        )}
        {onCheckStatus && (
          <button
            onClick={onCheckStatus}
            className="mt-3 flex w-full items-center justify-center gap-1.5 rounded-lg border border-emerald-500/40 py-2 text-sm font-medium text-emerald-500 hover:bg-emerald-500/10"
          >
            <RefreshCw className="h-4 w-4" />
            Check status
          </button>
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

// A coarse band, not a precise %: a two-sig-fig confidence on an LLM judgement
// reads as calibrated when it isn't.
function confidenceLabel(c: number): string {
  if (c >= 0.8) return "High";
  if (c >= 0.5) return "Medium";
  return "Low";
}

// LLMs occasionally open a ```fence mid-line ("run this: ```bash kubectl …") or
// put the command on the same line as the ```lang marker. GFM then won't parse
// it as a fence — it leaks the literal ``` and renders an empty code box. Coerce
// fence markers onto their own lines and push trailing content off the opener so
// the block renders. (Well-formed markdown is unaffected.)
function tidyFences(md: string): string {
  if (!md || !md.includes("```")) return md;
  return md
    .replace(/([^\n])```/g, "$1\n\n```") // opener/closer must start a line
    .replace(/```([A-Za-z0-9_-]*)[ \t]+(\S)/g, "```$1\n$2"); // content off the opener line
}

// Diagnosis output is dense with inline `code`; the shared chip's brand tint is
// too loud at that density, so neutralize it (border/bg only) for this surface.
const SOFT_INLINE_CODE =
  "[&_.inline-code]:border-theme-border/60 [&_.inline-code]:bg-theme-base [&_.inline-code]:font-normal";

// Markdown for agent-generated text — normalizes flaky fences + softens code.
function AIMarkdown({
  className,
  children,
}: {
  className?: string;
  children: string;
}) {
  return (
    <Markdown className={`${SOFT_INLINE_CODE} ${className ?? ""}`}>
      {tidyFences(children)}
    </Markdown>
  );
}
