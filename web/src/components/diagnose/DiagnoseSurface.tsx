// The right-docked shell of the AI surface: resize / maximize / close, a
// back-to-home affordance, and the three views (Home · Investigation · Saved).
// Stateless re: what to show — it reads the controller (DiagnoseContext).
import { createPortal } from "react-dom";
import { Sparkles, X, Maximize2, Minimize2, ArrowLeft } from "lucide-react";
import { useDiagnose } from "./DiagnoseContext";
import { InvestigationView } from "./InvestigationView";
import { Home } from "./Home";
import { SavedReportView } from "./parts";

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
        localStorage.setItem(widthKey, String(w));
        return w;
      });
    };
    document.addEventListener("mousemove", onMove);
    document.addEventListener("mouseup", onUp);
  };

  const effWidth = maximized ? "100vw" : narrow ? "min(440px, 92vw)" : width;

  const title =
    d.view === "home"
      ? "AI investigations"
      : d.view === "saved" && d.saved
        ? `${d.saved.kind} ${d.saved.namespace}/${d.saved.name}`
        : d.target
          ? `${d.target.kind} ${d.target.namespace}/${d.target.name}`
          : "Investigation";
  const subtitle = d.view === "home" ? `via ${d.agentLabel}` : d.agentLabel;

  return createPortal(
    <div
      role="dialog"
      aria-label="AI investigations"
      className="fixed bottom-0 right-0 top-0 z-50 flex flex-col border-l border-theme-border bg-theme-surface shadow-2xl"
      style={{
        width: effWidth,
        maxWidth: "100vw",
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
      <div className="flex items-center justify-between border-b border-theme-border px-3 py-3">
        <div className="flex min-w-0 items-center gap-2">
          {d.view === "home" ? (
            <Sparkles className="ml-1 h-4 w-4 shrink-0 text-accent" />
          ) : (
            <button
              onClick={d.goHome}
              className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
              aria-label="Back to investigations"
              title="Back to investigations"
            >
              <ArrowLeft className="h-4 w-4" />
            </button>
          )}
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-theme-text-primary">
              {title}
            </div>
            <div className="truncate text-xs text-theme-text-tertiary">
              {subtitle}
            </div>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5">
          <button
            onClick={() => setMaximized((v) => !v)}
            className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
            aria-label={maximized ? "Restore" : "Expand"}
            title={maximized ? "Restore" : "Expand"}
          >
            {maximized ? (
              <Minimize2 className="h-4 w-4" />
            ) : (
              <Maximize2 className="h-4 w-4" />
            )}
          </button>
          <button
            onClick={d.close}
            className="rounded-md p-1 text-theme-text-tertiary hover:bg-theme-hover hover:text-theme-text-primary"
            aria-label="Close"
            title="Close"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      {/* Body */}
      {d.view === "investigation" && d.target ? (
        <InvestigationView
          key={`${d.target.kind}/${d.target.namespace}/${d.target.name}`}
          target={d.target}
          agentLabel={d.agentLabel}
          maximized={maximized}
        />
      ) : (
        <div className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-3">
          <div className={maximized ? "mx-auto max-w-3xl" : ""}>
            {d.view === "home" ? (
              <Home agentLabel={d.agentLabel} onOpenSaved={d.openSaved} />
            ) : d.saved ? (
              <SavedReportView entry={d.saved} />
            ) : null}
          </div>
        </div>
      )}
    </div>,
    document.body,
  );
}
