// The single controller for the AI assistant surface. One instance app-wide:
// the per-resource "Diagnose" button and the global top-bar entry both dispatch
// here. Owns open/view/target state + the push-content layout (reserves a right
// column so the cluster UI reflows instead of being covered; overlay fallback on
// narrow viewports). Mounts exactly one <DiagnoseSurface/>.
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { fetchAgents } from "../../api/diagnose";
import { type HistoryEntry } from "./history";
import { DiagnoseSurface } from "./DiagnoseSurface";

export interface Target {
  kind: string;
  namespace: string;
  name: string;
}
export type DiagnoseView = "home" | "investigation" | "saved";

interface DiagnoseCtx {
  available: boolean; // an agent CLI is present (button/entry gate)
  agentLabel: string; // e.g. "Claude Code"
  open: boolean;
  view: DiagnoseView;
  target: Target | null;
  saved: HistoryEntry | null;
  openInvestigation: (t: Target) => void;
  openHome: () => void;
  openSaved: (e: HistoryEntry) => void;
  goHome: () => void;
  close: () => void;
}

const Ctx = createContext<DiagnoseCtx | null>(null);

export function useDiagnose(): DiagnoseCtx {
  const c = useContext(Ctx);
  if (!c) throw new Error("useDiagnose must be used within DiagnoseProvider");
  return c;
}

const MIN_W = 400;
const MAX_W = 1100;
const WIDTH_KEY = "radar-ai-panel-width";
const PUSH_MIN_VIEWPORT = 1024; // below this, overlay instead of pushing

const AGENT_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  gemini: "Gemini CLI",
  "cursor-agent": "Cursor Agent",
};

export function DiagnoseProvider({ children }: { children: ReactNode }) {
  const [available, setAvailable] = useState(false);
  const [agentLabel, setAgentLabel] = useState("your AI agent");
  const [open, setOpen] = useState(false);
  const [view, setView] = useState<DiagnoseView>("home");
  const [target, setTarget] = useState<Target | null>(null);
  const [saved, setSaved] = useState<HistoryEntry | null>(null);
  const [width, setWidth] = useState<number>(() => {
    // localStorage can throw (private mode / disabled). This provider wraps the
    // whole app, so a throw here would take down all of Radar — fail to default.
    try {
      const v = Number(localStorage.getItem(WIDTH_KEY));
      return v >= MIN_W && v <= MAX_W ? v : 560;
    } catch {
      return 560;
    }
  });
  const [maximized, setMaximized] = useState(false);
  const [narrow, setNarrow] = useState(
    () =>
      typeof window !== "undefined" && window.innerWidth < PUSH_MIN_VIEWPORT,
  );

  useEffect(() => {
    let live = true;
    fetchAgents()
      .then((r) => {
        if (!live) return;
        setAvailable(r.enabled);
        const primary = r.agents.find((a) => a.supported) ?? r.agents[0];
        if (primary)
          setAgentLabel(
            primary.label || AGENT_LABELS[primary.name] || primary.name,
          );
      })
      .catch(() => {});
    return () => {
      live = false;
    };
  }, []);

  useEffect(() => {
    const onResize = () => setNarrow(window.innerWidth < PUSH_MIN_VIEWPORT);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const openInvestigation = useCallback((t: Target) => {
    setTarget(t);
    setSaved(null);
    setView("investigation");
    setOpen(true);
  }, []);
  const openHome = useCallback(() => {
    setSaved(null);
    setView("home");
    setOpen(true);
  }, []);
  const openSaved = useCallback((e: HistoryEntry) => {
    setSaved(e);
    setView("saved");
    setOpen(true);
  }, []);
  const goHome = useCallback(() => {
    setSaved(null);
    setView("home");
  }, []);
  const close = useCallback(() => setOpen(false), []);

  const value: DiagnoseCtx = {
    available,
    agentLabel,
    open,
    view,
    target,
    saved,
    openInvestigation,
    openHome,
    openSaved,
    goHome,
    close,
  };

  // Push the whole app left by reserving the surface's width (wide viewports
  // only; maximized or narrow → overlay, no reserved gutter).
  const pushed = open && !narrow && !maximized;

  return (
    <Ctx.Provider value={value}>
      <div
        style={{
          paddingRight: pushed ? width : 0,
          transition: "padding-right 0.2s ease",
        }}
      >
        {children}
      </div>
      {open && (
        <DiagnoseSurface
          width={width}
          setWidth={setWidth}
          maximized={maximized}
          setMaximized={setMaximized}
          narrow={narrow}
          minW={MIN_W}
          maxW={MAX_W}
          widthKey={WIDTH_KEY}
        />
      )}
    </Ctx.Provider>
  );
}
