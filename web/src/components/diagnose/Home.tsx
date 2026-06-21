// Home view of the AI surface: the list of recent investigations. This is where
// history lives (not a clock icon in a per-resource drawer). Click one → its
// saved report. Cloud swaps loadHistory/clearHistory for a server-backed source.
import { useState } from "react";
import { Sparkles, Trash2 } from "lucide-react";
import {
  loadHistory,
  clearHistory,
  relativeTime,
  type HistoryEntry,
} from "./history";

function oneLiner(e: HistoryEntry): string {
  const last = e.turns[e.turns.length - 1];
  const rc = (last?.rootCause || "").trim();
  return rc.length > 120 ? rc.slice(0, 120) + "…" : rc;
}

export function Home({
  agentLabel,
  onOpenSaved,
}: {
  agentLabel: string;
  onOpenSaved: (e: HistoryEntry) => void;
}) {
  const [list, setList] = useState<HistoryEntry[]>(() => loadHistory());
  const now = Date.now();

  if (list.length === 0) {
    return (
      <div className="flex flex-col items-center px-4 py-12 text-center">
        <Sparkles className="mb-3 h-7 w-7 text-accent" />
        <div className="text-sm font-medium text-theme-text-primary">
          No investigations yet
        </div>
        <p className="mt-1 max-w-xs text-sm text-theme-text-tertiary">
          Open a resource and click{" "}
          <span className="font-medium text-theme-text-secondary">
            Diagnose with AI
          </span>{" "}
          to investigate it with {agentLabel}. Past investigations show up here.
        </p>
      </div>
    );
  }

  return (
    <div className="space-y-2">
      <div className="text-[11px] font-medium uppercase tracking-wide text-theme-text-tertiary">
        Recent investigations
      </div>
      {list.map((e) => (
        <button
          key={e.id}
          onClick={() => onOpenSaved(e)}
          className="flex w-full flex-col gap-0.5 rounded-md border border-theme-border/60 bg-theme-base/40 px-2.5 py-2 text-left hover:bg-theme-hover"
        >
          <div className="flex items-center gap-2">
            <span
              className={`h-1.5 w-1.5 shrink-0 rounded-full ${e.status === "error" ? "bg-red-400" : "bg-emerald-400"}`}
            />
            <span className="min-w-0 flex-1 truncate text-sm text-theme-text-primary">
              {e.kind} {e.namespace}/{e.name}
            </span>
            <span className="shrink-0 text-[11px] text-theme-text-tertiary">
              {relativeTime(e.ts, now)}
            </span>
          </div>
          {oneLiner(e) && (
            <div className="truncate pl-3.5 text-xs text-theme-text-tertiary">
              {oneLiner(e)}
            </div>
          )}
        </button>
      ))}
      <button
        onClick={() => {
          clearHistory();
          setList([]);
        }}
        className="flex items-center gap-1.5 pt-1 text-xs text-theme-text-tertiary hover:text-red-400"
      >
        <Trash2 className="h-3.5 w-3.5" /> Clear history
      </button>
    </div>
  );
}
