// Local, client-side history of recent AI investigations (OSS). Radar Cloud uses
// a server-persisted, cross-cluster, audited history instead — this is the
// lightweight OSS counterpart, kept entirely in the browser.
//
// Design (per review): schema-versioned key, hard per-field caps, cap on entry
// count, QuotaExceeded handling, and NO raw tool results stored (only tool
// names) — investigation output can contain logs/identifiers, so we keep it
// bounded and offer a Clear control. Restore is VIEW-ONLY ("saved report") — the
// live CLI session is gone after the run, so a saved entry can't be resumed.

const KEY = "radar-ai-history-v1"; // bump suffix on shape change → old data ignored
const CAP = 20;
const MAX_REPORT = 8000;
const MAX_FIELD = 2000;
const MAX_REMEDIATION = 12;

export interface SavedTurn {
  question?: string;
  rootCause: string;
  report: string;
  remediation: string[];
  confidence?: number;
  costUsd?: number;
  tools: string[]; // tool names only — never raw results
}

export interface HistoryEntry {
  id: string; // thread/session id (stable across follow-ups)
  ctx: string; // kube context / cluster, best-effort
  kind: string;
  namespace: string;
  name: string;
  ts: number; // last-updated epoch ms
  status: "done" | "error";
  turns: SavedTurn[];
}

function cap(s: string | undefined, n: number): string {
  if (!s) return "";
  return s.length > n ? s.slice(0, n) + "…" : s;
}

function trim(entry: HistoryEntry): HistoryEntry {
  return {
    ...entry,
    turns: entry.turns.map((t) => ({
      question: t.question ? cap(t.question, MAX_FIELD) : undefined,
      rootCause: cap(t.rootCause, MAX_FIELD),
      report: cap(t.report, MAX_REPORT),
      remediation: (t.remediation || [])
        .slice(0, MAX_REMEDIATION)
        .map((r) => cap(r, MAX_FIELD)),
      confidence: t.confidence,
      costUsd: t.costUsd,
      tools: (t.tools || []).slice(0, 50),
    })),
  };
}

export function loadHistory(): HistoryEntry[] {
  try {
    const raw = localStorage.getItem(KEY);
    if (!raw) return [];
    const v = JSON.parse(raw);
    return Array.isArray(v) ? v : [];
  } catch {
    return []; // corrupt / disabled → behave as empty
  }
}

// saveEntry upserts by id (so follow-ups update the same thread), newest first.
// Caps the list and degrades gracefully on QuotaExceededError by dropping oldest.
export function saveEntry(entry: HistoryEntry): void {
  let list = loadHistory().filter((e) => e.id !== entry.id);
  list.unshift(trim(entry));
  list = list.slice(0, CAP);
  for (let attempt = 0; attempt < 5; attempt++) {
    try {
      localStorage.setItem(KEY, JSON.stringify(list));
      return;
    } catch {
      if (list.length <= 1) return; // give up silently — history is best-effort
      list = list.slice(0, list.length - 1); // drop oldest, retry
    }
  }
}

export function clearHistory(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    /* ignore */
  }
}

export function forResource(
  list: HistoryEntry[],
  kind: string,
  namespace: string,
  name: string,
): HistoryEntry[] {
  return list.filter(
    (e) => e.kind === kind && e.namespace === namespace && e.name === name,
  );
}

// relativeTime renders a compact "3m ago" / "2h ago" / "Apr 3" label.
export function relativeTime(ts: number, now: number): string {
  const s = Math.max(0, Math.round((now - ts) / 1000));
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)}m ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  if (s < 7 * 86400) return `${Math.floor(s / 86400)}d ago`;
  return new Date(ts).toLocaleDateString();
}
