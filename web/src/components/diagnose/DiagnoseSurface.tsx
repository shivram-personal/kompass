// The right-docked shell of the AI surface. Two layouts:
//  - docked: a single-pane right column (app reflows left via the provider's push)
//  - expanded: a master-detail workspace that fills ONLY the content area (does
//    not cover the left nav rail or top bar) — recent list on the left, the
//    selected investigation/report on the right.
import { useLayoutEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Sparkles, X, Maximize2, Minimize2, ChevronLeft } from "lucide-react";
import { Tooltip } from "../ui/Tooltip";
import { useDiagnose } from "./DiagnoseContext";
import { InvestigationView } from "./InvestigationView";
import { RecentList } from "./Home";

export function DiagnoseSurface({
  width,
  setWidth,
  maximized,
  setMaximized,
  narrow,
  minW,
  maxW,
  widthKey,
}: {
  width: number;
  setWidth: (fn: (w: number) => number) => void;
  maximized: boolean;
  setMaximized: (fn: (v: boolean) => boolean) => void;
  narrow: boolean;
  minW: number;
  maxW: number;
  widthKey: string;
}) {
  const d = useDiagnose();
  // When expanded, fill the content area only — measure the top bar + nav rail
  // so we don't cover the app chrome.
  const [chrome, setChrome] = useState({ top: 0, left: 0 });
  useLayoutEffect(() => {
    if (!maximized) return;
    const measure = () => {
      const h = document.querySelector("header");
      const nav = document.querySelector('[aria-label="Primary navigation"]');
      setChrome({
        top: h ? Math.round(h.getBoundingClientRect().bottom) : 0,
        left: nav ? Math.round(nav.getBoundingClientRect().right) : 0,
      });
    };
    measure();
    window.addEventListener("resize", measure);
    return () => window.removeEventListener("resize", measure);
  }, [maximized]);

  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    const onMove = (m: MouseEvent) =>
      setWidth(() =>
        Math.min(maxW, Math.max(minW, window.innerWidth - m.clientX)),
      );
    const onUp = () => {
      document.removeEventListener("mousemove", onMove);
      document.removeEventListener("mouseup", onUp);
      setWidth((w) => {
        try {
          localStorage.setItem(widthKey, String(w));
        } catch {
          /* storage disabled — width just won't persist */
        }
        return w;
      });
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  };

  const detailTitle =
    d.view === "saved" && d.saved
      ? `${d.saved.kind} ${d.saved.namespace}/${d.saved.name}`
      : d.view === "investigation" && d.target
        ? `${d.target.kind} ${d.target.namespace}/${d.target.name}`
        : "AI investigations";

  const positionStyle: React.CSSProperties = maximized
    ? { top: chrome.top, left: chrome.left, right: 0, bottom: 0 }
    : { top: 0, right: 0, bottom: 0, width, maxWidth: "100vw" };

  // The detail pane (right side when expanded; the whole body when docked). A
  // saved entry reopens as a continuable investigation (seeded with its saved
  // turns; resumes the agent session on follow-up / apply) rather than a
  // read-only report.
  const detail =
    d.view === "investigation" && d.target ? (
      <InvestigationView
        key={`${d.target.kind}/${d.target.namespace}/${d.target.name}`}
        target={d.target}
        agentLabel={d.agentLabel}
        maximized={maximized}
      />
    ) : d.view === "saved" && d.saved ? (
      <InvestigationView
        key={d.saved.id}
        target={{
          kind: d.saved.kind,
          namespace: d.saved.namespace,
          name: d.saved.name,
        }}
        agentLabel={d.agentLabel}
        maximized={maximized}
        seed={d.saved}
      />
    ) : (
      <div className="flex flex-1 items-center justify-center px-6 text-center text-sm text-theme-text-tertiary">
        Select an investigation, or open a resource and click Diagnose.
      </div>
    );

  const showBreadcrumb = !maximized && d.view !== "home";

  return createPortal(
    <div
      role="dialog"
      aria-label="AI investigations"
      className="fixed z-50 flex flex-col border-l border-theme-border bg-theme-surface shadow-2xl"
      style={{
        ...positionStyle,
        animation: "slide-in-from-right 0.22s cubic-bezier(0.32,0.72,0,1)",
      }}
    >
      {!maximized && !narrow && (
        <div
          onMouseDown={startResize}
          className="absolute left-0 top-0 z-10 h-full w-1 cursor-col-resize hover:bg-accent/40"
          title="Drag to resize"
        />
      )}

      {/* Header */}
      <div className="flex items-center justify-between border-b border-theme-border px-4 py-2.5">
        <div className="flex min-w-0 items-center gap-2">
          {!showBreadcrumb && (
            <Sparkles className="h-4 w-4 shrink-0 text-accent" />
          )}
          <div className="min-w-0">
            {showBreadcrumb && (
              <button
                onClick={d.goHome}
                className="-ml-1 mb-0.5 flex items-center gap-0.5 rounded px-1 text-[11px] text-theme-text-tertiary hover:text-theme-text-primary"
              >
                <ChevronLeft className="h-3 w-3" />
                Investigations
              </button>
            )}
            <div className="truncate text-sm font-medium text-theme-text-primary">
              {detailTitle}
            </div>
            <div className="truncate text-xs text-theme-text-tertiary">
              {d.view === "home" ? `via ${d.agentLabel}` : d.agentLabel}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <Tooltip content={maximized ? "Restore" : "Expand"} position="bottom">
            <button
              onClick={() => setMaximized((v) => !v)}
              className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
              aria-label={maximized ? "Restore" : "Expand"}
            >
              {maximized ? (
                <Minimize2 className="h-4 w-4" />
              ) : (
                <Maximize2 className="h-4 w-4" />
              )}
            </button>
          </Tooltip>
          <Tooltip content="Close" position="bottom">
            <button
              onClick={d.close}
              className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
              aria-label="Close"
            >
              <X className="h-4 w-4" />
            </button>
          </Tooltip>
        </div>
      </div>

      {/* Body. The detail wrapper keeps a stable position + key across both
          layouts so toggling Expand doesn't remount a live InvestigationView
          (which would discard its transcript and re-run the agent). The aside
          only appears when expanded; keys keep the detail node identity-stable
          as it comes and goes. */}
      <div className="flex min-h-0 flex-1">
        {maximized && (
          <aside
            key="recent"
            className="w-72 shrink-0 overflow-y-auto border-r border-theme-border px-3 py-3"
          >
            <RecentList
              agentLabel={d.agentLabel}
              selectedId={d.saved?.id}
              onSelect={d.openSaved}
            />
          </aside>
        )}
        {!maximized && d.view === "home" ? (
          <div
            key="main"
            className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-3"
          >
            <RecentList agentLabel={d.agentLabel} onSelect={d.openSaved} />
          </div>
        ) : (
          <div key="main" className="flex min-h-0 min-w-0 flex-1 flex-col">
            {detail}
          </div>
        )}
      </div>
    </div>,
    document.body,
  );
}
