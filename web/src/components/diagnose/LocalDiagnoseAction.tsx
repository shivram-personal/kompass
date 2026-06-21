import { useEffect, useState } from "react";
import { Sparkles } from "lucide-react";
import { fetchAgents, type AgentsResponse } from "../../api/diagnose";
import { DiagnosePanel } from "./DiagnosePanel";
import type { RenderDiagnoseAction } from "../../context/DiagnoseCustomization";

// Module-level cache so every resource action bar doesn't re-probe /api/agents.
let agentsPromise: Promise<AgentsResponse> | null = null;
function getAgents(): Promise<AgentsResponse> {
  if (!agentsPromise) {
    agentsPromise = fetchAgents().catch(() => ({ agents: [], enabled: false }));
  }
  return agentsPromise;
}

const AGENT_LABELS: Record<string, string> = {
  claude: "Claude Code",
  codex: "Codex",
  gemini: "Gemini CLI",
  "cursor-agent": "Cursor Agent",
};

function useLocalAgent() {
  const [state, setState] = useState<{ enabled: boolean; label: string }>({
    enabled: false,
    label: "your AI agent",
  });
  useEffect(() => {
    let live = true;
    getAgents().then((r) => {
      if (!live) return;
      // Prefer a supported (drivable) agent — that's the one the engine runs.
      const primary = r.agents.find((a) => a.supported) ?? r.agents[0];
      setState({
        enabled: r.enabled,
        label: primary
          ? primary.label || AGENT_LABELS[primary.name] || primary.name
          : "your AI agent",
      });
    });
    return () => {
      live = false;
    };
  }, []);
  return state;
}

function LocalDiagnoseButton({
  kind,
  namespace,
  name,
}: {
  kind: string;
  namespace: string;
  name: string;
}) {
  const { enabled, label } = useLocalAgent();
  const [open, setOpen] = useState(false);

  if (!enabled) return null;

  return (
    <>
      <button
        onClick={() => setOpen(true)}
        className="inline-flex items-center gap-1.5 rounded-lg border border-theme-border px-2.5 py-1.5 text-sm text-theme-text-secondary hover:bg-theme-hover hover:text-theme-text-primary"
        title="Investigate this resource with a local AI agent"
      >
        <Sparkles className="h-3.5 w-3.5 text-accent" />
        Diagnose with AI
      </button>
      {open && (
        <DiagnosePanel
          kind={kind}
          namespace={namespace}
          name={name}
          agentName={label}
          onClose={() => setOpen(false)}
        />
      )}
    </>
  );
}

/**
 * Default Diagnose action for standalone Radar: renders the local-agent button
 * (which self-hides when no agent CLI is available). A host like Radar Hub
 * overrides this by passing its own `renderDiagnoseAction` to <RadarApp>.
 */
export const defaultDiagnoseAction: RenderDiagnoseAction = ({
  kind,
  namespace,
  name,
}) => <LocalDiagnoseButton kind={kind} namespace={namespace} name={name} />;
